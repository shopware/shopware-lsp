package protocol

// DocumentHighlightParams identifies a symbol in an open text document whose
// same-document semantic occurrences should be highlighted.
type DocumentHighlightParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type DocumentHighlightKind int

const (
	DocumentHighlightText  DocumentHighlightKind = 1
	DocumentHighlightRead  DocumentHighlightKind = 2
	DocumentHighlightWrite DocumentHighlightKind = 3
)

type DocumentHighlight struct {
	Range Range                 `json:"range"`
	Kind  DocumentHighlightKind `json:"kind,omitempty"`
}
