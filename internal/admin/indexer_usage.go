package admin

import (
	"path/filepath"
	"sort"
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (idx *AdminComponentIndexer) GetUsages(
	kind AdminSymbolKind,
	owner,
	name string,
) ([]AdminUsageSet, error) {
	return idx.usageIndex.GetValues(AdminUsageKey(kind, owner, name))
}

// GetSymbolUsages expands source-owned component events and slots through all
// effective components that expose the same declaration. Other symbol kinds
// retain their direct persisted identity.
func (idx *AdminComponentIndexer) GetSymbolUsages(
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	sets, err := idx.GetUsages(target.Kind, target.Owner, target.Name)
	if err != nil {
		return sets, err
	}
	if target.Kind == AdminSymbolDirective {
		return idx.directiveSymbolUsages(target, sets)
	}
	if target.Kind == AdminSymbolComponentMember {
		return idx.componentMemberSymbolUsages(target, sets)
	}
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return sets, err
	}
	sets, err = idx.exposedComponentSymbolUsages(target, sets)
	if err != nil {
		return nil, err
	}
	sets, err = idx.localComponentSymbolUsages(target, sets)
	if err != nil {
		return nil, err
	}
	dynamicSets, err := idx.dynamicComponentSymbolUsages(target)
	if err != nil {
		return nil, err
	}
	sets = append(sets, dynamicSets...)
	return uniqueAdminUsageSets(sets), nil
}

func (idx *AdminComponentIndexer) exposedComponentSymbolUsages(
	target AdminSymbolTarget,
	sets []AdminUsageSet,
) ([]AdminUsageSet, error) {
	components, err := idx.GetComponentsExposingSymbol(target)
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		componentSets, usageErr := idx.GetUsages(
			target.Kind,
			component.Name,
			target.Name,
		)
		if usageErr != nil {
			return nil, usageErr
		}
		for _, set := range componentSets {
			matches, matchErr := idx.componentUsageSetMatchesSource(
				set, component.Name, target,
			)
			if matchErr != nil {
				return nil, matchErr
			}
			if matches {
				sets = append(sets, set)
			}
		}
		modelSets, modelErr := idx.componentModelUsageSets(
			component, component.Name, target,
		)
		if modelErr != nil {
			return nil, modelErr
		}
		for _, set := range modelSets {
			matches, matchErr := idx.componentModelUsageSetMatchesSource(
				set, component.Name, target,
			)
			if matchErr != nil {
				return nil, matchErr
			}
			if matches {
				sets = append(sets, set)
			}
		}
	}
	return sets, nil
}

func (idx *AdminComponentIndexer) localComponentSymbolUsages(
	target AdminSymbolTarget,
	sets []AdminUsageSet,
) ([]AdminUsageSet, error) {
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		owner, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if owner == nil || owner.TemplatePath == "" {
			continue
		}
		for _, local := range owner.LocalComponents {
			component, found, localErr := idx.GetComponentForTemplateTag(
				owner.TemplatePath, local.Name,
			)
			if localErr != nil {
				return nil, localErr
			}
			if !found || component == nil {
				continue
			}
			source, exposes := component.SymbolSource(
				target.Kind, target.Name,
			)
			if !exposes || filepath.Clean(source) != filepath.Clean(target.Owner) {
				continue
			}
			localSets, usageErr := idx.GetUsages(
				target.Kind, local.Name, target.Name,
			)
			if usageErr != nil {
				return nil, usageErr
			}
			for _, set := range localSets {
				if normalizeDefinitionPath(set.FilePath) ==
					normalizeDefinitionPath(owner.TemplatePath) {
					sets = append(sets, set)
				}
			}
			modelSets, modelErr := idx.componentModelUsageSets(
				*component, local.Name, target,
			)
			if modelErr != nil {
				return nil, modelErr
			}
			for _, set := range modelSets {
				if normalizeDefinitionPath(set.FilePath) ==
					normalizeDefinitionPath(owner.TemplatePath) {
					sets = append(sets, set)
				}
			}
		}
	}
	return sets, nil
}

type dynamicUsageResolutionKey struct {
	filePath   string
	selector   string
	routerView bool
}

type dynamicUsageResolution struct {
	components []VueComponent
	complete   bool
}

// dynamicComponentSymbolUsages resolves symbolically persisted dynamic
// component usages against the latest effective component graph. Keeping this
// join query-time avoids making template indexing depend on whether component
// definitions, CMS registrations, or module routes happened to be indexed
// first.
func (idx *AdminComponentIndexer) dynamicComponentSymbolUsages(
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return nil, nil
	}
	raw, err := idx.GetUsages(
		target.Kind, adminDynamicComponentUsageOwner, target.Name,
	)
	if err != nil {
		return nil, err
	}
	if target.Kind == AdminSymbolComponentProp ||
		target.Kind == AdminSymbolComponentEvent {
		keys, keyErr := idx.usageIndex.GetAllKeys()
		if keyErr != nil {
			return nil, keyErr
		}
		prefix := AdminUsageKey(
			AdminSymbolComponentModel,
			adminDynamicComponentUsageOwner,
			"",
		)
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			modelSets, modelErr := idx.GetUsages(
				AdminSymbolComponentModel,
				adminDynamicComponentUsageOwner,
				strings.TrimPrefix(key, prefix),
			)
			if modelErr != nil {
				return nil, modelErr
			}
			raw = append(raw, modelSets...)
		}
	}
	cache := make(map[dynamicUsageResolutionKey]dynamicUsageResolution)
	var result []AdminUsageSet
	for _, set := range raw {
		filtered := set
		filtered.Occurrences = nil
		for _, occurrence := range set.Occurrences {
			key := dynamicUsageResolutionKey{
				filePath:   normalizeDefinitionPath(set.FilePath),
				selector:   occurrence.DynamicComponentSelector,
				routerView: occurrence.DynamicRouterView,
			}
			resolution, cached := cache[key]
			if !cached {
				components, complete, resolveErr :=
					idx.resolvePersistedDynamicUsageComponents(
						set.FilePath, occurrence,
					)
				if resolveErr != nil {
					return nil, resolveErr
				}
				resolution = dynamicUsageResolution{
					components: components, complete: complete,
				}
				cache[key] = resolution
			}
			if !resolution.complete ||
				!dynamicUsageComponentsMatchTarget(
					resolution.components, set, target,
				) {
				continue
			}
			filtered.Occurrences = append(
				filtered.Occurrences, occurrence,
			)
		}
		if len(filtered.Occurrences) > 0 {
			result = append(result, filtered)
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) resolvePersistedDynamicUsageComponents(
	templatePath string,
	occurrence AdminSourceRange,
) ([]VueComponent, bool, error) {
	owner, err := idx.GetComponentByTemplatePath(templatePath)
	if err != nil || owner == nil {
		return nil, false, err
	}
	var resolved dynamicComponentNames
	if occurrence.DynamicRouterView {
		resolved, err = idx.resolveRouterViewRouteComponentNames(*owner)
	} else {
		resolved = idx.resolveDynamicComponentNames(
			*owner,
			occurrence.DynamicComponentSelector,
			make(map[string]bool),
		)
	}
	if err != nil || !resolved.found || !resolved.complete ||
		len(resolved.names) == 0 {
		return nil, false, err
	}
	selector := VueDynamicComponentSelector{Complete: true}
	for _, name := range resolved.names {
		selector.Candidates = append(
			selector.Candidates,
			VueDynamicComponentCandidate{Name: name},
		)
	}
	return idx.ResolveDynamicComponents(templatePath, selector)
}

func dynamicUsageComponentsMatchTarget(
	components []VueComponent,
	set AdminUsageSet,
	target AdminSymbolTarget,
) bool {
	for _, component := range components {
		if set.Kind == AdminSymbolComponentModel {
			for _, binding := range component.ComponentModels() {
				if binding.AttributeName == set.Name &&
					componentModelBindingMatchesTarget(
						component, binding, target,
					) {
					return true
				}
			}
			continue
		}
		source, found := component.SymbolSource(target.Kind, target.Name)
		if found && normalizeDefinitionPath(source) ==
			normalizeDefinitionPath(target.Owner) {
			return true
		}
	}
	return false
}

// DynamicComponentUsageRenameSafe reports whether rewriting a dynamically
// owned attribute keeps the same declaration identity for every possible
// component. References may legitimately belong to several declarations, but
// a rename must not change a shared spelling when another runtime candidate
// owns a distinct contract.
func (idx *AdminComponentIndexer) DynamicComponentUsageRenameSafe(
	set AdminUsageSet,
	occurrence AdminSourceRange,
	target AdminSymbolTarget,
) (bool, error) {
	if occurrence.DynamicComponentSelector == "" &&
		!occurrence.DynamicRouterView {
		return true, nil
	}
	components, complete, err := idx.resolvePersistedDynamicUsageComponents(
		set.FilePath, occurrence,
	)
	if err != nil || !complete || len(components) == 0 {
		return false, err
	}
	if set.Kind != target.Kind || set.Name != target.Name {
		return false, nil
	}
	for _, component := range components {
		source, found := component.SymbolSource(set.Kind, set.Name)
		if !found || normalizeDefinitionPath(source) !=
			normalizeDefinitionPath(target.Owner) {
			return false, nil
		}
	}
	return true, nil
}

func (idx *AdminComponentIndexer) directiveSymbolUsages(
	target AdminSymbolTarget,
	direct []AdminUsageSet,
) ([]AdminUsageSet, error) {
	var result []AdminUsageSet
	if target.Owner != "" {
		// Source-owned sets contain the local declaration. Template references
		// are intentionally persisted without an owner and are scoped below.
		result = append(result, direct...)
	}
	raw, err := idx.GetUsages(AdminSymbolDirective, "", target.Name)
	if err != nil {
		return nil, err
	}
	for _, set := range raw {
		if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
			if target.Owner == "" {
				result = append(result, set)
			}
			continue
		}
		local, found, localErr := idx.GetLocalDirectiveForTemplate(
			set.FilePath, target.Name,
		)
		if localErr != nil {
			return nil, localErr
		}
		if target.Owner == "" {
			if !found {
				result = append(result, set)
			}
			continue
		}
		if found && normalizeDefinitionPath(local.FilePath) ==
			normalizeDefinitionPath(target.Owner) {
			result = append(result, set)
		}
	}
	return uniqueAdminUsageSets(result), nil
}

func (idx *AdminComponentIndexer) componentModelUsageSets(
	component VueComponent,
	usageOwner string,
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent {
		return nil, nil
	}
	var result []AdminUsageSet
	for _, binding := range component.ComponentModels() {
		if !componentModelBindingMatchesTarget(component, binding, target) {
			continue
		}
		sets, err := idx.GetUsages(
			AdminSymbolComponentModel, usageOwner, binding.AttributeName,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, sets...)
	}
	return result, nil
}

func componentModelBindingMatchesTarget(
	component VueComponent,
	binding VueComponentModelBinding,
	target AdminSymbolTarget,
) bool {
	name := binding.PropName
	if target.Kind == AdminSymbolComponentEvent {
		name = binding.EventName
	}
	if name != target.Name {
		return false
	}
	source, found := component.SymbolSource(target.Kind, target.Name)
	return found && filepath.Clean(source) == filepath.Clean(target.Owner)
}

func (idx *AdminComponentIndexer) componentModelUsageSetMatchesSource(
	set AdminUsageSet,
	componentName string,
	target AdminSymbolTarget,
) (bool, error) {
	if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		return true, nil
	}
	owner, err := idx.GetComponentByTemplatePath(set.FilePath)
	if err != nil || owner == nil {
		return owner == nil, err
	}
	if _, local := owner.LocalComponent(componentName); !local {
		return true, nil
	}
	component, found, err := idx.GetComponentForTemplateTag(
		set.FilePath, componentName,
	)
	if err != nil || !found || component == nil {
		return false, err
	}
	for _, binding := range component.ComponentModels() {
		if binding.AttributeName == set.Name &&
			componentModelBindingMatchesTarget(*component, binding, target) {
			return true, nil
		}
	}
	return false, nil
}

func (idx *AdminComponentIndexer) componentMemberSymbolUsages(
	target AdminSymbolTarget,
	sets []AdminUsageSet,
) ([]AdminUsageSet, error) {
	raw, err := idx.GetUsages(
		AdminSymbolComponentMember, "", target.Name,
	)
	if err != nil {
		return nil, err
	}
	for _, set := range raw {
		matches, matchErr := idx.componentMemberUsageSetMatchesSource(
			set, target,
		)
		if matchErr != nil {
			return nil, matchErr
		}
		if matches {
			sets = append(sets, set)
		}
	}
	return uniqueAdminUsageSets(sets), nil
}

func (idx *AdminComponentIndexer) componentMemberUsageSetMatchesSource(
	set AdminUsageSet,
	target AdminSymbolTarget,
) (bool, error) {
	identities := make(map[string]bool)
	add := func(component *VueComponent) {
		if component == nil {
			return
		}
		member, found := component.TemplateMember(target.Name)
		if !found || !member.Renameable() {
			return
		}
		if identity := member.SourceIdentity(); identity != "" {
			identities[identity] = true
		}
	}
	if strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		component, err := idx.GetComponentByTemplatePath(set.FilePath)
		if err != nil {
			return false, err
		}
		add(component)
	} else {
		components, err := idx.GetComponentsByDefinitionPath(set.FilePath)
		if err != nil {
			return false, err
		}
		for index := range components {
			add(&components[index])
		}
	}
	return len(identities) == 1 && identities[target.Owner], nil
}

func (idx *AdminComponentIndexer) GetComponentsExposingMember(
	target AdminSymbolTarget,
) ([]VueComponent, error) {
	if target.Kind != AdminSymbolComponentMember || target.Name == "" ||
		target.Owner == "" {
		return nil, nil
	}
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	var result []VueComponent
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil {
			continue
		}
		member, found := component.TemplateMember(target.Name)
		if found && member.Renameable() &&
			member.SourceIdentity() == target.Owner {
			result = append(result, *component)
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) componentUsageSetMatchesSource(
	set AdminUsageSet,
	componentName string,
	target AdminSymbolTarget,
) (bool, error) {
	if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		return true, nil
	}
	owner, err := idx.GetComponentByTemplatePath(set.FilePath)
	if err != nil || owner == nil {
		return owner == nil, err
	}
	if _, local := owner.LocalComponent(componentName); !local {
		return true, nil
	}
	component, found, err := idx.GetComponentForTemplateTag(
		set.FilePath, componentName,
	)
	if err != nil || !found || component == nil {
		return false, err
	}
	source, exposes := component.SymbolSource(target.Kind, target.Name)
	return exposes && filepath.Clean(source) == filepath.Clean(target.Owner), nil
}

func uniqueAdminUsageSets(values []AdminUsageSet) []AdminUsageSet {
	result := make([]AdminUsageSet, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		key := AdminUsageKey(value.Kind, value.Owner, value.Name) + "\x00" +
			normalizeDefinitionPath(value.FilePath)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func (idx *AdminComponentIndexer) GetComponentsExposingSymbol(
	target AdminSymbolTarget,
) ([]VueComponent, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return nil, nil
	}
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	result := make([]VueComponent, 0)
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil {
			continue
		}
		owner, found := component.SymbolSource(target.Kind, target.Name)
		if !found || filepath.Clean(owner) != filepath.Clean(target.Owner) {
			continue
		}
		result = append(result, *component)
	}
	return result, nil
}

// IsDynamicComponentSlot reports whether a resolved concrete consumer name is
// owned by a computed slot family. Such consumers support navigation and
// references, but renaming one concrete spelling cannot safely rewrite the
// runtime declaration expression.
func (idx *AdminComponentIndexer) IsDynamicComponentSlot(
	target AdminSymbolTarget,
) (bool, error) {
	if target.Kind != AdminSymbolComponentSlot {
		return false, nil
	}
	components, err := idx.GetComponentsExposingSymbol(target)
	if err != nil {
		return false, err
	}
	for _, component := range components {
		slot, found := component.ComponentSlot(target.Name)
		if found && slot.IsDynamicName() {
			return true, nil
		}
	}
	return false, nil
}

func componentMemberTarget(member VueComponentMember) AdminSymbolTarget {
	return AdminSymbolTarget{
		Kind:  AdminSymbolComponentMember,
		Owner: member.SourceIdentity(),
		Name:  member.Name,
	}
}

// ResolveComponentMemberTarget returns the source declaration encoded by a
// component-member symbol identity. Effective components may expose the same
// declaration through inheritance, but the returned member is the one stable
// source node used by navigation, references, rename, and call hierarchy.
func (idx *AdminComponentIndexer) ResolveComponentMemberTarget(
	target AdminSymbolTarget,
) (VueComponentMember, bool, error) {
	if target.Kind != AdminSymbolComponentMember || target.Owner == "" ||
		target.Name == "" {
		return VueComponentMember{}, false, nil
	}
	separator := strings.IndexByte(target.Owner, 0)
	if separator <= 0 {
		return VueComponentMember{}, false, nil
	}
	members, err := idx.componentMembersDeclaredIn(target.Owner[:separator])
	if err != nil {
		return VueComponentMember{}, false, err
	}
	for _, member := range members {
		if member.SourceIdentity() == target.Owner && member.Name == target.Name {
			return member, true, nil
		}
	}
	return VueComponentMember{}, false, nil
}

// StoreActionTargetsAtDefinitionPosition resolves a Pinia action declaration
// by its indexed source line. A setup-store factory can feed more than one
// public store, so all unambiguous public targets are returned.
func (idx *AdminComponentIndexer) StoreActionTargetsAtDefinitionPosition(
	filePath,
	name string,
	line int,
) ([]AdminSymbolTarget, error) {
	if filePath == "" || name == "" || line < 0 {
		return nil, nil
	}
	stores, err := idx.GetAllStores()
	if err != nil {
		return nil, err
	}
	var result []AdminSymbolTarget
	seen := make(map[AdminSymbolTarget]bool)
	for _, store := range stores {
		member, found := store.Member(name)
		if !found || member.Kind != AdminStoreAction ||
			normalizeDefinitionPath(member.FilePath) !=
				normalizeDefinitionPath(filePath) || member.Line != line+1 {
			continue
		}
		target := AdminSymbolTarget{
			Kind: AdminSymbolStoreMember, Owner: store.Name, Name: member.Name,
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Owner != result[right].Owner {
			return result[left].Owner < result[right].Owner
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func (idx *AdminComponentIndexer) TwigComponentMemberAt(
	filePath string,
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (AdminSymbolTarget, VueComponentMember, bool, error) {
	name, rangeValue, found := TwigVueExpressionRootIdentifierAtOffset(
		root, content, offset,
	)
	if !found || twigVueRootIdentifierIsLocal(
		root, content, TwigVueMember{Name: name, Range: rangeValue},
	) {
		return AdminSymbolTarget{}, VueComponentMember{}, false, nil
	}
	component, err := idx.GetComponentByTemplatePath(filePath)
	if err != nil || component == nil {
		return AdminSymbolTarget{}, VueComponentMember{}, false, err
	}
	member, found := component.TemplateMember(name)
	if !found || !member.Renameable() {
		return AdminSymbolTarget{}, VueComponentMember{}, false, nil
	}
	return componentMemberTarget(member), member, true, nil
}

func (idx *AdminComponentIndexer) GetComponentMemberAtDefinitionPosition(
	filePath string,
	line,
	character int,
) (VueComponentMember, bool, error) {
	members, err := idx.componentMembersDeclaredIn(filePath)
	if err != nil {
		return VueComponentMember{}, false, err
	}
	for _, member := range members {
		if member.Renameable() && adminSourceRangeContainsPosition(
			member.NameRange, line, character,
		) {
			return member, true, nil
		}
	}
	return VueComponentMember{}, false, nil
}

func (idx *AdminComponentIndexer) componentMembersDeclaredIn(
	filePath string,
) ([]VueComponentMember, error) {
	normalized := normalizeDefinitionPath(filePath)
	definitions, err := idx.definitionIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var result []VueComponentMember
	add := func(definition ComponentDefinition) {
		if normalizeDefinitionPath(definition.FilePath) != normalized {
			return
		}
		for _, member := range definition.Members {
			identity := member.SourceIdentity()
			if identity == "" || seen[identity] {
				continue
			}
			seen[identity] = true
			result = append(result, member)
		}
	}
	for _, definition := range definitions {
		add(definition)
	}
	mixins, err := idx.GetAllMixins()
	if err != nil {
		return nil, err
	}
	for _, mixin := range mixins {
		add(mixin.Definition)
	}
	return result, nil
}

func (idx *AdminComponentIndexer) TwigSymbolAt(
	filePath string,
	root *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool, error) {
	target, found := TwigSymbolAtOffset(root, offset)
	if !found {
		return AdminSymbolTarget{}, false, nil
	}
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot &&
		target.Kind != AdminSymbolDirective {
		return target, true, nil
	}
	if target.Kind == AdminSymbolDirective {
		local, localFound, localErr := idx.GetLocalDirectiveForTemplate(
			filePath, target.Name,
		)
		if localErr != nil {
			return AdminSymbolTarget{}, false, localErr
		}
		if localFound {
			return AdminSymbolTarget{
				Kind:  AdminSymbolDirective,
				Owner: local.FilePath,
				Name:  local.Name,
			}, true, nil
		}
		return target, true, nil
	}
	var component *VueComponent
	var err error
	if target.Owner != "" {
		component, _, err = idx.GetComponentForTemplateTag(
			filePath, target.Owner,
		)
	} else if target.Kind == AdminSymbolComponentSlot {
		component, err = idx.GetComponentByTemplatePath(filePath)
	}
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	if component == nil {
		return AdminSymbolTarget{}, false, nil
	}
	switch target.Kind {
	case AdminSymbolComponentProp:
		prop, exists := component.ComponentProp(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := prop.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentProp, Owner: owner, Name: prop.Name,
		}, true, nil
	case AdminSymbolComponentEvent:
		event, exists := component.ComponentEvent(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := event.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind:  AdminSymbolComponentEvent,
			Owner: owner,
			Name:  CanonicalEventName(event.Name),
		}, true, nil
	case AdminSymbolComponentSlot:
		slot, exists := component.ComponentSlot(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := slot.FilePath
		if owner == "" {
			owner = component.TemplatePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentSlot, Owner: owner, Name: target.Name,
		}, true, nil
	}
	return AdminSymbolTarget{}, false, nil
}
