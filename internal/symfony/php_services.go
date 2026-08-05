package symfony

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	php "github.com/shopware/shopware-lsp/internal/php"
)

const containerConfiguratorClass = "Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ContainerConfigurator"

// ServicePrototype is a PSR-4 service registration declared with load().
// Resource and exclude paths are normalized relative to the config file.
type ServicePrototype struct {
	Namespace      string
	Resource       string
	Excludes       []string
	Autowire       bool
	AutowireSet    bool
	Tags           map[string]string
	InstanceofTags map[string]string
	Path           string
	Line           int
	Range          cst.TextRange
	NamespaceRange cst.TextRange
	ResourceRange  cst.TextRange
}

var configuratorCallbackPattern = regexp.MustCompile(`(?i)\bfunction\s*\(([^)]*)\)`)
var typedCallbackParameterPattern = regexp.MustCompile(
	`(?i)(\\?[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff\\]*)\s+(\$[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)`,
)

// ParsePHPServices parses Symfony's PHP service-configurator format.
//
// The parser evaluates only statically knowable configuration. This makes it
// safe for incomplete editor input and avoids inventing service IDs for
// variables, interpolated strings, or arbitrary PHP return values.
func ParsePHPServices(path string, data []byte) ([]Service, []Parameter, error) {
	result := phpparser.ParseBytes(data)
	return ParsePHPServicesTree(path, result.Tree.Root, phpsyntax.NewLineIndex(string(data)))
}

// ParsePHPServicesTree reuses the shared PHP CST and line index owned by the
// workspace scanner.
func ParsePHPServicesTree(
	path string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) ([]Service, []Parameter, error) {
	config, err := parsePHPServiceConfigTree(path, root, lineIndex)
	return config.Services, config.Parameters, err
}

// PHPServiceArgumentReferences returns statically knowable service references
// supplied through Symfony's PHP configurator API or legacy array definitions.
func PHPServiceArgumentReferences(
	path string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) ([]ServiceArgumentReference, error) {
	if root == nil {
		return nil, nil
	}
	source := root.Text()
	hasFluentArguments := strings.Contains(source, "->arg")
	hasLegacyArguments := strings.Contains(source, "'arguments'") ||
		strings.Contains(source, `"arguments"`)
	if !hasFluentArguments && !hasLegacyArguments {
		return nil, nil
	}

	var references []ServiceArgumentReference
	if hasFluentArguments {
		config, err := parsePHPServiceConfigTreeWithReferences(
			path,
			root,
			lineIndex,
			true,
		)
		if err != nil {
			return nil, err
		}
		references = append(references, config.References...)
		references = append(
			references,
			directPHPServiceArgumentReferences(path, root)...,
		)
	}
	if hasLegacyArguments {
		references = append(
			references,
			legacyPHPServiceArgumentReferences(path, root)...,
		)
	}
	return uniqueServiceArgumentReferences(references), nil
}

type phpServiceConfig struct {
	Services   []Service
	Parameters []Parameter
	Prototypes []ServicePrototype
	References []ServiceArgumentReference
}

func parsePHPServiceConfigTree(
	path string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) (phpServiceConfig, error) {
	config, err := parsePHPServiceConfigTreeWithReferences(
		path,
		root,
		lineIndex,
		false,
	)
	if err != nil {
		return phpServiceConfig{}, err
	}
	return mergePHPServiceConfigs(
		config,
		parsePHPArrayServiceConfig(path, root, lineIndex),
	), nil
}

func parsePHPServiceConfigTreeWithReferences(
	path string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
	collectReferences bool,
) (phpServiceConfig, error) {
	if root == nil || lineIndex == nil {
		return phpServiceConfig{
			Services:   []Service{},
			Parameters: []Parameter{},
			Prototypes: []ServicePrototype{},
		}, nil
	}

	resolver := php.NewNameResolver(root)
	callbacks := configuratorCallbacks(root, resolver)
	if len(callbacks) == 0 {
		return phpServiceConfig{
			Services:   []Service{},
			Parameters: []Parameter{},
			Prototypes: []ServicePrototype{},
		}, nil
	}

	evaluator := phpServiceEvaluator{
		path:               path,
		lineIndex:          lineIndex,
		resolver:           resolver,
		services:           make(map[string]Service),
		parameters:         make(map[string]Parameter),
		serviceVariables:   make(map[string]struct{}),
		parameterVariables: make(map[string]struct{}),
		definitionVars:     make(map[string]string),
		defaultTags:        make(map[string]string),
		prototypes:         make([]ServicePrototype, 0),
		collectReferences:  collectReferences,
	}
	for _, callback := range callbacks {
		evaluator.containerVariable = callback.containerVariable
		evaluator.serviceVariables = make(map[string]struct{})
		evaluator.parameterVariables = make(map[string]struct{})
		evaluator.definitionVars = make(map[string]string)
		evaluator.defaultTags = make(map[string]string)
		evaluator.instanceofTags = make(map[string]string)
		evaluator.defaultAutowire = false
		evaluator.defaultAutowireSet = false
		evaluator.evaluate(callback.body)
	}

	services := make([]Service, 0, len(evaluator.services))
	for _, service := range evaluator.services {
		services = append(services, service)
	}
	sort.Slice(services, func(left, right int) bool {
		if services[left].Line == services[right].Line {
			return services[left].ID < services[right].ID
		}
		return services[left].Line < services[right].Line
	})

	parameters := make([]Parameter, 0, len(evaluator.parameters))
	for _, parameter := range evaluator.parameters {
		parameters = append(parameters, parameter)
	}
	sort.Slice(parameters, func(left, right int) bool {
		if parameters[left].Line == parameters[right].Line {
			return parameters[left].Name < parameters[right].Name
		}
		return parameters[left].Line < parameters[right].Line
	})
	return phpServiceConfig{
		Services:   services,
		Parameters: parameters,
		Prototypes: evaluator.prototypes,
		References: evaluator.references,
	}, nil
}

type configuratorCallback struct {
	containerVariable string
	body              *phpsyntax.Node
}

func configuratorCallbacks(root *phpsyntax.Node, resolver *php.NameResolver) []configuratorCallback {
	source := root.Text()
	matches := configuratorCallbackPattern.FindAllStringSubmatchIndex(source, -1)
	blocks := phpquery.Nodes(root, phpsyntax.PhpBlock)
	callbacks := make([]configuratorCallback, 0, len(matches))
	claimed := make(map[*phpsyntax.Node]struct{})

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		parameters := source[match[2]:match[3]]
		var containerVariable string
		for _, parameter := range typedCallbackParameterPattern.FindAllStringSubmatch(parameters, -1) {
			if resolver.Resolve(parameter[1]) == containerConfiguratorClass {
				containerVariable = parameter[2]
				break
			}
		}
		if containerVariable == "" {
			continue
		}
		var body *phpsyntax.Node
		for _, block := range blocks {
			if int(block.Range().Start) < match[1] {
				continue
			}
			if body == nil || block.Range().Start < body.Range().Start {
				body = block
			}
		}
		if body == nil {
			continue
		}
		if _, exists := claimed[body]; exists {
			continue
		}
		claimed[body] = struct{}{}
		callbacks = append(callbacks, configuratorCallback{
			containerVariable: containerVariable,
			body:              body,
		})
	}
	return callbacks
}

type phpServiceEvaluator struct {
	path              string
	lineIndex         *phpsyntax.LineIndex
	resolver          *php.NameResolver
	containerVariable string

	services           map[string]Service
	parameters         map[string]Parameter
	serviceVariables   map[string]struct{}
	parameterVariables map[string]struct{}
	definitionVars     map[string]string
	defaultTags        map[string]string
	instanceofTags     map[string]string
	defaultAutowire    bool
	defaultAutowireSet bool
	prototypes         []ServicePrototype
	collectReferences  bool
	references         []ServiceArgumentReference
}

func (e *phpServiceEvaluator) evaluate(body *phpsyntax.Node) {
	for _, statement := range phpquery.ExpressionStatements(body) {
		e.evaluateStatement(statement)
	}
}

func (e *phpServiceEvaluator) evaluateStatement(statement *phpsyntax.Node) {
	calls := phpquery.Calls(statement)
	if len(calls) == 0 {
		return
	}

	assignedVariable := phpquery.AssignedVariable(statement)
	if e.isAccessorAssignment(calls, "services") {
		if assignedVariable != "" {
			e.serviceVariables[assignedVariable] = struct{}{}
		}
		return
	}
	if e.isAccessorAssignment(calls, "parameters") {
		if assignedVariable != "" {
			e.parameterVariables[assignedVariable] = struct{}{}
		}
		return
	}

	if parameterSet := e.rootedCall(calls, e.parameterVariables, "parameters", "set"); parameterSet != nil {
		e.setParameter(parameterSet)
		return
	}

	if remove := e.rootedCall(calls, e.serviceVariables, "services", "remove"); remove != nil {
		if id, ok := e.argumentValue(remove, 0); ok {
			delete(e.services, id)
			for variable, definitionID := range e.definitionVars {
				if definitionID == id {
					delete(e.definitionVars, variable)
				}
			}
		}
		return
	}

	if defaults := e.rootedCall(calls, e.serviceVariables, "services", "defaults"); defaults != nil {
		for _, autowireCall := range methodCalls(calls, "autowire") {
			if !e.callHasConfigurator(
				phpquery.CallName(autowireCall),
				"defaults",
			) {
				continue
			}
			e.defaultAutowire, e.defaultAutowireSet =
				e.phpAutowireCall(autowireCall)
		}
		for _, tagCall := range methodCalls(calls, "tag") {
			if !e.callHasConfigurator(phpquery.CallName(tagCall), "defaults") {
				continue
			}
			if tag, ok := e.argumentValue(tagCall, 0); ok {
				e.defaultTags[tag] = ""
			}
		}
		return
	}

	if instanceOf := e.rootedCall(calls, e.serviceVariables, "services", "instanceof"); instanceOf != nil {
		typeName, typeOK := e.argumentValue(instanceOf, 0)
		if !typeOK {
			return
		}
		for _, tagCall := range methodCalls(calls, "tag") {
			if !e.callHasConfigurator(phpquery.CallName(tagCall), "instanceof") {
				continue
			}
			if tag, ok := e.argumentValue(tagCall, 0); ok {
				e.instanceofTags[tag] = typeName
			}
		}
		return
	}

	if load := e.rootedCall(calls, e.serviceVariables, "services", "load"); load != nil {
		e.setPrototype(load, calls)
		return
	}

	definitionID := ""
	if set := e.rootedCall(calls, e.serviceVariables, "services", "set"); set != nil {
		definitionID = e.setService(set)
	} else if invoke := e.invokedServicesCall(calls); invoke != nil {
		definitionID = e.setService(invoke)
	} else if alias := e.rootedCall(calls, e.serviceVariables, "services", "alias"); alias != nil {
		definitionID = e.setAlias(alias)
	} else if stack := e.rootedCall(calls, e.serviceVariables, "services", "stack"); stack != nil {
		definitionID = e.setStack(stack)
	} else if get := e.rootedCall(calls, e.serviceVariables, "services", "get"); get != nil {
		definitionID, _ = e.argumentValue(get, 0)
	} else {
		definitionID = e.definitionForCalls(calls)
	}
	if definitionID == "" {
		return
	}

	if assignedVariable != "" {
		e.definitionVars[assignedVariable] = definitionID
	}
	service, exists := e.services[definitionID]
	if !exists {
		return
	}
	for _, classCall := range methodCalls(calls, "class") {
		if !e.callTargetsDefinition(classCall, definitionID) {
			continue
		}
		if className, ok := e.argumentValue(classCall, 0); ok {
			service.Class = className
		}
	}
	if service.Tags == nil {
		service.Tags = make(map[string]string)
	}
	for _, tagCall := range methodCalls(calls, "tag") {
		if e.callTargetsDefinition(tagCall, definitionID) {
			if tag, ok := e.argumentValue(tagCall, 0); ok {
				service.Tags[tag] = ""
			}
		}
	}
	for _, autowireCall := range methodCalls(calls, "autowire") {
		if !e.callTargetsDefinition(autowireCall, definitionID) {
			continue
		}
		service.Autowire, service.AutowireSet =
			e.phpAutowireCall(autowireCall)
	}
	for _, decorateCall := range methodCalls(calls, "decorate") {
		if !e.callTargetsDefinition(decorateCall, definitionID) {
			continue
		}
		if target, ok := e.argumentValue(decorateCall, 0); ok {
			service.Decorates = target
			service.DecoratesRange = phpArgumentContentRange(
				decorateCall,
				0,
			)
		}
	}
	for _, parentCall := range methodCalls(calls, "parent") {
		if !e.callTargetsDefinition(parentCall, definitionID) {
			continue
		}
		if target, ok := e.argumentValue(parentCall, 0); ok {
			service.Parent = target
			service.ParentRange = phpArgumentContentRange(parentCall, 0)
		}
	}
	for _, deprecateCall := range methodCalls(calls, "deprecate") {
		if !e.callTargetsDefinition(deprecateCall, definitionID) {
			continue
		}
		service.Deprecated = true
		service.DeprecatedRange = deprecateCall.RangeTrimmedTrivia()
		if message, ok := e.argumentValue(deprecateCall, 2); ok {
			service.Deprecation = message
		}
	}
	if e.collectReferences {
		e.references = append(
			e.references,
			e.serviceArgumentReferences(service, calls)...,
		)
	}
	e.services[definitionID] = service
}

func (e *phpServiceEvaluator) serviceArgumentReferences(
	service Service,
	calls []*phpsyntax.Node,
) []ServiceArgumentReference {
	base := ServiceArgumentReference{
		OwnerServiceID: service.ID,
		OwnerClass:     service.Class,
		ParameterIndex: -1,
		Format:         "php",
	}
	return phpServiceArgumentReferencesForCalls(
		base,
		calls,
		e.resolver,
		e.path,
		func(call *phpsyntax.Node) bool {
			return e.callTargetsDefinition(call, service.ID)
		},
	)
}

func phpServiceArgumentReferencesForCalls(
	base ServiceArgumentReference,
	calls []*phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
	accept func(*phpsyntax.Node) bool,
) []ServiceArgumentReference {
	var result []ServiceArgumentReference
	for _, argsCall := range methodCalls(calls, "args") {
		if !accept(argsCall) {
			continue
		}
		expression := phpquery.ArgumentExpression(argsCall, 0)
		if expression == nil || expression.Kind() != phpsyntax.PhpArray {
			continue
		}
		for index, item := range phpquery.ArrayItems(expression) {
			reference, found := phpServiceReferenceExpression(
				phpquery.ArrayItemValue(item),
				resolver,
				path,
				base,
			)
			if !found {
				continue
			}
			reference.ParameterIndex = index
			if key := phpquery.ArrayItemKey(item); key != nil {
				if value, ok := staticPHPValue(
					key.Text(),
					resolver,
					path,
				); ok {
					switch {
					case strings.HasPrefix(value, "$"):
						reference.ParameterName = value
						reference.ParameterIndex = -1
					case numericIndex(value) >= 0:
						reference.ParameterIndex = numericIndex(value)
					}
				}
			}
			result = append(result, reference)
		}
	}
	for _, argCall := range methodCalls(calls, "arg") {
		if !accept(argCall) {
			continue
		}
		reference, found := phpServiceReferenceExpression(
			phpquery.ArgumentExpression(argCall, 1),
			resolver,
			path,
			base,
		)
		if !found {
			continue
		}
		if key, ok := staticPHPValue(
			phpquery.ArgumentValueText(argCall, 0),
			resolver,
			path,
		); ok {
			switch {
			case strings.HasPrefix(key, "$"):
				reference.ParameterName = key
			case numericIndex(key) >= 0:
				reference.ParameterIndex = numericIndex(key)
			}
		}
		result = append(result, reference)
	}
	return result
}

func phpServiceReferenceExpression(
	expression *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
	base ServiceArgumentReference,
) (ServiceArgumentReference, bool) {
	if expression == nil {
		return ServiceArgumentReference{}, false
	}
	for _, call := range phpquery.Calls(expression) {
		name := strings.ToLower(phpquery.CallMethodName(call))
		if name != "service" && name != "ref" {
			continue
		}
		serviceID, ok := staticPHPValue(
			phpquery.ArgumentValueText(call, 0),
			resolver,
			path,
		)
		if !ok {
			return ServiceArgumentReference{}, false
		}
		if normalized, _, parsed := ParseServiceReference(serviceID); parsed {
			serviceID = normalized
		}
		if serviceID == "" || strings.ContainsAny(serviceID, "%${}") {
			return ServiceArgumentReference{}, false
		}
		base.ServiceID = serviceID
		argument := phpquery.ArgumentExpression(call, 0)
		base.Range = argument.RangeTrimmedTrivia()
		base.Replacement = "php_helper_expression"
		if argument.Kind() == phpsyntax.PhpString {
			base.Range = phpArgumentContentRange(call, 0)
			base.Replacement = "php_helper_string"
		}
		return base, true
	}

	value, ok := staticPHPValue(expression.Text(), resolver, path)
	if !ok {
		return ServiceArgumentReference{}, false
	}
	if serviceID, _, parsed := ParseServiceReference(value); parsed {
		base.ServiceID = serviceID
		base.Range = phpExpressionContentRange(expression)
		base.Replacement = "php_raw_string"
		return base, true
	}
	if phpquery.ClassConstantName(expression) == "" {
		return ServiceArgumentReference{}, false
	}
	base.ServiceID = value
	base.Range = expression.RangeTrimmedTrivia()
	base.Replacement = "php_raw_expression"
	return base, true
}

func phpExpressionContentRange(expression *phpsyntax.Node) cst.TextRange {
	if expression == nil {
		return cst.TextRange{}
	}
	rng := expression.RangeTrimmedTrivia()
	text := strings.TrimSpace(expression.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func legacyPHPServiceArgumentReferences(
	path string,
	root *phpsyntax.Node,
) []ServiceArgumentReference {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []ServiceArgumentReference
	for _, array := range phpquery.Arrays(root) {
		for _, definition := range phpquery.ArrayItems(array) {
			key := phpquery.ArrayItemKey(definition)
			config := phpquery.ArrayItemValue(definition)
			if key == nil || config == nil ||
				config.Kind() != phpsyntax.PhpArray {
				continue
			}
			ownerID, ok := staticPHPValue(key.Text(), resolver, path)
			if !ok || ownerID == "" {
				continue
			}
			ownerClass := ""
			if strings.Contains(ownerID, "\\") {
				ownerClass = ownerID
			}
			for _, option := range phpquery.ArrayItems(config) {
				optionKey := phpquery.ArrayItemKey(option)
				if optionKey == nil {
					continue
				}
				name, static := staticPHPValue(
					optionKey.Text(),
					resolver,
					path,
				)
				if !static || name != "class" {
					continue
				}
				classValue := phpquery.ArrayItemValue(option)
				if classValue == nil {
					continue
				}
				if className, classStatic := staticPHPValue(
					classValue.Text(),
					resolver,
					path,
				); classStatic {
					ownerClass = className
				}
			}
			if ownerClass == "" {
				continue
			}
			for _, option := range phpquery.ArrayItems(config) {
				optionKey := phpquery.ArrayItemKey(option)
				arguments := phpquery.ArrayItemValue(option)
				if optionKey == nil {
					continue
				}
				name, ok := staticPHPValue(optionKey.Text(), resolver, path)
				if !ok || name != "arguments" || arguments == nil ||
					arguments.Kind() != phpsyntax.PhpArray {
					continue
				}
				base := ServiceArgumentReference{
					OwnerServiceID: ownerID,
					OwnerClass:     ownerClass,
					ParameterIndex: -1,
					Format:         "php",
				}
				for index, item := range phpquery.ArrayItems(arguments) {
					reference, found := phpServiceReferenceExpression(
						phpquery.ArrayItemValue(item),
						resolver,
						path,
						base,
					)
					if !found {
						continue
					}
					reference.ParameterIndex = index
					if argumentKey := phpquery.ArrayItemKey(item); argumentKey != nil {
						value, static := staticPHPValue(
							argumentKey.Text(),
							resolver,
							path,
						)
						if !static {
							continue
						}
						switch {
						case strings.HasPrefix(value, "$"):
							reference.ParameterName = value
							reference.ParameterIndex = -1
						case numericIndex(value) >= 0:
							reference.ParameterIndex = numericIndex(value)
						default:
							continue
						}
					}
					result = append(result, reference)
				}
			}
		}
	}
	return result
}

func directPHPServiceArgumentReferences(
	path string,
	root *phpsyntax.Node,
) []ServiceArgumentReference {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []ServiceArgumentReference
	for _, statement := range phpquery.ExpressionStatements(root) {
		calls := phpquery.Calls(statement)
		var setCall *phpsyntax.Node
		for _, candidate := range methodCalls(calls, "set") {
			if strings.Contains(
				strings.ToLower(phpquery.CallName(candidate)),
				"->services()->set",
			) {
				setCall = candidate
				break
			}
		}
		if setCall == nil {
			continue
		}
		ownerID, ok := staticPHPValue(
			phpquery.ArgumentValueText(setCall, 0),
			resolver,
			path,
		)
		if !ok || ownerID == "" {
			continue
		}
		ownerClass := ownerID
		if value := phpquery.ArgumentValueText(setCall, 1); value != "" {
			if className, static := staticPHPValue(value, resolver, path); static {
				ownerClass = className
			}
		}
		if !strings.Contains(ownerClass, "\\") {
			continue
		}
		base := ServiceArgumentReference{
			OwnerServiceID: ownerID,
			OwnerClass:     ownerClass,
			ParameterIndex: -1,
			Format:         "php",
		}
		result = append(
			result,
			phpServiceArgumentReferencesForCalls(
				base,
				calls,
				resolver,
				path,
				func(*phpsyntax.Node) bool { return true },
			)...,
		)
	}
	return result
}

func uniqueServiceArgumentReferences(
	references []ServiceArgumentReference,
) []ServiceArgumentReference {
	type key struct {
		owner          string
		service        string
		method         string
		parameter      string
		parameterIndex int
		start          uint32
		end            uint32
	}
	seen := make(map[key]struct{}, len(references))
	result := make([]ServiceArgumentReference, 0, len(references))
	for _, reference := range references {
		value := key{
			owner:          reference.OwnerServiceID,
			service:        reference.ServiceID,
			method:         reference.MethodName,
			parameter:      reference.ParameterName,
			parameterIndex: reference.ParameterIndex,
			start:          reference.Range.Start,
			end:            reference.Range.End,
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func (e *phpServiceEvaluator) invokedServicesCall(calls []*phpsyntax.Node) *phpsyntax.Node {
	for _, call := range calls {
		name := phpquery.CallName(call)
		for variable := range e.serviceVariables {
			if name == variable {
				return call
			}
		}
		if name == e.containerVariable+"->services()" {
			return call
		}
	}
	return nil
}

func (e *phpServiceEvaluator) callHasConfigurator(name, configurator string) bool {
	for variable := range e.serviceVariables {
		if strings.HasPrefix(name, variable+"->"+configurator+"(") {
			return true
		}
	}
	return strings.HasPrefix(name, e.containerVariable+"->services()->"+configurator+"(")
}

func (e *phpServiceEvaluator) isAccessorAssignment(calls []*phpsyntax.Node, accessor string) bool {
	found := false
	for _, call := range calls {
		if phpquery.CallMethodName(call) != accessor {
			return false
		}
		if phpquery.CallName(call) == e.containerVariable+"->"+accessor {
			found = true
		}
	}
	return found
}

func (e *phpServiceEvaluator) rootedCall(
	calls []*phpsyntax.Node,
	variables map[string]struct{},
	accessor string,
	method string,
) *phpsyntax.Node {
	for _, call := range calls {
		if phpquery.CallMethodName(call) != method {
			continue
		}
		name := phpquery.CallName(call)
		for variable := range variables {
			if strings.HasPrefix(name, variable+"->") {
				return call
			}
		}
		if strings.HasPrefix(name, e.containerVariable+"->"+accessor+"()->") {
			return call
		}
	}
	return nil
}

func (e *phpServiceEvaluator) definitionForCalls(calls []*phpsyntax.Node) string {
	for variable, id := range e.definitionVars {
		for _, call := range calls {
			if strings.HasPrefix(phpquery.CallName(call), variable+"->") {
				return id
			}
		}
	}
	return ""
}

func (e *phpServiceEvaluator) callTargetsDefinition(call *phpsyntax.Node, definitionID string) bool {
	name := phpquery.CallName(call)
	for variable := range e.serviceVariables {
		if strings.HasPrefix(name, variable+"->") || strings.HasPrefix(name, variable+"(") {
			return true
		}
	}
	if strings.HasPrefix(name, e.containerVariable+"->services()->") {
		return true
	}
	for variable, id := range e.definitionVars {
		if id == definitionID && strings.HasPrefix(name, variable+"->") {
			return true
		}
	}
	return false
}

func (e *phpServiceEvaluator) setService(call *phpsyntax.Node) string {
	id, ok := e.argumentValue(call, 0)
	if !ok || id == "" || id == "null" {
		return ""
	}
	className := id
	classRange := phpArgumentContentRange(call, 0)
	if len(phpquery.Arguments(call)) > 1 {
		className = ""
		classRange = cst.TextRange{}
		if value, static := e.argumentValue(call, 1); static {
			if value == "null" {
				className = id
				classRange = phpArgumentContentRange(call, 0)
			} else if value != "" {
				className = value
				classRange = phpArgumentContentRange(call, 1)
			}
		}
	}
	tags := make(map[string]string, len(e.defaultTags))
	for tag, value := range e.defaultTags {
		tags[tag] = value
	}
	e.services[id] = Service{
		ID:             id,
		Class:          className,
		Autowire:       e.defaultAutowire,
		AutowireSet:    e.defaultAutowireSet,
		Tags:           tags,
		InstanceofTags: cloneStringMap(e.instanceofTags),
		Path:           e.path,
		Line:           e.line(call),
		Range:          call.RangeTrimmedTrivia(),
		IDRange:        phpArgumentContentRange(call, 0),
		ClassRange:     classRange,
	}
	return id
}

func (e *phpServiceEvaluator) setPrototype(load *phpsyntax.Node, calls []*phpsyntax.Node) {
	namespace, namespaceOK := e.argumentValue(load, 0)
	resource, resourceOK := e.argumentValue(load, 1)
	if !namespaceOK || !resourceOK || namespace == "" || resource == "" {
		return
	}
	prototype := ServicePrototype{
		Namespace:      strings.TrimPrefix(namespace, "\\"),
		Resource:       e.configPath(resource),
		Autowire:       e.defaultAutowire,
		AutowireSet:    e.defaultAutowireSet,
		Tags:           cloneStringMap(e.defaultTags),
		InstanceofTags: cloneStringMap(e.instanceofTags),
		Path:           e.path,
		Line:           e.line(load),
		Range:          load.RangeTrimmedTrivia(),
		NamespaceRange: phpArgumentContentRange(load, 0),
		ResourceRange:  phpArgumentContentRange(load, 1),
	}
	for _, autowireCall := range methodCalls(calls, "autowire") {
		if !e.callTargetsServiceConfigurator(autowireCall, "load") {
			continue
		}
		prototype.Autowire, prototype.AutowireSet =
			e.phpAutowireCall(autowireCall)
	}
	for _, exclude := range methodCalls(calls, "exclude") {
		if !e.callTargetsServiceConfigurator(exclude, "load") {
			continue
		}
		for _, value := range e.argumentValues(exclude, 0) {
			prototype.Excludes = append(prototype.Excludes, e.configPath(value))
		}
	}
	if prototype.Tags == nil {
		prototype.Tags = make(map[string]string)
	}
	for _, tagCall := range methodCalls(calls, "tag") {
		if !e.callTargetsServiceConfigurator(tagCall, "load") {
			continue
		}
		if tag, ok := e.argumentValue(tagCall, 0); ok {
			prototype.Tags[tag] = ""
		}
	}
	e.prototypes = append(e.prototypes, prototype)
}

func (e *phpServiceEvaluator) phpAutowireCall(
	call *phpsyntax.Node,
) (bool, bool) {
	if call == nil {
		return false, false
	}
	if len(phpquery.Arguments(call)) == 0 {
		return true, true
	}
	value, static := e.argumentValue(call, 0)
	if !static {
		return false, true
	}
	autowire, _ := configuredServiceBool(value)
	return autowire, true
}

func (e *phpServiceEvaluator) callTargetsServiceConfigurator(call *phpsyntax.Node, method string) bool {
	name := phpquery.CallName(call)
	for variable := range e.serviceVariables {
		if strings.HasPrefix(name, variable+"->"+method+"(") {
			return true
		}
	}
	return strings.HasPrefix(name, e.containerVariable+"->services()->"+method+"(")
}

func (e *phpServiceEvaluator) argumentValues(call *phpsyntax.Node, index int) []string {
	argument := phpquery.Argument(call, index)
	if argument == nil {
		return nil
	}
	arrays := phpquery.Arrays(argument)
	if len(arrays) == 0 {
		if value, ok := e.argumentValue(call, index); ok {
			return []string{value}
		}
		return nil
	}
	var values []string
	for _, item := range phpquery.ArrayItems(arrays[0]) {
		if value, ok := staticPHPValue(item.Text(), e.resolver, e.path); ok {
			values = append(values, value)
		}
	}
	return values
}

func (e *phpServiceEvaluator) configPath(value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(e.path), value))
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (e *phpServiceEvaluator) setAlias(call *phpsyntax.Node) string {
	id, idOK := e.argumentValue(call, 0)
	target, targetOK := e.argumentValue(call, 1)
	if !idOK || !targetOK || id == "" || target == "" {
		return ""
	}
	e.services[id] = Service{
		ID:          id,
		AliasTarget: target,
		Path:        e.path,
		Line:        e.line(call),
		Range:       call.RangeTrimmedTrivia(),
		IDRange:     phpArgumentContentRange(call, 0),
	}
	return id
}

func (e *phpServiceEvaluator) setStack(call *phpsyntax.Node) string {
	id, ok := e.argumentValue(call, 0)
	if !ok || id == "" {
		return ""
	}
	e.services[id] = Service{
		ID:      id,
		Tags:    make(map[string]string),
		Path:    e.path,
		Line:    e.line(call),
		Range:   call.RangeTrimmedTrivia(),
		IDRange: phpArgumentContentRange(call, 0),
	}
	return id
}

func (e *phpServiceEvaluator) setParameter(call *phpsyntax.Node) {
	name, ok := e.argumentValue(call, 0)
	if !ok || name == "" {
		return
	}
	value := ""
	if expression := phpquery.ArgumentValueText(call, 1); expression != "" {
		if evaluated, static := staticPHPValue(expression, e.resolver, e.path); static {
			value = evaluated
		} else if isStaticParameterExpression(expression) {
			value = strings.TrimSpace(expression)
		}
	}
	e.parameters[name] = Parameter{
		Name:  name,
		Value: value,
		Path:  e.path,
		Line:  e.line(call),
	}
}

func (e *phpServiceEvaluator) argumentValue(call *phpsyntax.Node, index int) (string, bool) {
	expression := phpquery.ArgumentValueText(call, index)
	if expression == "" {
		return "", false
	}
	return staticPHPValue(expression, e.resolver, e.path)
}

func phpArgumentContentRange(
	call *phpsyntax.Node,
	index int,
) cst.TextRange {
	expression := phpquery.ArgumentExpression(call, index)
	if expression == nil {
		return cst.TextRange{}
	}
	rng := expression.RangeTrimmedTrivia()
	text := strings.TrimSpace(expression.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func (e *phpServiceEvaluator) line(node *phpsyntax.Node) int {
	line, _ := e.lineIndex.Position(node.RangeTrimmedTrivia().Start)
	return int(line) + 1
}

func methodCalls(calls []*phpsyntax.Node, method string) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, call := range calls {
		if phpquery.CallMethodName(call) == method {
			result = append(result, call)
		}
	}
	return result
}

func staticPHPValue(expression string, resolver *php.NameResolver, filePath string) (string, bool) {
	expression = strings.TrimSpace(expression)
	for wrappedByParentheses(expression) {
		expression = strings.TrimSpace(expression[1 : len(expression)-1])
	}
	if expression == "" {
		return "", false
	}

	parts := splitPHPConcatenation(expression)
	if len(parts) > 1 {
		var value strings.Builder
		for _, part := range parts {
			evaluated, ok := staticPHPValue(part, resolver, filePath)
			if !ok {
				return "", false
			}
			value.WriteString(evaluated)
		}
		return value.String(), true
	}

	if value, ok := phpStringLiteral(expression); ok {
		return value, true
	}
	lower := strings.ToLower(expression)
	if strings.HasSuffix(lower, "::class") {
		name := strings.TrimSpace(expression[:len(expression)-len("::class")])
		if !isPHPName(name) || strings.EqualFold(name, "self") ||
			strings.EqualFold(name, "static") || strings.EqualFold(name, "parent") {
			return "", false
		}
		return resolver.Resolve(name), true
	}
	switch lower {
	case "__dir__":
		if filePath == "" {
			return "", false
		}
		return filepath.Dir(filePath), true
	case "__file__":
		if filePath == "" {
			return "", false
		}
		return filePath, true
	case "directory_separator":
		return string(filepath.Separator), true
	case "true", "false", "null":
		return lower, true
	}
	if isPHPNumber(expression) {
		return expression, true
	}
	return "", false
}

func splitPHPConcatenation(expression string) []string {
	var parts []string
	start := 0
	depth := 0
	quote := byte(0)
	escaped := false
	for index := 0; index < len(expression); index++ {
		character := expression[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(expression[start:index]))
				start = index + 1
			}
		}
	}
	if len(parts) == 0 {
		return []string{expression}
	}
	parts = append(parts, strings.TrimSpace(expression[start:]))
	return parts
}

func phpStringLiteral(expression string) (string, bool) {
	if len(expression) < 2 {
		return "", false
	}
	quote := expression[0]
	if (quote != '\'' && quote != '"') || expression[len(expression)-1] != quote {
		return "", false
	}
	body := expression[1 : len(expression)-1]
	var value strings.Builder
	for index := 0; index < len(body); index++ {
		character := body[index]
		if quote == '"' && character == '$' {
			return "", false
		}
		if character != '\\' || index+1 >= len(body) {
			value.WriteByte(character)
			continue
		}
		next := body[index+1]
		if quote == '\'' {
			if next == '\\' || next == '\'' {
				value.WriteByte(next)
				index++
				continue
			}
			value.WriteByte(character)
			continue
		}
		switch next {
		case '\\', '"', '$':
			value.WriteByte(next)
			index++
		case 'n':
			value.WriteByte('\n')
			index++
		case 'r':
			value.WriteByte('\r')
			index++
		case 't':
			value.WriteByte('\t')
			index++
		default:
			// PHP keeps the slash for unknown double-quoted escapes.
			value.WriteByte(character)
		}
	}
	return value.String(), true
}

func wrappedByParentheses(expression string) bool {
	if len(expression) < 2 || expression[0] != '(' || expression[len(expression)-1] != ')' {
		return false
	}
	depth := 0
	quote := byte(0)
	escaped := false
	for index := 0; index < len(expression); index++ {
		character := expression[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && index != len(expression)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func isPHPName(value string) bool {
	value = strings.TrimPrefix(value, "\\")
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, "\\") {
		if part == "" || !isPHPIdentifier(part) {
			return false
		}
	}
	return true
}

func isPHPIdentifier(value string) bool {
	for index, character := range value {
		if character == '_' || character >= 0x80 ||
			(character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return value != ""
}

func isPHPNumber(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= '0' && character <= '9') || character == '_' ||
			character == '.' || character == 'x' || character == 'X' ||
			character == 'b' || character == 'B' ||
			(index == 0 && (character == '+' || character == '-')) {
			continue
		}
		return false
	}
	return true
}

func isStaticParameterExpression(expression string) bool {
	expression = strings.TrimSpace(expression)
	if expression == "" || strings.Contains(expression, "$") ||
		strings.Contains(expression, "->") || strings.Contains(strings.ToLower(expression), "new ") {
		return false
	}
	return (strings.HasPrefix(expression, "[") && strings.HasSuffix(expression, "]")) ||
		(strings.HasPrefix(strings.ToLower(expression), "array(") && strings.HasSuffix(expression, ")"))
}
