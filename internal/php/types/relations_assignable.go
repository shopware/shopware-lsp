package types

import (
	"strconv"
	"strings"
)

func sameObjectName(left, right string) bool {
	return strings.EqualFold(
		strings.TrimPrefix(left, "\\"),
		strings.TrimPrefix(right, "\\"),
	)
}

// IsAssignableTo includes PHP's safe parameter/return widening rules.
func (r Relations) IsAssignableTo(value, target Type) bool {
	value = r.resolveTypeAlias(value)
	target = r.resolveTypeAlias(target)
	if r.IsSubtype(value, target) {
		return true
	}
	if value.Kind() == ArrayKeyKind &&
		(target.Kind() == IntKind || target.Kind() == StringKind) {
		// PHPStan models array-key as a benevolent int|string union. It keeps
		// the broad key information while permitting code that consumes a
		// conventional numeric or associative array as its expected key kind.
		return true
	}
	if target.Kind() == CallableKind && possiblyCallable(value) {
		// PHP accepts function/method strings and two-element arrays as
		// callables. Broad string/array/object types do not prove that the
		// runtime value is invalid, so argument diagnostics stay conservative.
		return true
	}
	if value.Kind() == UnionKind {
		for _, alternative := range value.arguments() {
			if !r.IsAssignableTo(alternative, target) {
				return false
			}
		}
		return value.ArgumentCount() > 0
	}
	if value.Kind() == ConditionalKind {
		arguments := value.arguments()
		return len(arguments) == 4 &&
			r.IsAssignableTo(arguments[2], target) &&
			r.IsAssignableTo(arguments[3], target)
	}
	if target.Kind() == ConditionalKind {
		arguments := target.arguments()
		return len(arguments) == 4 &&
			r.IsAssignableTo(value, arguments[2]) &&
			r.IsAssignableTo(value, arguments[3])
	}
	if value.Kind() == ClassStringKind && target.Kind() == ClassStringKind &&
		value.ArgumentCount() == 1 && target.ArgumentCount() == 1 {
		// The object carried by class-string follows the same PHPDoc-only
		// generic compatibility rules as an ordinary object. In particular,
		// class-string<Collection> may come from a native declaration which
		// cannot express Collection<T> and must not be treated as proof of an
		// incompatible value.
		return r.IsAssignableTo(value.Argument(0), target.Argument(0))
	}
	if r.containerAssignable(value, target) {
		return true
	}
	if value.Kind() == ObjectKind && target.Kind() == ObjectKind &&
		sameObjectName(value.Name(), target.Name()) &&
		value.ArgumentCount() == 0 && target.ArgumentCount() > 0 {
		// PHP generics are PHPDoc-only. A raw native declaration carries
		// unknown type arguments, so it is not proof of an incompatible call
		// or return value.
		return true
	}
	if target.Kind() == UnionKind {
		for _, alternative := range target.arguments() {
			if r.IsAssignableTo(value, alternative) {
				return true
			}
		}
	}
	if value.Kind() == ArrayKind && target.Kind() == UnionKind &&
		r.arrayAssignableToContainerUnion(value, target) {
		return true
	}
	if value.Kind() == IntKind || value.Kind() == LiteralIntKind {
		return target.Kind() == FloatKind
	}
	return false
}

func (r Relations) containerAssignable(value, target Type) bool {
	switch value.Kind() {
	case ListKind, NonEmptyListKind:
		if value.ArgumentCount() != 1 {
			return false
		}
		switch target.Kind() {
		case ListKind:
			return target.ArgumentCount() == 1 &&
				r.IsAssignableTo(value.Argument(0), target.Argument(0))
		case NonEmptyListKind:
			return value.Kind() == NonEmptyListKind &&
				target.ArgumentCount() == 1 &&
				r.IsAssignableTo(value.Argument(0), target.Argument(0))
		case ArrayKind, IterableKind:
			return target.ArgumentCount() == 2 &&
				r.IsAssignableTo(Int(), target.Argument(0)) &&
				r.IsAssignableTo(value.Argument(0), target.Argument(1))
		}
	case ArrayKind, NonEmptyArrayKind:
		if value.ArgumentCount() != 2 {
			return false
		}
		switch target.Kind() {
		case ArrayKind, IterableKind:
			return target.ArgumentCount() == 2 &&
				r.IsAssignableTo(value.Argument(0), target.Argument(0)) &&
				r.IsAssignableTo(value.Argument(1), target.Argument(1))
		case NonEmptyArrayKind:
			return value.Kind() == NonEmptyArrayKind &&
				target.ArgumentCount() == 2 &&
				r.IsAssignableTo(value.Argument(0), target.Argument(0)) &&
				r.IsAssignableTo(value.Argument(1), target.Argument(1))
		}
	case ArrayShapeKind:
		if target.Kind() == ArrayShapeKind {
			return r.shapeAssignable(value, target)
		}
		if target.Kind() == ArrayKind || target.Kind() == IterableKind ||
			target.Kind() == NonEmptyArrayKind {
			if target.ArgumentCount() != 2 {
				return false
			}
			if target.Kind() == NonEmptyArrayKind &&
				!r.arrayShapeHasRequiredField(value) {
				return false
			}
			key, element := r.shapeIterableTypes(value)
			return r.IsAssignableTo(key, target.Argument(0)) &&
				r.IsAssignableTo(element, target.Argument(1))
		}
	}
	return false
}

func (r Relations) shapeAssignable(value, target Type) bool {
	valueFields := make(map[string]ShapeField, value.FieldCount())
	for index := 0; index < value.FieldCount(); index++ {
		field := value.Field(index)
		valueFields[shapeFieldName(field.Name)] = field
	}
	for index := 0; index < target.FieldCount(); index++ {
		expected := target.Field(index)
		actual, exists := valueFields[shapeFieldName(expected.Name)]
		if !exists {
			if expected.Optional {
				continue
			}
			return false
		}
		if !expected.Optional && actual.Optional {
			return false
		}
		if !r.IsAssignableTo(actual.Type, expected.Type) {
			return false
		}
	}
	return true
}

func possiblyCallable(value Type) bool {
	switch value.Kind() {
	case StringKind, LiteralStringKind, ClassStringKind,
		ArrayKind, NonEmptyArrayKind, ListKind, NonEmptyListKind:
		return true
	case ObjectKind:
		return value.Name() == ""
	case ArrayShapeKind:
		if value.IsOpenShape() {
			return true
		}
		if value.FieldCount() != 2 {
			return false
		}
		fields := value.fields()
		firstName := strings.Trim(fields[0].Name, `"'`)
		secondName := strings.Trim(fields[1].Name, `"'`)
		if firstName != "0" || secondName != "1" ||
			fields[0].Optional || fields[1].Optional {
			return false
		}
		first := fields[0].Type
		second := fields[1].Type
		firstPossible := first.Kind() == ObjectKind ||
			first.Kind() == StringKind ||
			first.Kind() == LiteralStringKind ||
			first.Kind() == ClassStringKind
		secondPossible := second.Kind() == StringKind ||
			second.Kind() == LiteralStringKind
		return firstPossible && secondPossible
	default:
		return false
	}
}

func (r Relations) resolveTypeAlias(value Type) Type {
	hierarchy, ok := r.Hierarchy.(TypeAliasHierarchy)
	if !ok {
		return value
	}
	var seen map[string]struct{}
	for depth := 0; depth < 16; depth++ {
		resolved, found := hierarchy.ResolveTypeAlias(value)
		if !found || resolved.IsUnknown() || resolved.Equal(value) {
			return value
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		key := value.Key()
		if _, duplicate := seen[key]; duplicate {
			return value
		}
		seen[key] = struct{}{}
		value = resolved
	}
	return value
}

func (r Relations) arrayAssignableToContainerUnion(
	value,
	target Type,
) bool {
	valueArguments := value.arguments()
	if len(valueArguments) != 2 {
		return false
	}
	keys := []Type{valueArguments[0]}
	if valueArguments[0].Kind() == ArrayKeyKind {
		keys = []Type{Int(), String()}
	} else if valueArguments[0].Kind() == UnionKind {
		keys = valueArguments[0].arguments()
	}
	for _, key := range keys {
		accepted := false
		for _, alternative := range target.arguments() {
			switch alternative.Kind() {
			case ListKind:
				arguments := alternative.arguments()
				accepted = len(arguments) == 1 &&
					r.IsSubtype(key, Int()) &&
					r.hasAssignableAlternative(
						valueArguments[1], arguments[0],
					)
			case ArrayKind:
				arguments := alternative.arguments()
				accepted = len(arguments) == 2 &&
					r.IsSubtype(key, arguments[0]) &&
					r.hasAssignableAlternative(
						valueArguments[1], arguments[1],
					)
			}
			if accepted {
				break
			}
		}
		if !accepted {
			return false
		}
	}
	return true
}

func (r Relations) hasAssignableAlternative(value, target Type) bool {
	if value.Kind() != UnionKind {
		return r.IsAssignableTo(value, target)
	}
	for _, alternative := range value.arguments() {
		if r.IsAssignableTo(alternative, target) {
			return true
		}
	}
	return false
}

func (r Relations) Join(left, right Type) Type {
	if left.IsUnknown() || right.IsUnknown() {
		return Unknown()
	}
	if joined, ok := r.joinArrayShapeBranches(left, right); ok {
		return joined
	}
	if r.IsSubtype(left, right) {
		return right
	}
	if r.IsSubtype(right, left) {
		return left
	}
	joined := Union(left, right)
	if joined.Kind() == UnionKind &&
		(joined.node.args.count() > maxPreciseUnionMembers ||
			literalUnionTooWide(joined)) {
		return widenComplexUnion(joined)
	}
	return joined
}

func (r Relations) joinArrayShapeBranches(left, right Type) (Type, bool) {
	leftFields, leftOpen, leftShape := branchArrayShape(left)
	rightFields, rightOpen, rightShape := branchArrayShape(right)
	if !leftShape || !rightShape {
		return Type{}, false
	}
	if len(leftFields) == 0 && len(rightFields) > 0 {
		if element, list := arrayShapeListElement(rightFields, rightOpen, r); list {
			return List(element), true
		}
	}
	if len(rightFields) == 0 && len(leftFields) > 0 {
		if element, list := arrayShapeListElement(leftFields, leftOpen, r); list {
			return List(element), true
		}
	}

	fields := make([]ShapeField, 0, len(leftFields)+len(rightFields))
	indices := make(map[string]int, len(leftFields)+len(rightFields))
	for _, field := range leftFields {
		field.Optional = field.Optional || len(rightFields) == 0
		indices[normalizedShapeFieldName(field.Name)] = len(fields)
		fields = append(fields, field)
	}
	for _, field := range rightFields {
		name := normalizedShapeFieldName(field.Name)
		if index, exists := indices[name]; exists {
			fields[index].Type = r.Join(fields[index].Type, field.Type)
			fields[index].Optional = fields[index].Optional || field.Optional
			continue
		}
		field.Optional = true
		indices[name] = len(fields)
		fields = append(fields, field)
	}
	if len(leftFields) > 0 && len(rightFields) > 0 {
		rightNames := make(map[string]struct{}, len(rightFields))
		for _, field := range rightFields {
			rightNames[normalizedShapeFieldName(field.Name)] = struct{}{}
		}
		for index := range fields {
			if _, exists := rightNames[normalizedShapeFieldName(fields[index].Name)]; !exists {
				fields[index].Optional = true
			}
		}
	}
	return ArrayShapeOwned(fields, leftOpen || rightOpen), true
}

func normalizedShapeFieldName(name string) string {
	return strings.Trim(name, `"'`)
}

func arrayShapeListElement(
	fields []ShapeField,
	open bool,
	relations Relations,
) (Type, bool) {
	if open || len(fields) == 0 {
		return Type{}, false
	}
	element := Never()
	for index, field := range fields {
		if field.Optional || normalizedShapeFieldName(field.Name) != strconv.Itoa(index) {
			return Type{}, false
		}
		element = relations.Join(element, field.Type)
	}
	return element, true
}

func branchArrayShape(value Type) ([]ShapeField, bool, bool) {
	if value.Kind() == ArrayShapeKind {
		return value.Fields(), value.IsOpenShape(), true
	}
	if value.Kind() == ArrayKind && value.ArgumentCount() == 2 &&
		value.Argument(1).Kind() == NeverKind {
		return nil, false, true
	}
	return nil, false, false
}

func literalUnionTooWide(value Type) bool {
	if value.Kind() != UnionKind ||
		value.node.args.count() <= maxPreciseLiteralUnionMembers {
		return false
	}
	for _, member := range value.node.args.values() {
		switch member.Kind() {
		case NullKind, TrueKind, FalseKind,
			LiteralIntKind, LiteralFloatKind, LiteralStringKind:
		default:
			return false
		}
	}
	return true
}

func widenComplexUnion(value Type) Type {
	if value.Kind() != UnionKind {
		return widenType(value)
	}
	members := value.node.args.values()
	widened := make([]Type, 0, len(members))
	for _, member := range members {
		widened = append(widened, widenType(member))
	}
	return Union(widened...)
}

func widenType(value Type) Type {
	switch value.Kind() {
	case TrueKind, FalseKind:
		return Bool()
	case LiteralIntKind:
		return Int()
	case LiteralFloatKind:
		return Float()
	case LiteralStringKind:
		return String()
	case ObjectKind, ObjectShapeKind, SelfKind, StaticKind, ParentKind:
		return Object()
	case ArrayKind, ArrayShapeKind:
		return Array(Mixed(), Mixed())
	case NonEmptyArrayKind:
		return NonEmptyArray(Mixed(), Mixed())
	case ListKind:
		return List(Mixed())
	case NonEmptyListKind:
		return NonEmptyList(Mixed())
	case IterableKind:
		return Iterable(Mixed(), Mixed())
	case CallableKind:
		return Callable(nil, Mixed())
	case ClassStringKind:
		return ClassString(Object())
	case TemplateKind, IntersectionKind:
		return Mixed()
	default:
		return value
	}
}

// Narrow applies a positive type constraint.
func (r Relations) Narrow(original, constraint Type) Type {
	if original.IsUnknown() {
		return constraint
	}
	if original.Kind() == IterableKind && constraint.Kind() == ObjectKind &&
		sameObjectName(constraint.Name(), "Traversable") &&
		original.ArgumentCount() == 2 {
		return Named("Traversable", original.arguments()...)
	}
	if r.IsSubtype(original, constraint) {
		return original
	}
	if r.IsSubtype(constraint, original) {
		return constraint
	}
	if original.Kind() == UnionKind {
		arguments := original.arguments()
		members := make([]Type, 0, len(arguments))
		for _, member := range arguments {
			if r.IsSubtype(member, constraint) || r.IsSubtype(constraint, member) {
				members = append(members, r.Narrow(member, constraint))
			}
		}
		if len(members) == 0 {
			return Never()
		}
		return Union(members...)
	}
	return Intersection(original, constraint)
}

// Without removes a proven-negative type from a union.
func (r Relations) Without(original, excluded Type) Type {
	if original.Kind() == IterableKind && excluded.Kind() == ObjectKind &&
		sameObjectName(excluded.Name(), "Traversable") &&
		original.ArgumentCount() == 2 {
		return Array(original.Argument(0), original.Argument(1))
	}
	if original.Kind() != UnionKind {
		if r.IsSubtype(original, excluded) {
			return Never()
		}
		return original
	}
	arguments := original.arguments()
	members := make([]Type, 0, len(arguments))
	for _, member := range arguments {
		if !r.IsSubtype(member, excluded) {
			members = append(members, member)
		}
	}
	return Union(members...)
}

func (r Relations) argumentsSubtype(candidate, target []Type) bool {
	if len(target) == 0 {
		return true
	}
	if len(candidate) != len(target) {
		return false
	}
	for index := range candidate {
		if !r.IsSubtype(candidate[index], target[index]) {
			return false
		}
	}
	return true
}

func (r Relations) objectArgumentsSubtype(
	name string,
	candidate,
	target []Type,
) bool {
	if len(target) == 0 {
		return true
	}
	if len(candidate) != len(target) {
		return false
	}
	var variances GenericHierarchy
	if hierarchy, ok := r.Hierarchy.(GenericHierarchy); ok {
		variances = hierarchy
	}
	for index := range candidate {
		variance := Invariant
		if variances != nil {
			variance = variances.TemplateVariance(name, index)
		}
		switch variance {
		case Covariant:
			if !r.IsSubtype(candidate[index], target[index]) {
				return false
			}
		case Contravariant:
			if !r.IsSubtype(target[index], candidate[index]) {
				return false
			}
		default:
			// Invariance requires semantic equivalence, not identical syntax.
			// A hierarchy-aware union such as Child|Parent is equivalent to
			// Parent even when the lossless type representation retains both
			// arms. Raw generic arguments are also unknown rather than proof of
			// incompatibility, so use assignability in both directions.
			if !r.IsAssignableTo(candidate[index], target[index]) ||
				!r.IsAssignableTo(target[index], candidate[index]) {
				return false
			}
		}
	}
	return true
}

func (r Relations) iterableArgumentsSubtype(key Type, candidate, target []Type) bool {
	if len(candidate) != 1 || len(target) != 2 {
		return false
	}
	return r.IsSubtype(key, target[0]) && r.IsSubtype(candidate[0], target[1])
}

func (r Relations) callableSubtype(candidate, target Type) bool {
	candidateParameters := candidate.parameters()
	targetParameters := target.parameters()
	if len(targetParameters) == 0 && target.Result().Kind() == MixedKind {
		return true
	}
	common := len(candidateParameters)
	if len(targetParameters) < common {
		common = len(targetParameters)
	}
	for index := 0; index < common; index++ {
		candidateParameter := candidateParameters[index]
		targetParameter := targetParameters[index]
		if targetParameter.Optional && !candidateParameter.Optional &&
			!candidateParameter.Variadic {
			return false
		}
		if targetParameter.Variadic && !candidateParameter.Variadic {
			return false
		}
		if targetParameter.ByReference != candidateParameter.ByReference {
			return false
		}
		// Parameters are contravariant.
		if !r.IsSubtype(targetParameter.Type, candidateParameter.Type) {
			return false
		}
	}
	for index := common; index < len(candidateParameters); index++ {
		if !candidateParameters[index].Optional &&
			!candidateParameters[index].Variadic {
			return false
		}
	}
	return r.IsSubtype(candidate.Result(), target.Result())
}

func (r Relations) shapeIterableTypes(shape Type) (Type, Type) {
	keyTypes := NewJoiner(r, Never())
	valueTypes := NewJoiner(r, Never())
	for _, field := range shape.fields() {
		name := strings.Trim(field.Name, `"'`)
		key := LiteralString(name)
		if _, err := strconv.ParseInt(name, 10, 64); err == nil {
			key = LiteralInt(name)
		}
		keyTypes.Add(key)
		valueTypes.Add(field.Type)
	}
	return keyTypes.Value(), valueTypes.Value()
}

func (r Relations) arrayShapeListSubtype(candidate, target Type) bool {
	if candidate.IsOpenShape() || len(target.arguments()) != 1 {
		return false
	}
	fields := make(map[string]ShapeField, len(candidate.fields()))
	for _, field := range candidate.fields() {
		fields[strings.Trim(field.Name, `"'`)] = field
	}
	optional := false
	for index := 0; index < len(fields); index++ {
		field, exists := fields[strconv.Itoa(index)]
		if !exists || !r.IsSubtype(field.Type, target.arguments()[0]) {
			return false
		}
		if field.Optional {
			optional = true
		} else if optional {
			// Required numeric fields after an optional gap can form a sparse
			// array, while trailing optional fields still describe a list.
			return false
		}
	}
	return true
}

func (r Relations) arrayShapeKnownNonEmpty(candidate Type) bool {
	for _, field := range candidate.fields() {
		if strings.Trim(field.Name, `"'`) == "0" && !field.Optional {
			return true
		}
	}
	return false
}

func (r Relations) arrayShapeHasRequiredField(candidate Type) bool {
	for _, field := range candidate.fields() {
		if !field.Optional {
			return true
		}
	}
	return false
}

func (r Relations) shapeSubtype(candidate, target Type) bool {
	candidateFields := make(map[string]ShapeField, len(candidate.fields()))
	for _, field := range candidate.fields() {
		candidateFields[shapeFieldName(field.Name)] = field
	}
	for _, expected := range target.fields() {
		actual, exists := candidateFields[shapeFieldName(expected.Name)]
		if !exists {
			if expected.Optional {
				continue
			}
			return false
		}
		if !expected.Optional && actual.Optional {
			return false
		}
		if !r.IsSubtype(actual.Type, expected.Type) {
			return false
		}
	}
	return true
}

func shapeFieldName(name string) string {
	return strings.Trim(name, `"'`)
}
