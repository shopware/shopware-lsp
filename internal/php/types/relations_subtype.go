package types

// IsSubtype reports whether every value represented by candidate is accepted
// by target. Composite-type dispatch is kept separate from kind-specific
// rules so adding one relation does not extend a single control-flow tree.
func (r Relations) IsSubtype(candidate, target Type) bool {
	candidate = r.resolveTypeAlias(candidate)
	target = r.resolveTypeAlias(target)
	if result, handled := r.compositeSubtype(candidate, target); handled {
		return result
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
		return target.Kind() == ArrayKeyKind
	case ListKind, NonEmptyListKind:
		return r.listSubtype(candidate, target)
	case ArrayKind, NonEmptyArrayKind:
		return r.arraySubtype(candidate, target)
	case IterableKind:
		return target.Kind() == IterableKind &&
			r.argumentsSubtype(candidate.arguments(), target.arguments())
	case CallableKind:
		return target.Kind() == CallableKind && r.callableSubtype(candidate, target)
	case ArrayShapeKind:
		return r.arrayShapeSubtype(candidate, target)
	case ClassStringKind:
		return r.classStringSubtype(candidate, target)
	case ObjectKind:
		return r.objectSubtype(candidate, target)
	case ObjectShapeKind:
		return target.Kind() == ObjectShapeKind && r.shapeSubtype(candidate, target) ||
			target.Kind() == ObjectKind && target.Name() == ""
	default:
		return false
	}
}

func (r Relations) compositeSubtype(candidate, target Type) (bool, bool) {
	switch {
	case candidate.Equal(target):
		return true, true
	case candidate.IsUnknown() || target.IsUnknown():
		return false, true
	case candidate.Kind() == ErrorKind || target.Kind() == ErrorKind:
		return true, true
	case candidate.Kind() == NeverKind || target.Kind() == MixedKind:
		return true, true
	case candidate.Kind() == ConditionalKind:
		arguments := candidate.arguments()
		return len(arguments) == 4 &&
			r.IsSubtype(arguments[2], target) &&
			r.IsSubtype(arguments[3], target), true
	case candidate.Kind() == UnionKind:
		return r.everySubtype(candidate.arguments(), target), true
	}

	if target.Kind() == UnionKind {
		if result, shorthand := r.shorthandSubtypeOfUnion(candidate, target); shorthand {
			return result, true
		}
		return r.anyTargetAccepts(candidate, target.arguments()), true
	}
	if target.Kind() == IntersectionKind {
		return r.everyTargetAccepts(candidate, target.arguments()), true
	}
	if candidate.Kind() == IntersectionKind {
		return r.anySubtype(candidate.arguments(), target), true
	}
	return false, false
}

func (r Relations) shorthandSubtypeOfUnion(candidate, target Type) (bool, bool) {
	switch candidate.Kind() {
	case ArrayKeyKind:
		return r.IsSubtype(Int(), target) && r.IsSubtype(String(), target), true
	case BoolKind:
		return r.IsSubtype(True(), target) && r.IsSubtype(False(), target), true
	case IterableKind:
		arguments := candidate.arguments()
		if len(arguments) != 2 {
			return false, true
		}
		// PHP's iterable declaration is exactly array|Traversable.
		return r.IsSubtype(Array(arguments[0], arguments[1]), target) &&
			r.IsSubtype(Named("Traversable", arguments...), target), true
	default:
		return false, false
	}
}

func (r Relations) everySubtype(candidates []Type, target Type) bool {
	for _, candidate := range candidates {
		if !r.IsSubtype(candidate, target) {
			return false
		}
	}
	return true
}

func (r Relations) anySubtype(candidates []Type, target Type) bool {
	for _, candidate := range candidates {
		if r.IsSubtype(candidate, target) {
			return true
		}
	}
	return false
}

func (r Relations) everyTargetAccepts(candidate Type, targets []Type) bool {
	for _, target := range targets {
		if !r.IsSubtype(candidate, target) {
			return false
		}
	}
	return true
}

func (r Relations) anyTargetAccepts(candidate Type, targets []Type) bool {
	for _, target := range targets {
		if r.IsSubtype(candidate, target) {
			return true
		}
	}
	return false
}

func (r Relations) listSubtype(candidate, target Type) bool {
	if target.Kind() == ListKind ||
		candidate.Kind() == NonEmptyListKind && target.Kind() == NonEmptyListKind {
		return r.argumentsSubtype(candidate.arguments(), target.arguments())
	}
	if target.Kind() == ArrayKind ||
		candidate.Kind() == NonEmptyListKind && target.Kind() == NonEmptyArrayKind {
		candidateArgs := candidate.arguments()
		targetArgs := target.arguments()
		return len(candidateArgs) == 1 && len(targetArgs) == 2 &&
			r.IsSubtype(Int(), targetArgs[0]) &&
			r.IsSubtype(candidateArgs[0], targetArgs[1])
	}
	return target.Kind() == IterableKind && r.iterableArgumentsSubtype(
		Int(),
		candidate.arguments(),
		target.arguments(),
	)
}

func (r Relations) arraySubtype(candidate, target Type) bool {
	candidateArgs := candidate.arguments()
	if candidate.Kind() == ArrayKind && len(candidateArgs) == 2 &&
		candidateArgs[1].Kind() == NeverKind {
		return r.emptyArraySubtype(target)
	}
	if target.Kind() == ArrayKind ||
		candidate.Kind() == NonEmptyArrayKind && target.Kind() == NonEmptyArrayKind {
		return r.argumentsSubtype(candidateArgs, target.arguments())
	}
	if target.Kind() != IterableKind {
		return false
	}
	targetArgs := target.arguments()
	return len(candidateArgs) == 2 && len(targetArgs) == 2 &&
		r.IsSubtype(candidateArgs[0], targetArgs[0]) &&
		r.IsSubtype(candidateArgs[1], targetArgs[1])
}

func (r Relations) emptyArraySubtype(target Type) bool {
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
	default:
		return false
	}
}

func (r Relations) arrayShapeSubtype(candidate, target Type) bool {
	switch target.Kind() {
	case ArrayKind, IterableKind, NonEmptyArrayKind:
		key, value := r.shapeIterableTypes(candidate)
		targetArguments := target.arguments()
		return (target.Kind() != NonEmptyArrayKind || r.arrayShapeHasRequiredField(candidate)) &&
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
	default:
		return false
	}
}

func (r Relations) classStringSubtype(candidate, target Type) bool {
	if target.Kind() == StringKind || target.Kind() == ArrayKeyKind {
		return true
	}
	return target.Kind() == ClassStringKind &&
		r.argumentsSubtype(candidate.arguments(), target.arguments())
}

func (r Relations) objectSubtype(candidate, target Type) bool {
	if target.Kind() == CallableKind {
		hierarchy, ok := r.Hierarchy.(CallableHierarchy)
		if !ok {
			return false
		}
		signature, found := hierarchy.CallableSignature(candidate)
		return found && r.callableSubtype(signature, target)
	}
	if target.Kind() == IterableKind {
		return r.objectIterableSubtype(candidate, target)
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
	if sameObjectName(candidate.Name(), target.Name()) {
		return r.objectArgumentsSubtype(
			target.Name(),
			candidate.arguments(),
			target.arguments(),
		)
	}
	if hierarchy, ok := r.Hierarchy.(SupertypeHierarchy); ok {
		if projected, found := hierarchy.AsSupertype(candidate, target.Name()); found {
			return r.IsSubtype(projected, target)
		}
	}
	if len(target.arguments()) > 0 {
		return false
	}
	return r.Hierarchy != nil &&
		r.Hierarchy.IsSubtypeOf(candidate.Name(), target.Name())
}

func (r Relations) objectIterableSubtype(candidate, target Type) bool {
	if !sameObjectName(candidate.Name(), "Traversable") &&
		(r.Hierarchy == nil || !r.Hierarchy.IsSubtypeOf(candidate.Name(), "Traversable")) {
		return false
	}
	targetArguments := target.arguments()
	return len(targetArguments) == 2 &&
		r.IsSubtype(ArrayKey(), targetArguments[0]) &&
		r.IsSubtype(Mixed(), targetArguments[1])
}
