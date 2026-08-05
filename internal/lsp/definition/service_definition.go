package definition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type serviceXMLDefinitionProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewServiceXMLDefinitionProvider(serviceIndex *symfony.ServiceIndex, phpIndex *php.PHPIndex) *serviceXMLDefinitionProvider {
	return &serviceXMLDefinitionProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

func (p *serviceXMLDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	fileExt := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	switch fileExt {
	case ".php":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.phpDefinition(ctx, params)
	case ".yaml", ".yml":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.yamlDefinition(ctx, params)
	case ".xml":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.xmlDefinition(ctx, params)
	default:
		return []protocol.Location{}
	}
}

func (p *serviceXMLDefinitionProvider) xmlDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if locations, matched := p.configuredServiceMethodDefinition(params); matched {
		return locations
	}

	// <argument type="service" id="<caret>"/>
	if symfony.XMLServiceIsServiceReference(params.Node) {
		serviceID, ok := symfony.XMLServiceReferenceName(params.Node)
		if !ok {
			return []protocol.Location{}
		}

		// Try to find the service definition
		service, found, err := p.serviceIndex.GetServiceByID(serviceID)
		if err != nil || !found {
			return []protocol.Location{}
		}

		// Create a location for the service
		return []protocol.Location{serviceLocation(service.Path, service.Line)}
	}

	// <argument type="tagged" tag="x"/>
	if symfony.XMLServiceIsArgumentTag(params.Node) || symfony.XMLServiceIsTagElement(params.Node) {
		serviceID := symfony.XMLContextValue(params.Node)
		if serviceID == "" {
			return []protocol.Location{}
		}

		services, err := p.serviceIndex.GetServicesByTag(serviceID)
		if err != nil {
			return nil
		}

		var locations []protocol.Location
		for _, serviceName := range services {
			service, found, err := p.serviceIndex.GetServiceByID(serviceName)
			if err != nil || !found {
				continue
			}

			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(service.Path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      service.Line - 1, // LSP uses 0-based line numbers
						Character: 0,
					},
					End: protocol.Position{
						Line:      service.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	// <argument>%<caret>%</argument>
	if references := symfony.ParameterReferences(
		symfony.XMLContextValue(params.Node),
	); len(references) != 0 {
		paramName := references[0]

		// Find parameter locations
		// Currently we don't store line numbers for parameters, so we can't
		// provide exact locations. This would require enhancing the Parameter struct
		// to store line numbers and updating the parser.

		// Check if the parameter exists
		parameter, found, err := p.serviceIndex.GetParameterByName(paramName)
		if err != nil || !found {
			return []protocol.Location{}
		}

		return []protocol.Location{
			serviceLocation(parameter.Path, parameter.Line),
		}
	}

	// <service id="<caret>">
	if symfony.XMLServiceIsServiceID(params.Node) {
		nodeText := symfony.XMLContextValue(params.Node)

		phpClass, found := p.phpIndex.FindClass(nodeText)
		if found {
			return []protocol.Location{phpSymbolLocation(phpClass)}
		}
	}

	return []protocol.Location{}
}

func (p *serviceXMLDefinitionProvider) yamlDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if locations, matched := p.configuredServiceMethodDefinition(params); matched {
		return locations
	}
	if locations, matched := p.yamlDefaultBindingDefinition(params); matched {
		return locations
	}
	if locations, matched := p.yamlNamedArgumentDefinition(params); matched {
		return locations
	}

	if symfony.IsYAMLServiceID(params.Node) || symfony.IsYAMLClassPropertyInService(params.Node) {
		value := symfony.YAMLContextValue(params.Node)
		phpClass, found := p.phpIndex.FindClass(value)
		if found {
			return []protocol.Location{phpSymbolLocation(phpClass)}
		}
	}

	if symfony.IsYAMLArgumentServiceID(params.Node) {
		value, ok := symfony.YAMLServiceReferenceName(params.Node)
		if !ok {
			return []protocol.Location{}
		}

		service, found, err := p.serviceIndex.GetServiceByID(value)

		if err == nil && found {
			return []protocol.Location{serviceLocation(service.Path, service.Line)}
		}
	}

	if references := symfony.ParameterReferences(
		symfony.YAMLContextValue(params.Node),
	); len(references) != 0 {
		parameter, found, err := p.serviceIndex.GetParameterByName(references[0])
		if err == nil && found {
			return []protocol.Location{
				serviceLocation(parameter.Path, parameter.Line),
			}
		}
	}

	return []protocol.Location{}
}

func (p *serviceXMLDefinitionProvider) configuredServiceMethodDefinition(
	params *lsp.DefinitionRequest,
) ([]protocol.Location, bool) {
	if p == nil || p.phpIndex == nil || params == nil ||
		params.DefinitionParams == nil || params.Root == nil ||
		params.LineIndex == nil {
		return nil, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	var (
		reference symfony.ServiceMethodReference
		found     bool
	)
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".yaml", ".yml":
		reference, found = symfony.YAMLServiceMethodReferenceAt(
			params.Root,
			offset,
		)
	case ".xml":
		reference, found = symfony.XMLServiceMethodReferenceAt(
			params.Root,
			offset,
		)
	}
	if !found {
		return nil, false
	}
	serviceID, explicitClass := reference.Receiver()
	className, resolved, err := symfony.ResolveConfiguredServiceClass(
		serviceID,
		explicitClass,
		configuredServiceDefinitionMap(params),
		p.serviceIndex,
	)
	if err != nil || !resolved || reference.MethodName == "" {
		return nil, true
	}
	var locations []protocol.Location
	for _, method := range p.phpIndex.FindMethods(
		className,
		reference.MethodName,
	) {
		if method.Visibility != semantic.Public {
			continue
		}
		locations = append(locations, phpSymbolLocation(method))
	}
	return locations, true
}

func configuredServiceDefinitionMap(
	params *lsp.DefinitionRequest,
) map[string]symfony.Service {
	result := make(map[string]symfony.Service)
	if params == nil || params.DocumentTree == nil ||
		params.LineIndex == nil {
		return result
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return result
	}
	configuration, err := symfony.ServiceConfigurationInDocument(
		path,
		params.DocumentTree,
		params.LineIndex,
	)
	if err != nil {
		return result
	}
	for _, service := range configuration.Services {
		if service.ID != "" {
			result[service.ID] = service
		}
	}
	return result
}

func (p *serviceXMLDefinitionProvider) yamlDefaultBindingDefinition(
	params *lsp.DefinitionRequest,
) ([]protocol.Location, bool) {
	if p == nil || p.phpIndex == nil || params == nil ||
		params.DefinitionParams == nil || params.Root == nil ||
		params.DocumentTree == nil || params.LineIndex == nil {
		return nil, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	binding, found := symfony.YAMLServiceDefaultBindingAt(
		params.Root,
		offset,
	)
	if !found {
		return nil, false
	}

	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, true
	}
	configuration, err := symfony.ServiceConfigurationInDocument(
		path,
		params.DocumentTree,
		params.LineIndex,
	)
	if err != nil {
		return nil, true
	}
	classNames := make([]string, 0, len(configuration.Services))
	seenClasses := make(map[string]struct{})
	addClass := func(className string) {
		className = strings.TrimPrefix(strings.TrimSpace(className), "\\")
		if className == "" {
			return
		}
		key := strings.ToLower(className)
		if _, duplicate := seenClasses[key]; duplicate {
			return
		}
		seenClasses[key] = struct{}{}
		classNames = append(classNames, className)
	}
	for _, service := range configuration.Services {
		if service.AliasTarget == "" {
			addClass(service.Class)
		}
	}
	if p.serviceIndex != nil {
		for _, prototype := range configuration.Prototypes {
			for _, class := range p.serviceIndex.PrototypeClasses(prototype) {
				addClass(class.FullyQualified)
			}
		}
	}

	name := strings.TrimPrefix(strings.TrimSpace(binding.Name), "$")
	seen := make(map[semantic.SymbolID]struct{})
	var locations []protocol.Location
	for _, className := range classNames {
		for _, constructor := range p.phpIndex.FindMethods(
			className,
			"__construct",
		) {
			for _, parameter := range constructor.Parameters {
				if strings.TrimPrefix(parameter.Name, "$") != name ||
					!yamlDefaultBindingTypeMatches(parameter, binding.Type) {
					continue
				}
				if _, duplicate := seen[parameter.ID]; duplicate {
					continue
				}
				seen[parameter.ID] = struct{}{}
				locations = append(
					locations,
					phpParameterLocation(constructor.Path, parameter),
				)
			}
		}
	}
	return locations, true
}

func yamlDefaultBindingTypeMatches(
	parameter semantic.Parameter,
	expected string,
) bool {
	expected = normalizeYAMLDefaultBindingType(expected)
	if expected == "" {
		return true
	}
	return yamlDefaultBindingPHPTypeMatches(parameter.NativeType, expected) ||
		yamlDefaultBindingPHPTypeMatches(parameter.Type, expected)
}

func yamlDefaultBindingPHPTypeMatches(
	value types.Type,
	expected string,
) bool {
	if value.IsUnknown() {
		return false
	}
	if strings.EqualFold(
		normalizeYAMLDefaultBindingType(value.String()),
		expected,
	) {
		return true
	}
	switch value.Kind() {
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			if yamlDefaultBindingPHPTypeMatches(member, expected) {
				return true
			}
		}
	}
	return false
}

func normalizeYAMLDefaultBindingType(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "\\")
}

func (p *serviceXMLDefinitionProvider) yamlNamedArgumentDefinition(
	params *lsp.DefinitionRequest,
) ([]protocol.Location, bool) {
	if p == nil || p.phpIndex == nil || params == nil ||
		params.DefinitionParams == nil || params.Root == nil ||
		params.LineIndex == nil {
		return nil, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	argument, found := symfony.YAMLServiceNamedArgumentAt(
		params.Root,
		offset,
	)
	if !found {
		return nil, false
	}
	if argument.HasFactory {
		return nil, true
	}
	className, found, err := symfony.ResolveYAMLServiceNamedArgumentClass(
		p.serviceIndex,
		argument,
	)
	if err != nil || !found {
		return nil, true
	}
	name := strings.TrimPrefix(strings.TrimSpace(argument.Name), "$")
	seen := make(map[semantic.SymbolID]struct{})
	var locations []protocol.Location
	for _, constructor := range p.phpIndex.FindMethods(
		className,
		"__construct",
	) {
		for _, parameter := range constructor.Parameters {
			if strings.TrimPrefix(parameter.Name, "$") != name {
				continue
			}
			if _, duplicate := seen[parameter.ID]; duplicate {
				continue
			}
			seen[parameter.ID] = struct{}{}
			locations = append(
				locations,
				phpParameterLocation(constructor.Path, parameter),
			)
		}
	}
	return locations, true
}

func (p *serviceXMLDefinitionProvider) phpDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if _, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Service",
	); found {
		return p.serviceIDDefinition(phpquery.StringValue(params.Node))
	}
	if _, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Parameter",
	); found {
		return p.parameterIDDefinition(phpquery.StringValue(params.Node))
	}
	if reference, found := symfony.PHPAutowireCallableMethodAt(
		params.Node,
	); found {
		return p.autowireCallableMethodDefinitions(params.Root, reference)
	}
	if reference, found := symfony.PHPArrayServiceMethodAt(
		params.Root,
		params.Node,
	); found {
		return p.phpArrayServiceMethodDefinitions(params.Root, reference)
	}
	kind := symfony.PHPArrayServiceReferenceAt(params.Node)
	if kind == symfony.PHPConfigReferenceNone {
		kind = symfony.PHPAttributeReferenceAt(params.Node)
	}
	if kind == symfony.PHPConfigReferenceNone &&
		phpquery.StringArgumentIndex(params.Node) == 0 &&
		p.isParameterAccessCall(ctx, params) {
		kind = symfony.PHPConfigReferenceParameter
	}
	if kind == symfony.PHPConfigReferenceNone &&
		(strings.Contains(
			string(params.DocumentContent),
			"ContainerConfigurator",
		) || symfony.PHPArrayServiceContextAt(params.Node)) {
		kind = symfony.PHPConfigReferenceAt(params.Node)
	}
	if kind == symfony.PHPConfigReferenceNone &&
		phpquery.StringInCall(params.Node, 0, "get", "has") {
		if p.isContainerCall(ctx, params) {
			kind = symfony.PHPConfigReferenceService
		}
	}
	value := symfony.PHPConfigReferenceValue(params.Node)
	switch kind {
	case symfony.PHPConfigReferenceService:
		return p.serviceIDDefinition(value)
	case symfony.PHPConfigReferenceParameter:
		if references := symfony.ParameterReferences(value); len(references) != 0 {
			value = references[0]
		}
		return p.parameterIDDefinition(value)
	case symfony.PHPConfigReferenceTag:
		return p.taggedServiceLocations(value)
	case symfony.PHPConfigReferenceClass:
		class, found := p.phpIndex.FindClass(
			strings.TrimPrefix(strings.TrimSpace(value), "\\"),
		)
		if !found {
			return nil
		}
		return []protocol.Location{phpSymbolLocation(class)}
	}

	className := phpquery.ClassConstantName(params.Node)
	if className == "" || params.Root == nil {
		return []protocol.Location{}
	}
	resolver := php.NewNameResolver(params.Root)
	class, found := p.phpIndex.FindClass(resolver.Resolve(className))
	if !found {
		return []protocol.Location{}
	}
	return []protocol.Location{phpSymbolLocation(class)}
}

func (p *serviceXMLDefinitionProvider) serviceIDDefinition(
	serviceID string,
) []protocol.Location {
	if p == nil || p.serviceIndex == nil {
		return nil
	}
	if normalized, _, ok := symfony.ParseServiceReference(serviceID); ok {
		serviceID = normalized
	}
	service, found, err := p.serviceIndex.GetServiceByID(serviceID)
	if err != nil || !found {
		return nil
	}
	return []protocol.Location{serviceLocation(service.Path, service.Line)}
}

func (p *serviceXMLDefinitionProvider) parameterIDDefinition(
	name string,
) []protocol.Location {
	if p == nil || p.serviceIndex == nil {
		return nil
	}
	parameter, found, err := p.serviceIndex.GetParameterByName(name)
	if err != nil || !found {
		return nil
	}
	return []protocol.Location{
		serviceLocation(parameter.Path, parameter.Line),
	}
}

func (p *serviceXMLDefinitionProvider) taggedServiceLocations(
	tag string,
) []protocol.Location {
	serviceIDs, err := p.serviceIndex.GetServicesByTag(tag)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(serviceIDs))
	seen := make(map[string]struct{}, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		service, found, serviceErr := p.serviceIndex.GetServiceByID(serviceID)
		if serviceErr != nil || !found {
			continue
		}
		location := serviceLocation(service.Path, service.Line)
		key := fmt.Sprintf(
			"%s:%d",
			location.URI,
			location.Range.Start.Line,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		locations = append(locations, location)
	}
	return locations
}

func (p *serviceXMLDefinitionProvider) autowireCallableMethodDefinitions(
	root *cst.Node,
	reference symfony.PHPAutowireCallableMethodReference,
) []protocol.Location {
	if reference.Method == "" {
		return nil
	}
	className := ""
	if reference.Class != "" && root != nil {
		className = strings.TrimPrefix(
			php.NewNameResolver(root).Resolve(reference.Class),
			"\\",
		)
	} else {
		serviceID := strings.TrimSpace(reference.Service)
		if normalized, _, ok := symfony.ParseServiceReference(serviceID); ok {
			serviceID = normalized
		}
		if resolved, found, err := p.serviceIndex.ResolveServiceClassName(
			serviceID,
		); err == nil && found {
			className = resolved
		} else if class, exists := p.phpIndex.FindClass(serviceID); exists {
			className = strings.TrimPrefix(class.FullyQualified, "\\")
		}
	}
	if className == "" {
		return nil
	}
	var locations []protocol.Location
	for _, method := range p.phpIndex.FindMethods(
		className,
		reference.Method,
	) {
		if method.Visibility != semantic.Public ||
			method.Flags.Has(semantic.StaticFlag) {
			continue
		}
		locations = append(locations, phpSymbolLocation(method))
	}
	return locations
}

func (p *serviceXMLDefinitionProvider) phpArrayServiceMethodDefinitions(
	root *cst.Node,
	reference symfony.PHPArrayServiceMethodReference,
) []protocol.Location {
	if reference.Method == "" {
		return nil
	}
	className := p.serviceOrClassName(
		root,
		reference.Class,
		reference.Service,
	)
	if className == "" {
		return nil
	}
	var locations []protocol.Location
	for _, method := range p.phpIndex.FindMethods(
		className,
		reference.Method,
	) {
		if method.Visibility != semantic.Public ||
			!reference.AllowStatic &&
				method.Flags.Has(semantic.StaticFlag) {
			continue
		}
		locations = append(locations, phpSymbolLocation(method))
	}
	return locations
}

func (p *serviceXMLDefinitionProvider) serviceOrClassName(
	root *cst.Node,
	className,
	serviceID string,
) string {
	if className != "" && root != nil {
		return strings.TrimPrefix(
			php.NewNameResolver(root).Resolve(className),
			"\\",
		)
	}
	serviceID = strings.TrimSpace(serviceID)
	if normalized, _, ok := symfony.ParseServiceReference(serviceID); ok {
		serviceID = normalized
	}
	if resolved, found, err := p.serviceIndex.ResolveServiceClassName(
		serviceID,
	); err == nil && found {
		return resolved
	}
	if class, exists := p.phpIndex.FindClass(serviceID); exists {
		return strings.TrimPrefix(class.FullyQualified, "\\")
	}
	return ""
}

func (p *serviceXMLDefinitionProvider) isParameterAccessCall(
	ctx context.Context,
	params *lsp.DefinitionRequest,
) bool {
	call := phpquery.CallAt(params.Node)
	for _, className := range symfony.PHPParameterAccessTypes(
		phpquery.CallMethodName(call),
	) {
		if p.phpIndex.IsMethodCalledOnClass(
			ctx,
			params.Node,
			params.DocumentContent,
			className,
		) {
			return true
		}
	}
	return false
}

func (p *serviceXMLDefinitionProvider) isContainerCall(
	ctx context.Context,
	params *lsp.DefinitionRequest,
) bool {
	if p.isParameterAccessCall(ctx, params) {
		return false
	}
	for _, className := range []string{
		"Psr\\Container\\ContainerInterface",
		"Symfony\\Component\\DependencyInjection\\ContainerInterface",
		"Symfony\\Contracts\\Service\\ServiceProviderInterface",
	} {
		if p.phpIndex.IsMethodCalledOnClass(
			ctx,
			params.Node,
			params.DocumentContent,
			className,
		) {
			return true
		}
	}
	return false
}

func serviceLocation(path string, line int) protocol.Location {
	if line < 1 {
		line = 1
	}
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{Line: line - 1},
			End:   protocol.Position{Line: line - 1},
		},
	}
}

func phpSymbolLocation(symbol semantic.Symbol) protocol.Location {
	content, err := os.ReadFile(symbol.Path)
	if err != nil {
		return protocol.Location{URI: uriutil.FileURI(symbol.Path)}
	}
	lineIndex := cst.NewLineIndex(string(content))
	startLine, startCharacter := lineIndex.PositionUTF16(symbol.SelectionRange.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(symbol.SelectionRange.End)
	return protocol.Location{
		URI: uriutil.FileURI(symbol.Path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}
}

func phpParameterLocation(
	path string,
	parameter semantic.Parameter,
) protocol.Location {
	return phpSymbolLocation(semantic.Symbol{
		Path:           path,
		SelectionRange: parameter.SelectionRange,
	})
}
