package symbol

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type adminWorkspaceSymbolCollector struct {
	ctx     context.Context
	index   *admin.AdminComponentIndexer
	query   string
	result  []protocol.SymbolInformation
	seenAPI map[componentAPISymbolKey]bool
}

type componentAPISymbolKey struct {
	name string
	path string
	line int
	kind protocol.SymbolKind
}

func (p *AdminWorkspaceSymbolProvider) WorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, error) {
	if p == nil || p.index == nil {
		return nil, nil
	}
	collector := &adminWorkspaceSymbolCollector{
		ctx:     ctx,
		index:   p.index,
		query:   strings.ToLower(strings.TrimSpace(query)),
		seenAPI: make(map[componentAPISymbolKey]bool),
	}
	steps := []func() error{
		collector.collectComponents,
		collector.collectComponentAPI,
		collector.collectMixins,
		collector.collectDirectives,
		collector.collectFilters,
		collector.collectCMS,
		collector.collectModules,
		collector.collectServices,
		collector.collectStores,
		collector.collectPrivileges,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return collector.result, nil
}

func (c *adminWorkspaceSymbolCollector) append(
	name,
	container,
	path string,
	line int,
	kind protocol.SymbolKind,
	aliases ...string,
) {
	searchText := name + " " + container
	if len(aliases) > 0 {
		searchText += " " + strings.Join(aliases, " ")
	}
	if name == "" || path == "" ||
		(c.query != "" && !strings.Contains(strings.ToLower(searchText), c.query)) {
		return
	}
	if line < 1 {
		line = 1
	}
	c.result = append(c.result, protocol.SymbolInformation{
		Name: name, Kind: kind, ContainerName: container,
		Location: protocol.Location{
			URI: uriutil.FileURI(path),
			Range: protocol.Range{
				Start: protocol.Position{Line: line - 1},
				End:   protocol.Position{Line: line - 1},
			},
		},
	})
}

func (c *adminWorkspaceSymbolCollector) appendAPI(
	name,
	container,
	path string,
	line int,
	kind protocol.SymbolKind,
	aliases ...string,
) {
	key := componentAPISymbolKey{name: name, path: path, line: line, kind: kind}
	if c.seenAPI[key] {
		return
	}
	c.seenAPI[key] = true
	c.append(name, container, path, line, kind, aliases...)
}

func (c *adminWorkspaceSymbolCollector) collectComponents() error {
	components, err := c.index.GetAllComponentsView()
	if err != nil {
		return err
	}
	for _, component := range components {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		container := "Shopware Administration component"
		switch component.Kind {
		case admin.ComponentOverride:
			container += " override"
		case admin.ComponentExtend:
			container += " extension"
		}
		c.append(component.Name, container, component.FilePath, component.Line, protocol.SymbolClass)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectComponentAPI() error {
	names, err := c.index.GetAllComponentNames()
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		component, err := c.index.GetEffectiveComponent(name)
		if err != nil {
			return err
		}
		if component != nil {
			c.collectOneComponentAPI(component)
		}
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectOneComponentAPI(
	component *admin.VueComponent,
) {
	for _, prop := range component.Props {
		path := firstAdminSymbolPath(prop.FilePath, component.DefinitionPath, component.FilePath)
		c.appendAPI(
			prop.Name, component.Name+" · component prop", path, prop.Line,
			protocol.SymbolProperty, admin.CamelToKebab(prop.Name),
		)
	}
	for _, event := range component.ComponentEvents() {
		path := firstAdminSymbolPath(event.FilePath, component.DefinitionPath, component.FilePath)
		name := admin.CanonicalEventName(event.Name)
		c.appendAPI(
			name, component.Name+" · component event", path, event.Line,
			protocol.SymbolEvent, "@"+name,
		)
	}
	for _, model := range component.ComponentModels() {
		path := firstAdminSymbolPath(model.Prop.FilePath, component.DefinitionPath, component.FilePath)
		c.appendAPI(
			model.AttributeName, component.Name+" · component model", path,
			model.Prop.Line, protocol.SymbolProperty, model.PropName, model.EventName,
		)
	}
	for _, slot := range component.Slots {
		name := slot.DisplayName()
		path := firstAdminSymbolPath(slot.FilePath, component.TemplatePath)
		c.appendAPI(
			name, component.Name+" · component slot", path, slot.Line,
			protocol.SymbolProperty, "#"+name,
		)
	}
	for _, directive := range component.LocalDirectives {
		c.append(
			"v-"+directive.Name,
			component.Name+" · local Vue directive",
			directive.FilePath,
			directive.Line,
			protocol.SymbolFunction,
		)
	}
}

func firstAdminSymbolPath(paths ...string) string {
	for _, path := range paths {
		if path != "" {
			return path
		}
	}
	return ""
}

func (c *adminWorkspaceSymbolCollector) collectMixins() error {
	values, err := c.index.GetAllMixins()
	if err != nil {
		return err
	}
	for _, value := range values {
		c.append(value.Name, "Shopware Administration mixin", value.FilePath, value.Line, protocol.SymbolObject)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectDirectives() error {
	values, err := c.index.GetAllDirectives()
	if err != nil {
		return err
	}
	for _, value := range values {
		c.append("v-"+value.Name, "Shopware Administration Vue directive", value.FilePath, value.Line, protocol.SymbolFunction)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectFilters() error {
	values, err := c.index.GetAllFilters()
	if err != nil {
		return err
	}
	for _, value := range values {
		c.append(value.Name, "Shopware Administration filter", value.FilePath, value.Line, protocol.SymbolFunction)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectCMS() error {
	values, err := c.index.GetAllCMSRegistrations()
	if err != nil {
		return err
	}
	for _, value := range values {
		c.append(value.Name, "Shopware CMS "+string(value.Kind), value.FilePath, value.Line, protocol.SymbolObject)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectModules() error {
	modules, err := c.index.GetAllModules()
	if err != nil {
		return err
	}
	for _, module := range modules {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		c.append(module.Name, "Shopware Administration module", module.FilePath, module.Line, protocol.SymbolModule)
		for _, route := range module.Routes {
			c.append(route.Name, module.Name, module.FilePath, route.Line, protocol.SymbolFunction)
		}
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectServices() error {
	services, err := c.index.GetAllServices()
	if err != nil {
		return err
	}
	for _, service := range services {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		container := "Shopware Administration service"
		if service.Kind != "" {
			container += " · " + string(service.Kind)
		}
		c.append(service.Name, container, service.FilePath, service.Line, protocol.SymbolObject)
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectStores() error {
	stores, err := c.index.GetAllStores()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(stores))
	for _, raw := range stores {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		if seen[raw.Name] {
			continue
		}
		seen[raw.Name] = true
		resolved, err := c.index.GetStore(raw.Name)
		if err != nil {
			return err
		}
		for _, store := range resolved {
			c.collectStore(store)
		}
	}
	return nil
}

func (c *adminWorkspaceSymbolCollector) collectStore(store admin.AdminStore) {
	c.append(store.Name, "Shopware Administration store", store.FilePath, store.Line, protocol.SymbolObject)
	for _, member := range store.Members {
		kind := protocol.SymbolField
		switch member.Kind {
		case admin.AdminStoreGetter:
			kind = protocol.SymbolProperty
		case admin.AdminStoreAction:
			kind = protocol.SymbolMethod
		}
		c.append(
			member.Name,
			"Shopware Administration store · "+store.Name,
			firstAdminSymbolPath(member.FilePath, store.FilePath),
			member.Line,
			kind,
		)
	}
}

func (c *adminWorkspaceSymbolCollector) collectPrivileges() error {
	privileges, err := c.index.GetAllPrivileges()
	if err != nil {
		return err
	}
	for _, privilege := range privileges {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		container := "Shopware Administration ACL permission"
		kind := protocol.SymbolConstant
		if privilege.Kind == admin.AdminPrivilegeRole {
			container = "Shopware Administration ACL role"
			kind = protocol.SymbolEnumMember
		}
		if privilege.MappingKey != "" {
			container += " · " + privilege.MappingKey
		}
		c.append(privilege.Name, container, privilege.FilePath, privilege.Line, kind)
	}
	return nil
}
