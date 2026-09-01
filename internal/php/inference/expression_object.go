package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) inferObjectCreation(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	nameNode := phpquery.DirectChild(node, phpsyntax.PhpName)
	if nameNode == nil {
		if anonymous := phpquery.DirectChild(
			node,
			phpsyntax.PhpAnonymousClass,
		); anonymous != nil {
			return types.Named(semantic.AnonymousClassName(
				s.document.Path,
				anonymous.RangeTrimmedTrivia().Start,
			))
		}
		return types.Object()
	}
	context := s.nameContextAt(node.Range().Start)
	name := phpquery.NameValue(nameNode)
	if strings.HasPrefix(name, "$") {
		value, found := env.get(name)
		if !found {
			return types.Object()
		}
		return dynamicObjectType(value)
	}
	className := context.ResolveClass(name)
	preserveCurrentTemplates := false
	switch strings.ToLower(strings.TrimPrefix(name, "\\")) {
	case "self", "static":
		if s.currentClass.Kind() == types.ObjectKind {
			className = s.currentClass.Name()
			preserveCurrentTemplates = true
		}
	case "parent":
		if s.currentClass.Kind() == types.ObjectKind {
			s.analyzer.Snapshot.VisitClassViews(
				s.currentClass.Name(),
				func(classView semantic.SymbolView) bool {
					class := classView.Materialize()
					if len(class.Extends) > 0 {
						className = class.Extends[0]
					}
					return false
				},
			)
		}
	}
	result := s.namedType(className)
	arguments, uncertainUnpack := s.inferArguments(node, env)

	var inferred []types.Type
	hasConstructor := false
	permissiveConstructor := false
	foundClass := false
	s.analyzer.Snapshot.VisitClassViews(
		className,
		func(classView semantic.SymbolView) bool {
			foundClass = true
			class := classView.Materialize()
			currentTemplates := mapCurrentClassTemplates(
				class,
				s.currentClass,
				preserveCurrentTemplates,
			)
			constructorReceiver := s.namedType(class.FullyQualified)
			if len(class.Templates) > 0 {
				templateArguments := make(
					[]types.Type,
					len(class.Templates),
				)
				for index, template := range class.Templates {
					templateArguments[index] = types.Template(template.Name)
				}
				constructorReceiver = types.Named(
					class.FullyQualified,
					templateArguments...,
				)
			}
			foundConstructor := false
			(resolver.MemberResolver{
				Snapshot: s.analyzer.Snapshot,
			}).VisitMethods(
				constructorReceiver,
				"__construct",
				func(constructor resolver.ResolvedMember) bool {
					foundConstructor = true
					hasConstructor = true
					resolved := resolver.ResolveSignature(
						s.relations,
						constructor.Symbol,
						arguments,
					)
					if resolved.Compatible {
						s.applyByReferenceArguments(
							node,
							constructor.Symbol,
							arguments,
							env,
						)
						inferred = append(
							inferred,
							s.genericObjectType(
								class,
								mergeMissingTemplates(
									resolved.Templates,
									currentTemplates,
								),
							),
						)
					} else if constructor.Symbol.Flags.Has(
						semantic.GeneratedStubFlag,
					) {
						permissiveConstructor = true
					}
					return true
				},
			)
			if !foundConstructor {
				if len(arguments) == 0 {
					inferred = append(
						inferred,
						s.genericObjectType(class, currentTemplates),
					)
				}
				return true
			}
			return true
		},
	)
	if !foundClass {
		return result
	}
	if len(inferred) > 0 {
		return joinTypes(s.relations, inferred)
	}
	if permissiveConstructor {
		return result
	}
	if !uncertainUnpack && (hasConstructor || len(arguments) > 0) {
		s.report(
			node,
			"php.arguments",
			"No matching constructor for "+className,
		)
	}
	return result
}

func mapCurrentClassTemplates(
	class semantic.Symbol,
	current types.Type,
	enabled bool,
) map[string]types.Type {
	if !enabled ||
		current.Kind() != types.ObjectKind ||
		!strings.EqualFold(class.FullyQualified, current.Name()) {
		return nil
	}
	count := min(len(class.Templates), current.ArgumentCount())
	if count == 0 {
		return nil
	}
	result := make(map[string]types.Type, count)
	for index := 0; index < count; index++ {
		result[class.Templates[index].Name] = current.Argument(index)
	}
	return result
}

func mergeMissingTemplates(
	inferred,
	fallback map[string]types.Type,
) map[string]types.Type {
	if len(fallback) == 0 {
		return inferred
	}
	result := make(map[string]types.Type, len(inferred)+len(fallback))
	for name, value := range fallback {
		result[name] = value
	}
	for name, value := range inferred {
		result[name] = value
	}
	return result
}

func dynamicObjectType(value types.Type) types.Type {
	switch value.Kind() {
	case types.ClassStringKind:
		if value.ArgumentCount() == 1 {
			return value.Argument(0)
		}
	case types.ObjectKind:
		return value
	case types.UnionKind:
		var objects []types.Type
		for index := 0; index < value.ArgumentCount(); index++ {
			alternative := value.Argument(index)
			object := dynamicObjectType(alternative)
			if object.Kind() == types.ObjectKind {
				objects = append(objects, object)
			}
		}
		if len(objects) > 0 {
			return types.Union(objects...)
		}
	}
	// A plain string can name any runtime class. Keep it uncertain instead of
	// turning it into the concrete broad `object` contract, which would make
	// downstream argument diagnostics claim an incompatibility we cannot
	// prove. class-string<T> above still retains T precisely.
	return types.Unknown()
}

func (s *functionState) genericObjectType(
	class semantic.Symbol,
	inferred map[string]types.Type,
) types.Type {
	if len(class.Templates) == 0 {
		return s.namedType(class.FullyQualified)
	}
	arguments := make([]types.Type, 0, len(class.Templates))
	for _, template := range class.Templates {
		value, exists := inferred[template.Name]
		if !exists {
			value = template.Default
		}
		if value.IsUnknown() {
			return s.namedType(class.FullyQualified)
		}
		arguments = append(arguments, value)
	}
	return types.Named(class.FullyQualified, arguments...)
}

func (s *functionState) inferMember(
	node *phpsyntax.Node,
	env environment,
	static bool,
) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount < 2 {
		return types.Unknown()
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(nodeCount - 1))
	if static && strings.EqualFold(name, "class") {
		return types.ClassString(receiver)
	}
	memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	var members []types.Type
	if static {
		if strings.HasPrefix(name, "$") {
			members = memberResolver.PropertyTypes(receiver, name)
		} else {
			members = memberResolver.ConstantTypes(receiver, name)
		}
	} else {
		members = memberResolver.PropertyTypes(receiver, name)
		if refined, found := s.readonlyPropertyType(receiver, name); found {
			return refined
		}
	}
	return s.joinMembers(members, receiver)
}

func (s *functionState) recordReadonlyPropertyAssignment(
	target *phpsyntax.Node,
	value types.Type,
) {
	if target == nil || target.Kind() != phpsyntax.PhpMemberAccess ||
		!strings.EqualFold(s.symbol.Name, "__construct") || value.IsUnknown() {
		return
	}
	nodes := directNodes(target)
	if nodes.Len() < 2 || nodes.At(0).Kind() != phpsyntax.PhpVariable ||
		phpquery.VariableKey(nodes.At(0)) != "$this" {
		return
	}
	name := phpquery.NameValue(nodes.At(nodes.Len() - 1))
	memberResolver := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	for _, id := range memberResolver.PropertyIDs(s.currentClass, name) {
		property, found := s.analyzer.Snapshot.Symbol(id)
		if !found || property.Kind != semantic.PropertySymbol ||
			property.Visibility != semantic.Private ||
			!property.Flags.Has(semantic.ReadonlyFlag) ||
			!s.relations.IsAssignableTo(value, property.Type) {
			continue
		}
		if existing, exists := s.readonlyPropertyTypes[id]; exists {
			s.readonlyPropertyTypes[id] = s.relations.Join(existing, value)
		} else {
			s.readonlyPropertyTypes[id] = value
		}
	}
}

func (s *functionState) readonlyPropertyType(
	receiver types.Type,
	name string,
) (types.Type, bool) {
	ids := (resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).PropertyIDs(receiver, name)
	if len(ids) == 0 {
		return types.Type{}, false
	}
	values := make([]types.Type, 0, len(ids))
	for _, id := range ids {
		value, found := s.readonlyPropertyTypes[id]
		if !found {
			return types.Type{}, false
		}
		values = append(values, value)
	}
	return joinTypes(s.relations, values), true
}
