package admin

import (
	"sort"
	"strings"
)

func (idx *AdminComponentIndexer) GetAllComponentNames() ([]string, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(components))
	result := make([]string, 0, len(components))
	for _, component := range components {
		if component.Name == "" || seen[component.Name] {
			continue
		}
		seen[component.Name] = true
		result = append(result, component.Name)
	}
	sort.Strings(result)
	return result, nil
}

// GetComponentsByDefinitionPath returns effective components whose inline or
// imported configuration is owned by filePath.
func (idx *AdminComponentIndexer) GetComponentsByDefinitionPath(
	filePath string,
) ([]VueComponent, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	normalized := normalizeDefinitionPath(filePath)
	names := make(map[string]bool)
	for _, component := range components {
		definitionPath := component.DefinitionPath
		if definitionPath == "" && component.FilePath == filePath {
			definitionPath = component.FilePath
		}
		if normalizeDefinitionPath(definitionPath) == normalized {
			names[component.Name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	result := make([]VueComponent, 0, len(ordered))
	for _, name := range ordered {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component != nil {
			result = append(result, *component)
		}
	}
	return result, nil
}

// GetComponent returns components by name (may have multiple if extended)
func (idx *AdminComponentIndexer) GetComponent(name string) ([]VueComponent, error) {
	components, err := idx.componentIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return idx.registrationsWithLiveDocuments(components, name), nil
}

// GetComponentDefinition returns the component definition for a given definition path
func (idx *AdminComponentIndexer) GetComponentDefinition(definitionPath string) (*ComponentDefinition, error) {
	if definition, found, shadowed, err := idx.liveLegacyDefinition(
		definitionPath,
	); err != nil || found || shadowed {
		if !found {
			return nil, err
		}
		return &definition, err
	}
	normalizedPath := normalizeDefinitionPath(definitionPath)
	defs, err := idx.definitionIndex.GetValues(normalizedPath)
	if err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return nil, nil
	}
	definition, err := idx.definitionWithLiveTemplate(defs[0])
	if err != nil {
		return nil, err
	}
	return &definition, nil
}

// GetComponentDefinitionByName returns the inline component definition by component name
func (idx *AdminComponentIndexer) GetComponentDefinitionByName(name string) (*ComponentDefinition, error) {
	components, componentErr := idx.GetComponent(name)
	if componentErr != nil {
		return nil, componentErr
	}
	for _, component := range components {
		if component.InlineDefinition == nil {
			continue
		}
		definition, err := idx.definitionWithLiveTemplate(
			*component.InlineDefinition,
		)
		if err != nil {
			return nil, err
		}
		return &definition, nil
	}
	defs, err := idx.definitionIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	filtered := defs[:0]
	for _, definition := range defs {
		if !idx.isLiveLegacyDocumentPath(definition.FilePath) {
			filtered = append(filtered, definition)
		}
	}
	defs = filtered
	if len(defs) == 0 {
		return nil, nil
	}
	definition, err := idx.definitionWithLiveTemplate(defs[0])
	if err != nil {
		return nil, err
	}
	return &definition, nil
}

// GetComponentRegistrationWithDefinition attaches the definition owned by one
// concrete registration without applying parents, mixins, or sibling
// overrides. It is useful for source-oriented features which must distinguish
// an inherited template from a template declared by the registration itself.
func (idx *AdminComponentIndexer) GetComponentRegistrationWithDefinition(
	component VueComponent,
) (*VueComponent, error) {
	if err := idx.populateComponentDefinition(&component); err != nil {
		return nil, err
	}
	return &component, nil
}

// GetComponentWithDefinition returns the effective component definition. It
// resolves the extends chain and overlays all registrations and overrides while
// retaining source locations for inherited members.
func (idx *AdminComponentIndexer) GetComponentWithDefinition(name string) ([]VueComponent, error) {
	component, err := idx.GetEffectiveComponent(name)
	if err != nil || component == nil {
		return nil, err
	}
	return []VueComponent{*component}, nil
}

func (idx *AdminComponentIndexer) GetEffectiveComponent(name string) (*VueComponent, error) {
	return idx.effectiveComponent(name, make(map[string]bool))
}

func (idx *AdminComponentIndexer) effectiveComponent(
	name string,
	resolving map[string]bool,
) (*VueComponent, error) {
	if name == "" || resolving[name] {
		return nil, nil
	}
	idx.effectiveCacheMu.RLock()
	epoch := idx.effectiveCacheEpoch
	cached, cachedFound := idx.effectiveCache[name]
	idx.effectiveCacheMu.RUnlock()
	if cachedFound {
		result := cloneVueComponent(cached)
		return &result, nil
	}
	resolving[name] = true
	defer delete(resolving, name)

	components, err := idx.GetComponent(name)
	if err != nil {
		return nil, err
	}
	if len(components) == 0 {
		return nil, nil
	}

	for i := range components {
		if err := idx.populateComponentDefinition(&components[i]); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(components, func(left, right int) bool {
		leftOverride := components[left].Kind == ComponentOverride
		rightOverride := components[right].Kind == ComponentOverride
		if leftOverride != rightOverride {
			return !leftOverride
		}
		return components[left].FilePath < components[right].FilePath
	})

	result := VueComponent{Name: name}
	for index := range components {
		own, mergeErr := idx.componentWithMixins(components[index])
		if mergeErr != nil {
			return nil, mergeErr
		}
		if own.ExtendsComponent != "" {
			ownDeprecated := own.Deprecated
			parent, parentErr := idx.effectiveComponent(own.ExtendsComponent, resolving)
			if parentErr != nil {
				return nil, parentErr
			}
			if parent != nil {
				own = overlayComponents(*parent, own)
				// Extending a deprecated component does not make the child registry
				// name deprecated. Public props retain their own inherited metadata.
				own.Deprecated = ownDeprecated
			}
		}
		result = overlayComponents(result, own)
	}
	if err := idx.enrichEffectiveComponentMemberTypes(&result); err != nil {
		return nil, err
	}
	idx.effectiveCacheMu.Lock()
	if idx.effectiveCacheEpoch == epoch {
		if idx.effectiveCache == nil {
			idx.effectiveCache = make(map[string]VueComponent)
		}
		idx.effectiveCache[name] = cloneVueComponent(result)
	}
	idx.effectiveCacheMu.Unlock()
	return &result, nil
}

func (idx *AdminComponentIndexer) populateComponentDefinition(component *VueComponent) error {
	if component == nil {
		return nil
	}
	if component.InlineDefinition != nil {
		definition, err := idx.definitionWithLiveTemplate(
			*component.InlineDefinition,
		)
		if err != nil {
			return err
		}
		applyDefinition(component, definition)
		return nil
	}
	var definition *ComponentDefinition
	var err error
	if component.DefinitionPath != "" {
		definition, err = idx.GetComponentDefinition(component.DefinitionPath)
		if err != nil {
			return err
		}
	}
	if definition == nil {
		definitions, lookupErr := idx.definitionIndex.GetValues(component.Name)
		if lookupErr != nil {
			return lookupErr
		}
		for index := range definitions {
			if definitions[index].FilePath == component.FilePath {
				definition = &definitions[index]
				break
			}
		}
		if definition == nil && len(definitions) == 1 {
			definition = &definitions[0]
		}
		if definition != nil {
			resolved, definitionErr := idx.definitionWithLiveTemplate(*definition)
			if definitionErr != nil {
				return definitionErr
			}
			definition = &resolved
		}
	}
	if definition != nil {
		applyDefinition(component, *definition)
	}
	return nil
}

func applyDefinition(component *VueComponent, definition ComponentDefinition) {
	if definition.Deprecated != "" {
		component.Deprecated = definition.Deprecated
	}
	component.Props = definition.Props
	component.ModelProp = definition.ModelProp
	component.ModelEvent = definition.ModelEvent
	component.Emits = definition.Emits
	component.Events = definition.Events
	component.Methods = definition.Methods
	component.Computed = definition.Computed
	component.Data = definition.Data
	component.Injected = definition.Injected
	component.Mixins = definition.Mixins
	component.LocalComponents = definition.LocalComponents
	component.LocalDirectives = definition.LocalDirectives
	component.Members = definition.Members
	component.OpenRuntimeMembers = definition.OpenRuntimeMembers
	component.Assignments = definition.Assignments
	component.Slots = definition.Slots
	component.Blocks = definition.Blocks
	component.TemplatePath = definition.TemplatePath
}

func (idx *AdminComponentIndexer) componentWithMixins(
	component VueComponent,
) (VueComponent, error) {
	result := VueComponent{Name: component.Name}
	seen := make(map[string]bool)
	var applyMixin func(string) error
	applyMixin = func(name string) error {
		if name == "" || seen[name] {
			return nil
		}
		seen[name] = true
		mixins, err := idx.GetMixin(name)
		if err != nil {
			return err
		}
		if len(mixins) == 0 {
			result.OpenRuntimeMembers = true
		}
		sort.SliceStable(mixins, func(left, right int) bool {
			return mixins[left].FilePath < mixins[right].FilePath
		})
		for _, mixin := range mixins {
			for _, parent := range mixin.Definition.Mixins {
				if err := applyMixin(parent); err != nil {
					return err
				}
			}
			mixinComponent := VueComponent{Name: component.Name}
			applyDefinition(&mixinComponent, mixin.Definition)
			result = overlayComponents(result, mixinComponent)
		}
		return nil
	}
	for _, mixin := range component.Mixins {
		if err := applyMixin(mixin); err != nil {
			return component, err
		}
	}
	return overlayComponents(result, component), nil
}

// deduplicateComponents merges multiple component entries with the same name
// into a single entry, preferring entries with more complete data
func deduplicateComponents(components []VueComponent) []VueComponent {
	if len(components) <= 1 {
		return components
	}

	// Find the best component (one with the most complete data)
	best := components[0]
	for i := 1; i < len(components); i++ {
		comp := components[i]
		// Prefer component with props defined
		if len(comp.Props) > len(best.Props) {
			best = mergeComponents(best, comp)
		} else if len(comp.Props) < len(best.Props) {
			best = mergeComponents(comp, best)
		} else {
			// Same number of props, prefer one with definition path
			if comp.DefinitionPath != "" && best.DefinitionPath == "" {
				best = mergeComponents(best, comp)
			} else {
				best = mergeComponents(comp, best)
			}
		}
	}

	return []VueComponent{best}
}

// SaveComponentDefinition saves a component definition (primarily for testing)
func (idx *AdminComponentIndexer) SaveComponentDefinition(key string, def ComponentDefinition) error {
	idx.invalidateTemplateComponentCache()
	batchSave := make(map[string]map[string]ComponentDefinition)
	batchSave[def.FilePath] = map[string]ComponentDefinition{
		key: def,
	}
	return idx.definitionIndex.BatchSaveItems(batchSave)
}

// SaveComponent saves a component (primarily for testing)
func (idx *AdminComponentIndexer) SaveComponent(comp VueComponent) error {
	idx.invalidateTemplateComponentCache()
	batchSave := make(map[string]map[string]VueComponent)
	batchSave[comp.FilePath] = map[string]VueComponent{
		comp.Name: comp,
	}
	return idx.componentIndex.BatchSaveItems(batchSave)
}

// mergeComponents merges two components, taking data from 'preferred' when available,
// falling back to 'fallback' for missing data
func mergeComponents(fallback, preferred VueComponent) VueComponent {
	result := preferred
	result.OpenRuntimeMembers = preferred.OpenRuntimeMembers ||
		fallback.OpenRuntimeMembers
	if result.Deprecated == "" {
		result.Deprecated = fallback.Deprecated
	}
	if result.ExtendsComponent == "" {
		result.ExtendsComponent = fallback.ExtendsComponent
	}
	if result.ImportPath == "" {
		result.ImportPath = fallback.ImportPath
	}
	if result.DefinitionPath == "" {
		result.DefinitionPath = fallback.DefinitionPath
	}
	if len(result.Props) == 0 {
		result.Props = fallback.Props
	}
	if result.ModelProp == "" {
		result.ModelProp = fallback.ModelProp
	}
	if result.ModelEvent == "" {
		result.ModelEvent = fallback.ModelEvent
	}
	if len(result.Emits) == 0 {
		result.Emits = fallback.Emits
	}
	if len(result.Events) == 0 {
		result.Events = fallback.Events
	}
	if len(result.Methods) == 0 {
		result.Methods = fallback.Methods
	}
	if len(result.Computed) == 0 {
		result.Computed = fallback.Computed
	}
	if len(result.Data) == 0 {
		result.Data = fallback.Data
	}
	if len(result.Injected) == 0 {
		result.Injected = fallback.Injected
	}
	if len(result.Mixins) == 0 {
		result.Mixins = fallback.Mixins
	}
	if len(result.LocalComponents) == 0 {
		result.LocalComponents = fallback.LocalComponents
	}
	if len(result.LocalDirectives) == 0 {
		result.LocalDirectives = fallback.LocalDirectives
	}
	if len(result.Members) == 0 {
		result.Members = fallback.Members
	}
	if len(result.Assignments) == 0 {
		result.Assignments = fallback.Assignments
	}
	if len(result.Slots) == 0 {
		result.Slots = fallback.Slots
	}
	if len(result.Blocks) == 0 {
		result.Blocks = fallback.Blocks
	}
	if result.TemplatePath == "" {
		result.TemplatePath = fallback.TemplatePath
	}
	return result
}

func overlayComponents(base, overlay VueComponent) VueComponent {
	result := base
	result.OpenRuntimeMembers = result.OpenRuntimeMembers ||
		overlay.OpenRuntimeMembers
	if overlay.Name != "" {
		result.Name = overlay.Name
	}
	if overlay.Deprecated != "" {
		result.Deprecated = overlay.Deprecated
	}
	if overlay.Kind != "" {
		result.Kind = overlay.Kind
	}
	if overlay.TargetComponent != "" {
		result.TargetComponent = overlay.TargetComponent
	}
	if overlay.ExtendsComponent != "" {
		result.ExtendsComponent = overlay.ExtendsComponent
	}
	if overlay.ImportPath != "" {
		result.ImportPath = overlay.ImportPath
	}
	if overlay.FilePath != "" {
		result.FilePath = overlay.FilePath
	}
	if overlay.DefinitionPath != "" {
		result.DefinitionPath = overlay.DefinitionPath
	}
	if overlay.Line != 0 {
		result.Line = overlay.Line
	}
	if overlay.TemplatePath != "" {
		result.TemplatePath = overlay.TemplatePath
	}
	if overlay.ModelProp != "" {
		result.ModelProp = overlay.ModelProp
	}
	if overlay.ModelEvent != "" {
		result.ModelEvent = overlay.ModelEvent
	}
	result.Props = overlayProps(result.Props, overlay.Props)
	result.Emits = overlayNames(result.Emits, overlay.Emits)
	result.Events = overlayEvents(result.Events, overlay.Events)
	result.Methods = overlayNames(result.Methods, overlay.Methods)
	result.Computed = overlayNames(result.Computed, overlay.Computed)
	result.Data = overlayNames(result.Data, overlay.Data)
	result.Injected = overlayNames(result.Injected, overlay.Injected)
	result.Mixins = overlayNames(result.Mixins, overlay.Mixins)
	result.LocalComponents = overlayLocalComponents(
		result.LocalComponents,
		overlay.LocalComponents,
	)
	result.LocalDirectives = overlayLocalDirectives(
		result.LocalDirectives,
		overlay.LocalDirectives,
	)
	result.Members = overlayMembers(result.Members, overlay.Members)
	result.Assignments = append(result.Assignments, overlay.Assignments...)
	result.Slots = overlaySlots(result.Slots, overlay.Slots)
	result.Blocks = overlayBlocks(result.Blocks, overlay.Blocks)
	return result
}

func overlayProps(base, overlay []VueComponentProp) []VueComponentProp {
	result := append([]VueComponentProp(nil), base...)
	positions := make(map[string]int, len(result))
	for index, prop := range result {
		positions[prop.Name] = index
	}
	for _, prop := range overlay {
		if index, exists := positions[prop.Name]; exists {
			result[index] = prop
		} else {
			positions[prop.Name] = len(result)
			result = append(result, prop)
		}
	}
	return result
}

func overlayNames(base, overlay []string) []string {
	result := append([]string(nil), base...)
	seen := make(map[string]bool, len(result))
	for _, name := range result {
		seen[name] = true
	}
	for _, name := range overlay {
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func overlayLocalComponents(
	base,
	overlay []VueLocalComponent,
) []VueLocalComponent {
	result := append([]VueLocalComponent(nil), base...)
	positions := make(map[string]int, len(result))
	for index, component := range result {
		positions[strings.ToLower(component.Name)] = index
	}
	for _, component := range overlay {
		key := strings.ToLower(component.Name)
		if key == "" {
			continue
		}
		if index, exists := positions[key]; exists {
			result[index] = component
		} else {
			positions[key] = len(result)
			result = append(result, component)
		}
	}
	return result
}

func overlayLocalDirectives(
	base,
	overlay []VueLocalDirective,
) []VueLocalDirective {
	result := append([]VueLocalDirective(nil), base...)
	positions := make(map[string]int, len(result))
	for index, directive := range result {
		positions[strings.ToLower(directive.Name)] = index
	}
	for _, directive := range overlay {
		key := strings.ToLower(directive.Name)
		if key == "" {
			continue
		}
		if index, exists := positions[key]; exists {
			result[index] = directive
		} else {
			positions[key] = len(result)
			result = append(result, directive)
		}
	}
	return result
}

func overlayMembers(base, overlay []VueComponentMember) []VueComponentMember {
	result := append([]VueComponentMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[string(member.Kind)+"\x00"+member.Name] = index
	}
	for _, member := range overlay {
		key := string(member.Kind) + "\x00" + member.Name
		if index, exists := positions[key]; exists {
			if member.Deprecated == "" {
				member.Deprecated = result[index].Deprecated
			}
			result[index] = member
		} else {
			positions[key] = len(result)
			result = append(result, member)
		}
	}
	return result
}

func overlaySlots(base, overlay []VueComponentSlot) []VueComponentSlot {
	result := append([]VueComponentSlot(nil), base...)
	positions := make(map[string]int, len(result))
	for index, slot := range result {
		positions[slot.identityKey()] = index
	}
	for _, slot := range overlay {
		key := slot.identityKey()
		if index, exists := positions[key]; exists {
			current := result[index]
			if slot.FilePath == "" {
				slot.FilePath = current.FilePath
			}
			if slot.Line == 0 {
				slot.Line = current.Line
			}
			if slot.NameRange == (AdminSourceRange{}) &&
				slot.FilePath == current.FilePath {
				slot.NameRange = current.NameRange
			}
			result[index] = slot
		} else {
			positions[key] = len(result)
			result = append(result, slot)
		}
	}
	return result
}

func overlayEvents(base, overlay []VueComponentEvent) []VueComponentEvent {
	result := append([]VueComponentEvent(nil), base...)
	positions := make(map[string]int, len(result))
	for index, event := range result {
		positions[CanonicalEventName(event.Name)] = index
	}
	for _, event := range overlay {
		name := CanonicalEventName(event.Name)
		if name == "" {
			continue
		}
		if index, exists := positions[name]; exists {
			current := result[index]
			if event.Documentation == "" {
				event.Documentation = current.Documentation
			}
			if event.Type == "" {
				event.Type = current.Type
			}
			if event.FilePath == "" {
				event.FilePath = current.FilePath
			}
			if event.Line == 0 {
				event.Line = current.Line
			}
			if event.NameRange == (AdminSourceRange{}) {
				event.NameRange = current.NameRange
			}
			result[index] = event
		} else {
			positions[name] = len(result)
			result = append(result, event)
		}
	}
	return result
}

func overlayBlocks(base, overlay []TwigBlock) []TwigBlock {
	result := append([]TwigBlock(nil), base...)
	positions := make(map[string]int, len(result))
	for index, block := range result {
		positions[block.Name] = index
	}
	for _, block := range overlay {
		if index, exists := positions[block.Name]; exists {
			if block.Deprecated == "" {
				block.Deprecated = result[index].Deprecated
			}
			block.ScopeMembers = overlayBlockScopeMembers(
				result[index].ScopeMembers,
				block.ScopeMembers,
			)
			result[index] = block
		} else {
			positions[block.Name] = len(result)
			result = append(result, block)
		}
	}
	return result
}

func overlayBlockScopeMembers(
	base,
	overlay []TwigBlockScopeMember,
) []TwigBlockScopeMember {
	result := append([]TwigBlockScopeMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	for _, member := range overlay {
		if member.Name == "" {
			continue
		}
		if index, exists := positions[member.Name]; exists {
			result[index] = member
		} else {
			positions[member.Name] = len(result)
			result = append(result, member)
		}
	}
	return result
}

// parseComponentRegistrations extracts Shopware.Component.register and extend calls.
