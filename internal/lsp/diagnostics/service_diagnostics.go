package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingServiceCode    lsp.DiagnosticID = "symfony.service.missing"
	missingParameterCode  lsp.DiagnosticID = "symfony.parameter.missing"
	missingClassCode      lsp.DiagnosticID = "symfony.class.missing"
	deprecatedServiceCode lsp.DiagnosticID = "symfony.service.deprecated"
	deprecatedClassCode   lsp.DiagnosticID = "symfony.class.deprecated"
)

var containerTypes = []string{
	"Psr\\Container\\ContainerInterface",
	"Symfony\\Component\\DependencyInjection\\ContainerInterface",
	"Symfony\\Contracts\\Service\\ServiceProviderInterface",
}

type ServiceAnalyzer struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewServiceAnalyzer(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ServiceAnalyzer {
	return &ServiceAnalyzer{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

func (p *ServiceAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.serviceIndex == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}

	collector := referenceDiagnosticCollector{
		ctx:      ctx,
		document: document,
		provider: p,
		seen:     make(map[referenceDiagnosticKey]struct{}),
	}
	var err error
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".php":
		err = collector.collectPHP()
	case ".xml":
		err = collector.collectXML()
	case ".yaml", ".yml":
		err = collector.collectYAML()
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return collector.diagnostics, nil
}

type referenceDiagnosticKey struct {
	start uint32
	end   uint32
	code  lsp.DiagnosticID
	name  string
}

type referenceDiagnosticCollector struct {
	ctx              context.Context
	document         *lsp.TextDocument
	provider         *ServiceAnalyzer
	diagnostics      []lsp.Problem
	seen             map[referenceDiagnosticKey]struct{}
	localServices    map[string]symfony.Service
	localParameters  map[string]struct{}
	phpDocument      *semantic.Document
	phpSnapshot      *semantic.Snapshot
	serviceNames     []string
	parameterNames   []string
	classNames       []string
	servicesLoaded   bool
	parametersLoaded bool
	classesLoaded    bool
}

func (c *referenceDiagnosticCollector) collectPHP() error {
	root := c.document.SyntaxTree.Root
	configFile := strings.Contains(c.document.Source, "ContainerConfigurator")
	path, _ := uriutil.Path(c.document.URI)
	assistantContext := c.provider.phpIndex.AddDocumentContext(
		c.ctx,
		path,
		c.document.Version,
		root,
		root,
	)
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if c.ctx.Err() != nil {
			return nil
		}
		value := phpquery.StringValue(literal)
		if value == "" {
			continue
		}
		if _, tags := php.AssistantArgumentTags(
			assistantContext,
			literal,
			"Service",
			"Parameter",
			"Class",
			"Interface",
			"ClassInterface",
		); len(tags) != 0 {
			for _, tag := range tags {
				switch tag {
				case "Service":
					if err := c.checkService(literal, value); err != nil {
						return err
					}
				case "Parameter":
					if err := c.checkParameter(literal, value); err != nil {
						return err
					}
				case "Class", "Interface", "ClassInterface":
					c.checkAssistantClass(literal, value, tag)
				}
			}
			continue
		}

		kind := symfony.PHPArrayServiceReferenceAt(literal)
		if kind == symfony.PHPConfigReferenceNone {
			kind = symfony.PHPAttributeReferenceAt(literal)
		}
		if kind == symfony.PHPConfigReferenceNone &&
			symfony.PHPArrayServiceContextAt(literal) {
			kind = symfony.PHPConfigReferenceAt(literal)
		}
		if kind != symfony.PHPConfigReferenceNone {
			if err := c.checkPHPValue(literal, value, kind); err != nil {
				return err
			}
			continue
		}
		if phpquery.StringArgumentIndex(literal) == 0 &&
			c.isTypedParameterReadCall(literal) {
			if err := c.checkParameter(literal, value); err != nil {
				return err
			}
			continue
		}
		if configFile {
			if kind := symfony.PHPConfigReferenceAt(literal); kind != symfony.PHPConfigReferenceNone {
				if err := c.checkPHPValue(literal, value, kind); err != nil {
					return err
				}
				continue
			}
		}
		if phpquery.StringInCall(literal, 0, "get", "has") {
			if c.isTypedContainerCall(literal) {
				if err := c.checkService(literal, value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *referenceDiagnosticCollector) checkPHPValue(
	node *cst.Node,
	value string,
	kind symfony.PHPConfigReferenceKind,
) error {
	switch kind {
	case symfony.PHPConfigReferenceService:
		return c.checkService(node, value)
	case symfony.PHPConfigReferenceParameter:
		references := symfony.ParameterReferences(value)
		if len(references) == 0 {
			references = []string{strings.TrimSpace(value)}
		}
		for _, name := range references {
			if err := c.checkParameter(node, name); err != nil {
				return err
			}
		}
	case symfony.PHPConfigReferenceClass:
		c.checkClass(node, strings.ReplaceAll(value, `\\`, `\`))
	}
	return nil
}

func (c *referenceDiagnosticCollector) isTypedContainerCall(node *cst.Node) bool {
	if c.isTypedParameterReadCall(node) {
		return false
	}
	return c.isTypedReceiverSubtype(node, containerTypes)
}

func (c *referenceDiagnosticCollector) isTypedParameterReadCall(
	node *cst.Node,
) bool {
	call := phpquery.CallAt(node)
	return c.isTypedReceiverSubtype(
		node,
		symfony.PHPParameterReadAccessTypes(
			phpquery.CallMethodName(call),
		),
	)
}

func (c *referenceDiagnosticCollector) isTypedReceiverSubtype(
	node *cst.Node,
	targets []string,
) bool {
	call := phpquery.CallAt(node)
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	if c.phpDocument == nil {
		path, _ := uriutil.Path(c.document.URI)
		c.phpDocument = c.provider.phpIndex.AnalyzeDocument(
			path,
			c.document.Version,
			c.document.SyntaxTree.Root,
		)
		c.phpSnapshot = c.provider.phpIndex.SemanticSnapshot().
			WithDocument(c.phpDocument)
	}
	receiverType := c.phpDocument.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	relations := c.phpSnapshot.Relations()
	for _, target := range targets {
		if relations.IsSubtype(receiverType, types.Named(target)) {
			return true
		}
	}
	return false
}

func (c *referenceDiagnosticCollector) collectXML() error {
	root := c.document.SyntaxTree.Root
	services, parameters, parseErr := symfony.ParseXMLServicesTree(
		"",
		c.document.SyntaxTree,
		c.document.LineIndex,
	)
	if parseErr == nil {
		c.addLocalReferences(services, parameters)
	}
	for _, attribute := range xmlquery.Nodes(root, xmlsyntax.XmlAttribute) {
		if c.ctx.Err() != nil {
			return nil
		}
		if name, ok := symfony.XMLServiceReferenceName(attribute); ok {
			value := name
			if symfony.XMLServiceReferenceOptional(attribute) {
				value = "@?" + name
			}
			if err := c.checkService(attribute, value); err != nil {
				return err
			}
		}
		if name, ok := symfony.XMLClassReferenceName(attribute); ok {
			c.checkClass(attribute, name)
		}
		if err := c.checkParameters(attribute, xmlquery.NodeValue(attribute)); err != nil {
			return err
		}
	}
	for _, text := range xmlquery.Nodes(root, xmlsyntax.XmlText) {
		if err := c.checkParameters(text, xmlquery.NodeValue(text)); err != nil {
			return err
		}
	}
	return nil
}

func (c *referenceDiagnosticCollector) collectYAML() error {
	services, parameters, parseErr := symfony.ParseYAMLServicesTree(
		"",
		c.document.SyntaxTree,
		c.document.LineIndex,
	)
	if parseErr == nil {
		c.addLocalReferences(services, parameters)
	}
	for _, scalar := range yamlquery.Nodes(
		c.document.SyntaxTree.Root,
		yamlsyntax.YamlScalar,
	) {
		if c.ctx.Err() != nil {
			return nil
		}
		value := yamlquery.ScalarValue(scalar)
		if name, ok := symfony.YAMLServiceReferenceName(scalar); ok {
			if _, _, parsed := symfony.ParseServiceReference(value); !parsed {
				value = name
			}
			if err := c.checkService(scalar, value); err != nil {
				return err
			}
		}
		if name, ok := symfony.YAMLClassReferenceName(scalar); ok {
			c.checkClass(scalar, name)
		}
		if err := c.checkParameters(scalar, value); err != nil {
			return err
		}
	}
	return nil
}

func (c *referenceDiagnosticCollector) checkService(
	node *cst.Node,
	value string,
) error {
	name, optional, parsed := symfony.ParseServiceReference(value)
	if !parsed {
		name = strings.TrimSpace(strings.TrimPrefix(value, "@"))
	}
	if name == "" || strings.ContainsAny(name, "%${}") {
		return nil
	}
	if service, found := c.localServices[name]; found {
		c.checkServiceDeprecations(node, service)
		return nil
	}
	service, found, err := c.provider.serviceIndex.GetServiceByID(name)
	if err != nil {
		return fmt.Errorf("query Symfony service %q: %w", name, err)
	}
	if found || service.ID != "" {
		c.checkServiceDeprecations(node, service)
		return nil
	}
	if optional || name == ".inner" ||
		strings.HasSuffix(name, ".inner") {
		return nil
	}
	candidates, candidateErr := c.allServiceNames()
	if candidateErr != nil {
		return candidateErr
	}
	c.add(
		node,
		missingServiceCode,
		name,
		fmt.Sprintf("Service '%s' not found", name),
		suggestion.Similar(name, candidates),
	)
	return nil
}

func (c *referenceDiagnosticCollector) checkServiceDeprecations(
	node *cst.Node,
	service symfony.Service,
) {
	c.checkDeprecatedService(node, service)
	className := c.serviceClass(service)
	if className == "" {
		return
	}
	symbol, found := c.provider.phpIndex.FindClass(className)
	if !found || !symbol.Flags.Has(semantic.DeprecatedFlag) {
		return
	}
	c.addDeprecated(
		node,
		deprecatedClassCode,
		service.ID,
		fmt.Sprintf("Class '%s' is deprecated", className),
	)
}

func (c *referenceDiagnosticCollector) serviceClass(
	service symfony.Service,
) string {
	seen := make(map[string]struct{})
	for range 16 {
		className := strings.TrimPrefix(strings.TrimSpace(service.Class), "\\")
		if className != "" {
			return className
		}
		target := strings.TrimSpace(service.AliasTarget)
		if target == "" {
			return ""
		}
		if _, exists := seen[target]; exists {
			return ""
		}
		seen[target] = struct{}{}
		if local, found := c.localServices[target]; found {
			service = local
			continue
		}
		indexed, found, err := c.provider.serviceIndex.GetServiceByID(target)
		if err != nil || !found {
			return ""
		}
		service = indexed
	}
	return ""
}

func (c *referenceDiagnosticCollector) checkDeprecatedService(
	node *cst.Node,
	service symfony.Service,
) {
	if !service.Deprecated {
		return
	}
	message := fmt.Sprintf("Service '%s' is deprecated", service.ID)
	if service.Deprecation != "" {
		detail := strings.ReplaceAll(
			service.Deprecation,
			"%service_id%",
			service.ID,
		)
		detail = strings.ReplaceAll(detail, "%alias_id%", service.ID)
		message += ": " + detail
	}
	c.addDeprecated(node, deprecatedServiceCode, service.ID, message)
}

func (c *referenceDiagnosticCollector) checkParameters(
	node *cst.Node,
	value string,
) error {
	for _, name := range symfony.ParameterReferences(value) {
		if err := c.checkParameter(node, name); err != nil {
			return err
		}
	}
	return nil
}

func (c *referenceDiagnosticCollector) checkParameter(
	node *cst.Node,
	name string,
) error {
	name = strings.TrimSpace(strings.Trim(name, "%"))
	if name == "" || strings.ContainsAny(name, "${}") {
		return nil
	}
	if _, found := c.localParameters[strings.ToLower(name)]; found {
		return nil
	}
	_, found, err := c.provider.serviceIndex.GetParameterByName(name)
	if err != nil {
		return fmt.Errorf("query Symfony parameter %q: %w", name, err)
	}
	if !found {
		candidates, candidateErr := c.allParameterNames()
		if candidateErr != nil {
			return candidateErr
		}
		c.add(
			node,
			missingParameterCode,
			name,
			fmt.Sprintf("Parameter '%s' not found", name),
			suggestion.Similar(name, candidates),
		)
	}
	return nil
}

func (c *referenceDiagnosticCollector) checkClass(node *cst.Node, name string) {
	c.checkClassKinds(node, name, "Class", nil)
}

func (c *referenceDiagnosticCollector) checkAssistantClass(
	node *cst.Node,
	name,
	tag string,
) {
	var kinds map[semantic.SymbolKind]struct{}
	label := "Class"
	switch tag {
	case "Class":
		kinds = map[semantic.SymbolKind]struct{}{
			semantic.ClassSymbol: {},
		}
	case "Interface":
		label = "Interface"
		kinds = map[semantic.SymbolKind]struct{}{
			semantic.InterfaceSymbol: {},
		}
	case "ClassInterface":
		label = "Class or interface"
		kinds = map[semantic.SymbolKind]struct{}{
			semantic.ClassSymbol:     {},
			semantic.InterfaceSymbol: {},
		}
	}
	c.checkClassKinds(node, name, label, kinds)
}

func (c *referenceDiagnosticCollector) checkClassKinds(
	node *cst.Node,
	name,
	label string,
	allowed map[semantic.SymbolKind]struct{},
) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "\\")
	if name == "" {
		return
	}
	var (
		symbol semantic.Symbol
		found  bool
	)
	for _, candidate := range c.provider.phpIndex.SemanticSnapshot().
		Classes(name) {
		if len(allowed) != 0 {
			if _, accepted := allowed[candidate.Kind]; !accepted {
				continue
			}
		}
		if !found || symbol.Flags.Has(semantic.InternalFlag) &&
			!candidate.Flags.Has(semantic.InternalFlag) {
			symbol = candidate
			found = true
		}
	}
	if !found {
		var candidates []string
		if len(allowed) == 0 {
			if !c.classesLoaded {
				c.classNames = c.provider.phpIndex.ClassNamesView()
				c.classesLoaded = true
			}
			candidates = c.classNames
		} else {
			seen := make(map[string]struct{})
			for _, candidate := range c.provider.phpIndex.ClassSymbolsView() {
				if _, accepted := allowed[candidate.Kind]; !accepted {
					continue
				}
				name := strings.TrimPrefix(candidate.FullyQualified, "\\")
				key := strings.ToLower(name)
				if name == "" {
					continue
				}
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, name)
			}
		}
		c.add(
			node,
			missingClassCode,
			name,
			fmt.Sprintf("%s '%s' not found", label, name),
			suggestion.Similar(name, candidates),
		)
		return
	}
	if symbol.Flags.Has(semantic.DeprecatedFlag) {
		c.addDeprecated(
			node,
			deprecatedClassCode,
			name,
			fmt.Sprintf("Class '%s' is deprecated", name),
		)
	}
}

func (c *referenceDiagnosticCollector) allServiceNames() ([]string, error) {
	if c.servicesLoaded {
		return c.serviceNames, nil
	}
	names, err := c.provider.serviceIndex.GetAllServices()
	if err != nil {
		return nil, fmt.Errorf("query Symfony services: %w", err)
	}
	c.serviceNames = names
	c.servicesLoaded = true
	return names, nil
}

func (c *referenceDiagnosticCollector) allParameterNames() ([]string, error) {
	if c.parametersLoaded {
		return c.parameterNames, nil
	}
	parameters, err := c.provider.serviceIndex.GetAllParameters()
	if err != nil {
		return nil, fmt.Errorf("query Symfony parameters: %w", err)
	}
	c.parameterNames = make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		c.parameterNames = append(c.parameterNames, parameter.Name)
	}
	c.parametersLoaded = true
	return c.parameterNames, nil
}

func (c *referenceDiagnosticCollector) addLocalReferences(
	services []symfony.Service,
	parameters []symfony.Parameter,
) {
	if c.localServices == nil {
		c.localServices = make(map[string]symfony.Service, len(services))
	}
	for _, service := range services {
		c.localServices[service.ID] = service
	}
	if c.localParameters == nil {
		c.localParameters = make(map[string]struct{}, len(parameters))
	}
	for _, parameter := range parameters {
		c.localParameters[strings.ToLower(parameter.Name)] = struct{}{}
	}
}

func (c *referenceDiagnosticCollector) addDeprecated(
	node *cst.Node,
	code lsp.DiagnosticID,
	name,
	message string,
) {
	rng := node.RangeTrimmedTrivia()
	key := referenceDiagnosticKey{
		start: rng.Start,
		end:   rng.End,
		code:  code,
		name:  strings.ToLower(name),
	}
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.diagnostics = append(c.diagnostics, lsp.Problem{
		Range:    valueNodeTextRange(node, name),
		Message:  message,
		Source:   "symfony",
		Severity: protocol.DiagnosticSeverityHint,
		ID:       code,
		Tags:     []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
	})
}

func (c *referenceDiagnosticCollector) add(
	node *cst.Node,
	code lsp.DiagnosticID,
	name,
	message string,
	suggestions []string,
) {
	rng := node.RangeTrimmedTrivia()
	key := referenceDiagnosticKey{
		start: rng.Start,
		end:   rng.End,
		code:  code,
		name:  strings.ToLower(name),
	}
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.diagnostics = append(c.diagnostics, lsp.Problem{
		Range:    valueNodeTextRange(node, name),
		Message:  message,
		Source:   "symfony",
		Severity: protocol.DiagnosticSeverityError,
		ID:       code,
		Payload: map[string]any{
			"referenceName": name,
			"suggestions":   suggestions,
		},
	})
}
