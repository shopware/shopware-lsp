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
	resolver := signatureResolver{
		relations:   relations,
		arguments:   arguments,
		contracts:   contracts,
		provided:    newParameterPresence(len(symbol.Parameters)),
		mappedTypes: make([]types.Type, len(symbol.Parameters)),
		result: ResolvedSignature{
			Symbol:     symbol,
			ReturnType: symbol.ReturnType,
			Compatible: true,
		},
	}
	return resolver.resolve()
}

type signatureResolver struct {
	relations      types.Relations
	arguments      []Argument
	contracts      []semantic.CallReturnContract
	provided       parameterPresence
	mappedTypes    []types.Type
	nextPositional int
	sawNamed       bool
	result         ResolvedSignature
}

func (r *signatureResolver) resolve() ResolvedSignature {
	for _, argument := range r.arguments {
		if !r.addArgument(argument) {
			return r.result
		}
	}
	if !r.hasRequiredParameters() {
		r.result.Compatible = false
		return r.result
	}
	r.validateTemplateBounds()
	r.resolveReturnType()
	return r.result
}

func (r *signatureResolver) addArgument(argument Argument) bool {
	parameterIndex, valid := r.parameterIndex(argument)
	if !valid {
		r.result.Compatible = false
		return false
	}
	if parameterIndex < 0 {
		if argument.Name == "" &&
			!r.result.Symbol.Flags.Has(semantic.GeneratedStubFlag) {
			// PHP user-defined callables accept additional positional arguments,
			// which remain available through func_get_arg(s). Internal functions
			// reject them, so generated stubs retain strict arity validation.
			return true
		}
		r.result.Compatible = false
		return false
	}
	parameter := r.result.Symbol.Parameters[parameterIndex]
	r.captureDirectReturnTemplate(parameter, argument.Type)
	if r.provided.Has(parameterIndex) &&
		!parameter.Flags.Has(semantic.VariadicFlag) {
		r.result.Compatible = false
		return false
	}
	r.captureArgument(parameterIndex, parameter, argument)
	return true
}

func (r *signatureResolver) captureDirectReturnTemplate(
	parameter semantic.Parameter,
	actual types.Type,
) {
	if !typeContainsTemplateNamed(r.result.Symbol.ReturnType, parameter.Name) {
		return
	}
	if r.result.Templates == nil {
		r.result.Templates = make(map[string]types.Type)
	}
	r.result.Templates[parameter.Name] = actual
}

func (r *signatureResolver) parameterIndex(argument Argument) (int, bool) {
	if argument.Name != "" {
		r.sawNamed = true
		return namedParameterIndex(r.result.Symbol.Parameters, argument.Name), true
	}
	if r.sawNamed {
		return -1, false
	}
	parameters := r.result.Symbol.Parameters
	for r.nextPositional < len(parameters) &&
		r.provided.Has(r.nextPositional) &&
		!parameters[r.nextPositional].Flags.Has(semantic.VariadicFlag) {
		r.nextPositional++
	}
	if r.nextPositional < len(parameters) {
		return r.nextPositional, true
	}
	if len(parameters) != 0 &&
		parameters[len(parameters)-1].Flags.Has(semantic.VariadicFlag) {
		return len(parameters) - 1, true
	}
	return -1, true
}

func namedParameterIndex(parameters []semantic.Parameter, argumentName string) int {
	name := strings.TrimPrefix(argumentName, "$")
	for index, parameter := range parameters {
		if strings.TrimPrefix(parameter.Name, "$") == name {
			return index
		}
	}
	if len(parameters) != 0 &&
		parameters[len(parameters)-1].Flags.Has(semantic.VariadicFlag) {
		return len(parameters) - 1
	}
	return -1
}

func (r *signatureResolver) captureArgument(
	parameterIndex int,
	parameter semantic.Parameter,
	argument Argument,
) {
	r.provided.Add(parameterIndex)
	r.mappedTypes[parameterIndex] = argument.Type
	if !parameter.Flags.Has(semantic.VariadicFlag) {
		r.nextPositional = parameterIndex + 1
	}
	inferTemplates(
		r.relations,
		parameter.Type,
		argument.Type,
		&r.result.Templates,
	)
	if !r.argumentTypeCompatible(parameter, argument.Type) {
		r.result.Compatible = false
	}
}

func (r *signatureResolver) argumentTypeCompatible(
	parameter semantic.Parameter,
	actual types.Type,
) bool {
	expected := parameter.Type
	if len(r.result.Templates) > 0 {
		expected = types.Substitute(expected, r.result.Templates)
	}
	if types.ContainsUncertain(actual) ||
		r.relations.IsAssignableTo(actual, expected) {
		return true
	}
	documented := parameter.DocType
	native := parameter.NativeType
	if len(r.result.Templates) > 0 {
		documented = types.Substitute(documented, r.result.Templates)
		native = types.Substitute(native, r.result.Templates)
	}
	documentedMatches := !documented.IsUnknown() &&
		r.relations.IsAssignableTo(actual, documented)
	nativeMatches := !native.IsUnknown() &&
		r.relations.IsAssignableTo(actual, native)
	return documentedMatches || nativeMatches
}

func (r *signatureResolver) hasRequiredParameters() bool {
	for index, parameter := range r.result.Symbol.Parameters {
		if !r.provided.Has(index) && !parameter.Optional &&
			!parameter.Flags.Has(semantic.VariadicFlag) {
			return false
		}
	}
	return true
}

func (r *signatureResolver) validateTemplateBounds() {
	for _, template := range r.result.Symbol.Templates {
		value, inferred := r.result.Templates[template.Name]
		if !inferred && !template.Default.IsUnknown() {
			value = types.Substitute(template.Default, r.result.Templates)
			if r.result.Templates == nil {
				r.result.Templates = make(
					map[string]types.Type,
					len(r.result.Symbol.Templates),
				)
			}
			r.result.Templates[template.Name] = value
			inferred = true
		}
		if !inferred || types.ContainsUncertain(value) {
			continue
		}
		bound := types.Substitute(template.Bound, r.result.Templates)
		if !bound.IsUnknown() && !r.relations.IsAssignableTo(value, bound) &&
			!nominallySatisfiesGenericBound(r.relations, value, bound) {
			r.result.Compatible = false
		}
	}
}

func (r *signatureResolver) resolveReturnType() {
	if len(r.result.Templates) > 0 {
		r.result.ReturnType = types.Substitute(
			r.result.Symbol.ReturnType,
			r.result.Templates,
		)
	}
	if contracted, ok := returnTypeContract(
		r.result.Symbol.Parameters,
		r.provided,
		r.mappedTypes,
	); ok {
		r.result.ReturnType = refineContractedShape(
			contracted,
			r.result.ReturnType,
		)
		if len(r.result.Templates) > 0 {
			r.result.ReturnType = types.Substitute(
				r.result.ReturnType,
				r.result.Templates,
			)
		}
	}
	if contracted, ok := EffectiveCallReturnType(
		r.relations,
		r.arguments,
		r.contracts,
	); ok {
		r.result.ReturnType = contracted
		r.result.ContractApplied = true
	}
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
