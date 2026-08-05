package protocol

const (
	FoldingRangeKindComment = "comment"
	FoldingRangeKindImports = "imports"
	FoldingRangeKindRegion  = "region"
)

type FoldingRangeParams struct {
	TextDocument  TextDocumentIdentifier `json:"textDocument"`
	WorkDoneToken any                    `json:"workDoneToken,omitempty"`
}

// FoldingRange is line based unless the optional character positions are set.
// Administration folding intentionally stays line based so it works for
// clients that advertise lineFoldingOnly.
type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter *int   `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   *int   `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
	CollapsedText  string `json:"collapsedText,omitempty"`
}
