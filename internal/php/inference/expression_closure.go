package inference

import (
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) inferArrowFunction(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	return s.inferArrowFunctionWithParameters(node, env, nil)
}

func (s *functionState) inferArrowFunctionWithParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) types.Type {
	closureEnv := s.cloneEnvironment(env)
	parameters := s.callableParameters(node, closureEnv, parameterTypes)
	result := s.infer(lastDirectNode(node), closureEnv)
	if declared := phpquery.MethodReturnType(node); declared != "" {
		result = s.parseNativeType(declared, node.Range().Start)
	}
	return types.Callable(parameters, result)
}

func (s *functionState) inferClosure(
	node *phpsyntax.Node,
	env environment,
) types.Type {
	return s.inferClosureWithParameters(node, env, nil)
}

func (s *functionState) inferClosureWithParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) types.Type {
	closureEnv := s.newEnvironment(0)
	if !hasDirectTokenText(node, "static") {
		if value, ok := env.get("$this"); ok {
			closureEnv.set("$this", value)
		}
	}
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		captured := cursor.Node()
		if captured.Kind() != phpsyntax.PhpVariable {
			continue
		}
		name := phpquery.VariableKey(captured)
		if value, ok := env.get(name); ok {
			closureEnv.set(name, value)
		}
	}
	parameters := s.callableParameters(node, closureEnv, parameterTypes)
	nested := *s
	nested.returns = nil
	nested.dependencies = nil
	nested.symbol = semantic.Symbol{ReturnType: types.Unknown()}
	declared := types.Unknown()
	if source := phpquery.MethodReturnType(node); source != "" {
		declared = s.parseNativeType(source, node.Range().Start)
		nested.symbol.ReturnType = declared
	}
	if body := phpquery.DirectChild(node, phpsyntax.PhpBlock); body != nil {
		nested.generator = containsDirectYield(body)
		nested.analyzeBlock(body, closureEnv)
	}
	result := joinTypes(s.relations, nested.returns)
	if !declared.IsUnknown() {
		result = declared
	}
	return types.Callable(parameters, result)
}

func (s *functionState) callableParameters(
	node *phpsyntax.Node,
	env environment,
	parameterTypes []types.Type,
) []types.CallableParameter {
	parameters := phpquery.IterateParameters(node)
	parameterCount := parameters.Len()
	if parameterCount == 0 {
		return nil
	}
	result := make([]types.CallableParameter, 0, parameterCount)
	for parameters.Next() {
		parameter := parameters.Node()
		name := phpquery.ParameterName(parameter)
		value := s.parseNativeType(phpquery.ParameterType(parameter), parameter.Range().Start)
		if len(result) < len(parameterTypes) &&
			!parameterTypes[len(result)].IsUnknown() {
			value = parameterTypes[len(result)]
		}
		env.set(name, value)
		result = append(result, types.CallableParameter{
			Name:     name,
			Type:     value,
			Optional: phpquery.ParameterOptional(parameter),
			Variadic: hasDirectToken(parameter, phpsyntax.TkEllipsis),
		})
	}
	return result
}
