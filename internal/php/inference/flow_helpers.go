package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func flowExpressionKey(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case phpsyntax.PhpVariable:
		return phpquery.VariableKey(node)
	case phpsyntax.PhpMemberAccess, phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedAccess, phpsyntax.PhpScopedCall,
		phpsyntax.PhpArrayAccess:
		source := node.Text()
		fullRange := node.Range()
		trimmedRange := node.RangeTrimmedTrivia()
		if trimmedRange.Start > fullRange.Start {
			source = source[trimmedRange.Start-fullRange.Start:]
		}
		return normalizeFlowExpression(source)
	default:
		return ""
	}
}

// conditionFlowExpressionKey unwraps value-preserving condition syntax while
// keeping flowExpressionKey suitable for expression-result caching. An
// assignment expression has the value of its left-hand target, but treating
// it as that target during ordinary inference would skip the assignment.
func conditionFlowExpressionKey(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case phpsyntax.PhpParenthesized:
		return conditionFlowExpressionKey(firstDirectNode(node))
	case phpsyntax.PhpAssignmentExpression:
		return flowExpressionKey(firstDirectNode(node))
	default:
		return flowExpressionKey(node)
	}
}

// normalizeFlowExpression produces the stable environment key used for
// repeated member and array expressions. Ordinary compact expressions borrow
// their source text without allocating. Only whitespace or nullsafe access
// requires a normalized copy.
func normalizeFlowExpression(source string) string {
	source = strings.TrimSpace(source)
	needsNormalization := false
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			needsNormalization = true
		case '?':
			if index+2 < len(source) &&
				source[index+1] == '-' &&
				source[index+2] == '>' {
				needsNormalization = true
			}
		}
		if needsNormalization {
			break
		}
	}
	if !needsNormalization {
		return source
	}
	var result strings.Builder
	result.Grow(len(source))
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			continue
		case '?':
			if index+2 < len(source) &&
				source[index+1] == '-' &&
				source[index+2] == '>' {
				result.WriteString("->")
				index += 2
				continue
			}
		}
		result.WriteByte(source[index])
	}
	return result.String()
}

func predicateType(name string) types.Type {
	switch name {
	case "is_string":
		return types.String()
	case "is_int", "is_integer", "is_long":
		return types.Int()
	case "is_float", "is_double", "is_real":
		return types.Float()
	case "is_bool":
		return types.Bool()
	case "is_array":
		return types.Array(types.Mixed(), types.Mixed())
	case "is_callable":
		return types.Callable(nil, types.Mixed())
	case "is_iterable":
		return types.Iterable(types.Mixed(), types.Mixed())
	case "is_object":
		return types.Object()
	case "is_resource":
		return types.Resource()
	case "is_null":
		return types.Null()
	default:
		return types.Unknown()
	}
}

func isNullNode(node *phpsyntax.Node) bool {
	return node != nil && (node.Kind() == phpsyntax.PhpNull ||
		node.Kind() == phpsyntax.PhpName && strings.EqualFold(compact(node.Text()), "null"))
}

func cloneEnvironment(source environment) environment {
	if source.handle == nil {
		return newEnvironment(0)
	}
	source.handle.shared = true
	handle := newEnvironmentHandle(source.handle.arena)
	handle.bindings = source.handle.bindings
	handle.table = source.handle.table
	handle.override = source.handle.override
	handle.hasOverride = source.handle.hasOverride
	handle.shared = true
	return environment{handle: handle}
}

func environmentIsImpossible(env environment) bool {
	_, impossible := env.get(impossibleEnvironmentBinding)
	return impossible
}

func joinEnvironments(
	relations types.Relations,
	left,
	right environment,
) environment {
	if environmentIsImpossible(left) {
		return cloneEnvironment(right)
	}
	if environmentIsImpossible(right) {
		return cloneEnvironment(left)
	}
	// The branches normally contain the same variables, so their union is
	// usually close to the larger frame rather than the sum of both sizes.
	// Start with that tighter hint and let the hybrid frame grow if branches
	// introduced disjoint bindings.
	var arena *environmentArena
	if left.handle != nil {
		arena = left.handle.arena
	}
	if arena == nil && right.handle != nil {
		arena = right.handle.arena
	}
	result := newEnvironmentIn(arena, max(left.len(), right.len()))
	joinLeftEnvironmentValues(relations, result, left, right)
	addMissingEnvironmentValues(result, right)
	return result
}

func joinLeftEnvironmentValues(
	relations types.Relations,
	result,
	left,
	right environment,
) {
	if left.handle == nil {
		return
	}
	if left.handle.table != nil {
		overrideVisited := false
		for name, value := range left.handle.table {
			if left.handle.hasOverride && left.handle.override.name == name {
				value = left.handle.override.value
				overrideVisited = true
			}
			joinEnvironmentValue(relations, result, right, name, value)
		}
		if left.handle.hasOverride && !overrideVisited {
			joinEnvironmentValue(
				relations,
				result,
				right,
				left.handle.override.name,
				left.handle.override.value,
			)
		}
		return
	}
	overrideVisited := false
	for _, binding := range left.handle.bindings {
		if left.handle.hasOverride && left.handle.override.name == binding.name {
			binding.value = left.handle.override.value
			overrideVisited = true
		}
		joinEnvironmentValue(
			relations,
			result,
			right,
			binding.name,
			binding.value,
		)
	}
	if left.handle.hasOverride && !overrideVisited {
		joinEnvironmentValue(
			relations,
			result,
			right,
			left.handle.override.name,
			left.handle.override.value,
		)
	}
}

func joinEnvironmentValue(
	relations types.Relations,
	result,
	right environment,
	name string,
	value types.Type,
) {
	other, present := right.get(name)
	if strings.HasPrefix(name, booleanAliasBindingPrefix) && !present {
		return
	}
	if present {
		value = relations.Join(value, other)
	}
	result.set(name, value)
}

func addMissingEnvironmentValues(result, source environment) {
	if source.handle == nil {
		return
	}
	if source.handle.table != nil {
		overrideVisited := false
		for name, value := range source.handle.table {
			if source.handle.hasOverride && source.handle.override.name == name {
				value = source.handle.override.value
				overrideVisited = true
			}
			addMissingEnvironmentValue(result, name, value)
		}
		if source.handle.hasOverride && !overrideVisited {
			addMissingEnvironmentValue(
				result,
				source.handle.override.name,
				source.handle.override.value,
			)
		}
		return
	}
	overrideVisited := false
	for _, binding := range source.handle.bindings {
		if source.handle.hasOverride && source.handle.override.name == binding.name {
			binding.value = source.handle.override.value
			overrideVisited = true
		}
		addMissingEnvironmentValue(result, binding.name, binding.value)
	}
	if source.handle.hasOverride && !overrideVisited {
		addMissingEnvironmentValue(
			result,
			source.handle.override.name,
			source.handle.override.value,
		)
	}
}

func addMissingEnvironmentValue(
	result environment,
	name string,
	value types.Type,
) {
	if strings.HasPrefix(name, booleanAliasBindingPrefix) {
		return
	}
	if _, exists := result.get(name); !exists {
		result.set(name, value)
	}
}
