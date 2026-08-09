package admin

import (
	"strings"
)

func (idx *AdminComponentIndexer) GetAllServices() ([]AdminService, error) {
	values, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminService) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminService {
			return document.Services
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetService(name string) ([]AdminService, error) {
	values, err := idx.serviceIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminService) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminService {
			return document.Services
		},
		func(value AdminService) bool { return value.Name == name },
	)
	return preferredServices(values), nil
}

func (idx *AdminComponentIndexer) GetAllDirectives() ([]AdminDirective, error) {
	values, err := idx.directiveIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminDirective) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminDirective {
			return document.Directives
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetAllFilters() ([]AdminFilter, error) {
	values, err := idx.filterIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminFilter) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminFilter {
			return document.Filters
		},
		nil,
	)
	return preferRuntimeDefinitions(values, func(value AdminFilter) string {
		return value.FilePath
	}), nil
}

func (idx *AdminComponentIndexer) GetAllCMSRegistrations() ([]AdminCMSRegistration, error) {
	values, err := idx.cmsIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminCMSRegistration) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminCMSRegistration {
			return document.CMS
		},
		nil,
	)
	return preferredCMSRegistrations(values), nil
}

func (idx *AdminComponentIndexer) GetAllCMSRegistrationsByKind(
	kind AdminCMSRegistrationKind,
) ([]AdminCMSRegistration, error) {
	values, err := idx.GetAllCMSRegistrations()
	if err != nil {
		return nil, err
	}
	result := make([]AdminCMSRegistration, 0, len(values))
	for _, value := range values {
		if value.Kind == kind {
			result = append(result, value)
		}
	}
	return preferredCMSRegistrations(result), nil
}

func (idx *AdminComponentIndexer) GetCMSRegistration(
	kind AdminCMSRegistrationKind,
	name string,
) ([]AdminCMSRegistration, error) {
	values, err := idx.cmsIndex.GetValues(AdminCMSKey(kind, name))
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminCMSRegistration) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminCMSRegistration {
			return document.CMS
		},
		func(value AdminCMSRegistration) bool {
			return value.Kind == kind && value.Name == name
		},
	)
	return preferredCMSRegistrations(values), nil
}

func (idx *AdminComponentIndexer) GetDirective(
	name string,
) ([]AdminDirective, error) {
	values, err := idx.directiveIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminDirective) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminDirective {
			return document.Directives
		},
		func(value AdminDirective) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetFilter(
	name string,
) ([]AdminFilter, error) {
	values, err := idx.filterIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminFilter) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminFilter {
			return document.Filters
		},
		func(value AdminFilter) bool { return value.Name == name },
	)
	return preferRuntimeDefinitions(values, func(value AdminFilter) string {
		return value.FilePath
	}), nil
}

// GetLocalDirectiveForTemplate resolves an Options API directive in the
// component that owns templatePath. Local declarations shadow the global
// registry only in that component's effective template scope.
func (idx *AdminComponentIndexer) GetLocalDirectiveForTemplate(
	templatePath,
	name string,
) (VueLocalDirective, bool, error) {
	component, err := idx.GetComponentByTemplatePath(templatePath)
	if err != nil || component == nil {
		return VueLocalDirective{}, false, err
	}
	directive, found := component.LocalDirective(name)
	return directive, found, nil
}

func (idx *AdminComponentIndexer) GetDirectiveForTemplate(
	templatePath,
	name string,
) ([]AdminDirective, error) {
	local, found, err := idx.GetLocalDirectiveForTemplate(templatePath, name)
	if err != nil {
		return nil, err
	}
	if found {
		return []AdminDirective{{
			Name: local.Name, FilePath: local.FilePath, Line: local.Line,
			NameRange: local.NameRange, Local: true,
		}}, nil
	}
	return idx.GetDirective(name)
}

func (idx *AdminComponentIndexer) GetAllDirectivesForTemplate(
	templatePath string,
) ([]AdminDirective, error) {
	global, err := idx.GetAllDirectives()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]AdminDirective, len(global))
	order := make([]string, 0, len(global))
	for _, directive := range global {
		key := strings.ToLower(directive.Name)
		if key == "" {
			continue
		}
		if _, exists := byName[key]; !exists {
			order = append(order, key)
		}
		byName[key] = directive
	}
	component, componentErr := idx.GetComponentByTemplatePath(templatePath)
	if componentErr != nil {
		return nil, componentErr
	}
	if component != nil {
		for _, local := range component.LocalDirectives {
			key := strings.ToLower(local.Name)
			if key == "" {
				continue
			}
			if _, exists := byName[key]; !exists {
				order = append(order, key)
			}
			byName[key] = AdminDirective{
				Name: local.Name, FilePath: local.FilePath, Line: local.Line,
				NameRange: local.NameRange, Local: true,
			}
		}
	}
	result := make([]AdminDirective, 0, len(byName))
	for _, key := range order {
		result = append(result, byName[key])
	}
	return result, nil
}

func (idx *AdminComponentIndexer) GetAllStores() ([]AdminStore, error) {
	values, err := idx.storeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminStore) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminStore {
			return document.Stores
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetStore(name string) ([]AdminStore, error) {
	values, err := idx.storeIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	documents := idx.liveLegacyDocumentSnapshots()
	values = overlayLiveLegacyValues(
		values,
		documents,
		func(value AdminStore) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminStore {
			return document.Stores
		},
		func(value AdminStore) bool { return value.Name == name },
	)
	values = preferredStores(values)
	for index := range values {
		if values[index].FactoryPath != "" {
			factories, factoryErr := idx.storeFactoryIndex.GetValues(
				normalizeDefinitionPath(values[index].FactoryPath),
			)
			if factoryErr != nil {
				return nil, factoryErr
			}
			factoryPath := normalizeDefinitionPath(values[index].FactoryPath)
			factories = overlayLiveLegacyValues(
				factories,
				documents,
				func(value AdminStoreFactory) string { return value.FilePath },
				func(document liveLegacyDocument) []AdminStoreFactory {
					if document.StoreFactory == nil {
						return nil
					}
					return []AdminStoreFactory{*document.StoreFactory}
				},
				func(value AdminStoreFactory) bool {
					return normalizeDefinitionPath(value.FilePath) == factoryPath
				},
			)
			for _, factory := range factories {
				values[index].Members = mergeStoreMembers(
					factory.Members,
					values[index].Members,
				)
			}
		}
		if values[index].StateType != "" {
			shape, shapeErr := idx.ResolveVueType(
				values[index].StateType, values[index].FilePath,
			)
			if shapeErr != nil {
				return nil, shapeErr
			}
			for memberIndex := range values[index].Members {
				member := &values[index].Members[memberIndex]
				if member.Kind != AdminStoreState {
					continue
				}
				declared, found := twigVueMemberNamed(shape.Members, member.Name)
				if found && declared.Type != "" {
					member.Type = declared.Type
				}
			}
		}
	}
	return values, nil
}

func (idx *AdminComponentIndexer) GetAllPrivileges() ([]AdminPrivilege, error) {
	values, err := idx.privilegeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminPrivilege) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminPrivilege {
			return document.Privileges
		},
		nil,
	)
	hasAdministrator := false
	for _, value := range values {
		if value.Name == AdminPrivilegeAdministrator {
			hasAdministrator = true
			break
		}
	}
	if !hasAdministrator {
		administrator, _ := builtinAdminPrivilege(AdminPrivilegeAdministrator)
		values = append(values, administrator)
	}
	return values, nil
}

func (idx *AdminComponentIndexer) GetPrivilege(
	name string,
) ([]AdminPrivilege, error) {
	values, err := idx.privilegeIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminPrivilege) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminPrivilege {
			return document.Privileges
		},
		func(value AdminPrivilege) bool { return value.Name == name },
	)
	values = preferredPrivileges(values)
	if len(values) == 0 {
		if builtin, ok := builtinAdminPrivilege(name); ok {
			return []AdminPrivilege{builtin}, nil
		}
	}
	return values, nil
}
