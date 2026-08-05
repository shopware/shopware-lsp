// Package types defines the semantic PHP type algebra.
//
// It deliberately has no dependency on the PHP index or CST. Syntax frontends
// produce unresolved type expressions, binders resolve names, and workspace
// services provide hierarchy information to Relations.
package types

import "fmt"

// Kind identifies one semantic type constructor.
type Kind uint8

const (
	UnknownKind Kind = iota
	ErrorKind
	NeverKind
	MixedKind
	VoidKind
	NullKind
	BoolKind
	TrueKind
	FalseKind
	IntKind
	FloatKind
	StringKind
	ObjectKind
	ResourceKind
	ArrayKeyKind
	ArrayKind
	NonEmptyArrayKind
	ListKind
	NonEmptyListKind
	IterableKind
	CallableKind
	ClassStringKind
	TemplateKind
	SelfKind
	StaticKind
	ParentKind
	LiteralIntKind
	LiteralFloatKind
	LiteralStringKind
	ArrayShapeKind
	ObjectShapeKind
	UnionKind
	IntersectionKind
	ConditionalKind
)

func (k Kind) String() string {
	switch k {
	case UnknownKind:
		return "unknown"
	case ErrorKind:
		return "error"
	case NeverKind:
		return "never"
	case MixedKind:
		return "mixed"
	case VoidKind:
		return "void"
	case NullKind:
		return "null"
	case BoolKind:
		return "bool"
	case TrueKind:
		return "true"
	case FalseKind:
		return "false"
	case IntKind:
		return "int"
	case FloatKind:
		return "float"
	case StringKind:
		return "string"
	case ObjectKind:
		return "object"
	case ResourceKind:
		return "resource"
	case ArrayKeyKind:
		return "array-key"
	case ArrayKind:
		return "array"
	case NonEmptyArrayKind:
		return "non-empty-array"
	case ListKind:
		return "list"
	case NonEmptyListKind:
		return "non-empty-list"
	case IterableKind:
		return "iterable"
	case CallableKind:
		return "callable"
	case ClassStringKind:
		return "class-string"
	case TemplateKind:
		return "template"
	case SelfKind:
		return "self"
	case StaticKind:
		return "static"
	case ParentKind:
		return "parent"
	case LiteralIntKind:
		return "literal-int"
	case LiteralFloatKind:
		return "literal-float"
	case LiteralStringKind:
		return "literal-string"
	case ArrayShapeKind:
		return "array-shape"
	case ObjectShapeKind:
		return "object-shape"
	case UnionKind:
		return "union"
	case IntersectionKind:
		return "intersection"
	case ConditionalKind:
		return "conditional"
	default:
		return fmt.Sprintf("type-kind(%d)", k)
	}
}
