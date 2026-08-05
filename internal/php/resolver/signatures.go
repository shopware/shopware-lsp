package resolver

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Argument struct {
	Name       string
	Type       types.Type
	Expression string
}

type ResolvedSignature struct {
	Symbol          semantic.Symbol
	ReturnType      types.Type
	Templates       map[string]types.Type
	Compatible      bool
	ContractApplied bool
}

func ResolveSignature(
	relations types.Relations,
	symbol semantic.Symbol,
	arguments []Argument,
) ResolvedSignature {
	return ResolveSignatureWithContracts(relations, symbol, arguments, nil)
}

// ResolveSignatureWithContracts resolves an ordinary declaration signature and
// then applies any matching dynamic return metadata. Keeping the effective
// return on ResolvedSignature ensures consumers agree on overload
// compatibility before accepting a .phpstorm.meta.php override.
func ResolveSignatureWithContracts(
	relations types.Relations,
	symbol semantic.Symbol,
	arguments []Argument,
	contracts []semantic.CallReturnContract,
) ResolvedSignature {
	result := ResolvedSignature{
		Symbol:     symbol,
		ReturnType: symbol.ReturnType,
		Compatible: true,
	}
	provided := newParameterPresence(len(symbol.Parameters))
	mappedTypes := make([]types.Type, len(symbol.Parameters))
	nextPositional := 0
	sawNamed := false
	for _, argument := range arguments {
		parameterIndex := -1
		if argument.Name != "" {
			sawNamed = true
			name := strings.TrimPrefix(argument.Name, "$")
			for index, parameter := range symbol.Parameters {
				if strings.TrimPrefix(parameter.Name, "$") == name {
					parameterIndex = index
					break
				}
			}
			if parameterIndex < 0 && len(symbol.Parameters) > 0 &&
				symbol.Parameters[len(symbol.Parameters)-1].Flags.Has(semantic.VariadicFlag) {
				parameterIndex = len(symbol.Parameters) - 1
			}
		} else {
			if sawNamed {
				result.Compatible = false
				return result
			}
			for nextPositional < len(symbol.Parameters) &&
				provided.Has(nextPositional) &&
				!symbol.Parameters[nextPositional].Flags.Has(semantic.VariadicFlag) {
				nextPositional++
			}
			if nextPositional < len(symbol.Parameters) {
				parameterIndex = nextPositional
			} else if len(symbol.Parameters) > 0 &&
				symbol.Parameters[len(symbol.Parameters)-1].Flags.Has(semantic.VariadicFlag) {
				parameterIndex = len(symbol.Parameters) - 1
			}
		}
		if parameterIndex < 0 {
			if argument.Name == "" &&
				!symbol.Flags.Has(semantic.GeneratedStubFlag) {
				// PHP user-defined callables accept additional positional
				// arguments, which remain available through func_get_arg(s).
				// Internal functions reject them, so generated stubs retain
				// strict arity validation.
				continue
			}
			result.Compatible = false
			return result
		}

		parameter := symbol.Parameters[parameterIndex]
		if typeContainsTemplateNamed(symbol.ReturnType, parameter.Name) {
			if result.Templates == nil {
				result.Templates = make(map[string]types.Type)
			}
			result.Templates[parameter.Name] = argument.Type
		}
		if provided.Has(parameterIndex) &&
			!parameter.Flags.Has(semantic.VariadicFlag) {
			result.Compatible = false
			return result
		}
		provided.Add(parameterIndex)
		mappedTypes[parameterIndex] = argument.Type
		if !parameter.Flags.Has(semantic.VariadicFlag) {
			nextPositional = parameterIndex + 1
		}

		inferTemplates(
			relations,
			parameter.Type,
			argument.Type,
			&result.Templates,
		)
		expected := parameter.Type
		if len(result.Templates) > 0 {
			expected = types.Substitute(expected, result.Templates)
		}
		if !types.ContainsUncertain(argument.Type) &&
			!relations.IsAssignableTo(argument.Type, expected) {
			documented := parameter.DocType
			native := parameter.NativeType
			if len(result.Templates) > 0 {
				documented = types.Substitute(
					documented,
					result.Templates,
				)
				native = types.Substitute(
					native,
					result.Templates,
				)
			}
			documentedMatches := !documented.IsUnknown() &&
				relations.IsAssignableTo(argument.Type, documented)
			nativeMatches := !native.IsUnknown() &&
				relations.IsAssignableTo(argument.Type, native)
			if !documentedMatches && !nativeMatches {
				result.Compatible = false
			}
		}
	}
	for index, parameter := range symbol.Parameters {
		if !provided.Has(index) && !parameter.Optional &&
			!parameter.Flags.Has(semantic.VariadicFlag) {
			result.Compatible = false
			return result
		}
	}
	for _, template := range symbol.Templates {
		value, inferred := result.Templates[template.Name]
		if !inferred && !template.Default.IsUnknown() {
			value = types.Substitute(template.Default, result.Templates)
			if result.Templates == nil {
				result.Templates = make(map[string]types.Type, len(symbol.Templates))
			}
			result.Templates[template.Name] = value
			inferred = true
		}
		if !inferred || types.ContainsUncertain(value) {
			continue
		}
		bound := types.Substitute(template.Bound, result.Templates)
		if !bound.IsUnknown() && !relations.IsAssignableTo(value, bound) &&
			!nominallySatisfiesGenericBound(relations, value, bound) {
			result.Compatible = false
		}
	}
	if len(result.Templates) > 0 {
		result.ReturnType = types.Substitute(symbol.ReturnType, result.Templates)
	}
	if contracted, ok := returnTypeContract(
		symbol.Parameters,
		provided,
		mappedTypes,
	); ok {
		result.ReturnType = refineContractedShape(contracted, result.ReturnType)
		if len(result.Templates) > 0 {
			result.ReturnType = types.Substitute(
				result.ReturnType,
				result.Templates,
			)
		}
	}
	if contracted, ok := EffectiveCallReturnType(
		relations,
		arguments,
		contracts,
	); ok {
		result.ReturnType = contracted
		result.ContractApplied = true
	}
	return result
}

func refineContractedShape(contracted, declared types.Type) types.Type {
	wanted := types.UnknownKind
	switch contracted.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind,
		types.ListKind, types.NonEmptyListKind:
		wanted = types.ArrayShapeKind
	case types.ObjectKind:
		if contracted.Name() == "" {
			wanted = types.ObjectShapeKind
		}
	}
	if wanted == types.UnknownKind {
		return contracted
	}
	if declared.Kind() == wanted {
		return declared
	}
	if declared.Kind() == types.UnionKind {
		for index := 0; index < declared.ArgumentCount(); index++ {
			if member := declared.Argument(index); member.Kind() == wanted {
				return member
			}
		}
	}
	return contracted
}

func returnTypeContract(
	parameters []semantic.Parameter,
	provided parameterPresence,
	mappedTypes []types.Type,
) (types.Type, bool) {
	for index := range parameters {
		attribute, found := semantic.AttributeNamed(
			parameters[index].Attributes,
			"ReturnTypeContract",
		)
		if !found {
			continue
		}
		argumentName := ""
		if provided.Has(index) && index < len(mappedTypes) {
			switch mappedTypes[index].Kind() {
			case types.TrueKind:
				argumentName = "true"
			case types.FalseKind:
				argumentName = "false"
			default:
				argumentName = "exists"
			}
		} else {
			argumentName = defaultContractBranch(parameters[index].DefaultValue)
			if argumentName == "" {
				argumentName = "notExists"
			}
		}
		value, ok := attribute.Argument(argumentName, -1)
		if !ok || value.Kind != semantic.AttributeValueString ||
			value.Value == "" {
			continue
		}
		contracted, err := types.Parse(value.Value)
		if err != nil || contracted.IsUnknown() ||
			contracted.Kind() == types.ErrorKind {
			continue
		}
		return contracted, true
	}
	return types.Type{}, false
}

func defaultContractBranch(value *semantic.AttributeValue) string {
	if value == nil || value.Kind != semantic.AttributeValueBool {
		return ""
	}
	if strings.EqualFold(value.Value, "true") {
		return "true"
	}
	if strings.EqualFold(value.Value, "false") {
		return "false"
	}
	return ""
}

func nominallySatisfiesGenericBound(
	relations types.Relations,
	value,
	bound types.Type,
) bool {
	if relations.Hierarchy == nil ||
		value.Kind() != types.ObjectKind || value.Name() == "" ||
		bound.Kind() != types.ObjectKind || bound.Name() == "" ||
		bound.ArgumentCount() == 0 {
		return false
	}
	return strings.EqualFold(value.Name(), bound.Name()) ||
		relations.Hierarchy.IsSubtypeOf(value.Name(), bound.Name())
}

type parameterPresence struct {
	bits     uint64
	overflow []bool
}

func newParameterPresence(count int) parameterPresence {
	var result parameterPresence
	if count > 64 {
		result.overflow = make([]bool, count-64)
	}
	return result
}

func (presence parameterPresence) Has(index int) bool {
	if index < 0 {
		return false
	}
	if index < 64 {
		return presence.bits&(uint64(1)<<uint(index)) != 0
	}
	index -= 64
	return index < len(presence.overflow) && presence.overflow[index]
}

func (presence *parameterPresence) Add(index int) {
	if index < 0 {
		return
	}
	if index < 64 {
		presence.bits |= uint64(1) << uint(index)
		return
	}
	index -= 64
	if index < len(presence.overflow) {
		presence.overflow[index] = true
	}
}

func inferTemplates(
	relations types.Relations,
	pattern,
	actual types.Type,
	result *map[string]types.Type,
) {
	if actual.IsUnknown() || actual.Kind() == types.MixedKind {
		return
	}
	if pattern.Kind() == types.TemplateKind {
		if *result == nil {
			*result = make(map[string]types.Type)
		}
		if existing, ok := (*result)[pattern.Name()]; ok {
			(*result)[pattern.Name()] = types.Union(existing, actual)
		} else {
			(*result)[pattern.Name()] = actual
		}
		return
	}
	if pattern.Kind() == types.CallableKind &&
		actual.Kind() == types.CallableKind {
		patternParameterCount := pattern.ParameterCount()
		if patternParameterCount == actual.ParameterCount() {
			for index := 0; index < patternParameterCount; index++ {
				inferTemplates(
					relations,
					pattern.Parameter(index).Type,
					actual.Parameter(index).Type,
					result,
				)
			}
		}
		inferTemplates(
			relations,
			pattern.Result(),
			actual.Result(),
			result,
		)
		return
	}
	if pattern.Kind() == types.UnionKind {
		inferTemplatesFromUnion(relations, pattern, actual, result)
		return
	}
	if actual.Kind() == types.UnionKind {
		for index := 0; index < actual.ArgumentCount(); index++ {
			inferTemplates(
				relations,
				pattern,
				actual.Argument(index),
				result,
			)
		}
		return
	}
	if pattern.Kind() == types.ObjectKind &&
		actual.Kind() == types.ObjectKind &&
		!strings.EqualFold(pattern.Name(), actual.Name()) {
		if hierarchy, ok := relations.Hierarchy.(types.SupertypeHierarchy); ok {
			if projected, found := hierarchy.AsSupertype(
				actual,
				pattern.Name(),
			); found {
				actual = projected
			}
		}
	}
	if (pattern.Kind() == types.ArrayKind ||
		pattern.Kind() == types.NonEmptyArrayKind ||
		pattern.Kind() == types.IterableKind) &&
		(actual.Kind() == types.ListKind ||
			actual.Kind() == types.NonEmptyListKind) &&
		pattern.ArgumentCount() == 2 &&
		actual.ArgumentCount() == 1 {
		inferTemplates(relations, pattern.Argument(0), types.Int(), result)
		inferTemplates(
			relations,
			pattern.Argument(1),
			actual.Argument(0),
			result,
		)
		return
	}
	if (pattern.Kind() == types.ArrayKind ||
		pattern.Kind() == types.NonEmptyArrayKind ||
		pattern.Kind() == types.IterableKind) &&
		actual.Kind() == types.ArrayShapeKind &&
		pattern.ArgumentCount() == 2 {
		key, value := shapeIterableTypes(actual, relations)
		inferTemplates(relations, pattern.Argument(0), key, result)
		inferTemplates(relations, pattern.Argument(1), value, result)
		return
	}
	argumentCount := pattern.ArgumentCount()
	if argumentCount != actual.ArgumentCount() {
		return
	}
	for index := 0; index < argumentCount; index++ {
		inferTemplates(
			relations,
			pattern.Argument(index),
			actual.Argument(index),
			result,
		)
	}
}

func shapeIterableTypes(
	value types.Type,
	relations types.Relations,
) (types.Type, types.Type) {
	keys := types.NewJoiner(relations, types.Never())
	values := types.NewJoiner(relations, types.Never())
	for index := 0; index < value.FieldCount(); index++ {
		field := value.Field(index)
		name := strings.Trim(field.Name, `"'`)
		if _, err := strconv.Atoi(name); err == nil {
			keys.Add(types.LiteralInt(name))
		} else {
			keys.Add(types.LiteralString(name))
		}
		values.Add(field.Type)
	}
	return keys.Value(), values.Value()
}

func inferTemplatesFromUnion(
	relations types.Relations,
	pattern,
	actual types.Type,
	result *map[string]types.Type,
) {
	var concretePatterns, templatePatterns []types.Type
	for index := 0; index < pattern.ArgumentCount(); index++ {
		alternative := pattern.Argument(index)
		if containsTemplate(alternative) {
			templatePatterns = append(templatePatterns, alternative)
		} else {
			concretePatterns = append(concretePatterns, alternative)
		}
	}
	if len(templatePatterns) == 0 {
		return
	}

	actualAlternativeCount := 1
	if actual.Kind() == types.UnionKind {
		actualAlternativeCount = actual.ArgumentCount()
	}
	remaining := make([]types.Type, 0, actualAlternativeCount)
	for index := 0; index < actualAlternativeCount; index++ {
		alternative := actual
		if actual.Kind() == types.UnionKind {
			alternative = actual.Argument(index)
		}
		if alternative.IsUnknown() ||
			alternative.Kind() == types.MixedKind {
			continue
		}
		structuralMatch := false
		for _, templatePattern := range templatePatterns {
			if templatePattern.Kind() != types.TemplateKind &&
				templatePattern.Kind() == alternative.Kind() {
				inferTemplates(relations, templatePattern, alternative, result)
				structuralMatch = true
				break
			}
		}
		if structuralMatch {
			continue
		}
		matched := false
		for _, concrete := range concretePatterns {
			if relations.IsAssignableTo(alternative, concrete) {
				matched = true
				break
			}
		}
		if !matched {
			remaining = append(remaining, alternative)
		}
	}
	if len(remaining) == 0 {
		return
	}
	if len(templatePatterns) == 1 {
		inferTemplates(
			relations,
			templatePatterns[0],
			types.Union(remaining...),
			result,
		)
		return
	}

	// Multiple generic alternatives are inherently ambiguous. Preserve the
	// useful structural cases without assigning every actual alternative to
	// every template-bearing branch.
	for _, alternative := range remaining {
		for _, templatePattern := range templatePatterns {
			if templatePattern.Kind() == types.TemplateKind ||
				templatePattern.Kind() == alternative.Kind() {
				inferTemplates(
					relations,
					templatePattern,
					alternative,
					result,
				)
				break
			}
		}
	}
}

func containsTemplate(value types.Type) bool {
	if value.Kind() == types.TemplateKind {
		return true
	}
	if value.Kind() == types.CallableKind {
		for index := 0; index < value.ParameterCount(); index++ {
			parameter := value.Parameter(index)
			if containsTemplate(parameter.Type) {
				return true
			}
		}
		return containsTemplate(value.Result())
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if containsTemplate(value.Argument(index)) {
			return true
		}
	}
	return false
}
