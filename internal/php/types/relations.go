package types

import (
	"strconv"
	"strings"
)

// Hierarchy supplies class/interface inheritance without coupling the type
// package to a particular index implementation.
type Hierarchy interface {
	IsSubtypeOf(candidate, target string) bool
}

type Variance uint8

const (
	Invariant Variance = iota
	Covariant
	Contravariant
)

// GenericHierarchy optionally supplies declared template variance for named
// object types. Hierarchies that do not implement it use safe invariance.
type GenericHierarchy interface {
	TemplateVariance(name string, index int) Variance
}

// SupertypeHierarchy resolves generic arguments while projecting a named type
// through its extends/implements graph.
type SupertypeHierarchy interface {
	AsSupertype(candidate Type, target string) (Type, bool)
}

// TypeAliasHierarchy expands nominal PHPDoc alias references retained in
// persisted signatures.
type TypeAliasHierarchy interface {
	ResolveTypeAlias(value Type) (Type, bool)
}

// CallableHierarchy resolves the effective __invoke signature of a named
// object without coupling the type package to a semantic symbol index.
type CallableHierarchy interface {
	CallableSignature(candidate Type) (Type, bool)
}

// Relations implements semantic relationships between types.
type Relations struct {
	Hierarchy Hierarchy
}

// Joiner incrementally joins a sequence while deferring canonical-text
// construction until Value. It is useful for array literals and other folds
// where every intermediate union is immediately superseded.
type Joiner struct {
	relations Relations
	value     Type
	overflow  []Type
	inline    [maxPreciseLiteralUnionMembers]Type
	count     uint8
	union     bool
}

// NewJoiner starts an incremental join at initial.
func NewJoiner(relations Relations, initial Type) Joiner {
	return Joiner{relations: relations, value: initial}
}

// Add joins value into the accumulated type without caching text for a
// short-lived intermediate union.
func (j *Joiner) Add(value Type) {
	if !j.union {
		switch {
		case j.value.IsUnknown() || value.IsUnknown():
			j.value = Unknown()
		case j.relations.IsSubtype(j.value, value):
			j.value = value
		case j.relations.IsSubtype(value, j.value):
		default:
			j.union = true
			j.appendMembers(j.value)
			j.appendMembers(value)
			j.value = Type{}
			j.widenIfNeeded()
		}
		return
	}
	if value.IsUnknown() {
		j.resetMembers()
		j.value = Unknown()
		return
	}
	if j.membersSubtype(value) {
		j.resetMembers()
		j.value = value
		return
	}
	if j.typeSubtypeMembers(value) {
		return
	}
	j.appendMembers(value)
	j.widenIfNeeded()
}

// Value finishes and returns the canonical accumulated type.
func (j *Joiner) Value() Type {
	if j.union {
		j.value = Union(j.memberValues()...)
		j.resetMembers()
	}
	return j.value
}

func (j *Joiner) membersSubtype(target Type) bool {
	for _, member := range j.memberValues() {
		if !j.relations.IsSubtype(member, target) {
			return false
		}
	}
	return true
}

func (j *Joiner) typeSubtypeMembers(candidate Type) bool {
	if candidate.Kind() == UnionKind {
		for _, member := range candidate.node.args.values() {
			if !j.typeSubtypeMembers(member) {
				return false
			}
		}
		return true
	}
	for _, member := range j.memberValues() {
		if j.relations.IsSubtype(candidate, member) {
			return true
		}
	}
	return false
}

func (j *Joiner) appendMembers(value Type) {
	if value.Kind() == UnionKind {
		for _, member := range value.node.args.values() {
			j.appendMember(member)
		}
		return
	}
	j.appendMember(value)
}

func (j *Joiner) appendMember(value Type) {
	hasBool := false
	members := j.memberValues()
	for _, member := range members {
		if member.Equal(value) {
			return
		}
		if member.Kind() == BoolKind {
			hasBool = true
		}
	}
	if hasBool && (value.Kind() == TrueKind || value.Kind() == FalseKind) {
		return
	}
	if value.Kind() == BoolKind {
		filtered := members[:0]
		for _, member := range members {
			if member.Kind() != TrueKind && member.Kind() != FalseKind {
				filtered = append(filtered, member)
			}
		}
		j.setMemberLength(len(filtered))
	}
	if j.overflow != nil {
		j.overflow = append(j.overflow, value)
		return
	}
	if int(j.count) < len(j.inline) {
		j.inline[j.count] = value
		j.count++
		return
	}
	j.overflow = make([]Type, len(j.inline), len(j.inline)*2)
	copy(j.overflow, j.inline[:])
	j.overflow = append(j.overflow, value)
}

func (j *Joiner) widenIfNeeded() {
	members := j.memberValues()
	if len(members) <= maxPreciseUnionMembers &&
		!literalMembersTooWide(members) {
		return
	}
	j.value = widenComplexUnion(union(members, false))
	j.resetMembers()
}

func (j *Joiner) memberValues() []Type {
	if j.overflow != nil {
		return j.overflow
	}
	return j.inline[:j.count]
}

func (j *Joiner) setMemberLength(length int) {
	if j.overflow != nil {
		clear(j.overflow[length:])
		j.overflow = j.overflow[:length]
		return
	}
	clear(j.inline[length:j.count])
	j.count = uint8(length)
}

func (j *Joiner) resetMembers() {
	clear(j.inline[:j.count])
	clear(j.overflow)
	j.overflow = nil
	j.count = 0
	j.union = false
}

func literalMembersTooWide(members []Type) bool {
	if len(members) <= maxPreciseLiteralUnionMembers {
		return false
	}
	for _, member := range members {
		switch member.Kind() {
		case NullKind, TrueKind, FalseKind,
			LiteralIntKind, LiteralFloatKind, LiteralStringKind:
		default:
			return false
		}
	}
	return true
}

const (
	maxPreciseLiteralUnionMembers = 8
	maxPreciseUnionMembers        = 32
)

func (r Relations) IsSubtype(candidate, target Type) bool {
	candidate = r.resolveTypeAlias(candidate)
	target = r.resolveTypeAlias(target)
	if candidate.Equal(target) {
		return true
	}
	if candidate.IsUnknown() || target.IsUnknown() {
		return false
	}
	if candidate.Kind() == ErrorKind || target.Kind() == ErrorKind {
		return true
	}
	if candidate.Kind() == NeverKind || target.Kind() == MixedKind {
		return true
	}
	if candidate.Kind() == ConditionalKind {
		arguments := candidate.arguments()
		return len(arguments) == 4 &&
			r.IsSubtype(arguments[2], target) &&
			r.IsSubtype(arguments[3], target)
	}
	if candidate.Kind() == UnionKind {
		for _, member := range candidate.arguments() {
			if !r.IsSubtype(member, target) {
				return false
			}
		}
		return true
	}
	// array-key and bool are canonical shorthand unions. Expand them when the
	// target itself is a union so semantic equivalence does not depend on which
	// spelling a PHPDoc producer chose.
	if target.Kind() == UnionKind {
		switch candidate.Kind() {
		case ArrayKeyKind:
			return r.IsSubtype(Int(), target) &&
				r.IsSubtype(String(), target)
		case BoolKind:
			return r.IsSubtype(True(), target) &&
				r.IsSubtype(False(), target)
		case IterableKind:
			arguments := candidate.arguments()
			if len(arguments) != 2 {
				return false
			}
			// PHP's iterable declaration is exactly array|Traversable.
			return r.IsSubtype(Array(arguments[0], arguments[1]), target) &&
				r.IsSubtype(Named("Traversable", arguments...), target)
		}
	}
	if target.Kind() == UnionKind {
		for _, member := range target.arguments() {
			if r.IsSubtype(candidate, member) {
				return true
			}
		}
		return false
	}
	if target.Kind() == IntersectionKind {
		for _, member := range target.arguments() {
			if !r.IsSubtype(candidate, member) {
				return false
			}
		}
		return true
	}
	if candidate.Kind() == IntersectionKind {
		for _, member := range candidate.arguments() {
			if r.IsSubtype(member, target) {
				return true
			}
		}
		return false
	}

	switch candidate.Kind() {
	case TrueKind, FalseKind:
		return target.Kind() == BoolKind
	case LiteralIntKind:
		return target.Kind() == IntKind || target.Kind() == ArrayKeyKind
	case LiteralFloatKind:
		return target.Kind() == FloatKind
	case LiteralStringKind:
		return target.Kind() == StringKind || target.Kind() == ArrayKeyKind
	case IntKind, StringKind:
		if target.Kind() == ArrayKeyKind {
			return true
		}
	case ListKind, NonEmptyListKind:
		if target.Kind() == ListKind ||
			candidate.Kind() == NonEmptyListKind && target.Kind() == NonEmptyListKind {
			return r.argumentsSubtype(
				candidate.arguments(),
				target.arguments(),
			)
		}
		if target.Kind() == ArrayKind ||
			candidate.Kind() == NonEmptyListKind && target.Kind() == NonEmptyArrayKind {
			candidateArgs := candidate.arguments()
			targetArgs := target.arguments()
			return len(candidateArgs) == 1 && len(targetArgs) == 2 &&
				r.IsSubtype(Int(), targetArgs[0]) &&
				r.IsSubtype(candidateArgs[0], targetArgs[1])
		}
		if target.Kind() == IterableKind {
			return r.iterableArgumentsSubtype(Int(), candidate.arguments(), target.arguments())
		}
	case ArrayKind, NonEmptyArrayKind:
		candidateArgs := candidate.arguments()
		if candidate.Kind() == ArrayKind && len(candidateArgs) == 2 &&
			candidateArgs[1].Kind() == NeverKind {
			switch target.Kind() {
			case ArrayKind, ListKind, IterableKind:
				return true
			case ArrayShapeKind:
				for _, field := range target.fields() {
					if !field.Optional {
						return false
					}
				}
				return true
			}
		}
		if target.Kind() == ArrayKind ||
			candidate.Kind() == NonEmptyArrayKind && target.Kind() == NonEmptyArrayKind {
			return r.argumentsSubtype(
				candidateArgs,
				target.arguments(),
			)
		}
		if target.Kind() == IterableKind {
			targetArgs := target.arguments()
			if len(candidateArgs) == 2 && len(targetArgs) == 2 {
				return r.IsSubtype(candidateArgs[0], targetArgs[0]) &&
					r.IsSubtype(candidateArgs[1], targetArgs[1])
			}
		}
	case IterableKind:
		if target.Kind() == IterableKind {
			return r.argumentsSubtype(
				candidate.arguments(),
				target.arguments(),
			)
		}
	case CallableKind:
		if target.Kind() == CallableKind {
			return r.callableSubtype(candidate, target)
		}
	case ArrayShapeKind:
		switch target.Kind() {
		case ArrayKind, IterableKind, NonEmptyArrayKind:
			key, value := r.shapeIterableTypes(candidate)
			targetArguments := target.arguments()
			return (target.Kind() != NonEmptyArrayKind ||
				r.arrayShapeHasRequiredField(candidate)) &&
				len(targetArguments) == 2 &&
				r.IsSubtype(key, targetArguments[0]) &&
				r.IsSubtype(value, targetArguments[1])
		case ListKind:
			return r.arrayShapeListSubtype(candidate, target)
		case NonEmptyListKind:
			return r.arrayShapeListSubtype(candidate, target) &&
				r.arrayShapeKnownNonEmpty(candidate)
		case ArrayShapeKind:
			return r.shapeSubtype(candidate, target)
		}
	case ClassStringKind:
		if target.Kind() == StringKind ||
			target.Kind() == ArrayKeyKind {
			return true
		}
		if target.Kind() == ClassStringKind {
			return r.argumentsSubtype(candidate.arguments(), target.arguments())
		}
	case ObjectKind:
		if target.Kind() == CallableKind {
			hierarchy, ok := r.Hierarchy.(CallableHierarchy)
			if !ok {
				return false
			}
			signature, found := hierarchy.CallableSignature(candidate)
			return found && r.callableSubtype(signature, target)
		}
		if target.Kind() == IterableKind &&
			(sameObjectName(candidate.Name(), "Traversable") ||
				r.Hierarchy != nil &&
					r.Hierarchy.IsSubtypeOf(candidate.Name(), "Traversable")) {
			targetArguments := target.arguments()
			return len(targetArguments) == 2 &&
				r.IsSubtype(ArrayKey(), targetArguments[0]) &&
				r.IsSubtype(Mixed(), targetArguments[1])
		}
		if target.Kind() != ObjectKind {
			return false
		}
		if target.Name() == "" {
			return true
		}
		if candidate.Name() == "" {
			return false
		}
		// PHP class-like names are case-insensitive. Treat equivalent spelling
		// as the same nominal type before asking a hierarchy to project it;
		// otherwise a case-normalizing hierarchy can return the candidate
		// unchanged and recurse forever.
		if sameObjectName(candidate.Name(), target.Name()) {
			return r.objectArgumentsSubtype(
				target.Name(),
				candidate.arguments(),
				target.arguments(),
			)
		}
		if hierarchy, ok := r.Hierarchy.(SupertypeHierarchy); ok {
			if projected, found := hierarchy.AsSupertype(
				candidate,
				target.Name(),
			); found {
				return r.IsSubtype(projected, target)
			}
		}
		if len(target.arguments()) > 0 {
			return false
		}
		return r.Hierarchy != nil &&
			r.Hierarchy.IsSubtypeOf(candidate.Name(), target.Name())
	case ObjectShapeKind:
		if target.Kind() == ObjectShapeKind {
			return r.shapeSubtype(candidate, target)
		}
		return target.Kind() == ObjectKind && target.Name() == ""
	}
	return false
}

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
