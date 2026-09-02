package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) inferCall(
	node *phpsyntax.Node,
	env environment,
	static bool,
	function bool,
) types.Type {
	nodes := directNodes(node)
	if nodes.Len() == 0 {
		return types.Unknown()
	}
	if isFirstClassCallable(node) {
		return s.inferFirstClassCallable(node, env, static, function)
	}
	arguments, uncertainUnpack := s.inferArguments(node, env)
	if function {
		callee := nodes.At(0)
		if callee.Kind() != phpsyntax.PhpName {
			callable := s.infer(callee, env)
			if result, found := callableResult(callable); found {
				return result
			}
			return types.Unknown()
		}
		name := phpquery.NameValue(callee)
		if extension, ok := s.extensionType(node, name, types.Unknown(), arguments, false); ok {
			return extension
		}
		context := s.nameContextAt(node.Range().Start)
		var results []types.Type
		var generatedFallbacks []types.Type
		candidateCount := 0
		context.VisitFunctionNames(name, func(candidateName string) bool {
			s.analyzer.Snapshot.VisitFunctionViews(
				candidateName,
				func(candidate semantic.SymbolView) bool {
					candidateCount++
					candidateSymbol := candidate.Materialize()
					resolved := resolver.ResolveSignature(
						s.relations,
						candidateSymbol,
						arguments,
					)
					if resolved.Compatible {
						s.applyByReferenceArguments(
							node,
							candidateSymbol,
							arguments,
							env,
						)
						s.recordReturnDependency(candidateSymbol)
						results = append(results, resolved.ReturnType)
					} else if candidateSymbol.Flags.Has(
						semantic.GeneratedStubFlag,
					) {
						generatedFallbacks = append(
							generatedFallbacks,
							resolved.ReturnType,
						)
					}
					return true
				},
			)
			return len(results) == 0
		})
		if len(results) == 0 && len(generatedFallbacks) > 0 {
			return joinTypes(s.relations, generatedFallbacks)
		}
		if candidateCount > 0 && len(results) == 0 && !uncertainUnpack {
			s.report(
				node,
				"php.arguments",
				"No matching signature for "+name,
			)
		}
		return joinTypes(s.relations, results)
	}

	if nodes.Len() < 2 {
		return types.Unknown()
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(1))
	if static && nodes.At(0).Kind() == phpsyntax.PhpObjectCreation &&
		strings.HasPrefix(name, "$") {
		propertyTypes := resolver.MemberResolver{
			Snapshot: s.analyzer.Snapshot,
		}.PropertyTypes(receiver, name)
		return dynamicObjectType(s.joinMembers(propertyTypes, receiver))
	}
	lateStaticReceiver := receiver
	if static &&
		nodes.At(0).Kind() == phpsyntax.PhpName &&
		s.currentClass.Kind() == types.ObjectKind {
		switch strings.ToLower(phpquery.NameValue(nodes.At(0))) {
		case "self", "static", "parent":
			lateStaticReceiver = s.currentClass
		}
	}
	if extension, ok := s.extensionType(node, name, receiver, arguments, static); ok {
		return extension
	}
	hasMembers := false
	var results []types.Type
	var generatedFallbacks []types.Type
	(resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}).VisitMethods(
		receiver,
		name,
		func(member resolver.ResolvedMember) bool {
			hasMembers = true
			selfType := s.memberSelfType(member.Symbol, receiver)
			symbol := resolveMemberSpecialTypes(
				member.Symbol,
				lateStaticReceiver,
				selfType,
			)
			resolved := resolver.ResolveSignature(
				s.relations,
				symbol,
				arguments,
			)
			if resolved.Compatible {
				s.applyByReferenceArguments(
					node,
					symbol,
					arguments,
					env,
				)
				s.recordReturnDependency(member.Symbol)
				results = append(
					results,
					resolved.ReturnType,
				)
			} else if member.Symbol.Flags.Has(semantic.GeneratedStubFlag) {
				generatedFallbacks = append(
					generatedFallbacks,
					resolved.ReturnType,
				)
			}
			return true
		},
	)
	if len(results) == 0 && len(generatedFallbacks) > 0 {
		results = generatedFallbacks
	}
	if hasMembers && len(results) == 0 && !uncertainUnpack {
		s.report(
			node,
			"php.arguments",
			"No matching signature for "+name,
		)
	}
	result := joinTypes(s.relations, results)
	if hasDirectToken(node, phpsyntax.TkNullsafeObjectOperator) {
		result = types.Nullable(result)
	}
	return result
}

func callableResult(value types.Type) (types.Type, bool) {
	if value.Kind() == types.CallableKind {
		return value.Result(), true
	}
	if value.Kind() != types.UnionKind {
		return types.Unknown(), false
	}
	var results []types.Type
	for index := 0; index < value.ArgumentCount(); index++ {
		alternative := value.Argument(index)
		if alternative.Kind() == types.CallableKind {
			results = append(results, alternative.Result())
		}
	}
	if len(results) == 0 {
		return types.Unknown(), false
	}
	return types.Union(results...), true
}

func isFirstClassCallable(node *phpsyntax.Node) bool {
	arguments := phpquery.IterateArguments(node)
	if !arguments.Next() {
		return false
	}
	argument := arguments.Node()
	return !arguments.Next() &&
		hasDirectToken(argument, phpsyntax.TkEllipsis) &&
		lastDirectNode(argument) == nil
}

func (s *functionState) inferFirstClassCallable(
	node *phpsyntax.Node,
	env environment,
	static bool,
	function bool,
) types.Type {
	nodes := directNodes(node)
	if nodes.Len() == 0 {
		return types.Callable(nil, types.Mixed())
	}
	var callables []types.Type
	if function {
		callee := nodes.At(0)
		if callee.Kind() != phpsyntax.PhpName {
			value := s.infer(callee, env)
			if value.Kind() == types.CallableKind {
				return value
			}
			return types.Callable(nil, types.Mixed())
		}
		name := phpquery.NameValue(callee)
		context := s.nameContextAt(node.Range().Start)
		context.VisitFunctionNames(name, func(candidateName string) bool {
			s.analyzer.Snapshot.VisitFunctionViews(
				candidateName,
				func(candidate semantic.SymbolView) bool {
					callables = append(
						callables,
						callableFromSymbol(candidate.Materialize()),
					)
					return true
				},
			)
			return len(callables) == 0
		})
		return joinTypes(s.relations, callables)
	}
	if nodes.Len() < 2 {
		return types.Callable(nil, types.Mixed())
	}
	receiver := s.inferReceiver(nodes.At(0), env, static)
	name := phpquery.NameValue(nodes.At(1))
	(resolver.MemberResolver{
		Snapshot: s.analyzer.Snapshot,
	}).VisitMethods(receiver, name, func(member resolver.ResolvedMember) bool {
		selfType := s.memberSelfType(member.Symbol, receiver)
		symbol := resolveMemberSpecialTypes(
			member.Symbol,
			receiver,
			selfType,
		)
		callables = append(callables, callableFromSymbol(symbol))
		return true
	})
	return joinTypes(s.relations, callables)
}

func resolveMemberSpecialTypes(
	symbol semantic.Symbol,
	receiver,
	selfType types.Type,
) semantic.Symbol {
	symbol.ReturnType = resolveSpecialType(
		symbol.ReturnType,
		receiver,
		selfType,
	)
	symbol.Parameters = append([]semantic.Parameter(nil), symbol.Parameters...)
	for index := range symbol.Parameters {
		parameter := &symbol.Parameters[index]
		parameter.Type = resolveSpecialType(
			parameter.Type,
			receiver,
			selfType,
		)
		parameter.NativeType = resolveSpecialType(
			parameter.NativeType,
			receiver,
			selfType,
		)
		parameter.DocType = resolveSpecialType(
			parameter.DocType,
			receiver,
			selfType,
		)
	}
	return symbol
}

func callableFromSymbol(symbol semantic.Symbol) types.Type {
	parameters := make(
		[]types.CallableParameter,
		len(symbol.Parameters),
	)
	for index, parameter := range symbol.Parameters {
		parameters[index] = types.CallableParameter{
			Name:        parameter.Name,
			Type:        parameter.Type,
			Optional:    parameter.Optional,
			Variadic:    parameter.Flags.Has(semantic.VariadicFlag),
			ByReference: parameter.Flags.Has(semantic.ByReferenceFlag),
		}
	}
	result := symbol.ReturnType
	if result.IsUnknown() {
		result = types.Mixed()
	}
	return types.Callable(parameters, result)
}

func (s *functionState) memberSelfType(
	member semantic.Symbol,
	receiver types.Type,
) types.Type {
	container := member.Container
	for container != "" {
		symbol, found := s.analyzer.Snapshot.Symbol(container)
		if !found {
			break
		}
		if symbol.IsClassLike() {
			if symbol.Kind == semantic.TraitSymbol {
				return receiver
			}
			if receiver.Kind() == types.ObjectKind {
				if strings.EqualFold(
					strings.TrimPrefix(receiver.Name(), "\\"),
					strings.TrimPrefix(symbol.FullyQualified, "\\"),
				) {
					return receiver
				}
				if hierarchy, ok := s.relations.Hierarchy.(types.SupertypeHierarchy); ok {
					if projected, found := hierarchy.AsSupertype(
						receiver,
						symbol.FullyQualified,
					); found {
						return projected
					}
				}
			}
			return s.namedType(symbol.FullyQualified)
		}
		container = symbol.Container
	}
	return receiver
}

func (s *functionState) recordReturnDependency(symbol semantic.Symbol) {
	if !symbol.ReturnType.IsUnknown() ||
		symbol.Path != s.document.Path {
		return
	}
	if _, found := s.functionIndices[symbol.ID]; !found {
		return
	}
	if s.dependencies == nil {
		s.dependencies = make(map[semantic.SymbolID]struct{})
	}
	s.dependencies[symbol.ID] = struct{}{}
}

func (s *functionState) inferArguments(
	node *phpsyntax.Node,
	env environment,
) ([]CallArgument, bool) {
	arguments := phpquery.IterateArguments(node)
	result := s.arguments.allocate(arguments.Len())[:0]
	uncertainUnpack := false
	argumentIndex := 0
	for arguments.Next() {
		argument := arguments.Node()
		currentArgumentIndex := argumentIndex
		argumentIndex++
		expression := lastDirectNode(argument)
		value := types.Unknown()
		parameterTypes := s.contextualCallbackParameterTypes(
			node,
			currentArgumentIndex,
			env,
		)
		if len(parameterTypes) > 0 {
			switch expression.Kind() {
			case phpsyntax.PhpArrowFunction:
				value = s.inferArrowFunctionWithParameters(
					expression,
					env,
					parameterTypes,
				)
			case phpsyntax.PhpClosure:
				value = s.inferClosureWithParameters(
					expression,
					env,
					parameterTypes,
				)
			}
		}
		if value.IsUnknown() {
			value = s.infer(expression, env)
		}
		if hasDirectToken(argument, phpsyntax.TkEllipsis) {
			if expanded, ok := positionalTupleArguments(value); ok {
				result = append(result, expanded...)
				continue
			}
			_, element := iterableTypes(value, s.relations)
			if !element.IsUnknown() {
				value = element
			}
			uncertainUnpack = true
		}
		result = append(result, CallArgument{
			Name:       phpquery.ArgumentName(argument),
			Type:       value,
			Expression: expression.Text(),
		})
	}
	return result, uncertainUnpack
}

func (s *functionState) contextualCallbackParameterTypes(
	call *phpsyntax.Node,
	argumentIndex int,
	env environment,
) []types.Type {
	if call == nil || call.Kind() != phpsyntax.PhpFunctionCall {
		return nil
	}
	name := strings.ToLower(strings.TrimPrefix(
		phpquery.CallMethodName(call),
		"\\",
	))
	switch name {
	case "preg_replace_callback":
		if argumentIndex == 1 {
			return []types.Type{regexCallbackMatchesType()}
		}
	case "array_map":
		if argumentIndex != 0 {
			return nil
		}
		arguments := phpquery.Arguments(call)
		parameterTypes := make([]types.Type, 0, max(0, len(arguments)-1))
		for index := 1; index < len(arguments); index++ {
			source := phpquery.ArgumentExpression(call, index)
			_, element := iterableTypes(s.infer(source, env), s.relations)
			if element.IsUnknown() {
				return nil
			}
			parameterTypes = append(parameterTypes, element)
		}
		return parameterTypes
	case "array_filter":
		if argumentIndex == 1 {
			source := phpquery.ArgumentExpression(call, 0)
			_, element := iterableTypes(s.infer(source, env), s.relations)
			if !element.IsUnknown() {
				return []types.Type{element}
			}
		}
	}
	return nil
}

func regexCallbackMatchesType() types.Type {
	return types.ArrayShapeOwned([]types.ShapeField{{
		Name: "0",
		Type: types.String(),
	}}, true)
}

func positionalTupleArguments(value types.Type) ([]CallArgument, bool) {
	if value.Kind() != types.ArrayShapeKind || !arrayShapeIsList(value) {
		return nil, false
	}
	result := make([]CallArgument, value.FieldCount())
	for index := 0; index < value.FieldCount(); index++ {
		found := false
		for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
			field := value.Field(fieldIndex)
			if strings.Trim(field.Name, `"'`) != strconvItoa(index) {
				continue
			}
			result[index] = CallArgument{Type: field.Type}
			found = true
			break
		}
		if !found {
			return nil, false
		}
	}
	return result, true
}

func (s *functionState) inferReceiver(
	node *phpsyntax.Node,
	env environment,
	static bool,
) types.Type {
	if static && node.Kind() == phpsyntax.PhpName {
		name := phpquery.NameValue(node)
		switch strings.ToLower(name) {
		case "self":
			return s.currentClass
		case "static":
			return s.currentClass
		case "parent":
			if s.currentClass.Kind() == types.ObjectKind {
				parent := types.Unknown()
				s.analyzer.Snapshot.VisitClassViews(
					s.currentClass.Name(),
					func(classView semantic.SymbolView) bool {
						class := classView.Materialize()
						if len(class.Extends()) > 0 {
							parent = s.namedType(class.Extends()[0])
						}
						return false
					},
				)
				if !parent.IsUnknown() {
					return parent
				}
			}
			return types.Unknown()
		default:
			return s.namedType(
				s.nameContextAt(node.Range().Start).ResolveClass(name),
			)
		}
	}
	return s.infer(node, env)
}
