package protocol

type SelectionRangeParams struct {
	TextDocument  TextDocumentIdentifier `json:"textDocument"`
	Positions     []Position             `json:"positions"`
	WorkDoneToken any                    `json:"workDoneToken,omitempty"`
}

// SelectionRange is one nested source selection. Parent must contain Range and
// forms the expansion sequence used by editor "Expand Selection" commands.
type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}
