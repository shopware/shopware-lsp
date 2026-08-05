package style

import "github.com/shopware/shopware-lsp/internal/parser/cst"

type ClassOccurrenceKind uint8

const (
	ClassDeclaration ClassOccurrenceKind = iota
	ClassUsage
)

type SourcePosition struct {
	Line      int
	Character int
}

// ClassOccurrence is one static CSS class declaration or markup usage. Range
// remains available for live-document matching while Start and End avoid
// reopening indexed files when LSP locations are requested.
type ClassOccurrence struct {
	Name  string
	File  string
	Range cst.TextRange
	Start SourcePosition
	End   SourcePosition
	Kind  ClassOccurrenceKind
}

type ClassCatalog struct {
	Name        string
	File        string
	Occurrences []ClassOccurrence
}
