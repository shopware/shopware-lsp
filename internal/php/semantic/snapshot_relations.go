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
	return s.isSubtypeOf(candidate, normalizedTarget, make(map[string]struct{}))
}

func (s *Snapshot) classAliasCanonicalName(name string) string {
	visited := make(map[string]struct{})
	for name != "" {
		key := s.lowerName(name, false)
		if _, exists := visited[key]; exists {
			return name
		}
		visited[key] = struct{}{}
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
		if aliasTarget == "" {
			return name
		}
		name = aliasTarget
	}
	return name
}

func (s *Snapshot) isSubtypeOf(
	candidate,
	normalizedTarget string,
	visited map[string]struct{},
) bool {
	normalized := s.lowerName(candidate, false)
	if _, exists := visited[normalized]; exists {
		return false
	}
	visited[normalized] = struct{}{}
	found := false
	s.VisitClassViews(candidate, func(classView SymbolView) bool {
		class := classView.Materialize()
		for _, parent := range class.Extends {
			if s.lowerName(parent, false) == normalizedTarget ||
				s.isSubtypeOf(parent, normalizedTarget, visited) {
				found = true
				return false
			}
		}
		for _, parent := range class.Implements {
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
		for _, trait := range class.Traits {
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
		if index < 0 || index >= len(class.Templates) {
			return true
		}
		template := class.Templates[index]
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
	if len(class.Templates) == 0 {
		return nil
	}
	templates := make(map[string]types.Type, len(class.Templates))
	for index, template := range class.Templates {
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
	if len(class.Templates) != 0 || candidate.ArgumentCount() == 0 ||
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
	declared := append(
		append([]types.Type(nil), class.ExtendsTypes...),
		class.ImplementsTypes...,
	)
	names := append(append([]string(nil), class.Extends...), class.Implements...)
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
