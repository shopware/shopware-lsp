package resolver

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// EffectiveCallReturnType evaluates normalized .phpstorm.meta.php return
// contracts against already inferred call arguments. It intentionally lives at
// the signature boundary so diagnostics, inference, hover, and future
// signature help can consume the same effective result.
func EffectiveCallReturnType(
	relations types.Relations,
	arguments []Argument,
	contracts []semantic.CallReturnContract,
) (types.Type, bool) {
	var joiner types.Joiner
	found := false
	for _, contract := range contracts {
		value, ok := effectiveCallReturnType(relations, arguments, contract)
		if !ok {
			continue
		}
		if !found {
			joiner = types.NewJoiner(relations, value)
			found = true
			continue
		}
		joiner.Add(value)
	}
	if !found {
		return types.Type{}, false
	}
	return joiner.Value(), true
}

func effectiveCallReturnType(
	relations types.Relations,
	arguments []Argument,
	contract semantic.CallReturnContract,
) (types.Type, bool) {
	index := int(contract.Argument)
	if index < 0 || index >= len(arguments) {
		return types.Type{}, false
	}
	argument := arguments[index].Type
	if argument.IsUnknown() {
		return types.Type{}, false
	}
	switch contract.Kind {
	case semantic.CallReturnArgumentType:
		return argument, true
	case semantic.CallReturnArgumentElementType:
		_, element := callContractIterableTypes(argument, relations)
		if element.IsUnknown() {
			return types.Type{}, false
		}
		return element, true
	case semantic.CallReturnArgumentMap:
		return effectiveCallMap(arguments, contract)
	default:
		return types.Type{}, false
	}
}

func effectiveCallMap(
	arguments []Argument,
	contract semantic.CallReturnContract,
) (types.Type, bool) {
	index := int(contract.Argument)
	argument := arguments[index]
	var fallback *semantic.CallMapEntry
	for entryIndex := range contract.Map {
		entry := &contract.Map[entryIndex]
		if entry.Key.Kind == semantic.CallValueString && entry.Key.Value == "" {
			fallback = entry
			continue
		}
		if callContractValueMatches(entry.Key, argument) {
			return callContractMapResult(arguments, entry.Result, argument.Type)
		}
	}
	if fallback != nil {
		return callContractMapResult(arguments, fallback.Result, argument.Type)
	}
	return types.Type{}, false
}

func callContractValueMatches(value semantic.CallValue, argument Argument) bool {
	switch value.Kind {
	case semantic.CallValueString:
		return argument.Type.Kind() == types.LiteralStringKind &&
			argument.Type.Name() == value.Value
	case semantic.CallValueNumber:
		return argument.Type.String() == normalizeContractExpression(value.Expression)
	case semantic.CallValueClassConstant:
		expected := strings.TrimSuffix(
			strings.TrimPrefix(normalizeContractExpression(value.Expression), "\\"),
			"::class",
		)
		return strings.EqualFold(expected, callContractArgumentClassName(argument.Type))
	default:
		return strings.EqualFold(
			normalizeContractExpression(value.Expression),
			normalizeContractExpression(argument.Expression),
		)
	}
}

func callContractMapResult(
	arguments []Argument,
	result semantic.CallValue,
	selected types.Type,
) (types.Type, bool) {
	switch result.Kind {
	case semantic.CallValueClassConstant:
		name := strings.TrimSuffix(
			strings.TrimPrefix(normalizeContractExpression(result.Expression), "\\"),
			"::class",
		)
		if name != "" {
			return types.Named(name), true
		}
	case semantic.CallValueString:
		pattern := result.Value
		if strings.HasPrefix(pattern, "$") {
			index, err := strconv.Atoi(strings.TrimPrefix(pattern, "$"))
			if err == nil && index >= 0 && index < len(arguments) {
				if name := callContractArgumentClassName(arguments[index].Type); name != "" {
					return types.Named(name), true
				}
			}
		}
		if strings.Contains(pattern, "@") {
			if name := callContractArgumentClassName(selected); name != "" {
				return types.Named(strings.ReplaceAll(pattern, "@", name)), true
			}
		}
		if pattern != "" {
			return types.Named(strings.TrimPrefix(pattern, "\\")), true
		}
	}
	return types.Type{}, false
}

func callContractArgumentClassName(value types.Type) string {
	switch value.Kind() {
	case types.LiteralStringKind:
		return strings.TrimPrefix(value.Name(), "\\")
	case types.ClassStringKind:
		if value.ArgumentCount() == 1 && value.Argument(0).Kind() == types.ObjectKind {
			return strings.TrimPrefix(value.Argument(0).Name(), "\\")
		}
	case types.ObjectKind:
		return strings.TrimPrefix(value.Name(), "\\")
	}
	return ""
}

func callContractIterableTypes(
	value types.Type,
	relations types.Relations,
) (types.Type, types.Type) {
	switch value.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		if value.ArgumentCount() == 2 {
			if value.Kind() != types.IterableKind &&
				value.Argument(1).Kind() == types.NeverKind {
				return types.Never(), types.Never()
			}
			return value.Argument(0), value.Argument(1)
		}
	case types.ListKind, types.NonEmptyListKind:
		if value.ArgumentCount() == 1 {
			return types.Int(), value.Argument(0)
		}
	case types.ArrayShapeKind:
		return shapeIterableTypes(value, relations)
	case types.UnionKind:
		keys := types.NewJoiner(relations, types.Never())
		values := types.NewJoiner(relations, types.Never())
		for index := 0; index < value.ArgumentCount(); index++ {
			key, element := callContractIterableTypes(value.Argument(index), relations)
			if key.IsUnknown() || element.IsUnknown() {
				return types.Unknown(), types.Unknown()
			}
			keys.Add(key)
			values.Add(element)
		}
		return keys.Value(), values.Value()
	case types.ObjectKind:
		if value.ArgumentCount() == 1 {
			switch strings.ToLower(strings.TrimPrefix(value.Name(), "\\")) {
			case "iterator", "iteratoraggregate", "traversable":
				return types.ArrayKey(), value.Argument(0)
			}
		}
		if hierarchy, ok := relations.Hierarchy.(types.SupertypeHierarchy); ok {
			for _, target := range []string{"IteratorAggregate", "Traversable"} {
				projected, found := hierarchy.AsSupertype(value, target)
				if !found {
					continue
				}
				switch projected.ArgumentCount() {
				case 1:
					return types.ArrayKey(), projected.Argument(0)
				case 2:
					return projected.Argument(0), projected.Argument(1)
				}
			}
		}
	}
	return types.Unknown(), types.Unknown()
}

func normalizeContractExpression(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}
