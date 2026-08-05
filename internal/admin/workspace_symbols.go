package admin

import "github.com/shopware/shopware-lsp/internal/indexer"

func adminWorkspaceRange(source AdminSourceRange, line int) indexer.WorkspaceSymbolRange {
	if source.EndLine > source.StartLine ||
		source.EndCharacter > source.StartCharacter {
		return indexer.WorkspaceSymbolRange{
			Start: indexer.WorkspaceSymbolPosition{
				Line: source.StartLine, Character: source.StartCharacter,
			},
			End: indexer.WorkspaceSymbolPosition{
				Line: source.EndLine, Character: source.EndCharacter,
			},
		}
	}
	return indexer.WorkspaceSymbolRangeAtLine(line)
}

func addAdminComponentWorkspaceSymbols(
	file *indexer.ParsedFile,
	components ...VueComponent,
) {
	var result []indexer.WorkspaceSymbol
	for _, component := range components {
		if component.Name == "" || component.FilePath == "" {
			continue
		}
		container := "Shopware Administration component"
		switch component.Kind {
		case ComponentOverride:
			container += " override"
		case ComponentExtend:
			container += " extension"
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: component.Name, ContainerName: container,
			Aliases: []string{component.TargetComponent}, Path: component.FilePath,
			Domain: "admin.component", Kind: indexer.WorkspaceSymbolClass,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(component.Line),
		})
		for _, prop := range component.Props {
			path := prop.FilePath
			if path == "" {
				path = component.DefinitionPath
			}
			result = append(result, indexer.WorkspaceSymbol{
				Name: prop.Name, ContainerName: component.Name + " · component prop",
				Aliases: []string{CamelToKebab(prop.Name)}, Path: path,
				Domain: "admin.component.prop", Kind: indexer.WorkspaceSymbolProperty,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    adminWorkspaceRange(prop.NameRange, prop.Line),
			})
		}
		for _, event := range component.ComponentEvents() {
			name := CanonicalEventName(event.Name)
			result = append(result, indexer.WorkspaceSymbol{
				Name: name, ContainerName: component.Name + " · component event",
				Aliases: []string{"@" + name}, Path: event.FilePath,
				Domain: "admin.component.event", Kind: indexer.WorkspaceSymbolEvent,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    adminWorkspaceRange(event.NameRange, event.Line),
			})
		}
		for _, slot := range component.Slots {
			name := slot.DisplayName()
			result = append(result, indexer.WorkspaceSymbol{
				Name: name, ContainerName: component.Name + " · component slot",
				Aliases: []string{"#" + name}, Path: slot.FilePath,
				Domain: "admin.component.slot", Kind: indexer.WorkspaceSymbolProperty,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    adminWorkspaceRange(slot.NameRange, slot.Line),
			})
		}
		for _, directive := range component.LocalDirectives {
			result = append(result, indexer.WorkspaceSymbol{
				Name:          "v-" + directive.Name,
				ContainerName: component.Name + " · local Vue directive",
				Path:          directive.FilePath, Domain: "admin.component.directive",
				Kind:     indexer.WorkspaceSymbolFunction,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    adminWorkspaceRange(directive.NameRange, directive.Line),
			})
		}
	}
	file.AddWorkspaceSymbols(result...)
}

func addAdminMixinsAndModulesWorkspaceSymbols(
	file *indexer.ParsedFile,
	mixins []AdminMixin,
	modules []AdminModule,
) {
	var result []indexer.WorkspaceSymbol
	for _, mixin := range mixins {
		result = append(result, indexer.WorkspaceSymbol{
			Name: mixin.Name, ContainerName: "Shopware Administration mixin",
			Path: mixin.FilePath, Domain: "admin.mixin",
			Kind:     indexer.WorkspaceSymbolObject,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(mixin.Line),
		})
	}
	for _, module := range modules {
		result = append(result, indexer.WorkspaceSymbol{
			Name: module.Name, ContainerName: "Shopware Administration module",
			Aliases: []string{module.DisplayName, module.Title}, Path: module.FilePath,
			Domain: "admin.module", Kind: indexer.WorkspaceSymbolModule,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(module.Line),
		})
		for _, route := range module.Routes {
			result = append(result, indexer.WorkspaceSymbol{
				Name: route.Name, ContainerName: module.Name,
				Aliases: []string{route.LocalName, route.Path, route.Component},
				Path:    module.FilePath, Domain: "admin.module.route",
				Kind:     indexer.WorkspaceSymbolFunction,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    indexer.WorkspaceSymbolRangeAtLine(route.Line),
			})
		}
	}
	file.AddWorkspaceSymbols(result...)
}

func addAdminRuntimeWorkspaceSymbols(
	file *indexer.ParsedFile,
	services []AdminService,
	stores []AdminStore,
	directives []AdminDirective,
	filters []AdminFilter,
	cms []AdminCMSRegistration,
	privileges []AdminPrivilege,
) {
	var result []indexer.WorkspaceSymbol
	for _, service := range services {
		result = append(result, indexer.WorkspaceSymbol{
			Name: service.Name, ContainerName: "Shopware Administration service · " + string(service.Kind),
			Aliases: []string{service.ImplementationName}, Path: service.FilePath,
			Domain: "admin.service", Kind: indexer.WorkspaceSymbolObject,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(service.Line),
		})
	}
	for _, store := range stores {
		result = append(result, indexer.WorkspaceSymbol{
			Name: store.Name, ContainerName: "Shopware Administration store",
			Path: store.FilePath, Domain: "admin.store",
			Kind:     indexer.WorkspaceSymbolObject,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(store.Line),
		})
		for _, member := range store.Members {
			kind := indexer.WorkspaceSymbolField
			switch member.Kind {
			case AdminStoreGetter:
				kind = indexer.WorkspaceSymbolProperty
			case AdminStoreAction:
				kind = indexer.WorkspaceSymbolMethod
			}
			result = append(result, indexer.WorkspaceSymbol{
				Name:          member.Name,
				ContainerName: "Shopware Administration store · " + store.Name,
				Path:          member.FilePath, Domain: "admin.store.member", Kind: kind,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    indexer.WorkspaceSymbolRangeAtLine(member.Line),
			})
		}
	}
	for _, directive := range directives {
		result = append(result, indexer.WorkspaceSymbol{
			Name:          "v-" + directive.Name,
			ContainerName: "Shopware Administration Vue directive",
			Path:          directive.FilePath, Domain: "admin.directive",
			Kind:     indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    adminWorkspaceRange(directive.NameRange, directive.Line),
		})
	}
	for _, filter := range filters {
		result = append(result, indexer.WorkspaceSymbol{
			Name: filter.Name, ContainerName: "Shopware Administration filter",
			Path: filter.FilePath, Domain: "admin.filter",
			Kind:     indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    adminWorkspaceRange(filter.NameRange, filter.Line),
		})
	}
	for _, registration := range cms {
		result = append(result, indexer.WorkspaceSymbol{
			Name:          registration.Name,
			ContainerName: "Shopware CMS " + string(registration.Kind),
			Path:          registration.FilePath, Domain: "admin.cms",
			Kind:     indexer.WorkspaceSymbolObject,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    adminWorkspaceRange(registration.NameRange, registration.Line),
		})
	}
	for _, privilege := range privileges {
		kind := indexer.WorkspaceSymbolConstant
		container := "Shopware Administration ACL permission"
		if privilege.Kind == AdminPrivilegeRole {
			kind = indexer.WorkspaceSymbolEnumMember
			container = "Shopware Administration ACL role"
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: privilege.Name, ContainerName: container,
			Aliases: []string{privilege.MappingKey}, Path: privilege.FilePath,
			Domain: "admin.privilege", Kind: kind,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(privilege.Line),
		})
	}
	file.AddWorkspaceSymbols(result...)
}
