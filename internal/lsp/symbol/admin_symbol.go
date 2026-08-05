package symbol

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminWorkspaceSymbolProvider exposes the registry-like Administration
// declarations that do not have equivalents in the PHP symbol graph.
type AdminWorkspaceSymbolProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminWorkspaceSymbolProvider(index *admin.AdminComponentIndexer) *AdminWorkspaceSymbolProvider {
	return &AdminWorkspaceSymbolProvider{index: index}
}

func (p *AdminWorkspaceSymbolProvider) WorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, error) {
	if p == nil || p.index == nil {
		return nil, nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var result []protocol.SymbolInformation
	appendSymbol := func(
		name, container, path string,
		line int,
		kind protocol.SymbolKind,
		aliases ...string,
	) {
		searchText := name + " " + container
		if len(aliases) > 0 {
			searchText += " " + strings.Join(aliases, " ")
		}
		if name == "" || path == "" || (query != "" &&
			!strings.Contains(strings.ToLower(searchText), query)) {
			return
		}
		if line < 1 {
			line = 1
		}
		result = append(result, protocol.SymbolInformation{
			Name:          name,
			Kind:          kind,
			ContainerName: container,
			Location: protocol.Location{
				URI: uriutil.FileURI(path),
				Range: protocol.Range{
					Start: protocol.Position{Line: line - 1},
					End:   protocol.Position{Line: line - 1},
				},
			},
		})
	}
	components, err := p.index.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		container := "Shopware Administration component"
		switch component.Kind {
		case admin.ComponentOverride:
			container += " override"
		case admin.ComponentExtend:
			container += " extension"
		}
		appendSymbol(component.Name, container, component.FilePath, component.Line, protocol.SymbolClass)
	}
	componentNames, err := p.index.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	sort.Strings(componentNames)
	type componentAPISymbolKey struct {
		name string
		path string
		line int
		kind protocol.SymbolKind
	}
	seenComponentAPI := make(map[componentAPISymbolKey]bool)
	appendComponentAPI := func(
		name, container, path string,
		line int,
		kind protocol.SymbolKind,
		aliases ...string,
	) {
		key := componentAPISymbolKey{
			name: name,
			path: path,
			line: line,
			kind: kind,
		}
		if seenComponentAPI[key] {
			return
		}
		seenComponentAPI[key] = true
		appendSymbol(name, container, path, line, kind, aliases...)
	}
	for _, componentName := range componentNames {
		component, resolveErr := p.index.GetEffectiveComponent(componentName)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil {
			continue
		}
		for _, prop := range component.Props {
			path := prop.FilePath
			if path == "" {
				path = component.DefinitionPath
			}
			if path == "" {
				path = component.FilePath
			}
			appendComponentAPI(
				prop.Name,
				component.Name+" · component prop",
				path,
				prop.Line,
				protocol.SymbolProperty,
				admin.CamelToKebab(prop.Name),
			)
		}
		for _, event := range component.ComponentEvents() {
			path := event.FilePath
			if path == "" {
				path = component.DefinitionPath
			}
			if path == "" {
				path = component.FilePath
			}
			name := admin.CanonicalEventName(event.Name)
			appendComponentAPI(
				name,
				component.Name+" · component event",
				path,
				event.Line,
				protocol.SymbolEvent,
				"@"+name,
			)
		}
		for _, model := range component.ComponentModels() {
			path := model.Prop.FilePath
			if path == "" {
				path = component.DefinitionPath
			}
			if path == "" {
				path = component.FilePath
			}
			appendComponentAPI(
				model.AttributeName,
				component.Name+" · component model",
				path,
				model.Prop.Line,
				protocol.SymbolProperty,
				model.PropName,
				model.EventName,
			)
		}
		for _, slot := range component.Slots {
			name := slot.DisplayName()
			path := slot.FilePath
			if path == "" {
				path = component.TemplatePath
			}
			appendComponentAPI(
				name,
				component.Name+" · component slot",
				path,
				slot.Line,
				protocol.SymbolProperty,
				"#"+name,
			)
		}
		for _, directive := range component.LocalDirectives {
			appendSymbol(
				"v-"+directive.Name,
				component.Name+" · local Vue directive",
				directive.FilePath,
				directive.Line,
				protocol.SymbolFunction,
			)
		}
	}
	mixins, err := p.index.GetAllMixins()
	if err != nil {
		return nil, err
	}
	for _, mixin := range mixins {
		appendSymbol(mixin.Name, "Shopware Administration mixin", mixin.FilePath, mixin.Line, protocol.SymbolObject)
	}
	directives, err := p.index.GetAllDirectives()
	if err != nil {
		return nil, err
	}
	for _, directive := range directives {
		appendSymbol(
			"v-"+directive.Name,
			"Shopware Administration Vue directive",
			directive.FilePath,
			directive.Line,
			protocol.SymbolFunction,
		)
	}
	filters, err := p.index.GetAllFilters()
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		appendSymbol(
			filter.Name,
			"Shopware Administration filter",
			filter.FilePath,
			filter.Line,
			protocol.SymbolFunction,
		)
	}
	cmsRegistrations, err := p.index.GetAllCMSRegistrations()
	if err != nil {
		return nil, err
	}
	for _, registration := range cmsRegistrations {
		appendSymbol(
			registration.Name,
			"Shopware CMS "+string(registration.Kind),
			registration.FilePath,
			registration.Line,
			protocol.SymbolObject,
		)
	}
	modules, err := p.index.GetAllModules()
	if err != nil {
		return nil, err
	}
	for _, module := range modules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		appendSymbol(module.Name, "Shopware Administration module", module.FilePath, module.Line, protocol.SymbolModule)
		for _, route := range module.Routes {
			appendSymbol(route.Name, module.Name, module.FilePath, route.Line, protocol.SymbolFunction)
		}
	}
	services, err := p.index.GetAllServices()
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		container := "Shopware Administration service"
		if service.Kind != "" {
			container += " · " + string(service.Kind)
		}
		appendSymbol(
			service.Name,
			container,
			service.FilePath,
			service.Line,
			protocol.SymbolObject,
		)
	}
	stores, err := p.index.GetAllStores()
	if err != nil {
		return nil, err
	}
	seenStores := make(map[string]bool, len(stores))
	for _, rawStore := range stores {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seenStores[rawStore.Name] {
			continue
		}
		seenStores[rawStore.Name] = true
		resolvedStores, resolveErr := p.index.GetStore(rawStore.Name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		for _, store := range resolvedStores {
			appendSymbol(
				store.Name,
				"Shopware Administration store",
				store.FilePath,
				store.Line,
				protocol.SymbolObject,
			)
			for _, member := range store.Members {
				kind := protocol.SymbolField
				switch member.Kind {
				case admin.AdminStoreGetter:
					kind = protocol.SymbolProperty
				case admin.AdminStoreAction:
					kind = protocol.SymbolMethod
				}
				path := member.FilePath
				if path == "" {
					path = store.FilePath
				}
				appendSymbol(
					member.Name,
					"Shopware Administration store · "+store.Name,
					path,
					member.Line,
					kind,
				)
			}
		}
	}
	privileges, err := p.index.GetAllPrivileges()
	if err != nil {
		return nil, err
	}
	for _, privilege := range privileges {
		if err := ctx.Err(); err != nil {
			return nil, err
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
		appendSymbol(
			privilege.Name,
			container,
			privilege.FilePath,
			privilege.Line,
			kind,
		)
	}
	return result, nil
}
