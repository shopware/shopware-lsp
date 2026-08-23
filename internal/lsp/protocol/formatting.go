package protocol

// DocumentFormattingParams identifies a document and the editor's indentation
// preferences for a textDocument/formatting request.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// FormattingOptions are the standard LSP document formatting options. Twig
// formatting currently consumes TabSize and InsertSpaces; the remaining flags
// are retained on the wire boundary for forward-compatible behavior.
type FormattingOptions struct {
	TabSize                int  `json:"tabSize"`
	InsertSpaces           bool `json:"insertSpaces"`
	TrimTrailingWhitespace bool `json:"trimTrailingWhitespace,omitempty"`
	InsertFinalNewline     bool `json:"insertFinalNewline,omitempty"`
	TrimFinalNewlines      bool `json:"trimFinalNewlines,omitempty"`
}
