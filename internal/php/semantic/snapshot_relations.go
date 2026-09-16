package semantic

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *Snapshot) IsSubtypeOf(candidate, target string) bool {
	target = s.classAliasCanonicalName(target)
	normalizedTarget := s.lowerName(target, false)
	if s.lowerName(candidate, false) == normalizedTarget {
		return true
	}
	visited := inlineStringSet{}
	return s.isSubtypeOf(candidate, normalizedTarget, &visited)
}

func (s *Snapshot) classAliasCanonicalName(name string) string {
	// Fast path: most names are no class alias, so avoid the cycle-guard map.
	aliasTarget := s.classAliasTarget(name)
	if aliasTarget == "" {
		return name
	}
	visited := make(map[string]struct{}, 4)
	visited[s.lowerName(name, false)] = struct{}{}
	for aliasTarget != "" {
		key := s.lowerName(aliasTarget, false)
		if _, exists := visited[key]; exists {
			return aliasTarget
		}
		visited[key] = struct{}{}
		next := s.classAliasTarget(aliasTarget)
		if next == "" {
			return aliasTarget
		}
		aliasTarget = next
	}
	return name
}

func (s *Snapshot) classAliasTarget(name string) string {
	aliasTarget := ""
	s.VisitClassViews(name, func(view SymbolView) bool {
		if !view.Flags().Has(ClassAliasFlag) {
			return true
		}
		_, extends, _ := view.HierarchyNames()
		if len(extends) == 1 {
			aliasTarget = extends[0]
			return false
		}
		return true
	})
	return aliasTarget
}

// inlineStringSet tracks a handful of visited names without allocating; deep
// hierarchy walks overflow into a map.
type inlineStringSet struct {
	values   [8]string
	length   uint8
	overflow map[string]struct{}
}

func (s *inlineStringSet) add(value string) bool {
	for index := uint8(0); index < s.length; index++ {
		if s.values[index] == value {
			return false
		}
	}
	if s.length < uint8(len(s.values)) {
		s.values[s.length] = value
		s.length++
		return true
	}
	if s.overflow == nil {
		s.overflow = make(map[string]struct{})
	}
	if _, exists := s.overflow[value]; exists {
		return false
	}
	s.overflow[value] = struct{}{}
	return true
}

func (s *Snapshot) isSubtypeOf(
	candidate,
	normalizedTarget string,
	visited *inlineStringSet,
) bool {
	normalized := s.lowerName(candidate, false)
	if !visited.add(normalized) {
		return false
	}
	found := false
	s.VisitClassViews(candidate, func(classView SymbolView) bool {
		// Hierarchy edges are resident summary data. Materializing the symbol
		// would load the persisted full document graph for every walked
		// ancestor, which dominates request-time subtype scans.
		_, extends, implements := classView.HierarchyNames()
		for _, parent := range extends {
			if s.lowerName(parent, false) == normalizedTarget ||
				s.isSubtypeOf(parent, normalizedTarget, visited) {
				found = true
				return false
			}
		}
		for _, parent := range implements {
			if s.lowerName(parent, false) == normalizedTarget ||
				s.isSubtypeOf(parent, normalizedTarget, visited) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func (s *Snapshot) Relations() types.Relations {
	return types.Relations{Hierarchy: s}
}

// CallableSignature returns the effective __invoke contract for an object.
// It follows traits and parents while preserving generic class arguments.
func (s *Snapshot) CallableSignature(
	candidate types.Type,
) (types.Type, bool) {
	return s.callableSignature(candidate, make(map[string]struct{}))
}

func (s *Snapshot) callableSignature(
	candidate types.Type,
	visited map[string]struct{},
) (types.Type, bool) {
	if candidate.Kind() != types.ObjectKind || candidate.Name() == "" {
		return types.Unknown(), false
	}
	if _, exists := visited[candidate.Key()]; exists {
		return types.Unknown(), false
	}
	visited[candidate.Key()] = struct{}{}

	result := types.Unknown()
	found := false
	s.VisitClassViews(candidate.Name(), func(classView SymbolView) bool {
		class := classView.Materialize()
		templates := classTemplateBindings(class, candidate)
		s.VisitMemberViews(
			class.ID,
			"__invoke",
			func(memberView SymbolView) bool {
				member := memberView.Materialize()
				if member.Kind != MethodSymbol {
					return true
				}
				parameters := make(
					[]types.CallableParameter,
					len(member.Parameters),
				)
				for index, parameter := range member.Parameters {
					parameters[index] = types.CallableParameter{
						Name:        parameter.Name,
						Type:        types.Substitute(parameter.Type, templates),
						Optional:    parameter.Optional,
						Variadic:    parameter.Flags.Has(VariadicFlag),
						ByReference: parameter.Flags.Has(ByReferenceFlag),
					}
				}
				returnType := types.Substitute(member.ReturnType, templates)
				result = types.Callable(parameters, returnType)
				found = true
				return false
			},
		)
		if found {
			return false
		}
		for _, trait := range class.Traits() {
			if signature, ok := s.callableSignature(
				types.Named(trait),
				visited,
			); ok {
				result, found = signature, true
				return false
			}
		}
		for _, parent := range classParentTypes(class) {
			parent = types.Substitute(parent, templates)
			if signature, ok := s.callableSignature(parent, visited); ok {
				result, found = signature, true
				return false
			}
		}
		return true
	})
	return result, found
}

// ResolveTypeAlias expands a nominal PHPDoc alias through the declaring
// class's synthetic alias member.
func (s *Snapshot) ResolveTypeAlias(value types.Type) (types.Type, bool) {
	className, alias, ok := types.PHPDocAliasParts(value)
	if !ok {
		return types.Unknown(), false
	}
	resolved := types.Unknown()
	found := false
	s.VisitClassViews(className, func(classView SymbolView) bool {
		s.VisitMemberViews(
			classView.ID(),
			alias,
			func(aliasView SymbolView) bool {
				if aliasView.Kind() != TypeAliasSymbol {
					return true
				}
				resolved = aliasView.Materialize().Type
				found = !resolved.IsUnknown()
				return !found
			},
		)
		return !found
	})
	return resolved, found
}

func (s *Snapshot) TemplateVariance(name string, index int) types.Variance {
	result := types.Invariant
	s.VisitClassViews(name, func(classView SymbolView) bool {
		class := classView.Materialize()
		templates := class.Templates()
		if index < 0 || index >= len(templates) {
			return true
		}
		template := templates[index]
		switch {
		case template.Covariant:
			result = types.Covariant
		case template.Contravariant:
			result = types.Contravariant
		}
		return false
	})
	return result
}

func (s *Snapshot) AsSupertype(
	candidate types.Type,
	target string,
) (types.Type, bool) {
	if candidate.Kind() != types.ObjectKind || candidate.Name() == "" {
		return types.Unknown(), false
	}
	return s.asSupertype(candidate, target, make(map[string]struct{}))
}

func (s *Snapshot) asSupertype(
	candidate types.Type,
	target string,
	visited map[string]struct{},
) (types.Type, bool) {
	normalizedTarget := s.lowerName(target, false)
	if s.lowerName(candidate.Name(), false) == normalizedTarget {
		return candidate, true
	}
	if _, exists := visited[candidate.Key()]; exists {
		return types.Unknown(), false
	}
	visited[candidate.Key()] = struct{}{}
	result := types.Unknown()
	found := false
	s.VisitClassViews(candidate.Name(), func(classView SymbolView) bool {
		extendsTypes, implementsTypes := classView.HierarchyTypes()
		_, extends, implements := classView.HierarchyNames()
		parents := classParentTypesFromEdges(
			extendsTypes,
			implementsTypes,
			extends,
			implements,
		)
		// Template bindings live in the lazy signature side. Without generic
		// arguments on the candidate or any parent edge there is nothing a
		// template could substitute into, so the resident hierarchy is enough
		// and the persisted full document graph stays unloaded.
		needsTemplates := candidate.ArgumentCount() > 0
		for _, parent := range parents {
			if needsTemplates {
				break
			}
			needsTemplates = parent.ArgumentCount() > 0
		}
		if !needsTemplates {
			for _, parent := range parents {
				if s.lowerName(parent.Name(), false) == normalizedTarget {
					result = parent
					found = true
					return false
				}
				if projected, ok := s.asSupertype(parent, target, visited); ok {
					result = projected
					found = true
					return false
				}
			}
			return true
		}
		class := classView.Materialize()
		templates := classTemplateBindings(class, candidate)
		for _, parent := range classParentTypes(class) {
			parent = types.Substitute(parent, templates)
			parent = inheritExplicitArguments(class, candidate, parent)
			if s.lowerName(parent.Name(), false) == normalizedTarget {
				result = parent
				found = true
				return false
			}
			if projected, ok := s.asSupertype(parent, target, visited); ok {
				result = projected
				found = true
				return false
			}
		}
		return true
	})
	return result, found
}

func classTemplateBindings(
	class Symbol,
	candidate types.Type,
) map[string]types.Type {
	classTemplates := class.Templates()
	if len(classTemplates) == 0 {
		return nil
	}
	templates := make(map[string]types.Type, len(classTemplates))
	for index, template := range classTemplates {
		switch {
		case index < candidate.ArgumentCount():
			templates[template.Name] = candidate.Argument(index)
		case !template.Default.IsUnknown():
			templates[template.Name] = template.Default
		}
	}
	return templates
}

// inheritExplicitArguments preserves a PHPDoc specialization across
// non-template bridge classes. Projects commonly annotate concrete collection
// subclasses as CollectionSubclass<Element> even when the subclass itself does
// not redeclare the template inherited from a generic ancestor.
func inheritExplicitArguments(
	class Symbol,
	candidate types.Type,
	parent types.Type,
) types.Type {
	if len(class.Templates()) != 0 || candidate.ArgumentCount() == 0 ||
		parent.Kind() != types.ObjectKind || parent.Name() == "" {
		return parent
	}

	arguments := parent.Arguments()
	if len(arguments) < candidate.ArgumentCount() {
		arguments = append(
			arguments,
			make([]types.Type, candidate.ArgumentCount()-len(arguments))...,
		)
	}
	for index, argument := range candidate.Arguments() {
		arguments[index] = argument
	}
	return types.Named(parent.Name(), arguments...)
}

func classParentTypes(class Symbol) []types.Type {
	return classParentTypesFromEdges(
		class.ExtendsTypes(),
		class.ImplementsTypes(),
		class.Extends(),
		class.Implements(),
	)
}

func classParentTypesFromEdges(
	extendsTypes []types.Type,
	implementsTypes []types.Type,
	extends []string,
	implements []string,
) []types.Type {
	declared := append(
		append([]types.Type(nil), extendsTypes...),
		implementsTypes...,
	)
	names := append(
		append([]string(nil), extends...),
		implements...,
	)
	result := append([]types.Type(nil), declared...)
	for _, name := range names {
		found := false
		for _, value := range declared {
			if strings.EqualFold(value.Name(), name) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, types.Named(name))
		}
	}
	return result
}
