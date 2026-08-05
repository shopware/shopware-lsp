package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const missingServiceArgumentsCode lsp.DiagnosticID = "symfony.service.arguments.missing"
const unknownServiceNamedArgumentCode lsp.DiagnosticID = "symfony.service.named_argument.unknown"
const missingConfiguredServiceMethodCode lsp.DiagnosticID = "symfony.service.method.missing"

var unsupportedServiceConfigurationKeys = map[string]struct{}{
	"parent":          {},
	"factory_class":   {},
	"factory_service": {},
	"factory_method":  {},
	"abstract":        {},
	"factory":         {},
	"resource":        {},
	"exclude":         {},
	"alias":           {},
}

type ServiceArgumentAnalyzer struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewServiceArgumentAnalyzer(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ServiceArgumentAnalyzer {
	return &ServiceArgumentAnalyzer{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

type serviceArgumentDefinition struct {
	node            *cst.Node
	serviceID       string
	className       string
	positionalCount int
	namedArguments  map[string]struct{}
	format          string
}

func (p *ServiceArgumentAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	var definitions []serviceArgumentDefinition
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".xml":
		definitions = xmlServiceArgumentDefinitions(document.SyntaxTree.Root)
	case ".yaml", ".yml":
		definitions = yamlServiceArgumentDefinitions(document.SyntaxTree.Root)
	case ".php":
	default:
		return nil, nil
	}

	serviceSuggestions := make(map[string][]string)
	result, err := p.configuredServiceMethodDiagnostics(ctx, document)
	if err != nil {
		return nil, err
	}
	typeDiagnostics, err := p.serviceArgumentTypeDiagnostics(ctx, document)
	if err != nil {
		return nil, err
	}
	result = append(result, typeDiagnostics...)
	namedArgumentDiagnostics, err := p.yamlNamedArgumentDiagnostics(
		ctx,
		document,
	)
	if err != nil {
		return nil, err
	}
	result = append(result, namedArgumentDiagnostics...)
	for _, definition := range definitions {
		if ctx.Err() != nil {
			return nil, nil
		}
		constructors := p.phpIndex.FindMethods(
			definition.className,
			"__construct",
		)
		if len(constructors) == 0 {
			continue
		}
		missing := missingConstructorParameters(
			constructors[0],
			definition,
		)
		if len(missing) == 0 {
			continue
		}

		arguments := make([]map[string]any, 0, len(missing))
		var names []string
		for _, parameter := range missing {
			typeName := injectableObjectType(parameter.Type)
			candidates, exists := serviceSuggestions[typeName]
			if !exists && typeName != "" && p.serviceIndex != nil {
				services, err := p.serviceIndex.GetServicesByType(typeName)
				if err != nil {
					return nil, fmt.Errorf(
						"find services for constructor type %q: %w",
						typeName,
						err,
					)
				}
				for _, service := range services {
					candidates = append(candidates, service.ID)
				}
				serviceSuggestions[typeName] = candidates
			}
			suggestedService := "?"
			if len(candidates) == 1 {
				suggestedService = candidates[0]
			}
			arguments = append(arguments, map[string]any{
				"name":             parameter.Name,
				"type":             parameter.Type.String(),
				"suggestedService": suggestedService,
				"candidates":       candidates,
			})
			names = append(names, parameter.Name)
		}

		label := definition.serviceID
		if label == "" {
			label = definition.className
		}
		result = append(result, lsp.Problem{
			Range: valueNodeTextRange(definition.node, definition.className),
			Message: fmt.Sprintf(
				"Service '%s' is missing required constructor arguments: %s",
				label,
				strings.Join(names, ", "),
			),
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       missingServiceArgumentsCode,
			Payload: map[string]any{
				"serviceID":        definition.serviceID,
				"className":        definition.className,
				"format":           definition.format,
				"missingArguments": arguments,
			},
		})
	}
	return result, nil
}

func (p *ServiceArgumentAnalyzer) configuredServiceMethodDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	var references []symfony.ServiceMethodReference
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		references = symfony.YAMLServiceMethodReferences(
			document.SyntaxTree.Root,
		)
	case ".xml":
		references = symfony.XMLServiceMethodReferences(
			document.SyntaxTree.Root,
		)
	default:
		return nil, nil
	}
	if len(references) == 0 {
		return nil, nil
	}
	localServices, err := configuredServices(document)
	if err != nil {
		return nil, err
	}

	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return result, nil
		}
		receiverService, receiverClass := reference.Receiver()
		className, found, resolveErr := symfony.ResolveConfiguredServiceClass(
			receiverService,
			receiverClass,
			localServices,
			p.serviceIndex,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !found ||
			len(p.phpIndex.FindMethods(
				className,
				reference.MethodName,
			)) != 0 {
			continue
		}
		_, classFound := p.phpIndex.FindClass(className)
		if !classFound {
			continue
		}
		names := publicPHPMethodNames(p.phpIndex, className)
		data := map[string]any{
			"className":  className,
			"methodName": reference.MethodName,
			"suggestions": suggestion.Similar(
				reference.MethodName,
				names,
			),
		}
		result = append(result, lsp.Problem{
			Range:    reference.Range,
			Message:  "Missing Method",
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       missingConfiguredServiceMethodCode,
			Payload:  data,
		})
	}
	return result, nil
}

func publicPHPMethodNames(
	index *php.PHPIndex,
	className string,
) []string {
	if index == nil || className == "" {
		return nil
	}
	members := (phpresolver.MemberResolver{
		Snapshot: index.SemanticSnapshot(),
	}).All(types.Named(className))
	seen := make(map[string]struct{})
	var result []string
	for _, member := range members {
		symbol := member.Symbol
		if symbol.Kind != semantic.MethodSymbol ||
			symbol.Visibility != semantic.Public {
			continue
		}
		key := strings.ToLower(symbol.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, symbol.Name)
	}
	sort.Strings(result)
	return result
}

const invalidServiceArgumentTypeCode lsp.DiagnosticID = "symfony.service.argument.type"

func (p *ServiceArgumentAnalyzer) serviceArgumentTypeDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	references, err := configuredServiceArgumentReferences(document)
	if err != nil {
		return nil, err
	}
	if len(references) == 0 {
		return nil, nil
	}
	localServices, err := configuredServices(document)
	if err != nil {
		return nil, err
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	relations := snapshot.Relations()
	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		ownerClass, found, err := symfony.ResolveConfiguredServiceClass(
			reference.OwnerServiceID,
			reference.OwnerClass,
			localServices,
			p.serviceIndex,
		)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		parameter, found := configuredTargetParameter(
			p.phpIndex,
			ownerClass,
			reference,
		)
		if !found || !hasNamedObjectType(parameter.Type) {
			continue
		}
		actualClass, found, err := symfony.ResolveConfiguredServiceClass(
			reference.ServiceID,
			"",
			localServices,
			p.serviceIndex,
		)
		if err != nil {
			return nil, err
		}
		if !found || !relations.IsAssignableTo(
			types.Named(actualClass),
			parameter.Type,
		) {
			if !found {
				continue
			}
			suggestions, suggestionErr := p.compatibleServiceSuggestions(
				parameter.Type,
				localServices,
				reference,
			)
			if suggestionErr != nil {
				return nil, suggestionErr
			}
			result = append(result, lsp.Problem{
				Range:   reference.Range,
				Message: "Expect instance of: " + parameter.Type.String(),
				Source:  "symfony",
				Severity: protocol.
					DiagnosticSeverityWarning,
				ID: invalidServiceArgumentTypeCode,
				Payload: map[string]any{
					"expectedType": parameter.Type.String(),
					"actualClass":  actualClass,
					"suggestions":  suggestions,
				},
			})
		}
	}
	return result, nil
}

func configuredServiceArgumentReferences(
	document *lsp.TextDocument,
) ([]symfony.ServiceArgumentReference, error) {
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		return symfony.YAMLServiceArgumentReferences(document.SyntaxTree.Root), nil
	case ".xml":
		return symfony.XMLServiceArgumentReferences(document.SyntaxTree.Root), nil
	case ".php":
		return symfony.PHPServiceArgumentReferences(
			document.URI,
			document.SyntaxTree.Root,
			document.LineIndex,
		)
	default:
		return nil, nil
	}
}

func configuredServices(
	document *lsp.TextDocument,
) (map[string]symfony.Service, error) {
	var services []symfony.Service
	var err error
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		services, _, err = symfony.ParseYAMLServicesTree(
			document.URI,
			document.SyntaxTree,
			document.LineIndex,
		)
	case ".xml":
		services, _, err = symfony.ParseXMLServicesTree(
			document.URI,
			document.SyntaxTree,
			document.LineIndex,
		)
	case ".php":
		services, _, err = symfony.ParsePHPServicesTree(
			document.URI,
			document.SyntaxTree.Root,
			document.LineIndex,
		)
	}
	if err != nil {
		return nil, err
	}
	result := make(map[string]symfony.Service, len(services))
	for _, service := range services {
		if service.ID != "" {
			result[service.ID] = service
		}
	}
	return result, nil
}

func staticConfiguredClass(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\\"))
	if value == "" || strings.ContainsAny(value, "%${}@ \t") {
		return ""
	}
	return value
}

func configuredTargetParameter(
	phpIndex *php.PHPIndex,
	className string,
	reference symfony.ServiceArgumentReference,
) (semantic.Parameter, bool) {
	methodName := reference.MethodName
	if methodName == "" {
		methodName = "__construct"
	}
	for _, method := range phpIndex.FindMethods(className, methodName) {
		if reference.ParameterName != "" {
			name := normalizeArgumentName(reference.ParameterName)
			for _, parameter := range method.Parameters {
				if normalizeArgumentName(parameter.Name) == name {
					return parameter, true
				}
			}
			continue
		}
		if reference.ParameterIndex >= 0 &&
			reference.ParameterIndex < len(method.Parameters) {
			return method.Parameters[reference.ParameterIndex], true
		}
	}
	return semantic.Parameter{}, false
}

func hasNamedObjectType(value types.Type) bool {
	switch value.Kind() {
	case types.ObjectKind:
		return value.Name() != ""
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			if hasNamedObjectType(member) {
				return true
			}
		}
	}
	return false
}

func namedObjectTypes(value types.Type) []string {
	switch value.Kind() {
	case types.ObjectKind:
		if value.Name() != "" {
			return []string{value.Name()}
		}
	case types.UnionKind, types.IntersectionKind:
		var result []string
		for _, member := range value.Arguments() {
			result = append(result, namedObjectTypes(member)...)
		}
		return result
	}
	return nil
}

func (p *ServiceArgumentAnalyzer) compatibleServiceSuggestions(
	expected types.Type,
	local map[string]symfony.Service,
	reference symfony.ServiceArgumentReference,
) ([]string, error) {
	relations := p.phpIndex.SemanticSnapshot().Relations()
	candidates := make(map[string]string)
	if p.serviceIndex != nil {
		for _, typeName := range namedObjectTypes(expected) {
			services, err := p.serviceIndex.GetServicesByType(typeName)
			if err != nil {
				return nil, err
			}
			for _, service := range services {
				if className := staticConfiguredClass(service.Class); className != "" &&
					relations.IsAssignableTo(types.Named(className), expected) {
					candidates[service.ID] = className
				}
			}
		}
	}
	for serviceID, service := range local {
		className := staticConfiguredClass(service.Class)
		if className != "" &&
			relations.IsAssignableTo(types.Named(className), expected) {
			candidates[serviceID] = className
		}
	}
	result := make([]string, 0, len(candidates))
	for serviceID, className := range candidates {
		result = append(
			result,
			formatCompatibleServiceSuggestion(
				serviceID,
				className,
				reference,
			),
		)
	}
	sort.Strings(result)
	return result, nil
}

func formatCompatibleServiceSuggestion(
	serviceID,
	className string,
	reference symfony.ServiceArgumentReference,
) string {
	switch reference.Replacement {
	case "php_helper_string":
		return serviceID
	case "php_helper_expression":
		if serviceID == className && className != "" {
			return "\\" + className + "::class"
		}
		return phpSingleQuotedString(serviceID)
	case "php_raw_string":
		return "@" + serviceID
	case "php_raw_expression":
		if serviceID == className && className != "" {
			return "service(\\" + className + "::class)"
		}
		return "service(" + phpSingleQuotedString(serviceID) + ")"
	}
	if reference.Format == "yaml" {
		return "@" + serviceID
	}
	return serviceID
}

func phpSingleQuotedString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "'", "\\'")
	return "'" + value + "'"
}

func (p *ServiceArgumentAnalyzer) yamlNamedArgumentDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
	default:
		return nil, nil
	}
	var result []lsp.Problem
	for _, argument := range symfony.YAMLServiceNamedArguments(
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if argument.HasFactory {
			continue
		}
		className, found, err := symfony.ResolveYAMLServiceNamedArgumentClass(
			p.serviceIndex,
			argument,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve service class for named argument %q: %w",
				argument.Name,
				err,
			)
		}
		if !found {
			continue
		}
		if _, classFound := p.phpIndex.FindClass(className); !classFound {
			continue
		}
		constructors := p.phpIndex.FindMethods(className, "__construct")
		name := normalizeArgumentName(argument.Name)
		var candidates []string
		valid := false
		for _, constructor := range constructors {
			for _, parameter := range constructor.Parameters {
				candidate := "$" + normalizeArgumentName(parameter.Name)
				candidates = append(candidates, candidate)
				if normalizeArgumentName(parameter.Name) == name {
					valid = true
				}
			}
		}
		if valid {
			continue
		}
		result = append(result, lsp.Problem{
			Range:    argument.Range,
			Message:  "Symfony: named argument does not exists",
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       unknownServiceNamedArgumentCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					argument.Name,
					candidates,
				),
			},
		})
	}
	return result, nil
}

func xmlServiceArgumentDefinitions(root *cst.Node) []serviceArgumentDefinition {
	var result []serviceArgumentDefinition
	for _, services := range xmlquery.Elements(root, "services") {
		defaultAutowire := false
		if defaults := xmlquery.ChildElement(services, "defaults"); defaults != nil {
			defaultAutowire = strings.EqualFold(
				xmlquery.AttributeValue(xmlquery.Attribute(defaults, "autowire")),
				"true",
			)
		}
		for _, service := range xmlquery.ChildElements(services, "service") {
			attributes := xmlquery.AttributeValues(service)
			if unsupportedXMLServiceConfiguration(attributes) ||
				xmlquery.ChildElement(service, "factory") != nil {
				continue
			}
			autowire := defaultAutowire
			switch strings.ToLower(attributes["autowire"]) {
			case "true":
				autowire = true
			case "false":
				autowire = false
			}
			if autowire {
				continue
			}
			className := attributes["class"]
			if className == "" && strings.Contains(attributes["id"], "\\") {
				className = attributes["id"]
			}
			className = strings.TrimPrefix(className, "\\")
			if className == "" || strings.ContainsAny(className, "%${}") {
				continue
			}
			named := make(map[string]struct{})
			arguments := xmlquery.ChildElements(service, "argument")
			positionalCount := 0
			for _, argument := range arguments {
				if key := xmlquery.AttributeValue(
					xmlquery.Attribute(argument, "key"),
				); key != "" {
					named[normalizeArgumentName(key)] = struct{}{}
				} else {
					positionalCount++
				}
			}
			result = append(result, serviceArgumentDefinition{
				node:            service,
				serviceID:       attributes["id"],
				className:       className,
				positionalCount: positionalCount,
				namedArguments:  named,
				format:          "xml",
			})
		}
	}
	return result
}

func unsupportedXMLServiceConfiguration(attributes map[string]string) bool {
	for key := range unsupportedServiceConfigurationKeys {
		if _, exists := attributes[strings.ReplaceAll(key, "_", "-")]; exists {
			return true
		}
	}
	return false
}

func yamlServiceArgumentDefinitions(root *cst.Node) []serviceArgumentDefinition {
	mapping := yamlquery.RootValue(root)
	services := yamlquery.Property(mapping, "services")
	if !yamlquery.IsMapping(services) {
		return nil
	}
	defaultAutowire := yamlBooleanProperty(
		yamlquery.PairValue(yamlquery.PropertyPair(services, "_defaults")),
		"autowire",
	)

	var result []serviceArgumentDefinition
	for _, pair := range yamlquery.Pairs(services) {
		key := yamlquery.PairKey(pair)
		serviceID := yamlquery.ScalarValue(key)
		if serviceID == "" || strings.HasPrefix(serviceID, "_") {
			continue
		}
		config := yamlquery.PairValue(pair)
		if !yamlquery.IsMapping(config) ||
			unsupportedYAMLServiceConfiguration(config) {
			continue
		}
		autowire := defaultAutowire
		if property := yamlquery.Property(config, "autowire"); property != nil {
			autowire = strings.EqualFold(yamlquery.ScalarValue(property), "true")
		}
		if autowire {
			continue
		}
		className := yamlquery.ScalarValue(yamlquery.Property(config, "class"))
		if className == "" && strings.Contains(serviceID, "\\") {
			className = serviceID
		}
		className = strings.TrimPrefix(className, "\\")
		if className == "" || strings.ContainsAny(className, "%${}") {
			continue
		}

		positionalCount := 0
		named := make(map[string]struct{})
		if arguments := yamlquery.Property(config, "arguments"); arguments != nil {
			switch {
			case yamlquery.IsSequence(arguments):
				positionalCount = len(yamlquery.Items(arguments))
			case yamlquery.IsMapping(arguments):
				for _, argument := range yamlquery.Pairs(arguments) {
					named[normalizeArgumentName(
						yamlquery.ScalarValue(yamlquery.PairKey(argument)),
					)] = struct{}{}
				}
			default:
				continue
			}
		}
		result = append(result, serviceArgumentDefinition{
			node:            key,
			serviceID:       serviceID,
			className:       className,
			positionalCount: positionalCount,
			namedArguments:  named,
			format:          "yaml",
		})
	}
	return result
}

func yamlBooleanProperty(mapping *cst.Node, name string) bool {
	if !yamlquery.IsMapping(mapping) {
		return false
	}
	return strings.EqualFold(
		yamlquery.ScalarValue(yamlquery.Property(mapping, name)),
		"true",
	)
}

func unsupportedYAMLServiceConfiguration(mapping *cst.Node) bool {
	for key := range unsupportedServiceConfigurationKeys {
		if yamlquery.Property(mapping, key) != nil {
			return true
		}
	}
	return false
}

func missingConstructorParameters(
	constructor semantic.Symbol,
	definition serviceArgumentDefinition,
) []semantic.Parameter {
	var required []semantic.Parameter
	for _, parameter := range constructor.Parameters {
		if !parameter.Optional && !parameter.Flags.Has(semantic.VariadicFlag) {
			required = append(required, parameter)
		}
	}
	var missing []semantic.Parameter
	for index, parameter := range required {
		if index < definition.positionalCount {
			continue
		}
		name := normalizeArgumentName(parameter.Name)
		if _, exists := definition.namedArguments[name]; exists {
			continue
		}
		missing = append(missing, parameter)
	}
	return missing
}

func normalizeArgumentName(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "$")
}

func injectableObjectType(value types.Type) string {
	if value.Kind() == types.ObjectKind {
		return value.Name()
	}
	if value.Kind() != types.UnionKind {
		return ""
	}
	var result string
	for _, member := range value.Arguments() {
		if member.Kind() != types.ObjectKind || member.Name() == "" {
			continue
		}
		if result != "" {
			return ""
		}
		result = member.Name()
	}
	return result
}
