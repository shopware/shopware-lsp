package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// SymfonyCompletionProvider provides completions for Symfony services and tags
type SymfonyCompletionProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

// NewServiceCompletionProvider creates a new service completion provider
func NewServiceCompletionProvider(serviceIndex *symfony.ServiceIndex, phpIndex *php.PHPIndex) *SymfonyCompletionProvider {
	return &SymfonyCompletionProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

// GetCompletions returns completion items based on the provider type
func (p *SymfonyCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	fileExt := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	switch fileExt {
	case ".yaml", ".yml":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.yamlCompletions(ctx, params)
	case ".xml":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.xmlCompletion(ctx, params)
	case ".php":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.phpCompletions(ctx, params)
	default:
		return []protocol.CompletionItem{}
	}
}

func (p *SymfonyCompletionProvider) phpCompletions(
	ctx context.Context,
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if reference, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Service",
	); found {
		return p.assistantServiceCompletionItems(params, reference)
	}
	if reference, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Parameter",
	); found {
		return p.assistantParameterCompletionItems(params, reference)
	}
	if symfony.PHPWhenEnvironmentAt(params.Node) {
		environments := []string{"prod", "dev", "test", "never"}
		items := make([]protocol.CompletionItem, 0, len(environments))
		for _, environment := range environments {
			items = append(items, protocol.CompletionItem{
				Label: environment,
				Kind:  int(protocol.ValueCompletion),
			})
		}
		return items
	}
	if reference, found := symfony.PHPAutowireCallableMethodAt(
		params.Node,
	); found {
		return p.autowireCallableMethodCompletions(params, reference)
	}
	if reference, found := symfony.PHPArrayServiceMethodAt(
		params.Root,
		params.Node,
	); found {
		return p.phpArrayServiceMethodCompletions(params, reference)
	}
	kind := symfony.PHPArrayServiceReferenceAt(params.Node)
	if kind == symfony.PHPConfigReferenceParameter &&
		symfony.PHPArrayParameterCompletionAt(params.Node) {
		return p.phpArrayParameterCompletions(params)
	}
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
	switch kind {
	case symfony.PHPConfigReferenceService:
		return p.serviceCompletionItems("", "")
	case symfony.PHPConfigReferenceParameter:
		parameters, err := p.serviceIndex.GetAllParameters()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(parameters))
		for _, parameter := range parameters {
			items = append(items, protocol.CompletionItem{
				Label: parameter.Name,
				Kind:  21,
			})
		}
		return items
	case symfony.PHPConfigReferenceTag:
		tags, err := p.serviceIndex.GetAllTags()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(tags))
		for _, tag := range tags {
			items = append(items, protocol.CompletionItem{
				Label: tag,
				Kind:  21,
			})
		}
		return items
	case symfony.PHPConfigReferenceClass:
		names := p.phpIndex.ClassNames()
		items := make([]protocol.CompletionItem, 0, len(names))
		for _, name := range names {
			items = append(items, protocol.CompletionItem{
				Label: strings.TrimPrefix(name, "\\"),
				Kind:  int(protocol.ClassCompletion),
			})
		}
		return items
	default:
		return []protocol.CompletionItem{}
	}
}

func (p *SymfonyCompletionProvider) assistantServiceCompletionItems(
	params *lsp.CompletionRequest,
	reference cst.TextRange,
) []protocol.CompletionItem {
	if p == nil || p.serviceIndex == nil {
		return nil
	}
	items := p.serviceCompletionItems("", "")
	if params == nil || params.LineIndex == nil {
		return items
	}
	replacement := namedArgumentCompletionRange(reference, params.LineIndex)
	for index := range items {
		items[index].TextEdit = protocol.TextEdit{
			Range:   replacement,
			NewText: items[index].Label,
		}
	}
	return items
}

func (p *SymfonyCompletionProvider) assistantParameterCompletionItems(
	params *lsp.CompletionRequest,
	reference cst.TextRange,
) []protocol.CompletionItem {
	if p == nil || p.serviceIndex == nil {
		return nil
	}
	parameters, err := p.serviceIndex.GetAllParameters()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(parameters))
	var replacement protocol.Range
	hasReplacement := params != nil && params.LineIndex != nil
	if hasReplacement {
		replacement = namedArgumentCompletionRange(reference, params.LineIndex)
	}
	for _, parameter := range parameters {
		item := protocol.CompletionItem{
			Label: parameter.Name,
			Kind:  int(protocol.ReferenceCompletion),
		}
		if hasReplacement {
			item.TextEdit = protocol.TextEdit{
				Range:   replacement,
				NewText: parameter.Name,
			}
		}
		items = append(items, item)
	}
	return items
}

func (p *SymfonyCompletionProvider) phpArrayParameterCompletions(
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	parameters, err := p.serviceIndex.GetAllParameters()
	if err != nil {
		return nil
	}
	literal := phpquery.StringAt(params.Node)
	contentRange := phpquery.StringContentRange(literal)
	items := make([]protocol.CompletionItem, 0, len(parameters))
	for _, parameter := range parameters {
		value := "%" + parameter.Name + "%"
		item := protocol.CompletionItem{
			Label: value,
			Kind:  int(protocol.ReferenceCompletion),
		}
		if params.LineIndex != nil {
			item.TextEdit = protocol.TextEdit{
				Range: namedArgumentCompletionRange(
					contentRange,
					params.LineIndex,
				),
				NewText: value,
			}
		}
		items = append(items, item)
	}
	return items
}

func (p *SymfonyCompletionProvider) autowireCallableMethodCompletions(
	params *lsp.CompletionRequest,
	reference symfony.PHPAutowireCallableMethodReference,
) []protocol.CompletionItem {
	className := p.autowireCallableClass(params.Root, reference)
	if className == "" {
		return nil
	}
	methods := p.phpIndex.Methods(className)
	items := make([]protocol.CompletionItem, 0, len(methods))
	for _, method := range methods {
		if method.Visibility != semantic.Public ||
			method.Flags.Has(semantic.StaticFlag) {
			continue
		}
		item := protocol.CompletionItem{
			Label:  method.Name,
			Kind:   int(protocol.MethodCompletion),
			Detail: className + "::" + method.Name + "()",
		}
		if params.LineIndex != nil {
			item.TextEdit = protocol.TextEdit{
				Range: namedArgumentCompletionRange(
					reference.Range,
					params.LineIndex,
				),
				NewText: method.Name,
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func (p *SymfonyCompletionProvider) phpArrayServiceMethodCompletions(
	params *lsp.CompletionRequest,
	reference symfony.PHPArrayServiceMethodReference,
) []protocol.CompletionItem {
	className := p.serviceOrClassName(
		params.Root,
		reference.Class,
		reference.Service,
	)
	if className == "" {
		return nil
	}
	methods := p.phpIndex.Methods(className)
	items := make([]protocol.CompletionItem, 0, len(methods))
	for _, method := range methods {
		if method.Visibility != semantic.Public ||
			!reference.AllowStatic &&
				method.Flags.Has(semantic.StaticFlag) {
			continue
		}
		item := protocol.CompletionItem{
			Label:  method.Name,
			Kind:   int(protocol.MethodCompletion),
			Detail: className + "::" + method.Name + "()",
		}
		if params.LineIndex != nil {
			item.TextEdit = protocol.TextEdit{
				Range: namedArgumentCompletionRange(
					reference.Range,
					params.LineIndex,
				),
				NewText: method.Name,
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func (p *SymfonyCompletionProvider) autowireCallableClass(
	root *cst.Node,
	reference symfony.PHPAutowireCallableMethodReference,
) string {
	return p.serviceOrClassName(
		root,
		reference.Class,
		reference.Service,
	)
}

func (p *SymfonyCompletionProvider) serviceOrClassName(
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
	className, found, err := p.serviceIndex.ResolveServiceClassName(serviceID)
	if err == nil && found {
		return className
	}
	if class, exists := p.phpIndex.FindClass(serviceID); exists {
		return strings.TrimPrefix(class.FullyQualified, "\\")
	}
	return ""
}

func (p *SymfonyCompletionProvider) isParameterAccessCall(
	ctx context.Context,
	params *lsp.CompletionRequest,
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

func (p *SymfonyCompletionProvider) isContainerCall(
	ctx context.Context,
	params *lsp.CompletionRequest,
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

func (p *SymfonyCompletionProvider) xmlCompletion(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {

	// Check if we're in an XML file
	uri := params.TextDocument.URI
	if !strings.HasSuffix(strings.ToLower(uri), ".xml") {
		return []protocol.CompletionItem{}
	}

	if items, matched := p.configuredServiceMethodCompletions(params); matched {
		return items
	}

	// <argument type="service" id="<caret>"/>
	if symfony.XMLServiceIsServiceReference(params.Node) {
		currentServiceId := symfony.XMLCurrentServiceID(params.Node)
		return p.serviceCompletionItems("", currentServiceId)
	}

	// <argument type="tagged" tag="<caret>"/>
	if symfony.XMLServiceIsArgumentTag(params.Node) {
		items := make([]protocol.CompletionItem, 0)
		tags, err := p.serviceIndex.GetAllTags()
		if err != nil {
			return nil
		}
		for _, tag := range tags {
			item := protocol.CompletionItem{
				Label: tag,
				Kind:  6, // 6 = Class
			}
			items = append(items, item)
		}
		return items
	}

	// <argument>%<caret>%</argument>
	if len(symfony.ParameterReferences(symfony.XMLContextValue(params.Node))) != 0 {
		items := make([]protocol.CompletionItem, 0)
		parameters, err := p.serviceIndex.GetAllParameters()
		if err != nil {
			return nil
		}

		for _, paramName := range parameters {
			item := protocol.CompletionItem{
				Label:      paramName.Name,
				InsertText: paramName.Name + "%",
				Kind:       21, // 21 = Constant
			}

			// Try to get parameter value for documentation
			if value, found, err := p.serviceIndex.GetParameterByName(paramName.Name); err == nil && found {
				item.Documentation.Kind = "markdown"
				item.Documentation.Value = "**Parameter:** `" + paramName.Name + "`\n\n**Value:** `" + value.Value + "`"
			}

			items = append(items, item)
		}
		return items
	}

	// <tag name="<caret>"/>
	if symfony.XMLServiceIsTagElement(params.Node) {
		tags, err := p.serviceIndex.GetAllTags()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0)
		for _, tag := range tags {
			item := protocol.CompletionItem{
				Label: tag,
				Kind:  6, // 6 = Class
			}
			items = append(items, item)
		}

		return items
	}

	// <service id="<caret>">
	if symfony.XMLServiceIsServiceID(params.Node) {
		return p.classCompletionItems()
	}

	return []protocol.CompletionItem{}
}

func (p *SymfonyCompletionProvider) yamlCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if items, matched := p.configuredServiceMethodCompletions(params); matched {
		return items
	}
	if items, matched := p.yamlNamedArgumentCompletions(params); matched {
		return items
	}

	if symfony.IsYAMLServiceID(params.Node) || symfony.IsYAMLClassPropertyInService(params.Node) {
		return p.classCompletionItems()
	}

	if symfony.IsYAMLArgumentServiceID(params.Node) {
		return p.serviceCompletionItems("@", "")
	}

	if len(symfony.ParameterReferences(symfony.YAMLContextValue(params.Node))) != 0 {
		parameters, err := p.serviceIndex.GetAllParameters()
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(parameters))
		for _, parameter := range parameters {
			items = append(items, protocol.CompletionItem{
				Label: parameter.Name,
				Kind:  21,
			})
		}
		return items
	}

	return []protocol.CompletionItem{}
}

func (p *SymfonyCompletionProvider) configuredServiceMethodCompletions(
	params *lsp.CompletionRequest,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.phpIndex == nil || params == nil ||
		params.CompletionParams == nil || params.Root == nil ||
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
		configuredServiceMap(params),
		p.serviceIndex,
	)
	if err != nil || !resolved {
		return nil, true
	}

	methods := p.phpIndex.Methods(className)
	items := make([]protocol.CompletionItem, 0, len(methods))
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method.Visibility != semantic.Public ||
			strings.HasPrefix(method.Name, "__") {
			continue
		}
		key := strings.ToLower(method.Name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		item := protocol.CompletionItem{
			Label:      method.Name,
			Kind:       int(protocol.MethodCompletion),
			Detail:     className + "::" + method.Name + "()",
			Deprecated: method.Flags.Has(semantic.DeprecatedFlag),
			TextEdit: protocol.TextEdit{
				Range: namedArgumentCompletionRange(
					reference.Range,
					params.LineIndex,
				),
				NewText: method.Name,
			},
		}
		if method.DocSummary() != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = method.DocSummary()
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items, true
}

func configuredServiceMap(
	params *lsp.CompletionRequest,
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

func (p *SymfonyCompletionProvider) yamlNamedArgumentCompletions(
	params *lsp.CompletionRequest,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.phpIndex == nil || params == nil ||
		params.CompletionParams == nil || params.Root == nil ||
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
	constructors := p.phpIndex.FindMethods(className, "__construct")
	if len(constructors) == 0 {
		return nil, true
	}

	current := normalizeNamedArgument(argument.Name)
	prefix := strings.ToLower(current)
	existing := make(map[string]struct{}, len(argument.Existing))
	for _, name := range argument.Existing {
		name = normalizeNamedArgument(name)
		if name != "" && name != current {
			existing[name] = struct{}{}
		}
	}

	var items []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, parameter := range constructors[0].Parameters {
		name := normalizeNamedArgument(parameter.Name)
		if name == "" ||
			!strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		if _, used := existing[name]; used {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		label := "$" + name
		newText := label
		if !argument.Complete {
			newText += ": "
		}
		items = append(items, protocol.CompletionItem{
			Label:  label,
			Kind:   int(protocol.VariableCompletion),
			Detail: parameter.Type.String(),
			TextEdit: protocol.TextEdit{
				Range: namedArgumentCompletionRange(
					argument.Range,
					params.LineIndex,
				),
				NewText: newText,
			},
			Documentation: struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}{
				Kind: string(protocol.Markdown),
				Value: fmt.Sprintf(
					"Constructor argument of `%s`",
					className,
				),
			},
		})
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items, true
}

func normalizeNamedArgument(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "$")
}

func namedArgumentCompletionRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func (p *SymfonyCompletionProvider) serviceCompletionItems(
	insertPrefix,
	excludedID string,
) []protocol.CompletionItem {
	services, err := p.serviceIndex.GetAllServiceDefinitions()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(services))
	for _, service := range services {
		if service.ID == excludedID {
			continue
		}
		item := protocol.CompletionItem{
			Label:      service.ID,
			Kind:       6,
			Deprecated: service.Deprecated,
		}
		if insertPrefix != "" {
			item.InsertText = fmt.Sprintf("%s%s", insertPrefix, service.ID)
		}
		if service.Deprecated {
			item.Detail = "Deprecated Symfony service"
		} else if service.Class != "" {
			item.Detail = service.Class
		}

		var documentation strings.Builder
		documentation.WriteString("Symfony service ID")
		if service.Class != "" {
			documentation.WriteString("\n\n**Class:** `")
			documentation.WriteString(service.Class)
			documentation.WriteString("`")
		}
		if service.AliasTarget != "" {
			documentation.WriteString("\n\n**Alias:** `")
			documentation.WriteString(service.AliasTarget)
			documentation.WriteString("`")
		}
		if service.Deprecated {
			documentation.WriteString("\n\n**Deprecated:** ")
			message := strings.TrimSpace(service.Deprecation)
			if message == "" {
				message = "This service is deprecated."
			} else {
				message = strings.ReplaceAll(
					message,
					"%service_id%",
					service.ID,
				)
				message = strings.ReplaceAll(
					message,
					"%alias_id%",
					service.ID,
				)
			}
			documentation.WriteString(message)
		}
		if len(service.Tags) > 0 {
			documentation.WriteString("\n\n**Tags:**\n")
			tags := make([]string, 0, len(service.Tags))
			for tag := range service.Tags {
				tags = append(tags, tag)
			}
			sort.Strings(tags)
			for _, tag := range tags {
				documentation.WriteString("- ")
				documentation.WriteString(tag)
				documentation.WriteByte('\n')
			}
		}
		item.Documentation.Kind = "markdown"
		item.Documentation.Value = documentation.String()
		items = append(items, item)
	}
	return items
}

func (p *SymfonyCompletionProvider) classCompletionItems() []protocol.CompletionItem {
	symbols := p.phpIndex.ClassSymbols()
	items := make([]protocol.CompletionItem, 0, len(symbols))
	seen := make(map[string]struct{}, len(symbols))
	for _, symbol := range symbols {
		name := symbol.FullyQualified
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deprecated := symbol.Flags.Has(semantic.DeprecatedFlag)
		item := protocol.CompletionItem{
			Label:      name,
			Kind:       6,
			Deprecated: deprecated,
		}
		if deprecated {
			item.Detail = "Deprecated PHP type"
			item.Documentation.Kind = "markdown"
			item.Documentation.Value = "**Deprecated PHP type**"
			if symbol.DocSummary() != "" {
				item.Documentation.Value += "\n\n" + symbol.DocSummary()
			}
		}
		items = append(items, item)
	}
	return items
}

// GetTriggerCharacters returns the characters that trigger this completion provider
func (p *SymfonyCompletionProvider) GetTriggerCharacters() []string {
	return []string{"\"", "'", "%", "$", ":"}
}
