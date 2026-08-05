// Package semantic contains PHP symbols, scopes, references, and immutable
// document snapshots. It is independent from persistence and LSP transport.
package semantic

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// SymbolID is stable across index generations for the same declaration.
type SymbolID string

type SymbolKind uint8

const (
	NamespaceSymbol SymbolKind = iota
	ClassSymbol
	InterfaceSymbol
	TraitSymbol
	EnumSymbol
	MethodSymbol
	FunctionSymbol
	ClosureSymbol
	PropertySymbol
	ParameterSymbol
	ClassConstantSymbol
	GlobalConstantSymbol
	EnumCaseSymbol
	LocalSymbol
	TemplateSymbol
	TypeAliasSymbol
)

type Visibility uint8

const (
	Public Visibility = iota
	Protected
	Private
)

type Flags uint32

const (
	StaticFlag Flags = 1 << iota
	FinalFlag
	AbstractFlag
	ReadonlyFlag
	ByReferenceFlag
	VariadicFlag
	PromotedFlag
	DeprecatedFlag
	InternalFlag
	SyntheticFlag
	GeneratedStubFlag
	ClassAliasFlag
)

func (f Flags) Has(flag Flags) bool {
	return f&flag != 0
}

// Parameter captures a function/method parameter in declaration order.
type Parameter struct {
	ID             SymbolID
	Name           string
	Type           types.Type
	NativeType     types.Type
	DocType        types.Type
	AssistantTags  []string
	Attributes     []Attribute
	DefaultValue   *AttributeValue
	Range          cst.TextRange
	SelectionRange cst.TextRange
	DefaultRange   cst.TextRange
	Flags          Flags
	Optional       bool
}

// TemplateParameter represents a PHPDoc generic parameter.
type TemplateParameter struct {
	Name          string
	Bound         types.Type
	Default       types.Type
	Covariant     bool
	Contravariant bool
}

// AttributeValueKind describes the constant subset of PHP expressions that
// can be retained from an attribute argument without keeping CST nodes alive.
type AttributeValueKind uint8

const (
	AttributeValueUnknown AttributeValueKind = iota
	AttributeValueString
	AttributeValueInteger
	AttributeValueFloat
	AttributeValueBool
	AttributeValueNull
	AttributeValueConstant
	AttributeValueClassConstant
	AttributeValueArray
	AttributeValueExpression
)

// AttributeValue is a lossless-enough, immutable representation of a PHP
// attribute constant expression. Expression retains valid insertion text;
// Value contains the decoded scalar or resolved constant identity where one
// is available. Array values recursively retain their ordered entries.
type AttributeValue struct {
	Kind       AttributeValueKind
	Value      string
	Expression string
	Items      []AttributeArrayItem
}

// AttributeArrayItem preserves both list and keyed PHP array entries.
type AttributeArrayItem struct {
	Key    AttributeValue
	HasKey bool
	Value  AttributeValue
}

// AttributeArgument preserves declaration order and optional PHP 8 named
// argument syntax.
type AttributeArgument struct {
	Name  string
	Value AttributeValue
	Range cst.TextRange
}

// Attribute records a resolved attribute name, its constant arguments, and
// the declaration range.
type Attribute struct {
	Name      string
	Arguments []AttributeArgument
	Range     cst.TextRange
}

// ConstantArrayItem preserves a top-level key/value declaration from a PHP
// array constant. Framework integrations use this semantic metadata without
// reopening or reparsing vendor source files.
type ConstantArrayItem struct {
	Key        string
	KeyRange   cst.TextRange
	Value      string
	ValueRange cst.TextRange
	Type       types.Type
}

// LiteralReturn records a scalar/null return expression declared directly by
// a function or method. Nested closure/function returns are excluded.
type LiteralReturn struct {
	Value string
	Range cst.TextRange
	Type  types.Type
}

// ConstantReturn records a class-constant return expression declared directly
// by a function or method. Receiver contains either a resolved class name or
// one of self, static, and parent for context-dependent resolution.
type ConstantReturn struct {
	Receiver string
	Name     string
	Range    cst.TextRange
}

// TraitAlias records a method adaptation declared by a trait use statement.
// Trait and Method identify the original declaration; Alias is the effective
// method name exposed by the consuming class. A visibility-only adaptation
// keeps Alias equal to Method.
type TraitAlias struct {
	Trait         string
	Method        string
	Alias         string
	Visibility    Visibility
	HasVisibility bool
}

// TypeAssertion describes a PHPStan/Psalm conditional assertion made by a
// function or method when its boolean result has the selected value.
type TypeAssertion struct {
	Target      string
	Type        types.Type
	WhenTrue    bool
	Conditional bool
	Negated     bool
}

// Symbol is a declaration visible to semantic queries.
type Symbol struct {
	ID                 SymbolID
	Kind               SymbolKind
	Name               string
	FullyQualified     string
	Container          SymbolID
	Path               string
	Range              cst.TextRange
	SelectionRange     cst.TextRange
	BodyRange          cst.TextRange
	Visibility         Visibility
	WriteVisibility    Visibility
	HasWriteVisibility bool
	Flags              Flags

	Type       types.Type
	NativeType types.Type
	DocType    types.Type
	ReturnType types.Type

	Parameters      []Parameter
	Templates       []TemplateParameter
	Extends         []string
	Implements      []string
	Traits          []string
	ExtendsTypes    []types.Type
	ImplementsTypes []types.Type
	TraitTypes      []types.Type
	TraitAliases    []TraitAlias
	Throws          []types.Type
	Assertions      []TypeAssertion
	Attributes      []Attribute
	ConstantArray   []ConstantArrayItem
	LiteralReturns  []LiteralReturn
	ConstantReturns []ConstantReturn
	DocSummary      string
}

func (s Symbol) IsClassLike() bool {
	switch s.Kind {
	case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
		return true
	default:
		return false
	}
}

func (s Symbol) IsFunctionLike() bool {
	return s.Kind == MethodSymbol || s.Kind == FunctionSymbol || s.Kind == ClosureSymbol
}

// AnonymousClassName returns the internal, workspace-unique name used for an
// anonymous class declaration. PHP does not expose a source-level class name,
// but the semantic graph still needs a stable nominal identity so methods,
// properties, and $this can be resolved without treating the class body as
// part of its enclosing function.
func AnonymousClassName(path string, start uint32) string {
	return fmt.Sprintf("{anonymous@%s:%d}", path, start)
}

// NewSymbolID creates a deterministic declaration ID. The source offset
// disambiguates anonymous/local declarations while named declarations remain
// stable when unrelated declarations are added elsewhere in the file.
func NewSymbolID(kind SymbolKind, fullyQualified, path string, start uint32) SymbolID {
	var builder strings.Builder
	identifier := strings.TrimPrefix(fullyQualified, "\\")
	if fullyQualified != "" {
		// Kind plus separator. Unicode case folding may occasionally require
		// the builder to grow, but ASCII identifiers use the exact useful
		// reservation instead of also reserving the unused source path.
		builder.Grow(len(identifier) + 4)
	} else {
		// Kind, two separators, and a uint32 decimal source offset.
		builder.Grow(len(path) + 14)
	}
	builder.WriteString(strconv.Itoa(int(kind)))
	builder.WriteByte(':')
	if fullyQualified != "" {
		writeLowerIdentifier(&builder, identifier)
	} else {
		builder.WriteString(path)
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatUint(uint64(start), 10))
	}
	return SymbolID(builder.String())
}

func writeLowerIdentifier(builder *strings.Builder, value string) {
	for index := 0; index < len(value); index++ {
		if value[index] >= utf8.RuneSelf {
			builder.WriteString(strings.ToLower(value))
			return
		}
	}
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		builder.WriteByte(current)
	}
}
