package environment

import "github.com/shopware/shopware-lsp/internal/parser/cst"

type OccurrenceKind uint8

const (
	DeclarationOccurrence OccurrenceKind = iota
	ReferenceOccurrence
)

type SourceKind uint8

const (
	DotEnvSource SourceKind = iota
	DockerComposeSource
	DockerfileSource
	SymfonyEnvSource
)

type Occurrence struct {
	Kind       OccurrenceKind
	Source     SourceKind
	Name       string
	Value      string
	File       string
	Range      cst.TextRange
	NameRange  cst.TextRange
	Processors []string
}

type Variable struct {
	Name         string
	Declarations []Occurrence
	References   []Occurrence
}

type Reference struct {
	Name       string
	Range      cst.TextRange
	NameRange  cst.TextRange
	Processors []string
}
