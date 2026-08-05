package inference

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *analyzerState) validateDeclarations() {
	for _, class := range s.document.Symbols {
		if !class.IsClassLike() {
			continue
		}
		s.validateFinalParents(class)
		s.validateOverrides(class)
		s.validateRequiredMethods(class)
	}
}

func (s *analyzerState) validateFinalParents(class semantic.Symbol) {
	if class.Kind != semantic.ClassSymbol {
		return
	}
	for _, parentName := range class.Extends {
		s.analyzer.Snapshot.VisitClassViews(
			parentName,
			func(parentView semantic.SymbolView) bool {
				parent := parentView.Materialize()
				if !parent.Flags.Has(semantic.FinalFlag) {
					return true
				}
				s.reportRange(
					class.SelectionRange,
					"php.inheritance",
					"Class "+class.FullyQualified+" cannot extend final class "+
						parent.FullyQualified,
				)
				return true
			},
		)
	}
}

func (s *analyzerState) validateOverrides(class semantic.Symbol) {
	if class.Kind == semantic.InterfaceSymbol {
		return
	}
	parents := s.parentReceiverTypes(class)
	for _, memberView := range s.analyzer.Snapshot.MemberViewsOf(class.ID) {
		member := memberView.Materialize()
		if member.Kind != semantic.MethodSymbol &&
			member.Kind != semantic.PropertySymbol {
			continue
		}
		if member.Flags.Has(semantic.SyntheticFlag) {
			continue
		}
		var inherited []semantic.Symbol
		seen := make(map[semantic.SymbolID]struct{})
		for _, parent := range parents {
			var candidates []resolver.ResolvedMember
			memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
			if member.Kind == semantic.MethodSymbol {
				candidates = memberResolver.Methods(parent, member.Name)
			} else {
				candidates = memberResolver.Properties(parent, member.Name)
			}
			for _, candidate := range candidates {
				if candidate.Symbol.Visibility == semantic.Private {
					continue
				}
				if candidate.Symbol.Flags.Has(semantic.SyntheticFlag) {
					continue
				}
				if _, exists := seen[candidate.Symbol.ID]; exists {
					continue
				}
				seen[candidate.Symbol.ID] = struct{}{}
				inherited = append(inherited, candidate.Symbol)
			}
		}

		if member.Kind == semantic.MethodSymbol && hasOverrideAttribute(member) &&
			len(inherited) == 0 {
			s.reportRange(
				member.SelectionRange,
				"php.override",
				"Method "+class.FullyQualified+"::"+member.Name+
					" is marked #[Override] but does not override a parent method",
			)
		}
		for _, parent := range inherited {
			s.validateOverride(class, member, parent)
		}
	}
}

func (s *analyzerState) validateOverride(
	class,
	member,
	parent semantic.Symbol,
) {
	name := class.FullyQualified + "::" + member.Name
	if parent.Flags.Has(semantic.FinalFlag) {
		s.reportRange(
			member.SelectionRange,
			"php.override",
			name+" cannot override final member "+parent.FullyQualified,
		)
		return
	}
	if member.Visibility > parent.Visibility {
		s.reportRange(
			member.SelectionRange,
			"php.override",
			name+" cannot reduce the visibility of "+parent.FullyQualified,
		)
	}
	if member.Flags.Has(semantic.StaticFlag) !=
		parent.Flags.Has(semantic.StaticFlag) {
		s.reportRange(
			member.SelectionRange,
			"php.override",
			name+" must preserve static declaration of "+parent.FullyQualified,
		)
	}
	if member.Kind == semantic.PropertySymbol {
		s.validatePropertyOverride(class, member, parent)
		return
	}
	if strings.EqualFold(member.Name, "__construct") {
		return
	}
	if !s.compatibleMethod(class, member, parent) {
		s.reportRange(
			member.SelectionRange,
			"php.override",
			name+" has an incompatible signature with "+parent.FullyQualified,
		)
	}
}

func (s *analyzerState) validatePropertyOverride(
	class,
	property,
	parent semantic.Symbol,
) {
	childType := s.declarationType(property.NativeType, class)
	parentClass, ok := s.analyzer.Snapshot.Symbol(parent.Container)
	if !ok {
		return
	}
	parentType := s.declarationType(parent.NativeType, parentClass)
	if childType.Kind() == types.ErrorKind || parentType.Kind() == types.ErrorKind {
		return
	}
	if childType.IsUnknown() {
		childType = types.Mixed()
	}
	if parentType.IsUnknown() {
		parentType = types.Mixed()
	}
	if !childType.Equal(parentType) {
		s.reportRange(
			property.SelectionRange,
			"php.override",
			class.FullyQualified+"::$"+property.Name+
				" must keep the invariant type "+parentType.String(),
		)
	}
	if property.Flags.Has(semantic.ReadonlyFlag) !=
		parent.Flags.Has(semantic.ReadonlyFlag) {
		s.reportRange(
			property.SelectionRange,
			"php.override",
			class.FullyQualified+"::$"+property.Name+
				" must preserve readonly declaration of "+parent.FullyQualified,
		)
	}
}

func (s *analyzerState) compatibleMethod(
	class,
	method,
	parent semantic.Symbol,
) bool {
	parentClass, ok := s.analyzer.Snapshot.Symbol(parent.Container)
	if !ok {
		return true
	}
	childParameters := declaredParameters(method.Parameters)
	parentParameters := declaredParameters(parent.Parameters)
	if len(childParameters) < len(parentParameters) {
		return false
	}
	parentTypeContext := parentClass
	if parentClass.Kind == semantic.TraitSymbol {
		parentTypeContext = class
	}
	for index, parentParameter := range parentParameters {
		childParameter := childParameters[index]
		if parentParameter.Flags.Has(semantic.ByReferenceFlag) !=
			childParameter.Flags.Has(semantic.ByReferenceFlag) {
			return false
		}
		if parentParameter.Flags.Has(semantic.VariadicFlag) &&
			!childParameter.Flags.Has(semantic.VariadicFlag) {
			return false
		}
		if parentParameter.Optional && !childParameter.Optional &&
			!childParameter.Flags.Has(semantic.VariadicFlag) {
			return false
		}
		parentType := s.declarationType(
			parentParameter.NativeType,
			parentTypeContext,
		)
		childType := s.declarationType(childParameter.NativeType, class)
		if parentType.IsUnknown() {
			parentType = types.Mixed()
		}
		if childType.IsUnknown() {
			childType = types.Mixed()
		}
		if parentType.Kind() != types.ErrorKind &&
			childType.Kind() != types.ErrorKind &&
			!s.relations.IsSubtype(parentType, childType) {
			return false
		}
	}
	for _, added := range childParameters[len(parentParameters):] {
		if !added.Optional && !added.Flags.Has(semantic.VariadicFlag) {
			return false
		}
	}

	if hasAttributeNamed(method, "ReturnTypeWillChange") {
		return true
	}
	parentReturn := s.declarationType(parent.NativeType, parentTypeContext)
	if parentReturn.IsUnknown() {
		return true
	}
	childReturn := s.declarationType(method.NativeType, class)
	if childReturn.IsUnknown() {
		return false
	}
	if parentReturn.Kind() == types.ErrorKind ||
		childReturn.Kind() == types.ErrorKind {
		return true
	}
	return s.relations.IsSubtype(childReturn, parentReturn)
}

func declaredParameters(parameters []semantic.Parameter) []semantic.Parameter {
	var result []semantic.Parameter
	for _, parameter := range parameters {
		if parameter.Flags.Has(semantic.SyntheticFlag) {
			continue
		}
		result = append(result, parameter)
	}
	return result
}

func (s *analyzerState) validateRequiredMethods(class semantic.Symbol) {
	if class.Kind != semantic.ClassSymbol && class.Kind != semantic.EnumSymbol {
		return
	}
	if class.Flags.Has(semantic.AbstractFlag) {
		return
	}
	receiver := classReceiverType(class)
	(resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).VisitAllMethodIDs(
		receiver,
		func(methodID semantic.SymbolID) bool {
			method, ok := s.analyzer.Snapshot.SymbolView(methodID)
			if !ok || !s.methodIsAbstract(method) {
				return true
			}
			if s.hasConcreteMethod(receiver, method) {
				return true
			}
			s.reportRange(
				class.SelectionRange,
				"php.abstract",
				fmt.Sprintf(
					"%s must implement abstract method %s",
					class.FullyQualified,
					method.FullyQualified(),
				),
			)
			return true
		},
	)
}

func (s *analyzerState) hasConcreteMethod(
	receiver types.Type,
	required semantic.SymbolView,
) bool {
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return false
	}
	requiredMethod := required.Materialize()
	visited := make(map[semantic.SymbolID]struct{})
	var visitClass func(semantic.SymbolView) bool
	visitClass = func(class semantic.SymbolView) bool {
		if _, exists := visited[class.ID()]; exists {
			return false
		}
		visited[class.ID()] = struct{}{}

		found := false
		s.analyzer.Snapshot.VisitMemberViews(
			class.ID(),
			required.Name(),
			func(candidateView semantic.SymbolView) bool {
				candidate := candidateView.Materialize()
				if candidate.Kind != semantic.MethodSymbol ||
					s.methodIsAbstract(candidateView) ||
					candidate.Visibility > requiredMethod.Visibility ||
					candidate.Flags.Has(semantic.StaticFlag) !=
						requiredMethod.Flags.Has(semantic.StaticFlag) {
					return true
				}
				found = true
				return false
			},
		)
		if found {
			return true
		}

		traits, extends, _ := class.HierarchyNames()
		for _, relatedNames := range [][]string{traits, extends} {
			for _, relatedName := range relatedNames {
				if !s.analyzer.Snapshot.VisitClassViews(
					relatedName,
					func(related semantic.SymbolView) bool {
						if visitClass(related) {
							found = true
							return false
						}
						return true
					},
				) {
					return true
				}
			}
		}
		return found
	}

	found := false
	s.analyzer.Snapshot.VisitClassViews(
		receiver.Name(),
		func(class semantic.SymbolView) bool {
			if visitClass(class) {
				found = true
				return false
			}
			return true
		},
	)
	return found
}

func (s *analyzerState) methodIsAbstract(method semantic.SymbolView) bool {
	if method.Flags().Has(semantic.AbstractFlag) {
		return true
	}
	container, ok := s.analyzer.Snapshot.SymbolView(method.Container())
	return ok && container.Kind() == semantic.InterfaceSymbol
}

func (s *analyzerState) parentReceiverTypes(class semantic.Symbol) []types.Type {
	names := append(append([]string(nil), class.Extends...), class.Implements...)
	declared := append(
		append([]types.Type(nil), class.ExtendsTypes...),
		class.ImplementsTypes...,
	)
	result := make([]types.Type, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		value := types.Named(name)
		for _, candidate := range declared {
			if strings.EqualFold(candidate.Name(), name) {
				value = candidate
				break
			}
		}
		if _, exists := seen[value.Key()]; exists {
			continue
		}
		seen[value.Key()] = struct{}{}
		result = append(result, value)
	}
	return result
}

func classReceiverType(class semantic.Symbol) types.Type {
	arguments := make([]types.Type, 0, len(class.Templates))
	for _, template := range class.Templates {
		arguments = append(arguments, types.Template(template.Name))
	}
	return types.Named(class.FullyQualified, arguments...)
}

func (s *analyzerState) declarationType(
	value types.Type,
	class semantic.Symbol,
) types.Type {
	switch value.Kind() {
	case types.SelfKind, types.StaticKind:
		return types.Named(class.FullyQualified, value.Arguments()...)
	case types.ParentKind:
		if len(class.Extends) > 0 {
			return types.Named(class.Extends[0], value.Arguments()...)
		}
		return types.Unknown()
	case types.UnionKind:
		members := value.Arguments()
		for index := range members {
			members[index] = s.declarationType(members[index], class)
		}
		return types.Union(members...)
	case types.IntersectionKind:
		members := value.Arguments()
		for index := range members {
			members[index] = s.declarationType(members[index], class)
		}
		return types.Intersection(members...)
	default:
		return value
	}
}

func hasOverrideAttribute(symbol semantic.Symbol) bool {
	return hasAttributeNamed(symbol, "Override")
}

func hasAttributeNamed(symbol semantic.Symbol, expected string) bool {
	expected = strings.ToLower(strings.TrimPrefix(expected, "\\"))
	for _, attribute := range symbol.Attributes {
		name := strings.ToLower(strings.TrimPrefix(attribute.Name, "\\"))
		if name == expected || strings.HasSuffix(name, "\\"+expected) {
			return true
		}
	}
	return false
}
