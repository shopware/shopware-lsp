package protocol

// DocumentSymbolParams identifies the open document whose hierarchical
// outline is requested.
type DocumentSymbolParams struct {
	TextDocument       TextDocumentIdentifier `json:"textDocument"`
	WorkDoneToken      any                    `json:"workDoneToken,omitempty"`
	PartialResultToken any                    `json:"partialResultToken,omitempty"`
}

// DocumentSymbol is one hierarchical declaration in a document. Range spans
// the complete declaration while SelectionRange identifies its name.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}
