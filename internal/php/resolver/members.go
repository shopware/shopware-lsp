package resolver

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type MemberResolver struct {
	Snapshot *semantic.Snapshot
}

type ResolvedMember struct {
	Symbol semantic.Symbol
	Type   types.Type
}

type memberNameTracker struct {
	named      bool
	matched    bool
	mixedKinds bool
	seen       map[string]struct{}
}

func newMemberNameTracker(name string, mixedKinds bool) memberNameTracker {
	return memberNameTracker{
		named:      name != "",
		mixedKinds: mixedKinds,
	}
}

func (t *memberNameTracker) contains(
	name string,
	kind semantic.SymbolKind,
) bool {
	if t.named {
		return t.matched
	}
	_, exists := t.seen[memberResolutionKey(name, kind, t.mixedKinds)]
	return exists
}

func (t *memberNameTracker) add(
	name string,
	kind semantic.SymbolKind,
) bool {
	if t.named {
		if t.matched {
			return false
		}
		t.matched = true
		return true
	}
	key := memberResolutionKey(name, kind, t.mixedKinds)
	if _, exists := t.seen[key]; exists {
		return false
	}
	if t.seen == nil {
		t.seen = make(map[string]struct{})
	}
	t.seen[key] = struct{}{}
	return true
}

const inlineMemberClassCount = 4

type memberClassTracker struct {
	inline   [inlineMemberClassCount]semantic.SymbolID
	count    uint8
	overflow map[semantic.SymbolID]struct{}
}

func (t *memberClassTracker) add(id semantic.SymbolID) bool {
	for index := range int(t.count) {
		if t.inline[index] == id {
			return false
		}
	}
	if _, exists := t.overflow[id]; exists {
		return false
	}
	if int(t.count) < len(t.inline) {
		t.inline[t.count] = id
		t.count++
		return true
	}
	if t.overflow == nil {
		t.overflow = make(map[semantic.SymbolID]struct{})
	}
	t.overflow[id] = struct{}{}
	return true
}

func (t memberClassTracker) clone() memberClassTracker {
	t.overflow = maps.Clone(t.overflow)
	return t
}

func (r MemberResolver) Methods(receiver types.Type, name string) []ResolvedMember {
	return r.members(receiver, name, semantic.MethodSymbol)
}

// VisitMethods visits effective matching methods without materializing the
// compatibility result slice. Returning false stops the traversal.
func (r MemberResolver) VisitMethods(
	receiver types.Type,
	name string,
	visit func(ResolvedMember) bool,
) bool {
	if r.Snapshot == nil || visit == nil {
		return true
	}
	if receiver.Kind() == types.UnionKind ||
		receiver.Kind() == types.IntersectionKind {
		for _, member := range r.Methods(receiver, name) {
			if !visit(member) {
				return false
			}
		}
		return true
	}
	return r.visitObjectMembers(
		receiver,
		name,
		semantic.MethodSymbol,
		visit,
	)
}

func (r MemberResolver) Properties(receiver types.Type, name string) []ResolvedMember {
	return r.members(receiver, name, semantic.PropertySymbol)
}

func (r MemberResolver) Constants(receiver types.Type, name string) []ResolvedMember {
	result := r.members(receiver, name, semantic.ClassConstantSymbol)
	return append(result, r.members(receiver, name, semantic.EnumCaseSymbol)...)
}

func (r MemberResolver) MethodIDs(
	receiver types.Type,
	name string,
) []semantic.SymbolID {
	return r.memberIDs(receiver, name, semantic.MethodSymbol)
}

// VisitMethodIDs visits effective matching methods without materializing a
// result slice. Returning false stops the traversal.
func (r MemberResolver) VisitMethodIDs(
	receiver types.Type,
	name string,
	visit func(semantic.SymbolID) bool,
) bool {
	return r.visitMemberIDs(receiver, name, semantic.MethodSymbol, visit)
}

// AllMethodIDs returns the effective methods visible on receiver. Overridden
// declarations are omitted, just as they are for named member resolution.
func (r MemberResolver) AllMethodIDs(
	receiver types.Type,
) []semantic.SymbolID {
	return r.memberIDs(receiver, "", semantic.MethodSymbol)
}

// VisitAllMethodIDs is the non-materializing counterpart of AllMethodIDs.
func (r MemberResolver) VisitAllMethodIDs(
	receiver types.Type,
	visit func(semantic.SymbolID) bool,
) bool {
	return r.visitMemberIDs(receiver, "", semantic.MethodSymbol, visit)
}

func (r MemberResolver) PropertyIDs(
	receiver types.Type,
	name string,
) []semantic.SymbolID {
	return r.memberIDs(receiver, name, semantic.PropertySymbol)
}

// VisitPropertyIDs visits effective matching properties without materializing
// a result slice. Returning false stops the traversal.
func (r MemberResolver) VisitPropertyIDs(
	receiver types.Type,
	name string,
	visit func(semantic.SymbolID) bool,
) bool {
	return r.visitMemberIDs(receiver, name, semantic.PropertySymbol, visit)
}

func (r MemberResolver) ConstantIDs(
	receiver types.Type,
	name string,
) []semantic.SymbolID {
	result := r.memberIDs(receiver, name, semantic.ClassConstantSymbol)
	return append(result, r.memberIDs(receiver, name, semantic.EnumCaseSymbol)...)
}

// VisitConstantIDs visits matching class constants followed by enum cases.
func (r MemberResolver) VisitConstantIDs(
	receiver types.Type,
	name string,
	visit func(semantic.SymbolID) bool,
) bool {
	if !r.visitMemberIDs(
		receiver,
		name,
		semantic.ClassConstantSymbol,
		visit,
	) {
		return false
	}
	return r.visitMemberIDs(receiver, name, semantic.EnumCaseSymbol, visit)
}

func (r MemberResolver) PropertyTypes(
	receiver types.Type,
	name string,
) []types.Type {
	return r.memberTypeValues(receiver, name, semantic.PropertySymbol)
}

func (r MemberResolver) ConstantTypes(
	receiver types.Type,
	name string,
) []types.Type {
	result := r.memberTypeValues(receiver, name, semantic.ClassConstantSymbol)
	return append(
		result,
		r.memberTypeValues(receiver, name, semantic.EnumCaseSymbol)...,
	)
}

func (r MemberResolver) All(receiver types.Type) []ResolvedMember {
	return r.members(receiver, "", 255)
}

func (r MemberResolver) memberTypeValues(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
) []types.Type {
	if r.Snapshot == nil {
		return nil
	}
	var result []types.Type
	var seenIDs map[semantic.SymbolID]struct{}
	if receiver.Kind() == types.UnionKind ||
		receiver.Kind() == types.IntersectionKind {
		seenIDs = make(map[semantic.SymbolID]struct{})
	}
	r.appendMemberTypeValues(receiver, name, kind, seenIDs, &result)
	return result
}

func (r MemberResolver) appendMemberTypeValues(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
	seenIDs map[semantic.SymbolID]struct{},
	result *[]types.Type,
) {
	if receiver.Kind() == types.UnionKind ||
		receiver.Kind() == types.IntersectionKind {
		for index := 0; index < receiver.ArgumentCount(); index++ {
			r.appendMemberTypeValues(
				receiver.Argument(index),
				name,
				kind,
				seenIDs,
				result,
			)
		}
		return
	}
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return
	}
	seenNames := make(map[string]struct{})
	visited := make(map[semantic.SymbolID]struct{})
	r.Snapshot.VisitClassViews(receiver.Name(), func(classView semantic.SymbolView) bool {
		class := classView.Materialize()
		r.collectClassMemberTypeValues(
			class,
			name,
			kind,
			bindClassTemplatesFromType(class, receiver),
			seenNames,
			visited,
			seenIDs,
			result,
		)
		return true
	})
}

func (r MemberResolver) collectClassMemberTypeValues(
	class semantic.Symbol,
	name string,
	kind semantic.SymbolKind,
	templates map[string]types.Type,
	seenNames map[string]struct{},
	visited map[semantic.SymbolID]struct{},
	seenIDs map[semantic.SymbolID]struct{},
	result *[]types.Type,
) {
	if _, exists := visited[class.ID]; exists {
		return
	}
	visited[class.ID] = struct{}{}

	visitCandidate := func(candidateView semantic.SymbolView) bool {
		candidate := candidateView.Materialize()
		if candidate.Kind != kind {
			return true
		}
		key := memberResolutionKey(candidate.Name, candidate.Kind, false)
		if _, exists := seenNames[key]; exists {
			return true
		}
		seenNames[key] = struct{}{}
		if seenIDs != nil {
			if _, exists := seenIDs[candidate.ID]; exists {
				return true
			}
			seenIDs[candidate.ID] = struct{}{}
		}
		effective := maps.Clone(templates)
		for _, template := range candidate.Templates {
			delete(effective, template.Name)
		}
		value := candidate.Type
		if candidate.IsFunctionLike() {
			value = candidate.ReturnType
		}
		*result = append(*result, types.Substitute(value, effective))
		return true
	}
	if name == "" {
		candidates := r.Snapshot.MemberViewsOf(class.ID)
		*result = slices.Grow(*result, len(candidates))
		for _, candidate := range candidates {
			visitCandidate(candidate)
		}
	} else {
		r.Snapshot.VisitMemberViews(class.ID, name, visitCandidate)
	}

	for _, traitName := range class.Traits {
		r.Snapshot.VisitClassViews(traitName, func(traitView semantic.SymbolView) bool {
			r.collectClassMemberTypeValues(
				traitView.Materialize(),
				name,
				kind,
				templates,
				seenNames,
				visited,
				seenIDs,
				result,
			)
			return true
		})
	}
	parentTypes := append(
		append([]types.Type(nil), class.ExtendsTypes...),
		class.ImplementsTypes...,
	)
	parentNames := append(
		append([]string(nil), class.Extends...),
		class.Implements...,
	)
	for _, parentName := range parentNames {
		r.Snapshot.VisitClassViews(parentName, func(parentView semantic.SymbolView) bool {
			parent := parentView.Materialize()
			var arguments []types.Type
			for _, parentType := range parentTypes {
				if strings.EqualFold(parentType.Name(), parentName) {
					arguments = parentType.Arguments()
					for index := range arguments {
						arguments[index] = types.Substitute(
							arguments[index],
							templates,
						)
					}
					break
				}
			}
			r.collectClassMemberTypeValues(
				parent,
				name,
				kind,
				bindClassTemplates(parent, arguments),
				seenNames,
				visited,
				seenIDs,
				result,
			)
			return true
		})
	}
}

func (r MemberResolver) memberIDs(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
) []semantic.SymbolID {
	var result []semantic.SymbolID
	r.visitMemberIDs(
		receiver,
		name,
		kind,
		func(id semantic.SymbolID) bool {
			result = append(result, id)
			return true
		},
	)
	return result
}

func (r MemberResolver) visitMemberIDs(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
	visit func(semantic.SymbolID) bool,
) bool {
	if r.Snapshot == nil || visit == nil {
		return true
	}
	var seenIDs map[semantic.SymbolID]struct{}
	if receiver.Kind() == types.UnionKind ||
		receiver.Kind() == types.IntersectionKind {
		seenIDs = make(map[semantic.SymbolID]struct{})
	}
	return r.visitMemberTypeIDs(
		receiver,
		name,
		kind,
		seenIDs,
		visit,
	)
}

func (r MemberResolver) visitMemberTypeIDs(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
	seenIDs map[semantic.SymbolID]struct{},
	visit func(semantic.SymbolID) bool,
) bool {
	if receiver.Kind() == types.UnionKind ||
		receiver.Kind() == types.IntersectionKind {
		for index := 0; index < receiver.ArgumentCount(); index++ {
			if !r.visitMemberTypeIDs(
				receiver.Argument(index),
				name,
				kind,
				seenIDs,
				visit,
			) {
				return false
			}
		}
		return true
	}
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return true
	}
	seenNames := make(map[string]struct{})
	visited := make(map[semantic.SymbolID]struct{})
	return r.Snapshot.VisitClassViews(
		receiver.Name(),
		func(class semantic.SymbolView) bool {
			return r.visitClassMemberIDs(
				class,
				name,
				kind,
				seenNames,
				visited,
				seenIDs,
				visit,
			)
		},
	)
}

func (r MemberResolver) visitClassMemberIDs(
	class semantic.SymbolView,
	name string,
	kind semantic.SymbolKind,
	seenNames map[string]struct{},
	visited map[semantic.SymbolID]struct{},
	seenIDs map[semantic.SymbolID]struct{},
	visit func(semantic.SymbolID) bool,
) bool {
	if _, exists := visited[class.ID()]; exists {
		return true
	}
	visited[class.ID()] = struct{}{}

	visitCandidate := func(candidate semantic.SymbolView) bool {
		if candidate.Kind() != kind {
			return true
		}
		key := memberResolutionKey(
			candidate.Name(),
			candidate.Kind(),
			false,
		)
		if _, exists := seenNames[key]; exists {
			return true
		}
		seenNames[key] = struct{}{}
		if seenIDs != nil {
			if _, exists := seenIDs[candidate.ID()]; exists {
				return true
			}
			seenIDs[candidate.ID()] = struct{}{}
		}
		return visit(candidate.ID())
	}
	if name == "" {
		for _, candidate := range r.Snapshot.MemberViewsOf(class.ID()) {
			if !visitCandidate(candidate) {
				return false
			}
		}
	} else if !r.Snapshot.VisitMemberViews(
		class.ID(),
		name,
		visitCandidate,
	) {
		return false
	}
	if (kind == semantic.MethodSymbol || kind == 255) &&
		!r.visitTraitAliasIDs(class, name, seenNames, seenIDs, visit) {
		return false
	}

	traits, extends, implements := class.HierarchyNames()
	visitRelated := func(names []string) bool {
		for _, relatedName := range names {
			if !r.Snapshot.VisitClassViews(
				relatedName,
				func(related semantic.SymbolView) bool {
					return r.visitClassMemberIDs(
						related,
						name,
						kind,
						seenNames,
						visited,
						seenIDs,
						visit,
					)
				},
			) {
				return false
			}
		}
		return true
	}
	return visitRelated(traits) &&
		visitRelated(extends) &&
		visitRelated(implements)
}

func (r MemberResolver) visitTraitAliasIDs(
	class semantic.SymbolView,
	name string,
	seenNames map[string]struct{},
	seenIDs map[semantic.SymbolID]struct{},
	visit func(semantic.SymbolID) bool,
) bool {
	if name == "" {
		// ID-only enumeration cannot represent two effective names pointing at
		// one declaration. The original trait method is visited below; alias
		// names are surfaced by the materialized All() path instead.
		return true
	}
	for _, alias := range class.TraitAliases() {
		if name != "" && !strings.EqualFold(alias.Alias, name) {
			continue
		}
		key := memberResolutionKey(alias.Alias, semantic.MethodSymbol, false)
		if _, exists := seenNames[key]; exists {
			continue
		}
		seenNames[key] = struct{}{}
		completed := true
		if !r.Snapshot.VisitClassViews(
			alias.Trait,
			func(trait semantic.SymbolView) bool {
				completed = r.visitClassMemberIDs(
					trait,
					alias.Method,
					semantic.MethodSymbol,
					make(map[string]struct{}),
					make(map[semantic.SymbolID]struct{}),
					seenIDs,
					visit,
				)
				return completed
			},
		) || !completed {
			return false
		}
	}
	return true
}

func (r MemberResolver) members(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
) []ResolvedMember {
	if r.Snapshot == nil {
		return nil
	}
	if receiver.Kind() == types.UnionKind || receiver.Kind() == types.IntersectionKind {
		var result []ResolvedMember
		seen := make(map[semantic.SymbolID]int)
		relations := r.Snapshot.Relations()
		for index := 0; index < receiver.ArgumentCount(); index++ {
			memberType := receiver.Argument(index)
			for _, member := range r.members(memberType, name, kind) {
				if existing, exists := seen[member.Symbol.ID]; exists {
					result[existing].Type = relations.Join(
						result[existing].Type,
						member.Type,
					)
					if member.Symbol.IsFunctionLike() {
						result[existing].Symbol.ReturnType = relations.Join(
							result[existing].Symbol.ReturnType,
							member.Symbol.ReturnType,
						)
					}
					continue
				}
				seen[member.Symbol.ID] = len(result)
				result = append(result, member)
			}
		}
		return result
	}
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return nil
	}
	var result []ResolvedMember
	r.visitObjectMembers(
		receiver,
		name,
		kind,
		func(member ResolvedMember) bool {
			result = append(result, member)
			return true
		},
	)
	return result
}

func (r MemberResolver) visitObjectMembers(
	receiver types.Type,
	name string,
	kind semantic.SymbolKind,
	visit func(ResolvedMember) bool,
) bool {
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return true
	}
	seenNames := newMemberNameTracker(name, kind == 255)
	var visited memberClassTracker
	return r.Snapshot.VisitClassViews(receiver.Name(), func(classView semantic.SymbolView) bool {
		class := classView.Materialize()
		templates := bindClassTemplatesFromType(class, receiver)
		return r.visitClassMembers(
			class,
			name,
			kind,
			templates,
			&seenNames,
			&visited,
			visit,
		)
	})
}

func (r MemberResolver) visitClassMembers(
	class semantic.Symbol,
	name string,
	kind semantic.SymbolKind,
	templates map[string]types.Type,
	seenNames *memberNameTracker,
	visited *memberClassTracker,
	visit func(ResolvedMember) bool,
) bool {
	if !visited.add(class.ID) {
		return true
	}

	visitCandidate := func(candidateView semantic.SymbolView) bool {
		candidate := candidateView.Materialize()
		if kind == 255 && candidate.Kind == semantic.TypeAliasSymbol {
			return true
		}
		if kind != 255 && candidate.Kind != kind {
			return true
		}
		if !seenNames.add(candidate.Name, candidate.Kind) {
			return true
		}
		specialized := specializeMember(candidate, class, templates)
		memberType := specialized.Type
		if specialized.IsFunctionLike() {
			memberType = specialized.ReturnType
		}
		return visit(ResolvedMember{
			Symbol: specialized,
			Type:   memberType,
		})
	}
	if name == "" {
		for _, candidate := range r.Snapshot.MemberViewsOf(class.ID) {
			if !visitCandidate(candidate) {
				return false
			}
		}
	} else if !r.Snapshot.VisitMemberViews(class.ID, name, visitCandidate) {
		return false
	}
	if kind == semantic.MethodSymbol || kind == 255 {
		if !r.visitTraitAliasMembers(
			class,
			name,
			templates,
			seenNames,
			visited,
			visit,
		) {
			return false
		}
	}

	for _, traitName := range class.Traits {
		if !r.Snapshot.VisitClassViews(traitName, func(traitView semantic.SymbolView) bool {
			trait := traitView.Materialize()
			return r.visitClassMembers(
				trait,
				name,
				kind,
				templates,
				seenNames,
				visited,
				visit,
			)
		}) {
			return false
		}
	}
	if !r.visitRelatedClassMembers(
		class.Extends,
		class.ExtendsTypes,
		name,
		kind,
		templates,
		seenNames,
		visited,
		visit,
	) {
		return false
	}
	return r.visitRelatedClassMembers(
		class.Implements,
		class.ImplementsTypes,
		name,
		kind,
		templates,
		seenNames,
		visited,
		visit,
	)
}

func (r MemberResolver) visitRelatedClassMembers(
	parentNames []string,
	parentTypes []types.Type,
	name string,
	kind semantic.SymbolKind,
	templates map[string]types.Type,
	seenNames *memberNameTracker,
	visited *memberClassTracker,
	visit func(ResolvedMember) bool,
) bool {
	for _, parentName := range parentNames {
		if !r.Snapshot.VisitClassViews(parentName, func(parentView semantic.SymbolView) bool {
			parent := parentView.Materialize()
			var arguments []types.Type
			if len(parent.Templates) > 0 {
				for _, parentType := range parentTypes {
					if strings.EqualFold(parentType.Name(), parentName) {
						arguments = make(
							[]types.Type,
							parentType.ArgumentCount(),
						)
						for index := range arguments {
							arguments[index] = types.Substitute(
								parentType.Argument(index),
								templates,
							)
						}
						break
					}
				}
			}
			parentTemplates := bindClassTemplates(parent, arguments)
			return r.visitClassMembers(
				parent,
				name,
				kind,
				parentTemplates,
				seenNames,
				visited,
				visit,
			)
		}) {
			return false
		}
	}
	return true
}

func (r MemberResolver) visitTraitAliasMembers(
	class semantic.Symbol,
	name string,
	templates map[string]types.Type,
	seenNames *memberNameTracker,
	visited *memberClassTracker,
	visit func(ResolvedMember) bool,
) bool {
	for _, alias := range class.TraitAliases {
		if name != "" && !strings.EqualFold(alias.Alias, name) {
			continue
		}
		if seenNames.contains(alias.Alias, semantic.MethodSymbol) {
			continue
		}
		var targets []ResolvedMember
		targetNames := newMemberNameTracker(alias.Method, false)
		targetVisited := visited.clone()
		r.Snapshot.VisitClassViews(
			alias.Trait,
			func(traitView semantic.SymbolView) bool {
				return r.visitClassMembers(
					traitView.Materialize(),
					alias.Method,
					semantic.MethodSymbol,
					templates,
					&targetNames,
					&targetVisited,
					func(member ResolvedMember) bool {
						targets = append(targets, member)
						return true
					},
				)
			},
		)
		if len(targets) == 0 {
			continue
		}
		seenNames.add(alias.Alias, semantic.MethodSymbol)
		for _, target := range targets {
			symbol := target.Symbol
			symbol.Name = alias.Alias
			symbol.FullyQualified = class.FullyQualified + "::" + alias.Alias
			symbol.Container = class.ID
			if alias.HasVisibility {
				symbol.Visibility = alias.Visibility
			}
			if !visit(ResolvedMember{
				Symbol: symbol,
				Type:   target.Type,
			}) {
				return false
			}
		}
	}
	return true
}

func bindClassTemplates(class semantic.Symbol, arguments []types.Type) map[string]types.Type {
	if len(class.Templates) == 0 {
		return nil
	}
	result := make(map[string]types.Type, len(class.Templates))
	for index, template := range class.Templates {
		if index < len(arguments) {
			result[template.Name] = arguments[index]
		} else if !template.Default.IsUnknown() {
			result[template.Name] = template.Default
		}
	}
	return result
}

func bindClassTemplatesFromType(
	class semantic.Symbol,
	receiver types.Type,
) map[string]types.Type {
	if len(class.Templates) == 0 {
		return nil
	}
	result := make(map[string]types.Type, len(class.Templates))
	for index, template := range class.Templates {
		if index < receiver.ArgumentCount() {
			result[template.Name] = receiver.Argument(index)
		} else if !template.Default.IsUnknown() {
			result[template.Name] = template.Default
		}
	}
	return result
}

func specializeMember(
	member,
	class semantic.Symbol,
	templates map[string]types.Type,
) semantic.Symbol {
	if len(templates) == 0 && len(class.Templates) == 0 {
		// Materialized snapshot slices are immutable. When neither the receiver
		// nor its declaring class contributes templates, specialization is an
		// identity operation and the nested signature data can remain shared.
		return member
	}
	member.Parameters = append([]semantic.Parameter(nil), member.Parameters...)
	member.Templates = append(
		[]semantic.TemplateParameter(nil),
		member.Templates...,
	)
	member.Throws = append([]types.Type(nil), member.Throws...)
	member.Assertions = append([]semantic.TypeAssertion(nil), member.Assertions...)
	member = renameCollidingMethodTemplates(member, templates)
	effective := make(map[string]types.Type, len(templates))
	for name, value := range templates {
		effective[name] = value
	}
	for _, template := range member.Templates {
		delete(effective, template.Name)
	}
	member.Type = types.Substitute(member.Type, effective)
	member.NativeType = types.Substitute(member.NativeType, effective)
	member.DocType = types.Substitute(member.DocType, effective)
	member.ReturnType = types.Substitute(member.ReturnType, effective)
	for index := range member.Parameters {
		member.Parameters[index].Type = types.Substitute(
			member.Parameters[index].Type,
			effective,
		)
		member.Parameters[index].NativeType = types.Substitute(
			member.Parameters[index].NativeType,
			effective,
		)
		member.Parameters[index].DocType = types.Substitute(
			member.Parameters[index].DocType,
			effective,
		)
	}
	for index := range member.Throws {
		member.Throws[index] = types.Substitute(member.Throws[index], effective)
	}
	for index := range member.Assertions {
		member.Assertions[index].Type = types.Substitute(
			member.Assertions[index].Type,
			effective,
		)
	}
	methodTemplates := member.Templates
	member.Templates = mergeTemplates(class.Templates, methodTemplates)
	for index := range member.Templates {
		member.Templates[index].Bound = types.Substitute(
			member.Templates[index].Bound,
			effective,
		)
		member.Templates[index].Default = types.Substitute(
			member.Templates[index].Default,
			effective,
		)
		if !templateDeclaredByMethod(
			methodTemplates,
			member.Templates[index].Name,
		) {
			if bound, exists := templates[member.Templates[index].Name]; exists &&
				bound.Kind() == types.TemplateKind &&
				bound.Name() == member.Templates[index].Name {
				// A method called on Generic<T> inside Generic already has T
				// bound by its receiver. Retain the declaration for bound
				// validation, but do not replace that symbolic T with its
				// class-level default merely because no method argument
				// re-infers it.
				member.Templates[index].Default = types.Unknown()
			}
		}
	}
	return member
}

func renameCollidingMethodTemplates(
	member semantic.Symbol,
	classTemplates map[string]types.Type,
) semantic.Symbol {
	if len(member.Templates) == 0 || len(classTemplates) == 0 {
		return member
	}
	occupied := make(map[string]struct{}, len(classTemplates)+len(member.Templates))
	for name := range classTemplates {
		occupied[name] = struct{}{}
	}
	for _, template := range member.Templates {
		occupied[template.Name] = struct{}{}
	}
	renames := make(map[string]types.Type)
	for _, template := range member.Templates {
		collision := false
		for _, value := range classTemplates {
			if typeContainsTemplateNamed(value, template.Name) {
				collision = true
				break
			}
		}
		if !collision {
			continue
		}
		base := template.Name + "Method"
		name := base
		for suffix := 2; ; suffix++ {
			if _, exists := occupied[name]; !exists {
				break
			}
			name = base + strconv.Itoa(suffix)
		}
		occupied[name] = struct{}{}
		renames[template.Name] = types.Template(name)
	}
	if len(renames) == 0 {
		return member
	}
	member.Type = types.Substitute(member.Type, renames)
	member.NativeType = types.Substitute(member.NativeType, renames)
	member.DocType = types.Substitute(member.DocType, renames)
	member.ReturnType = types.Substitute(member.ReturnType, renames)
	for index := range member.Parameters {
		member.Parameters[index].Type = types.Substitute(
			member.Parameters[index].Type,
			renames,
		)
		member.Parameters[index].NativeType = types.Substitute(
			member.Parameters[index].NativeType,
			renames,
		)
		member.Parameters[index].DocType = types.Substitute(
			member.Parameters[index].DocType,
			renames,
		)
	}
	for index := range member.Throws {
		member.Throws[index] = types.Substitute(member.Throws[index], renames)
	}
	for index := range member.Assertions {
		member.Assertions[index].Type = types.Substitute(
			member.Assertions[index].Type,
			renames,
		)
	}
	for index := range member.Templates {
		member.Templates[index].Bound = types.Substitute(
			member.Templates[index].Bound,
			renames,
		)
		member.Templates[index].Default = types.Substitute(
			member.Templates[index].Default,
			renames,
		)
		if renamed, exists := renames[member.Templates[index].Name]; exists {
			member.Templates[index].Name = renamed.Name()
		}
	}
	return member
}

func typeContainsTemplateNamed(value types.Type, name string) bool {
	if value.Kind() == types.TemplateKind {
		return value.Name() == name
	}
	if value.Kind() == types.CallableKind {
		for index := 0; index < value.ParameterCount(); index++ {
			if typeContainsTemplateNamed(value.Parameter(index).Type, name) {
				return true
			}
		}
		return typeContainsTemplateNamed(value.Result(), name)
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if typeContainsTemplateNamed(value.Argument(index), name) {
			return true
		}
	}
	return false
}

func templateDeclaredByMethod(
	templates []semantic.TemplateParameter,
	name string,
) bool {
	for _, template := range templates {
		if template.Name == name {
			return true
		}
	}
	return false
}

func mergeTemplates(
	classTemplates,
	memberTemplates []semantic.TemplateParameter,
) []semantic.TemplateParameter {
	if len(classTemplates) == 0 {
		return memberTemplates
	}
	result := append([]semantic.TemplateParameter(nil), classTemplates...)
	seen := make(map[string]struct{}, len(result))
	for _, template := range result {
		seen[template.Name] = struct{}{}
	}
	for _, template := range memberTemplates {
		if _, exists := seen[template.Name]; exists {
			for index := range result {
				if result[index].Name == template.Name {
					result[index] = template
					break
				}
			}
			continue
		}
		result = append(result, template)
	}
	return result
}

func memberResolutionKey(
	name string,
	kind semantic.SymbolKind,
	mixedKinds bool,
) string {
	if kind == semantic.PropertySymbol {
		if mixedKinds {
			return "$" + name
		}
		return name
	}
	return strings.ToLower(name)
}
