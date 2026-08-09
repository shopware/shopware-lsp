package completion

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

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
