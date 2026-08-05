// Package doctrine provides editor-independent Doctrine ORM metadata and
// framework semantics. It intentionally models source declarations rather than
// Doctrine's runtime metadata objects so PHP attributes and external mapping
// files can participate in the same LSP features.
package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type ModelKind uint8

const (
	EntityModel ModelKind = iota
	MappedSuperclassModel
	EmbeddableModel
	DocumentModel
)

func (kind ModelKind) String() string {
	switch kind {
	case MappedSuperclassModel:
		return "mapped superclass"
	case EmbeddableModel:
		return "embeddable"
	case DocumentModel:
		return "document"
	default:
		return "entity"
	}
}

type SourceKind uint8

const (
	PHPAttributeSource SourceKind = iota
	PHPAnnotationSource
	XMLSource
	YAMLSource
)

type Field struct {
	Name          string
	Column        string
	Type          string
	PHPType       string
	EnumType      string
	Relation      string
	RelationType  string
	EmbeddedClass string
	ColumnPrefix  string
	Class         string
	File          string
	Range         cst.TextRange
	TypeRange     cst.TextRange
	EnumTypeRange cst.TextRange
	RelationRange cst.TextRange
	EmbeddedRange cst.TextRange
}

func (field Field) IsAssociation() bool {
	return field.Relation != "" || field.RelationType != ""
}

func (field Field) IsEmbedded() bool {
	return field.EmbeddedClass != ""
}

type LifecycleCallback struct {
	Event  string
	Method string
	Class  string
	File   string
	Range  cst.TextRange
}

type DiscriminatorMapping struct {
	Value      string
	Class      string
	File       string
	ValueRange cst.TextRange
	ClassRange cst.TextRange
}

type TableConstraintKind uint8

const (
	IndexConstraint TableConstraintKind = iota
	UniqueConstraint
)

type TableConstraintReference struct {
	Name  string
	Range cst.TextRange
}

type TableConstraint struct {
	Name      string
	Kind      TableConstraintKind
	File      string
	NameRange cst.TextRange
	Fields    []TableConstraintReference
	Columns   []TableConstraintReference
}

type Model struct {
	Class               string
	Parent              string
	Repository          string
	Table               string
	InheritanceType     string
	DiscriminatorColumn string
	DiscriminatorType   string
	Kind                ModelKind
	Source              SourceKind
	File                string
	Range               cst.TextRange
	NameRange           cst.TextRange
	RepositoryRange     cst.TextRange
	Fields              []Field
	Callbacks           []LifecycleCallback
	DiscriminatorMap    []DiscriminatorMapping
	TableConstraints    []TableConstraint
}

func normalizeClass(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), `\`)
}

func sameClass(left, right string) bool {
	return strings.EqualFold(normalizeClass(left), normalizeClass(right))
}
