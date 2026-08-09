package admin

import (
	"path/filepath"
	"sort"
	"strings"
)

func (idx *AdminComponentIndexer) GetAllMixins() ([]AdminMixin, error) {
	values, err := idx.mixinIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminMixin) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminMixin {
			return document.Mixins
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetMixin(name string) ([]AdminMixin, error) {
	values, err := idx.mixinIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminMixin) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminMixin {
			return document.Mixins
		},
		func(value AdminMixin) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetAllModules() ([]AdminModule, error) {
	values, err := idx.moduleIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminModule) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminModule {
			return document.Modules
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetModule(name string) ([]AdminModule, error) {
	values, err := idx.moduleIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminModule) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminModule {
			return document.Modules
		},
		func(value AdminModule) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetAllModuleRoutes() ([]AdminModuleRoute, error) {
	modules, err := idx.GetAllModules()
	if err != nil {
		return nil, err
	}
	var routes []AdminModuleRoute
	for _, module := range modules {
		routes = append(routes, module.Routes...)
	}
	return routes, nil
}

func (idx *AdminComponentIndexer) GetModuleRoute(name string) (*AdminModule, *AdminModuleRoute, error) {
	modules, err := idx.GetAllModules()
	if err != nil {
		return nil, nil, err
	}
	for moduleIndex := range modules {
		for routeIndex := range modules[moduleIndex].Routes {
			if modules[moduleIndex].Routes[routeIndex].Name == name {
				return &modules[moduleIndex], &modules[moduleIndex].Routes[routeIndex], nil
			}
		}
	}
	return nil, nil, nil
}

// GetAllComponents returns all registered Vue components
func (idx *AdminComponentIndexer) GetAllComponents() ([]VueComponent, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	return append([]VueComponent(nil), components...), nil
}

// GetAllComponentsView returns an immutable component catalog. Consumers that
// only inspect components can avoid copying the complete Administration
// registry for every document.
func (idx *AdminComponentIndexer) GetAllComponentsView() ([]VueComponent, error) {
	components, err := idx.componentIndex.GetAllValuesView()
	if err != nil {
		return nil, err
	}
	return idx.registrationsWithLiveDocuments(components, ""), nil
}

// GetComponentByTemplatePath returns the component that uses the given template path
func (idx *AdminComponentIndexer) GetComponentByTemplatePath(templatePath string) (*VueComponent, error) {
	normalizedPath := normalizeDefinitionPath(templatePath)
	if normalizedPath == "" {
		return nil, nil
	}
	resolveCached := func() (*VueComponent, bool, error) {
		name := idx.cachedTemplateComponent(normalizedPath)
		if name == "" {
			return nil, false, nil
		}
		component, err := idx.GetEffectiveComponent(name)
		if err != nil || component == nil {
			return component, true, err
		}
		if normalizeDefinitionPath(component.TemplatePath) == normalizedPath {
			return component, true, nil
		}
		return nil, false, nil
	}
	if component, found, err := resolveCached(); found || err != nil {
		return component, err
	}
	allComponents, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	if err := idx.ensureTemplateComponentCatalog(allComponents); err != nil {
		return nil, err
	}
	if component, found, err := resolveCached(); found || err != nil {
		return component, err
	}

	for _, comp := range allComponents {
		// Check if the component's template path matches
		if comp.TemplatePath != "" && normalizeDefinitionPath(comp.TemplatePath) == normalizedPath {
			// Get full component with definition
			fullComps, err := idx.GetComponentWithDefinition(comp.Name)
			if err == nil && len(fullComps) > 0 {
				idx.cacheTemplateComponent(normalizedPath, fullComps[0].Name)
				return &fullComps[0], nil
			}
			idx.cacheTemplateComponent(normalizedPath, comp.Name)
			return &comp, nil
		}

	}

	// Wrapped TypeScript definitions can acquire their template only after the
	// effective component is assembled. Resolve that authoritative view once
	// and cache the ownership for subsequent interactive requests.
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil ||
			normalizeDefinitionPath(component.TemplatePath) != normalizedPath {
			continue
		}
		idx.cacheTemplateComponent(normalizedPath, component.Name)
		return component, nil
	}

	return nil, nil
}

// GetComponentRegistrationByTemplatePath returns the source registration that
// owns templatePath without folding sibling overrides into it. Source-oriented
// features use this view to distinguish a template's own block declarations
// from the parent contract they override.
func (idx *AdminComponentIndexer) GetComponentRegistrationByTemplatePath(
	templatePath string,
) (*VueComponent, error) {
	normalizedPath := normalizeDefinitionPath(templatePath)
	if idx == nil || normalizedPath == "" {
		return nil, nil
	}
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	var matches []VueComponent
	for _, component := range components {
		resolved, resolveErr := idx.GetComponentRegistrationWithDefinition(component)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved == nil || normalizeDefinitionPath(resolved.TemplatePath) != normalizedPath {
			continue
		}
		matches = append(matches, *resolved)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].FilePath != matches[right].FilePath {
			return matches[left].FilePath < matches[right].FilePath
		}
		if matches[left].Line != matches[right].Line {
			return matches[left].Line < matches[right].Line
		}
		return matches[left].Name < matches[right].Name
	})
	return &matches[0], nil
}

// GetParentComponentForTemplate returns the effective component contract that
// exists immediately before the source registration owning templatePath. For
// Component.extend this is the named parent. For Component.override it is the
// base registration plus preceding overrides in deterministic index order.
func (idx *AdminComponentIndexer) GetParentComponentForTemplate(
	templatePath string,
) (*VueComponent, error) {
	owner, err := idx.GetComponentRegistrationByTemplatePath(templatePath)
	if err != nil || owner == nil {
		return nil, err
	}
	if owner.Kind == ComponentExtend || owner.ExtendsComponent != "" {
		parentName := owner.ExtendsComponent
		if parentName == "" {
			parentName = owner.TargetComponent
		}
		return idx.GetEffectiveComponent(parentName)
	}
	if owner.Kind != ComponentOverride {
		return nil, nil
	}

	components, err := idx.GetComponent(owner.Name)
	if err != nil {
		return nil, err
	}
	for index := range components {
		if err := idx.populateComponentDefinition(&components[index]); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(components, func(left, right int) bool {
		leftOverride := components[left].Kind == ComponentOverride
		rightOverride := components[right].Kind == ComponentOverride
		if leftOverride != rightOverride {
			return !leftOverride
		}
		if components[left].FilePath != components[right].FilePath {
			return components[left].FilePath < components[right].FilePath
		}
		return components[left].Line < components[right].Line
	})

	result := VueComponent{Name: owner.Name}
	foundPredecessor := false
	for _, component := range components {
		if sameComponentRegistration(component, *owner) {
			break
		}
		own, mergeErr := idx.componentWithMixins(component)
		if mergeErr != nil {
			return nil, mergeErr
		}
		if own.ExtendsComponent != "" {
			parent, parentErr := idx.GetEffectiveComponent(own.ExtendsComponent)
			if parentErr != nil {
				return nil, parentErr
			}
			if parent != nil {
				own = overlayComponents(*parent, own)
			}
		}
		result = overlayComponents(result, own)
		foundPredecessor = true
	}
	if !foundPredecessor {
		return nil, nil
	}
	return &result, nil
}

func sameComponentRegistration(left, right VueComponent) bool {
	return left.Name == right.Name && left.Kind == right.Kind &&
		left.Line == right.Line &&
		normalizeDefinitionPath(left.FilePath) ==
			normalizeDefinitionPath(right.FilePath) &&
		normalizeDefinitionPath(left.TemplatePath) ==
			normalizeDefinitionPath(right.TemplatePath)
}

// GetComponentForTemplateTag resolves a global Administration component or an
// Options API component local to the owner of templatePath. Local aliases are
// deliberately resolved only in their declaring template and never leak into
// workspace-wide component completion.
func (idx *AdminComponentIndexer) GetComponentForTemplateTag(
	templatePath,
	name string,
	owners ...*VueComponent,
) (*VueComponent, bool, error) {
	if templatePath != "" {
		var owner *VueComponent
		if len(owners) > 0 {
			owner = owners[0]
		}
		if owner == nil {
			var err error
			owner, err = idx.GetComponentByTemplatePath(templatePath)
			if err != nil {
				return nil, false, err
			}
		}
		if owner != nil {
			local, found := owner.LocalComponent(name)
			if !found {
				local, found = owner.LocalComponent(CamelToKebab(name))
			}
			if found {
				for _, targetName := range localComponentTargetNames(local) {
					target, targetErr := idx.GetEffectiveComponent(targetName)
					if targetErr != nil {
						return nil, false, targetErr
					}
					if target == nil {
						continue
					}
					resolved, liveErr := idx.componentWithLiveVueDocument(*target)
					if liveErr != nil {
						return nil, false, liveErr
					}
					resolved.Name = local.Name
					resolved.FilePath = local.FilePath
					resolved.DefinitionPath = local.FilePath
					resolved.Line = local.Line
					return &resolved, true, nil
				}
				for _, definitionPath := range localComponentDefinitionCandidates(local) {
					if live, liveFound, liveErr := idx.liveVueComponent(
						definitionPath,
					); liveErr != nil {
						return nil, false, liveErr
					} else if liveFound {
						live.Name = local.Name
						live.FilePath = local.FilePath
						live.DefinitionPath = definitionPath
						live.Line = local.Line
						return &live, true, nil
					}
					definition, definitionErr := idx.GetComponentDefinition(
						definitionPath,
					)
					if definitionErr != nil {
						return nil, false, definitionErr
					}
					if definition == nil {
						continue
					}
					resolved := VueComponent{
						Name: local.Name, FilePath: local.FilePath,
						DefinitionPath: definitionPath, Line: local.Line,
					}
					applyDefinition(&resolved, *definition)
					return &resolved, true, nil
				}
				return &VueComponent{
					Name: local.Name, FilePath: local.FilePath,
					DefinitionPath: local.FilePath, Line: local.Line,
				}, true, nil
			}
		}
	}

	components, err := idx.GetComponentWithDefinition(name)
	if err != nil {
		return nil, false, err
	}
	if len(components) == 0 {
		return nil, false, nil
	}
	resolved, err := idx.componentWithLiveVueDocument(components[0])
	if err != nil {
		return nil, false, err
	}
	return &resolved, true, nil
}

// ResolveDynamicComponents returns the complete finite component contract for
// a dynamic selector. It succeeds only when every possible selector name is a
// registered global or template-local component; native/runtime branches keep
// callers conservative.
func (idx *AdminComponentIndexer) ResolveDynamicComponents(
	templatePath string,
	selector VueDynamicComponentSelector,
	owners ...*VueComponent,
) ([]VueComponent, bool, error) {
	if idx == nil || !selector.Complete {
		return nil, false, nil
	}
	names := selector.Names()
	if len(names) == 0 {
		return nil, false, nil
	}
	result := make([]VueComponent, 0, len(names))
	for _, name := range names {
		if !IsComponentTag(name) {
			return nil, false, nil
		}
		component, found, err := idx.GetComponentForTemplateTag(
			templatePath, name, owners...,
		)
		if err != nil {
			return nil, false, err
		}
		if !found || component == nil {
			return nil, false, nil
		}
		result = append(result, *component)
	}
	return result, true, nil
}

// GetLocalComponentAtDefinitionPosition resolves an Options API component
// alias declaration under a JavaScript/TypeScript LSP position. It preserves
// the owning component so callers can keep references and refactors scoped to
// exactly one template.
func (idx *AdminComponentIndexer) GetLocalComponentAtDefinitionPosition(
	definitionPath string,
	line,
	character int,
) (*VueComponent, VueLocalComponent, bool, error) {
	components, err := idx.GetComponentsByDefinitionPath(definitionPath)
	if err != nil {
		return nil, VueLocalComponent{}, false, err
	}
	for componentIndex := range components {
		component := &components[componentIndex]
		for _, local := range component.LocalComponents {
			if !adminSourceRangeContainsPosition(
				local.NameRange, line, character,
			) {
				continue
			}
			return component, local, true, nil
		}
	}
	return nil, VueLocalComponent{}, false, nil
}

func (idx *AdminComponentIndexer) GetLocalDirectiveAtDefinitionPosition(
	definitionPath string,
	line,
	character int,
) (*VueComponent, VueLocalDirective, bool, error) {
	components, err := idx.GetComponentsByDefinitionPath(definitionPath)
	if err != nil {
		return nil, VueLocalDirective{}, false, err
	}
	for componentIndex := range components {
		component := &components[componentIndex]
		for _, local := range component.LocalDirectives {
			if !adminSourceRangeContainsPosition(
				local.NameRange, line, character,
			) {
				continue
			}
			return component, local, true, nil
		}
	}
	return nil, VueLocalDirective{}, false, nil
}

func adminSourceRangeContainsPosition(
	rangeValue AdminSourceRange,
	line,
	character int,
) bool {
	if line < rangeValue.StartLine || line > rangeValue.EndLine {
		return false
	}
	if line == rangeValue.StartLine && character < rangeValue.StartCharacter {
		return false
	}
	if line == rangeValue.EndLine && character > rangeValue.EndCharacter {
		return false
	}
	return true
}

func localComponentTargetNames(local VueLocalComponent) []string {
	var result []string
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, existing := range result {
			if existing == name {
				return
			}
		}
		result = append(result, name)
	}
	symbol := strings.TrimSuffix(local.Symbol, "Original")
	appendName(CamelToKebab(symbol))
	if local.ImportPath != "" && local.ImportPath != meteorPackagePath {
		base := filepath.Base(strings.TrimSuffix(local.ImportPath, "/"))
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if base != "index" {
			appendName(CamelToKebab(base))
		}
	}
	return result
}

func localComponentDefinitionCandidates(local VueLocalComponent) []string {
	var result []string
	appendPath := func(path string) {
		path = filepath.Clean(path)
		if path == "." || path == "" {
			return
		}
		for _, current := range result {
			if normalizeDefinitionPath(current) == normalizeDefinitionPath(path) {
				return
			}
		}
		result = append(result, path)
	}
	appendPath(resolveImportPath(local.FilePath, local.ImportPath))
	for _, candidate := range adminTypeImportCandidates(
		local.FilePath, local.ImportPath,
	) {
		appendPath(candidate)
	}
	return result
}

func (idx *AdminComponentIndexer) cachedTemplateComponent(path string) string {
	if idx == nil {
		return ""
	}
	idx.templateCacheMu.RLock()
	defer idx.templateCacheMu.RUnlock()
	return idx.templateCache[path]
}

func (idx *AdminComponentIndexer) cacheTemplateComponent(path, name string) {
	if idx == nil || path == "" || name == "" {
		return
	}
	idx.templateCacheMu.Lock()
	defer idx.templateCacheMu.Unlock()
	if idx.templateCache == nil {
		idx.templateCache = make(map[string]string)
	}
	idx.templateCache[path] = name
}

// ensureTemplateComponentCatalog projects the persisted component and
// definition repositories into a template-path lookup once per index
// generation. Previously every new Twig document scanned every registration
// and resolved each definition independently.
func (idx *AdminComponentIndexer) ensureTemplateComponentCatalog(
	components []VueComponent,
) error {
	if idx == nil {
		return nil
	}
	idx.templateCacheMu.Lock()
	defer idx.templateCacheMu.Unlock()
	if idx.templateCatalogBuilt {
		return nil
	}
	definitions, err := idx.definitionIndex.GetAllValuesView()
	if err != nil {
		return err
	}
	templatesByDefinition := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		definitionPath := normalizeDefinitionPath(definition.FilePath)
		templatePath := normalizeDefinitionPath(definition.TemplatePath)
		if definitionPath == "" || templatePath == "" {
			continue
		}
		if _, exists := templatesByDefinition[definitionPath]; !exists {
			templatesByDefinition[definitionPath] = templatePath
		}
	}
	if idx.templateCache == nil {
		idx.templateCache = make(map[string]string)
	}
	cache := func(templatePath, name string) {
		templatePath = normalizeDefinitionPath(templatePath)
		if templatePath == "" || name == "" {
			return
		}
		if _, exists := idx.templateCache[templatePath]; !exists {
			idx.templateCache[templatePath] = name
		}
	}
	for _, component := range components {
		cache(component.TemplatePath, component.Name)
		definitionPath := normalizeDefinitionPath(component.DefinitionPath)
		cache(templatesByDefinition[definitionPath], component.Name)
		if component.InlineDefinition != nil {
			cache(component.InlineDefinition.TemplatePath, component.Name)
		}
	}
	idx.templateCatalogBuilt = true
	return nil
}

func (idx *AdminComponentIndexer) invalidateTemplateComponentCache() {
	if idx == nil {
		return
	}
	idx.templateCacheMu.Lock()
	idx.templateCache = nil
	idx.templateCatalogBuilt = false
	idx.templateCacheMu.Unlock()
	idx.effectiveCacheMu.Lock()
	idx.effectiveCache = nil
	idx.effectiveCacheEpoch++
	idx.effectiveCacheMu.Unlock()
}

// ResolveTwigScopedSlot resolves the lexical v-slot scope at offset against
// the effective component API. It is shared by completion, hover, and
// definition so inherited slot contracts behave identically in every feature.
