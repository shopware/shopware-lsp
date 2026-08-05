package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminCompletionProvider provides completions for Shopware Admin Vue components
type AdminCompletionProvider struct {
	adminIndexer *admin.AdminComponentIndexer
}

// NewAdminCompletionProvider creates a new admin completion provider
func NewAdminCompletionProvider(adminIndexer *admin.AdminComponentIndexer) *AdminCompletionProvider {
	return &AdminCompletionProvider{adminIndexer: adminIndexer}
}

// GetCompletions returns completion items for admin components
func (p *AdminCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	languageAtCursor := lsp.EffectiveSyntaxLanguage(params.Language, params.Node)

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && languageAtCursor == language.JavaScript {
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.jsCompletions(ctx, params)
	}

	// Handle Twig files (admin templates)
	if ext == ".twig" || ext == ".vue" && languageAtCursor == language.Twig {
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		// Only process Twig files in administration directory
		if strings.Contains(params.TextDocument.URI, "Resources/app/administration") {
			return p.twigCompletions(ctx, params)
		}
	}

	return []protocol.CompletionItem{}
}

// jsCompletions handles completions in JS/TS files
func (p *AdminCompletionProvider) jsCompletions(_ context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if admin.IsApplicationContainerNameReference(params.Node) {
		return applicationContainerNameCompletions()
	}
	if _, _, matched := admin.JavaScriptShopwareEventBusEventAt(
		params.Node,
	); matched {
		return p.getShopwareEventBusEventCompletions(
			params.TextDocument.URI,
		)
	}
	if receiver, _, matched := admin.JavaScriptShopwareUtilsMember(
		params.Node,
	); matched {
		return p.getShopwareUtilsMemberCompletions(
			strings.Join(receiver, "."), params.TextDocument.URI,
		)
	}
	if receiver, _, matched := admin.JavaScriptShopwareContextMember(
		params.Node,
	); matched {
		return p.getShopwareContextMemberCompletions(
			strings.Join(receiver, "."), params.TextDocument.URI,
		)
	}
	if containerName, _, matched := admin.JavaScriptApplicationContainerMember(
		params.Node,
	); matched {
		return p.getApplicationContainerMemberCompletions(
			containerName, params.TextDocument.URI,
		)
	}
	if storeName, _, matched := jsquery.StoreMember(params.Node); matched {
		return p.getStoreMemberCompletions(storeName)
	}
	if _, matched := jsquery.ThisMember(params.Node); matched {
		if items, componentFile := p.getThisMemberCompletions(
			params.TextDocument.URI,
		); componentFile {
			return items
		}
	}
	var items []protocol.CompletionItem
	if admin.IsServiceReference(params.Node) {
		items = append(items, p.getServiceCompletions()...)
	}
	if admin.IsStoreReference(params.Node) {
		items = append(items, p.getStoreCompletions()...)
	}
	if admin.IsPrivilegeReference(params.Node) {
		items = append(items, p.getPrivilegeCompletions()...)
	}
	if kind, found := admin.JavaScriptCMSCompletionKindAt(params.Node); found {
		items = append(items, p.getCMSCompletions(kind)...)
	}
	if _, found := admin.JavaScriptCMSComponentReferenceAt(params.Node); found {
		items = append(items, p.getComponentCompletions()...)
	}

	// Check if we're in the second argument of Component.extend (parent component name)
	if p.isInExtendParentArgument(params.Node) {
		items = append(items, p.getComponentCompletions()...)
	}
	if jsquery.StringInCall(
		params.Node,
		0,
		"Component.override",
		"Shopware.Component.override",
	) {
		items = append(items, p.getComponentCompletions()...)
	}
	if jsquery.StringInCall(
		params.Node,
		0,
		"Mixin.getByName",
		"Shopware.Mixin.getByName",
	) {
		items = append(items, p.getMixinCompletions()...)
	}
	if jsquery.StringInCall(
		params.Node,
		0,
		"Directive.getByName",
		"Shopware.Directive.getByName",
	) {
		items = append(items, p.getDirectiveCompletions(false, "")...)
	}
	if jsquery.StringInCall(
		params.Node,
		0,
		"Filter.getByName",
		"Shopware.Filter.getByName",
	) {
		items = append(items, p.getFilterCompletions()...)
	}
	if reference, found := admin.JavaScriptRegistryReferenceAt(params.Node); found {
		switch reference.Kind {
		case admin.AdminSymbolComponent:
			items = append(items, p.getComponentCompletions()...)
		case admin.AdminSymbolModule:
			items = append(items, p.getModuleCompletions()...)
		}
	}
	if p.isModuleRouteReference(params.Node) {
		items = append(items, p.getModuleRouteCompletions()...)
	}

	return items
}

func (p *AdminCompletionProvider) getShopwareEventBusEventCompletions(
	uri string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveShopwareEventBusEvents(path)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(shape.Members))
	for _, event := range shape.Members {
		detail := "Shopware EventBus event"
		if event.Type != "" {
			detail += " · " + event.Type
		}
		items = append(items, protocol.CompletionItem{
			Label: event.Name, Kind: int(protocol.ValueCompletion), Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getShopwareUtilsMemberCompletions(
	receiver,
	uri string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveShopwareUtils(receiver, path)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(shape.Members))
	for _, member := range shape.Members {
		kind := protocol.PropertyCompletion
		memberType := strings.TrimSpace(member.Type)
		_, _, callable := admin.VueCallableSignature(memberType)
		if callable {
			kind = protocol.MethodCompletion
		}
		detail := "Shopware.Utils"
		if receiver != "" {
			detail += "." + receiver
		}
		if member.Type != "" {
			detail += " · " + member.Type
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: int(kind), Detail: detail,
		}
		if callable {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getShopwareContextMemberCompletions(
	receiver,
	uri string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveShopwareContext(receiver, path)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(shape.Members))
	for _, member := range shape.Members {
		kind := protocol.PropertyCompletion
		memberType := strings.TrimSpace(member.Type)
		callable := strings.Contains(memberType, "=>") ||
			strings.HasPrefix(memberType, "(")
		if callable {
			kind = protocol.MethodCompletion
		}
		detail := "Shopware.Context"
		if receiver != "" {
			detail += "." + receiver
		}
		if member.Type != "" {
			detail += " · " + member.Type
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: int(kind), Detail: detail,
		}
		if callable {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func applicationContainerNameCompletions() []protocol.CompletionItem {
	containers := admin.ApplicationContainers()
	items := make([]protocol.CompletionItem, 0, len(containers))
	for _, container := range containers {
		items = append(items, protocol.CompletionItem{
			Label: container.Name, Kind: int(protocol.ValueCompletion),
			Detail: container.Description + " · " + container.InterfaceName,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getApplicationContainerMemberCompletions(
	containerName,
	uri string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveApplicationContainer(
		containerName, path,
	)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(shape.Members))
	for _, member := range shape.Members {
		kind := protocol.PropertyCompletion
		memberType := strings.TrimSpace(member.Type)
		callable := strings.Contains(memberType, "=>") ||
			strings.HasPrefix(memberType, "(")
		if callable {
			kind = protocol.MethodCompletion
		}
		detail := "Application " + containerName + " container"
		if member.Type != "" {
			detail += " · " + member.Type
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: int(kind), Detail: detail,
		}
		if callable {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getServiceCompletions() []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	services, err := p.adminIndexer.GetAllServices()
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(services))
	for _, service := range services {
		itemsByName[service.Name] = protocol.CompletionItem{
			Label: service.Name, Kind: int(protocol.ReferenceCompletion),
			Detail: "Shopware Administration service",
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getCMSCompletions(
	kind admin.AdminCMSRegistrationKind,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	registrations, err := p.adminIndexer.GetAllCMSRegistrationsByKind(kind)
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(registrations))
	for _, registration := range registrations {
		detail := "Shopware CMS " + string(kind)
		if registration.Label != "" {
			detail += " · " + registration.Label
		}
		itemsByName[registration.Name] = protocol.CompletionItem{
			Label:  registration.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: detail,
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getStoreCompletions() []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	stores, err := p.adminIndexer.GetAllStores()
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(stores))
	for _, store := range stores {
		itemsByName[store.Name] = protocol.CompletionItem{
			Label: store.Name, Kind: int(protocol.ReferenceCompletion),
			Detail: "Shopware Administration store",
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getPrivilegeCompletions() []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	privileges, err := p.adminIndexer.GetAllPrivileges()
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(privileges))
	for _, privilege := range privileges {
		detail := "Administration privilege role"
		if privilege.IsBuiltin() {
			detail = "Built-in Shopware Administration privilege"
		} else if privilege.Kind == admin.AdminPrivilegePermission {
			detail = "Administration permission"
		}
		if privilege.MappingKey != "" {
			detail += " • " + privilege.MappingKey
			if privilege.Kind == admin.AdminPrivilegePermission && privilege.Role != "" {
				detail += "." + privilege.Role
			}
		}
		item := protocol.CompletionItem{
			Label: privilege.Name, Kind: int(protocol.ReferenceCompletion),
			Detail: detail,
		}
		// Prefer the public role key when an unusual mapping uses the same
		// spelling for both a role and a concrete permission.
		if existing, exists := itemsByName[privilege.Name]; !exists ||
			strings.Contains(existing.Detail, "permission") {
			itemsByName[privilege.Name] = item
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getStoreMemberCompletions(
	storeName string,
) []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	stores, err := p.adminIndexer.GetStore(storeName)
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem)
	for _, store := range stores {
		for _, member := range store.Members {
			detail := string(member.Kind) + " • " + store.Name
			if member.Type != "" {
				detail += " • " + member.Type
			}
			item := protocol.CompletionItem{
				Label:  member.Name,
				Kind:   adminStoreMemberCompletionKind(member.Kind),
				Detail: detail,
			}
			if member.Kind == admin.AdminStoreAction {
				item.InsertText = member.Name + "($0)"
				item.InsertTextFormat = int(protocol.SnippetTextFormat)
			}
			itemsByName[member.Name] = item
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func adminStoreMemberCompletionKind(kind admin.AdminStoreMemberKind) int {
	if kind == admin.AdminStoreAction {
		return int(protocol.MethodCompletion)
	}
	return int(protocol.PropertyCompletion)
}

func (p *AdminCompletionProvider) getThisMemberCompletions(
	uri string,
) ([]protocol.CompletionItem, bool) {
	path, err := uriutil.Path(uri)
	if err != nil || p.adminIndexer == nil {
		return nil, false
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil || len(components) == 0 {
		return nil, false
	}
	itemsByName := make(map[string]protocol.CompletionItem)
	for _, component := range components {
		for _, member := range component.TemplateMembers() {
			item := protocol.CompletionItem{
				Label:  member.Name,
				Kind:   adminMemberCompletionKind(member.Kind),
				Detail: adminMemberDetail(member) + " • " + component.Name,
			}
			if member.Kind == admin.ComponentMemberMethod {
				item.InsertText = member.Name + "($0)"
				item.InsertTextFormat = int(protocol.SnippetTextFormat)
			}
			markAdminCompletionDeprecated(&item, member.Deprecated)
			if member.Kind == admin.ComponentMemberProp {
				if prop, found := component.ComponentProp(member.Name); found {
					if !item.Deprecated {
						markAdminCompletionDeprecated(&item, prop.Deprecated)
					}
				}
			}
			itemsByName[member.Name] = item
		}
	}
	for _, member := range admin.VueBuiltinMembers() {
		if _, exists := itemsByName[member.Name]; exists {
			continue
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: adminMemberCompletionKind(member.Kind),
			Detail: "Vue instance member",
		}
		if member.Kind == admin.ComponentMemberMethod {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		itemsByName[member.Name] = item
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items, true
}

func (p *AdminCompletionProvider) getMixinCompletions() []protocol.CompletionItem {
	mixins, err := p.adminIndexer.GetAllMixins()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(mixins))
	for _, mixin := range mixins {
		items = append(items, protocol.CompletionItem{
			Label:  mixin.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: "Shopware Administration mixin",
		})
	}
	return items
}

func (p *AdminCompletionProvider) getDirectiveCompletions(
	markup bool,
	templatePath string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	var directives []admin.AdminDirective
	var err error
	if markup && templatePath != "" {
		directives, err = p.adminIndexer.GetAllDirectivesForTemplate(templatePath)
	} else {
		directives, err = p.adminIndexer.GetAllDirectives()
	}
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(directives))
	for _, directive := range directives {
		label := directive.Name
		if markup {
			label = "v-" + label
		}
		itemsByName[label] = protocol.CompletionItem{
			Label: label, Kind: int(protocol.FunctionCompletion),
			Detail: "Shopware Administration Vue directive",
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getFilterCompletions() []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil {
		return nil
	}
	filters, err := p.adminIndexer.GetAllFilters()
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(filters))
	for _, filter := range filters {
		detail := "Shopware Administration filter"
		if filter.Signature != "" {
			detail += " · " + filter.Signature
		}
		itemsByName[filter.Name] = protocol.CompletionItem{
			Label: filter.Name, Kind: int(protocol.FunctionCompletion), Detail: detail,
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getModuleCompletions() []protocol.CompletionItem {
	modules, err := p.adminIndexer.GetAllModules()
	if err != nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem, len(modules))
	for _, module := range modules {
		detail := "Shopware Administration module"
		if module.Title != "" {
			detail += " • " + module.Title
		}
		itemsByName[module.Name] = protocol.CompletionItem{
			Label: module.Name, Kind: int(protocol.ModuleCompletion),
			Detail: detail,
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) getModuleRouteCompletions() []protocol.CompletionItem {
	routes, err := p.adminIndexer.GetAllModuleRoutes()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		items = append(items, protocol.CompletionItem{
			Label:  route.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: route.Component,
		})
	}
	return items
}

func (p *AdminCompletionProvider) isModuleRouteReference(node *jssyntax.Node) bool {
	return admin.IsJavaScriptModuleRouteReference(node)
}

// twigCompletions handles completions in Twig admin templates
func (p *AdminCompletionProvider) twigCompletions(_ context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	node := params.Node
	token := params.Token
	content := params.DocumentContent
	offset, hasOffset := adminCompletionOffset(params)
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(content), params.LineIndex,
	)
	if hasOffset && admin.IsTwigBlockNameCompletionAt(node, offset) {
		return p.getParentBlockCompletions(templatePath)
	}

	if reference, found := adminTwigRegistryReferenceAtCompletion(params); found {
		switch reference.Kind {
		case admin.AdminSymbolPrivilege:
			return p.getPrivilegeCompletions()
		case admin.AdminSymbolModuleRoute:
			return p.getModuleRouteCompletions()
		}
	}
	if hasOffset {
		if items, handled := p.componentPropValueCompletions(
			params, offset, templatePath, liveOwner,
		); handled {
			return items
		}
	}
	if selector, dynamicComponent := adminDynamicComponentSelectorAtCompletion(
		params, offset, hasOffset,
	); dynamicComponent {
		return p.getDynamicComponentCompletions(
			templatePath, selector, offset, liveOwner,
		)
	}
	if hasOffset {
		if startTag, fields, objectBinding :=
			admin.TwigComponentObjectBindingKeyContextAtOffset(
				params.Root, offset,
			); objectBinding {
			return p.objectBindingPropCompletions(
				startTag, fields, templatePath, liveOwner,
			)
		}
	}

	resolvedSlots, _ := p.scopedSlotsAtCompletion(params)
	var resolvedSlot *admin.ResolvedTwigScopedSlot
	if len(resolvedSlots) > 0 {
		resolvedSlot = &resolvedSlots[len(resolvedSlots)-1]
	}
	vueBindings := p.twigVueBindingsAtCompletion(params, offset, hasOffset)
	if hasOffset && admin.IsTwigVueExpressionAt(node, offset) {
		if members, handled := p.twigVueMemberCompletionsAt(
			params, resolvedSlot, vueBindings, offset,
		); handled {
			return members
		}
	}
	if resolved := resolvedSlot; resolved != nil {
		if resolved.Scope.IsBindingOffset(offset) {
			return scopedSlotContractCompletions(*resolved)
		}
		if admin.IsTwigVueExpressionAt(node, offset) {
			return mergeAdminLexicalCompletions(
				p.getTemplateMemberCompletionsAt(
					params.TextDocument.URI, params.Node, params.Document,
				),
				resolvedSlots,
				vueBindings,
			)
		}
	}

	if twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar) != nil ||
		adminTwigVueExpressionAtCompletion(params) {
		return mergeAdminLexicalCompletions(
			p.getTemplateMemberCompletionsAt(
				params.TextDocument.URI, params.Node, params.Document,
			),
			resolvedSlots,
			vueBindings,
		)
	}

	// Check if we're in an HTML tag name position
	if p.isInHTMLTagName(node, token, content) {
		return p.getComponentTagCompletionsForOwner(templatePath, liveOwner)
	}

	// Check if we're in a slot name position (# or v-slot:)
	if items, handled := p.getResolvedSlotCompletions(
		node, content, templatePath, liveOwner,
	); handled {
		return items
	}
	if items, handled := p.dynamicComponentAttributeCompletions(
		node, templatePath, liveOwner,
	); handled {
		return items
	}

	// Custom directives are valid on both native elements and components. On a
	// component, merge them with its public prop/event/model contract.
	if p.isInHTMLAttributeCompletionPosition(node) {
		directives := p.getDirectiveCompletions(true, templatePath)
		if componentName := p.getComponentNameForAttributeCompletion(
			node, content,
		); componentName != "" {
			return mergeAdminCompletions(
				p.getComponentPropCompletionsForOwner(
					componentName, templatePath, liveOwner,
				),
				directives,
			)
		}
		return directives
	}

	return []protocol.CompletionItem{}
}

func (p *AdminCompletionProvider) getParentBlockCompletions(
	templatePath string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil || templatePath == "" {
		return nil
	}
	parent, err := p.adminIndexer.GetParentComponentForTemplate(templatePath)
	if err != nil || parent == nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(parent.Blocks))
	for _, block := range parent.Blocks {
		if block.Name == "" {
			continue
		}
		item := protocol.CompletionItem{
			Label: block.Name, Kind: int(protocol.MethodCompletion),
			Detail: "Administration Twig block · " + parent.Name,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Extensibility block declared by Administration component `" +
			parent.Name + "`."
		if block.FilePath != "" {
			item.Documentation.Value += "\n\n**Source:** `" +
				filepath.ToSlash(block.FilePath) + "`"
		}
		markAdminCompletionDeprecated(&item, block.Deprecated)
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

// getResolvedSlotCompletions resolves the component contract owning a slot
// directive. Closed dynamic component selectors contribute only slots shared
// by every possible component, so accepting a completion stays type-safe.
func (p *AdminCompletionProvider) getResolvedSlotCompletions(
	node *twigsyntax.Node,
	content []byte,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p.adminIndexer == nil || node == nil {
		return nil, false
	}
	attribute := twigquery.HTMLAttributeAt(node)
	if attribute == nil {
		return nil, false
	}
	attributeName := twigquery.HTMLAttributeName(attribute)
	if !strings.HasPrefix(attributeName, "#") &&
		!strings.HasPrefix(attributeName, "v-slot") {
		return nil, false
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components, complete, err :=
		p.adminIndexer.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, liveOwner,
		)
	if err != nil || !complete || len(components) == 0 {
		return []protocol.CompletionItem{}, true
	}
	return p.getSlotCompletionsForComponents(components, node, content), true
}

func (p *AdminCompletionProvider) componentPropValueCompletions(
	params *lsp.CompletionRequest,
	offset uint32,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil {
		return nil, false
	}
	startTag, propName, valueRange, found := componentPropValueContext(
		params.Root, params.DocumentContent, offset,
	)
	if !found {
		var field admin.VueObjectBindingField
		startTag, field, valueRange, found =
			admin.TwigComponentObjectBindingValueAtOffset(
				params.Root, offset,
			)
		propName = admin.NormalizePropName(field.Name)
	}
	if !found {
		return nil, false
	}
	components := p.componentsForPropValue(
		startTag, templatePath, liveOwner,
	)
	if len(components) == 0 {
		return nil, false
	}
	var common map[string]bool
	constrained := false
	complete := true
	for _, component := range components {
		prop, propFound := component.ComponentProp(propName)
		if !propFound {
			return nil, true
		}
		values, valuesComplete := admin.VuePropAllowedValues(prop)
		if len(values) == 0 {
			continue
		}
		complete = complete && valuesComplete
		current := make(map[string]bool, len(values))
		for _, value := range values {
			if value != "" {
				current[value] = true
			}
		}
		if !constrained {
			common = current
			constrained = true
			continue
		}
		for value := range common {
			if !current[value] {
				delete(common, value)
			}
		}
	}
	if !constrained {
		return nil, true
	}
	startLine, startCharacter := params.LineIndex.PositionUTF16(valueRange.Start)
	endLine, endCharacter := params.LineIndex.PositionUTF16(valueRange.End)
	editRange := protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
	detail := "component prop value • " + propName
	if !complete {
		detail = "known component prop value • " + propName
	}
	if len(components) > 1 {
		detail += " • all dynamic candidates"
	}
	items := make([]protocol.CompletionItem, 0, len(common))
	for value := range common {
		items = append(items, protocol.CompletionItem{
			Label: value, Kind: int(protocol.ValueCompletion), Detail: detail,
			TextEdit: protocol.TextEdit{Range: editRange, NewText: value},
		})
	}
	sortCompletionItems(items)
	return items, true
}

func (p *AdminCompletionProvider) componentsForPropValue(
	startTag *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
) []admin.VueComponent {
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, components, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
			)
		if err != nil || !complete {
			return nil
		}
		return components
	}
	name, found := admin.StaticComponentNameForTag(startTag)
	if !found {
		return nil
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, name, liveOwner,
	)
	if err != nil || !found || component == nil {
		return nil
	}
	return []admin.VueComponent{*component}
}

func componentPropValueContext(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (*twigsyntax.Node, string, cst.TextRange, bool) {
	if root == nil {
		return nil, "", cst.TextRange{}, false
	}
	for _, startTag := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if offset < startTag.Range().Start || offset > startTag.Range().End {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for _, attribute := range tag.Attributes() {
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			bound := strings.HasPrefix(name, ":") ||
				strings.HasPrefix(name, "v-bind:")
			if name == "" || strings.HasPrefix(name, "@") ||
				strings.HasPrefix(name, "#") ||
				strings.HasPrefix(name, "v-") && !bound {
				continue
			}
			if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic &&
				name == selector.AttributeName {
				continue
			}
			value, valueOK := attribute.Value()
			if !valueOK {
				continue
			}
			valueRange := cst.TextRange{}
			valueText := ""
			if inner, innerOK := value.GetInner(); innerOK {
				if bound {
					_, contentStart, contentEnd, literal :=
						admin.VueStaticStringLiteral(inner.Syntax().Text())
					if !literal {
						continue
					}
					valueRange = cst.TextRange{
						Start: inner.Syntax().Range().Start + contentStart,
						End:   inner.Syntax().Range().Start + contentEnd,
					}
				} else {
					valueRange = inner.Syntax().Range()
					valueText = inner.Syntax().Text()
				}
			} else {
				if bound {
					continue
				}
				opening := value.GetOpeningQuote()
				closing := value.GetClosingQuote()
				if opening == nil || closing == nil {
					continue
				}
				valueRange = cst.TextRange{
					Start: opening.Range().End, End: closing.Range().Start,
				}
			}
			if offset < valueRange.Start || offset > valueRange.End ||
				strings.Contains(valueText, "{{") ||
				strings.Contains(valueText, "{%") ||
				strings.Contains(valueText, "{#") ||
				valueRange.End > uint32(len(content)) {
				continue
			}
			propName := admin.NormalizePropName(name)
			if propName == "" {
				continue
			}
			return startTag, propName, valueRange, true
		}
	}
	return nil, "", cst.TextRange{}, false
}

func (p *AdminCompletionProvider) objectBindingPropCompletions(
	startTag *twigsyntax.Node,
	fields []admin.VueObjectBindingField,
	templatePath string,
	liveOwner *admin.VueComponent,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil
	}
	var components []admin.VueComponent
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, resolved, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, liveOwner, startTag,
			)
		if err != nil || !complete {
			return nil
		}
		components = resolved
	} else {
		name, found := admin.StaticComponentNameForTag(startTag)
		if !found {
			return nil
		}
		component, found, err := p.adminIndexer.GetComponentForTemplateTag(
			templatePath, name, liveOwner,
		)
		if err != nil || !found || component == nil {
			return nil
		}
		components = []admin.VueComponent{*component}
	}
	if len(components) == 0 {
		return nil
	}
	present := make(map[string]bool, len(fields))
	for _, field := range fields {
		present[admin.NormalizePropName(field.Name)] = true
	}
	common := make(map[string][]admin.VueComponentProp)
	for index, component := range components {
		current := make(map[string]admin.VueComponentProp, len(component.Props))
		for _, prop := range component.Props {
			current[prop.Name] = prop
		}
		if index == 0 {
			for name, prop := range current {
				common[name] = []admin.VueComponentProp{prop}
			}
			continue
		}
		for name, values := range common {
			prop, found := current[name]
			if !found {
				delete(common, name)
				continue
			}
			common[name] = append(values, prop)
		}
	}
	items := make([]protocol.CompletionItem, 0, len(common))
	for name, props := range common {
		if present[name] {
			continue
		}
		types := make([]string, 0, len(props))
		seenTypes := make(map[string]bool)
		required := true
		for _, prop := range props {
			typeName := strings.TrimSpace(prop.Type)
			if typeName != "" && !seenTypes[typeName] {
				seenTypes[typeName] = true
				types = append(types, typeName)
			}
			required = required && prop.Required
		}
		detail := strings.Join(types, " | ")
		if len(components) > 1 {
			detail = strings.TrimSpace(detail + " • all dynamic candidates")
		}
		item := protocol.CompletionItem{
			Label: name, Kind: int(protocol.PropertyCompletion), Detail: detail,
			InsertText: name + ": $0", InsertTextFormat: int(protocol.SnippetTextFormat),
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Forward component prop `" + name + "` through `v-bind`."
		if required {
			item.Documentation.Value += "\n\nRequired by every candidate contract."
		}
		markAdminCompletionDeprecated(&item, commonAdminPropDeprecation(props))
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func (p *AdminCompletionProvider) dynamicComponentAttributeCompletions(
	node *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || node == nil {
		return nil, false
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
	if !dynamic {
		return nil, false
	}
	_, components, complete, err :=
		p.adminIndexer.ResolveDynamicComponentContractsForOwner(
			templatePath, selector, liveOwner, startTag,
		)
	if err != nil || !complete || len(components) == 0 {
		return nil, true
	}
	common := make(map[string]protocol.CompletionItem)
	for index, component := range components {
		items := p.getComponentPropCompletionsForOwner(
			component.Name, templatePath, liveOwner,
		)
		current := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			current[item.Label] = item
		}
		if index == 0 {
			common = current
			continue
		}
		for label := range common {
			if _, found := current[label]; !found {
				delete(common, label)
			}
		}
	}
	result := make([]protocol.CompletionItem, 0, len(common))
	for _, item := range common {
		item.Detail = strings.TrimSpace(item.Detail + " • all dynamic candidates")
		result = append(result, item)
	}
	sortCompletionItems(result)
	return result, true
}

func adminDynamicComponentSelectorAtCompletion(
	params *lsp.CompletionRequest,
	offset uint32,
	hasOffset bool,
) (admin.VueDynamicComponentSelector, bool) {
	if params == nil || params.Node == nil || !hasOffset {
		return admin.VueDynamicComponentSelector{}, false
	}
	attribute := twigquery.HTMLAttributeAt(params.Node)
	startTag := twigquery.StartingHTMLTagAt(params.Node)
	if attribute == nil || startTag == nil {
		return admin.VueDynamicComponentSelector{}, false
	}
	selector, found := admin.TwigDynamicComponentSelector(startTag)
	if !found || twigquery.HTMLAttributeName(attribute) != selector.AttributeName {
		return admin.VueDynamicComponentSelector{}, false
	}
	if selector.ExpressionRange.Len() > 0 &&
		(offset < selector.ExpressionRange.Start || offset > selector.ExpressionRange.End) {
		return admin.VueDynamicComponentSelector{}, false
	}
	return selector, true
}

func (p *AdminCompletionProvider) twigVueMemberCompletionsAt(
	params *lsp.CompletionRequest,
	slot *admin.ResolvedTwigScopedSlot,
	_ []admin.TwigVueBinding,
	offset uint32,
) ([]protocol.CompletionItem, bool) {
	if p == nil || p.adminIndexer == nil || params == nil || params.Root == nil {
		return nil, false
	}
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found {
		return nil, false
	}
	slotOwnsRoot := false
	if slot != nil {
		for _, binding := range slot.Scope.Bindings {
			if binding.WholeObject && binding.LocalName == access.Root {
				slotOwnsRoot = true
				break
			}
		}
	}
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	resolvedVue, vueErr := p.adminIndexer.ResolveTwigVueMemberForComponent(
		params.Root, params.DocumentContent, offset, templatePath, liveComponent,
	)
	if vueErr != nil {
		return nil, true
	}
	if resolvedVue != nil && (!slotOwnsRoot || slot == nil ||
		resolvedVue.Binding.ScopeRange.Len() <= slot.Scope.TemplateRange.Len()) {
		if !resolvedVue.ReceiverFound {
			return nil, true
		}
		items := make(
			[]protocol.CompletionItem, 0, len(resolvedVue.ReceiverMembers),
		)
		for _, member := range resolvedVue.ReceiverMembers {
			if len(access.Receiver) == 0 && member.Range == access.MemberRange &&
				offset >= member.Range.End && len(
				admin.TwigVueBindingMemberReferences(
					params.Root, params.DocumentContent,
					resolvedVue.Binding, member.Name,
				),
			) == 1 {
				continue
			}
			kind := "observed member"
			if resolvedVue.MembersComplete || member.Type != "" ||
				member.DefinitionPath != "" {
				kind = "typed member"
			}
			detail := kind + " · " + resolvedVue.Binding.Name
			if resolvedVue.ReceiverType != "" {
				detail += " · " + resolvedVue.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			item := protocol.CompletionItem{
				Label: member.Name, Kind: int(protocol.PropertyCompletion),
				Detail: detail,
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Available on the lexical `" +
				resolvedVue.Binding.Name + "` binding in this template."
			items = append(items, item)
		}
		sortCompletionItems(items)
		return items, true
	}
	if slotOwnsRoot {
		resolvedMember, resolveErr :=
			p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
				params.Root, params.Node, params.DocumentContent, offset,
				templatePath, liveComponent,
			)
		if resolveErr != nil || resolvedMember == nil ||
			!resolvedMember.ReceiverFound {
			return nil, true
		}
		if len(access.Receiver) == 0 {
			return scopedSlotContractCompletions(*slot), true
		}
		items := make(
			[]protocol.CompletionItem, 0,
			len(resolvedMember.ReceiverMembers),
		)
		for _, member := range resolvedMember.ReceiverMembers {
			detail := "slot member · " + resolvedMember.QualifiedName()
			if resolvedMember.ReceiverType != "" {
				detail += " · " + resolvedMember.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			items = append(items, protocol.CompletionItem{
				Label: member.Name, Kind: int(protocol.PropertyCompletion),
				Detail: detail,
			})
		}
		sortCompletionItems(items)
		return items, true
	}
	resolvedInstance, instanceErr :=
		p.adminIndexer.ResolveTwigVueInstanceMemberForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveComponent,
		)
	if instanceErr != nil {
		return nil, true
	}
	if resolvedInstance != nil {
		if !resolvedInstance.ReceiverFound {
			return nil, true
		}
		items := make(
			[]protocol.CompletionItem, 0,
			len(resolvedInstance.ReceiverMembers),
		)
		for _, member := range resolvedInstance.ReceiverMembers {
			kind := protocol.PropertyCompletion
			if strings.Contains(member.Type, "=>") {
				kind = protocol.MethodCompletion
			}
			detail := "component member · " + resolvedInstance.Access.Root
			if resolvedInstance.ReceiverType != "" {
				detail += " · " + resolvedInstance.ReceiverType
			}
			if member.Type != "" {
				detail += " · " + member.Type
			}
			item := protocol.CompletionItem{
				Label: member.Name, Kind: int(kind), Detail: detail,
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Available through `" +
				resolvedInstance.QualifiedName() + "` on Administration component `" +
				resolvedInstance.Component.Name + "`."
			items = append(items, item)
		}
		sortCompletionItems(items)
		return items, true
	}
	// A direct member expression should never fall back to unrelated Vue
	// instance members. Unknown shapes intentionally produce no suggestions.
	return nil, true
}

func adminCompletionOffset(params *lsp.CompletionRequest) (uint32, bool) {
	if params == nil || params.LineIndex == nil || params.CompletionParams == nil {
		return 0, false
	}
	return params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	), true
}

func (p *AdminCompletionProvider) twigVueBindingsAtCompletion(
	params *lsp.CompletionRequest,
	offset uint32,
	hasOffset bool,
) []admin.TwigVueBinding {
	if p.adminIndexer == nil || params == nil || params.Root == nil || !hasOffset {
		return nil
	}
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	bindings, err := p.adminIndexer.ResolveTwigVueBindingsForComponent(
		params.Root, params.DocumentContent, offset,
		templatePath, liveComponent,
	)
	if err != nil {
		return nil
	}
	return bindings
}

func adminTemplatePath(uri string) string {
	path, err := uriutil.Path(uri)
	if err != nil {
		return ""
	}
	return path
}

func adminTwigVueExpressionAtCompletion(params *lsp.CompletionRequest) bool {
	if params == nil || params.Node == nil || params.LineIndex == nil ||
		params.CompletionParams == nil {
		return false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	return admin.IsTwigVueExpressionAt(params.Node, offset)
}

func (p *AdminCompletionProvider) scopedSlotsAtCompletion(
	params *lsp.CompletionRequest,
) ([]admin.ResolvedTwigScopedSlot, uint32) {
	if p.adminIndexer == nil || params == nil || params.Root == nil ||
		params.LineIndex == nil || params.CompletionParams == nil {
		return nil, 0
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line), uint32(params.Position.Character),
	)
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	resolved, err := p.adminIndexer.ResolveTwigScopedSlotsForOwner(
		params.Root, offset, templatePath, liveOwner,
	)
	if err != nil {
		return nil, offset
	}
	return resolved, offset
}

func scopedSlotContractCompletions(
	resolved admin.ResolvedTwigScopedSlot,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(resolved.Slot.Members))
	for _, member := range resolved.Slot.Members {
		detail := "slot prop · " + resolved.QualifiedName()
		if member.Type != "" {
			detail += " · " + member.Type
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: int(protocol.PropertyCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Exposed by scoped slot `" +
			resolved.QualifiedName() + "`."
		if resolved.Slot.IsDynamicName() {
			item.Documentation.Value += "\n\nProvided by dynamic family `" +
				resolved.Slot.DisplayName() + "`."
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func scopedSlotLocalCompletions(
	resolved admin.ResolvedTwigScopedSlot,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(resolved.Scope.Bindings))
	seen := make(map[string]bool, len(resolved.Scope.Bindings))
	for _, binding := range resolved.Scope.Bindings {
		if binding.LocalName == "" || seen[binding.LocalName] {
			continue
		}
		seen[binding.LocalName] = true
		detail := "slot local · " + resolved.QualifiedName()
		member, found := resolved.Slot.Member(binding.MemberName)
		memberType := ""
		if binding.WholeObject {
			memberType = resolved.Slot.PayloadType
		} else if found {
			memberType = member.Type
		}
		if memberType != "" {
			detail += " · " + memberType
		}
		item := protocol.CompletionItem{
			Label: binding.LocalName, Kind: int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Lexically bound by scoped slot `" +
			resolved.QualifiedName() + "`."
		if resolved.Slot.IsDynamicName() {
			item.Documentation.Value += "\n\nProvided by dynamic family `" +
				resolved.Slot.DisplayName() + "`."
		}
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func twigVueBindingCompletions(
	bindings []admin.TwigVueBinding,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Name == "" {
			continue
		}
		detail := "v-for local"
		documentation := "Introduced by the enclosing `v-for` expression."
		if binding.Kind == admin.TwigVueBindingEvent {
			detail = "event payload"
			if binding.ComponentName != "" && binding.EventName != "" {
				detail += " · " + binding.ComponentName + "." + binding.EventName
			}
			documentation = "Implicit payload of the current Vue event handler."
		} else if binding.Iterable != "" {
			detail += " · " + binding.Iterable
		}
		if binding.Type != "" {
			detail += " · " + binding.Type
		}
		item := protocol.CompletionItem{
			Label: binding.Name, Kind: int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = documentation
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

type adminLexicalCompletionLayer struct {
	scope uint32
	items []protocol.CompletionItem
}

// mergeAdminLexicalCompletions applies Vue's normal lexical shadowing: outer
// component members first, then increasingly smaller v-for/event/v-slot
// scopes. The innermost binding therefore owns a duplicate completion label.
func mergeAdminLexicalCompletions(
	base []protocol.CompletionItem,
	slots []admin.ResolvedTwigScopedSlot,
	bindings []admin.TwigVueBinding,
) []protocol.CompletionItem {
	var layers []adminLexicalCompletionLayer
	for slotIndex := range slots {
		slot := slots[slotIndex]
		layers = append(layers, adminLexicalCompletionLayer{
			scope: slot.Scope.TemplateRange.Len(),
			items: scopedSlotLocalCompletions(slot),
		})
	}
	byScope := make(map[admin.TwigVueBindingKind]map[cst.TextRange][]admin.TwigVueBinding)
	for _, binding := range bindings {
		if byScope[binding.Kind] == nil {
			byScope[binding.Kind] = make(map[cst.TextRange][]admin.TwigVueBinding)
		}
		byScope[binding.Kind][binding.ScopeRange] = append(
			byScope[binding.Kind][binding.ScopeRange], binding,
		)
	}
	for _, scopes := range byScope {
		for scope, values := range scopes {
			layers = append(layers, adminLexicalCompletionLayer{
				scope: scope.Len(), items: twigVueBindingCompletions(values),
			})
		}
	}
	sort.SliceStable(layers, func(left, right int) bool {
		return layers[left].scope > layers[right].scope
	})
	result := base
	for _, layer := range layers {
		result = mergeAdminCompletions(result, layer.items)
	}
	return result
}

// mergeAdminCompletions overlays right by label, giving lexical bindings the
// expected precedence over component members with the same name.
func mergeAdminCompletions(
	left, right []protocol.CompletionItem,
) []protocol.CompletionItem {
	itemsByLabel := make(map[string]protocol.CompletionItem, len(left)+len(right))
	for _, item := range left {
		itemsByLabel[item.Label] = item
	}
	for _, item := range right {
		itemsByLabel[item.Label] = item
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByLabel))
	for _, item := range itemsByLabel {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func adminTwigRegistryReferenceAtCompletion(
	params *lsp.CompletionRequest,
) (admin.AdminTwigRegistryReference, bool) {
	if params == nil || params.Root == nil || params.LineIndex == nil ||
		params.CompletionParams == nil {
		return admin.AdminTwigRegistryReference{}, false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	return admin.TwigRegistryReferenceAtOffset(params.Root, offset)
}

func (p *AdminCompletionProvider) getTemplateMemberCompletions(
	uri string,
) []protocol.CompletionItem {
	return p.getTemplateMemberCompletionsAt(uri, nil)
}

func (p *AdminCompletionProvider) getTemplateMemberCompletionsAt(
	uri string,
	node *twigsyntax.Node,
	documents ...*lsp.TextDocument,
) []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	var component *admin.VueComponent
	if len(documents) > 0 && documents[0] != nil &&
		documents[0].SyntaxTree != nil {
		document := documents[0]
		component, err = p.adminIndexer.GetComponentForDocument(
			path, document.SyntaxTree.Root, document.Source, document.LineIndex,
		)
	} else {
		component, err = p.adminIndexer.GetComponentByTemplatePath(path)
	}
	if err != nil || component == nil {
		return nil
	}
	itemsByName := make(map[string]protocol.CompletionItem)
	for _, member := range component.TemplateMembers() {
		item := protocol.CompletionItem{
			Label:  member.Name,
			Kind:   adminMemberCompletionKind(member.Kind),
			Detail: adminMemberDetail(member),
		}
		if member.Kind == admin.ComponentMemberMethod {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Provided by Administration component `" +
			component.Name + "`."
		markAdminCompletionDeprecated(&item, member.Deprecated)
		if member.Kind == admin.ComponentMemberProp && !item.Deprecated {
			if prop, found := component.ComponentProp(member.Name); found {
				markAdminCompletionDeprecated(&item, prop.Deprecated)
			}
		}
		itemsByName[member.Name] = item
	}
	addRuntime := func(member admin.VueComponentMember, detail, documentation string) {
		if member.Name == "" {
			return
		}
		if _, exists := itemsByName[member.Name]; exists {
			return
		}
		item := protocol.CompletionItem{
			Label: member.Name, Kind: adminMemberCompletionKind(member.Kind),
			Detail: detail,
		}
		if member.Kind == admin.ComponentMemberMethod {
			item.InsertText = member.Name + "($0)"
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = documentation
		if member.Type != "" {
			item.Documentation.Value += "\n\nType: `" + member.Type + "`."
		}
		itemsByName[member.Name] = item
	}
	for _, member := range admin.VueBuiltinMembers() {
		addRuntime(
			member,
			"Vue template instance member",
			"Available on the Administration Vue component instance.",
		)
	}
	for _, global := range admin.VueTemplateGlobals() {
		addRuntime(
			global,
			"JavaScript template global",
			"Available in Administration Vue template expressions.",
		)
	}
	if node != nil {
		if blockName := twigquery.BlockName(twigquery.BlockAt(node)); blockName != "" {
			if block, found := component.ComponentBlock(blockName); found {
				for _, member := range block.ScopeMembers {
					if member.Name == "" {
						continue
					}
					item := protocol.CompletionItem{
						Label:  member.Name,
						Kind:   int(protocol.VariableCompletion),
						Detail: "Twig block scope • " + block.Name,
					}
					item.Documentation.Kind = string(protocol.Markdown)
					item.Documentation.Value = "Provided by Administration Twig block `" +
						block.Name + "`."
					if member.Type != "" {
						item.Documentation.Value += "\n\nType: `" + member.Type + "`."
					}
					// Lexical block inputs shadow equally named component members.
					itemsByName[member.Name] = item
				}
			}
		}
	}
	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

func adminMemberCompletionKind(kind admin.VueComponentMemberKind) int {
	switch kind {
	case admin.ComponentMemberMethod:
		return int(protocol.MethodCompletion)
	case admin.ComponentMemberComputed:
		return int(protocol.PropertyCompletion)
	case admin.ComponentMemberInject:
		return int(protocol.ReferenceCompletion)
	default:
		return int(protocol.VariableCompletion)
	}
}

func adminMemberDetail(member admin.VueComponentMember) string {
	detail := string(member.Kind)
	if member.Type != "" {
		detail += " • " + member.Type
	}
	return detail
}

// isInHTMLTagName checks if the cursor is in an HTML tag name position
func (p *AdminCompletionProvider) isInHTMLTagName(node *twigsyntax.Node, token *twigsyntax.Token, content []byte) bool {
	if node == nil || token == nil {
		return false
	}

	parent := token.Parent()
	if parent != nil && (parent.Kind() == twigsyntax.HtmlStartingTag || parent.Kind() == twigsyntax.HtmlEndingTag) {
		for child := range parent.ChildTokens() {
			if (child.Kind() == twigsyntax.TkWord || child.Kind() == twigsyntax.TkTwigComponentName) &&
				child.Range() == token.Range() {
				return true
			}
		}
	}

	if token.Kind() == twigsyntax.TkLessThan || token.Kind() == twigsyntax.TkLessThanSlash {
		return true
	}

	beforeCursor := content
	if end := int(token.Range().End); end < len(beforeCursor) {
		beforeCursor = beforeCursor[:end]
	}
	lastLT := strings.LastIndex(string(beforeCursor), "<")
	if lastLT >= 0 {
		partial := string(beforeCursor[lastLT+1:])
		return !strings.ContainsAny(partial, "> \t\n\r")
	}

	return false
}

// getComponentTagCompletions returns completion items for component tags in Twig
func (p *AdminCompletionProvider) getComponentTagCompletions(
	templatePaths ...string,
) []protocol.CompletionItem {
	return p.getComponentTagCompletionsForOwner(
		optionalTemplatePath(templatePaths), nil,
	)
}

func (p *AdminCompletionProvider) getComponentTagCompletionsForOwner(
	templatePath string,
	owner *admin.VueComponent,
) []protocol.CompletionItem {
	componentNames, err := p.adminIndexer.GetAllComponentNames()
	if err != nil {
		return []protocol.CompletionItem{}
	}
	if templatePath != "" {
		if owner == nil {
			owner, _ = p.adminIndexer.GetComponentByTemplatePath(templatePath)
		}
		if owner != nil {
			for _, local := range owner.LocalComponents {
				componentNames = append(componentNames, local.Name)
			}
		}
	}
	componentNames = uniqueSortedStrings(componentNames)

	items := make([]protocol.CompletionItem, 0, len(componentNames))
	for _, name := range componentNames {
		// Create snippet: <component-name>$0</component-name>
		// $0 is the cursor position after insertion
		snippet := name + ">$0</" + name + ">"

		item := protocol.CompletionItem{
			Label:            name,
			Kind:             int(protocol.ClassCompletion),
			InsertText:       snippet,
			InsertTextFormat: int(protocol.SnippetTextFormat),
		}

		// Try to get component details for documentation
		component, found, resolveErr := p.adminIndexer.GetComponentForTemplateTag(
			templatePath, name, owner,
		)
		if resolveErr == nil && found && component != nil {
			comp := *component
			doc := "**Shopware Admin Component**\n\n"

			if comp.ExtendsComponent != "" {
				doc += "**Extends:** `" + comp.ExtendsComponent + "`\n\n"
			}

			if len(comp.Props) > 0 {
				doc += "**Props:** "
				propNames := make([]string, 0, len(comp.Props))
				for _, prop := range comp.Props {
					propNames = append(propNames, prop.Name)
				}
				doc += strings.Join(propNames, ", ") + "\n"
			}

			item.Documentation.Kind = "markdown"
			item.Documentation.Value = doc
			markAdminCompletionDeprecated(&item, comp.Deprecated)
		}

		items = append(items, item)
	}

	// Add template tag with slot shorthand
	// Don't close the template yet - the slot completion will close it
	templateItem := protocol.CompletionItem{
		Label:            "template",
		Kind:             int(protocol.ClassCompletion),
		Detail:           "slot template",
		InsertText:       "template #",
		InsertTextFormat: int(protocol.SnippetTextFormat),
	}
	templateItem.Documentation.Kind = "markdown"
	templateItem.Documentation.Value = "**Vue Slot Template**\n\nUsed to fill named slots in parent components.\n\nExample: `<template #default>...</template>`"
	items = append(items, templateItem)

	return items
}

// isInExtendParentArgument checks if cursor is in the parent component argument of Component.extend
// Pattern: Component.extend('name', '<caret>', ...)
func (p *AdminCompletionProvider) isInExtendParentArgument(node *jssyntax.Node) bool {
	if !p.isSecondStringArgument(node) {
		return false
	}
	name := jsquery.CallName(node)
	return name == "Component.extend" || name == "Shopware.Component.extend"
}

func (p *AdminCompletionProvider) isSecondStringArgument(node *jssyntax.Node) bool {
	return jsquery.StringAt(node) != nil && jsquery.StringArgumentIndex(node) == 1
}

// getComponentCompletions returns completion items for all registered components
func (p *AdminCompletionProvider) getComponentCompletions() []protocol.CompletionItem {
	componentNames, err := p.adminIndexer.GetAllComponentNames()
	if err != nil {
		return []protocol.CompletionItem{}
	}

	items := make([]protocol.CompletionItem, 0, len(componentNames))
	for _, name := range componentNames {
		item := protocol.CompletionItem{
			Label: name,
			Kind:  int(protocol.ClassCompletion),
		}

		// Try to get component details for documentation
		comp, err := p.adminIndexer.GetEffectiveComponent(name)
		if err == nil && comp != nil {
			doc := "**Shopware Admin Component**\n\n"

			if comp.ExtendsComponent != "" {
				doc += "**Extends:** `" + comp.ExtendsComponent + "`\n\n"
			}

			if comp.FilePath != "" {
				doc += "**Registered in:** `" + filepath.Base(comp.FilePath) + "`\n"
			}

			item.Documentation.Kind = "markdown"
			item.Documentation.Value = doc
			markAdminCompletionDeprecated(&item, comp.Deprecated)
		}

		items = append(items, item)
	}

	return items
}

func (p *AdminCompletionProvider) getDynamicComponentCompletions(
	templatePath string,
	selector admin.VueDynamicComponentSelector,
	offset uint32,
	owners ...*admin.VueComponent,
) []protocol.CompletionItem {
	items := p.getComponentCompletions()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Label] = true
	}
	if templatePath != "" {
		var owner *admin.VueComponent
		if len(owners) > 0 {
			owner = owners[0]
		}
		if owner == nil {
			owner, _ = p.adminIndexer.GetComponentByTemplatePath(templatePath)
		}
		if owner != nil {
			for _, local := range owner.LocalComponents {
				if local.Name == "" || seen[local.Name] {
					continue
				}
				seen[local.Name] = true
				items = append(items, protocol.CompletionItem{
					Label:  local.Name,
					Kind:   int(protocol.ClassCompletion),
					Detail: "Template-local Administration component",
				})
			}
		}
	}
	_, insideLiteral := selector.CandidateAt(offset)
	if !insideLiteral && offset > 0 {
		_, insideLiteral = selector.CandidateAt(offset - 1)
	}
	for index := range items {
		items[index].Detail = "Vue dynamic component"
		if items[index].Deprecated {
			items[index].Detail = "Deprecated Vue dynamic component"
		}
		items[index].InsertText = items[index].Label
		items[index].InsertTextFormat = int(protocol.PlainTextFormat)
		if selector.AttributeName != "is" && !insideLiteral {
			items[index].InsertText = "'" + items[index].Label + "'"
		}
	}
	sortCompletionItems(items)
	return items
}

// getComponentNameForAttributeCompletion checks if we're in a position to complete attributes
// and returns the component name if so, empty string otherwise
func (p *AdminCompletionProvider) getComponentNameForAttributeCompletion(node *twigsyntax.Node, content []byte) string {
	if !p.isInHTMLAttributeCompletionPosition(node) {
		return ""
	}

	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return ""
	}

	componentName := twigquery.HTMLTagName(startTag)
	if !admin.IsComponentTag(componentName) {
		var found bool
		componentName, found = admin.StaticComponentNameForTag(startTag)
		if !found {
			return ""
		}
	}

	return componentName
}

func (p *AdminCompletionProvider) isInHTMLAttributeCompletionPosition(
	node *twigsyntax.Node,
) bool {
	if node == nil {
		return false
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return false
	}
	if node.Kind() == twigsyntax.HtmlStartingTag ||
		twigquery.HTMLAttributeAt(node) != nil ||
		node.Kind() == twigsyntax.Error {
		return true
	}
	for child := range startTag.ChildTokens() {
		if (child.Kind() == twigsyntax.TkWord || child.Kind() == twigsyntax.TkTwigComponentName) &&
			node.Range().Start >= child.Range().Start && node.Range().End <= child.Range().End {
			return false
		}
	}
	return true
}

// getComponentPropCompletions returns completion items for component props
func (p *AdminCompletionProvider) getComponentPropCompletions(
	componentName string,
	templatePaths ...string,
) []protocol.CompletionItem {
	return p.getComponentPropCompletionsForOwner(
		componentName, optionalTemplatePath(templatePaths), nil,
	)
}

func (p *AdminCompletionProvider) getComponentPropCompletionsForOwner(
	componentName,
	templatePath string,
	owner *admin.VueComponent,
) []protocol.CompletionItem {
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, componentName, owner,
	)
	if err != nil || !found || component == nil {
		return []protocol.CompletionItem{}
	}

	var items []protocol.CompletionItem

	for _, comp := range []admin.VueComponent{*component} {
		// Add props
		for _, prop := range comp.Props {
			attributeName := admin.CamelToKebab(prop.Name)
			// Regular prop
			item := protocol.CompletionItem{
				Label:  attributeName,
				Kind:   int(protocol.PropertyCompletion),
				Detail: prop.Type,
			}

			// Build documentation
			doc := strings.TrimSpace(prop.Documentation)
			if doc != "" {
				doc += "\n\n"
			}
			if prop.Type != "" {
				doc += "**Type:** `" + prop.Type + "`\n\n"
			}
			if prop.Required {
				doc += "**Required**\n\n"
			}
			if prop.Default != "" {
				doc += "**Default:** `" + prop.Default + "`\n"
			}
			if values, complete := admin.VuePropAllowedValues(prop); len(values) > 0 {
				label := "**Allowed values:** "
				if !complete {
					label = "**Known values:** "
				}
				doc += label + adminCompletionValueList(values) + "\n"
			}

			if doc != "" {
				item.Documentation.Kind = "markdown"
				item.Documentation.Value = doc
			}
			markAdminCompletionDeprecated(&item, prop.Deprecated)

			items = append(items, item)

			// Also add Vue binding shorthand (:prop)
			bindingItem := protocol.CompletionItem{
				Label:            ":" + attributeName,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           prop.Type + " (v-bind)",
				InsertText:       ":" + attributeName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			if doc != "" {
				bindingItem.Documentation.Kind = "markdown"
				bindingItem.Documentation.Value = doc
			}
			markAdminCompletionDeprecated(&bindingItem, prop.Deprecated)
			items = append(items, bindingItem)
		}

		// Add events (emits)
		for _, event := range comp.ComponentEvents() {
			eventName := admin.CanonicalEventName(event.Name)
			if eventName == "" {
				continue
			}
			detail := "event"
			if event.Type != "" {
				detail += " • " + event.Type
			}
			item := protocol.CompletionItem{
				Label:            "@" + eventName,
				Kind:             int(protocol.EventCompletion),
				Detail:           detail,
				InsertText:       "@" + eventName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			documentation := strings.TrimSpace(event.Documentation)
			if event.Type != "" {
				if documentation != "" {
					documentation += "\n\n"
				}
				documentation += "**Payload:** `" + event.Type + "`"
			}
			if documentation != "" {
				item.Documentation.Kind = string(protocol.Markdown)
				item.Documentation.Value = documentation
			}
			items = append(items, item)
		}

		// A model completion represents both halves of Vue's public contract:
		// the readable prop and its matching update event.
		for _, model := range comp.ComponentModels() {
			detail := "v-model • " + model.PropName + " / " + model.EventName
			if valueType := admin.VuePropValueType(model.Prop.Type); valueType != "" {
				detail += " • " + valueType
			}
			item := protocol.CompletionItem{
				Label:            model.AttributeName,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           detail,
				InsertText:       model.AttributeName + "=\"$0\"",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = "Two-way binding for prop `" +
				model.PropName + "` through event `" + model.EventName + "`."
			markAdminCompletionDeprecated(&item, model.Prop.Deprecated)
			items = append(items, item)
		}
	}

	return items
}

func adminCompletionValueList(values []string) string {
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		display := value
		if display == "" {
			display = "(empty)"
		}
		formatted = append(formatted, "`"+strings.ReplaceAll(display, "`", "\\`")+"`")
	}
	return strings.Join(formatted, ", ")
}

func markAdminCompletionDeprecated(
	item *protocol.CompletionItem,
	message string,
) {
	message = strings.TrimSpace(message)
	if item == nil || message == "" {
		return
	}
	item.Deprecated = true
	if item.Detail == "" {
		item.Detail = "Deprecated Administration API"
	} else if !strings.HasPrefix(strings.ToLower(item.Detail), "deprecated") {
		item.Detail = "Deprecated • " + item.Detail
	}
	deprecation := "**Deprecated:** " + message
	if item.Documentation.Value == "" {
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = deprecation
	} else {
		item.Documentation.Value = deprecation + "\n\n" + item.Documentation.Value
	}
}

func commonAdminPropDeprecation(props []admin.VueComponentProp) string {
	if len(props) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var messages []string
	for _, prop := range props {
		message := strings.TrimSpace(prop.Deprecated)
		if message == "" {
			return ""
		}
		if !seen[message] {
			seen[message] = true
			messages = append(messages, message)
		}
	}
	return strings.Join(messages, " / ")
}

// GetTriggerCharacters returns the characters that trigger this completion provider
func (p *AdminCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\"", "<", " ", "#", ".", "-"}
}

// getSlotCompletions returns completion items for slot names of a component
func (p *AdminCompletionProvider) getSlotCompletions(
	componentName string,
	node *twigsyntax.Node,
	content []byte,
	templatePaths ...string,
) []protocol.CompletionItem {
	if p.adminIndexer == nil {
		return []protocol.CompletionItem{}
	}

	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		optionalTemplatePath(templatePaths), componentName,
	)
	if err != nil || !found || component == nil {
		return []protocol.CompletionItem{}
	}
	return p.getSlotCompletionsForComponents(
		[]admin.VueComponent{*component}, node, content,
	)
}

func (p *AdminCompletionProvider) getSlotCompletionsForComponents(
	components []admin.VueComponent,
	node *twigsyntax.Node,
	content []byte,
) []protocol.CompletionItem {
	_ = content
	if len(components) == 0 {
		return []protocol.CompletionItem{}
	}

	// Check if the node already contains a closing >
	// If so, we just insert the slot name (without > or </template>)
	var hasClosingBracket bool
	if node != nil {
		nodeText := node.Text()
		if strings.HasPrefix(nodeText, "#") && strings.Contains(nodeText, ">") {
			hasClosingBracket = true
		}
	}

	counts := make(map[string]int)
	slots := make(map[string]admin.VueComponentSlot)
	for _, comp := range components {
		seenComponentSlots := make(map[string]bool)
		for _, slot := range comp.Slots {
			if slot.IsDynamicName() || slot.Name == "" ||
				seenComponentSlots[slot.Name] {
				continue
			}
			seenComponentSlots[slot.Name] = true
			counts[slot.Name]++
			slots[slot.Name] = slot
		}
	}

	names := make([]string, 0, len(slots))
	for name := range slots {
		if counts[name] == len(components) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	componentNames := make([]string, 0, len(components))
	for _, component := range components {
		componentNames = append(componentNames, component.Name)
	}
	sort.Strings(componentNames)

	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		slot := slots[name]

		item := protocol.CompletionItem{
			Label:            slot.Name,
			Kind:             int(protocol.PropertyCompletion),
			Detail:           "slot",
			InsertTextFormat: int(protocol.SnippetTextFormat),
		}
		if len(components) > 1 {
			item.Detail = "slot • all dynamic candidates"
		}

		// If there's already a closing >, just insert the slot name
		// Otherwise insert the full snippet with > and </template>
		if hasClosingBracket {
			item.InsertText = slot.Name
			item.InsertTextFormat = int(protocol.PlainTextFormat)
		} else {
			item.InsertText = slot.Name + ">$0</template>"
		}

		// Add documentation
		doc := "**Slot:** `" + slot.Name + "`\n\n"
		if len(componentNames) == 1 {
			doc += "**Component:** `" + componentNames[0] + "`"
		} else {
			doc += "**Components:** `" + strings.Join(componentNames, "`, `") + "`"
		}
		item.Documentation.Kind = "markdown"
		item.Documentation.Value = doc

		items = append(items, item)
	}

	return items
}

func optionalTemplatePath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
