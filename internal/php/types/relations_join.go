package types

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
