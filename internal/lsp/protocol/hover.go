package protocol

// HoverParams represents the parameters for a hover request
type HoverParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
	WorkDoneToken interface{} `json:"workDoneToken,omitempty"`
}

// Hover represents the result of a hover request
type Hover struct {
	// The hover's content
	Contents MarkupContent `json:"contents"`

	// An optional range inside the text document that is used to
	// visualize the hover, e.g. by changing the background color
	Range *Range `json:"range,omitempty"`
}

// MarkupContent represents a string value which content is interpreted based on its kind flag
type MarkupContent struct {
	// The type of the Markup
	Kind MarkupKind `json:"kind"`

	// The content itself
	Value string `json:"value"`
}

// MarkupKind describes the content type that a client supports in various
// result literals like `Hover`, `ParameterInfo` or `CompletionItem`
type MarkupKind string

const (
	// PlainText plain text is supported as a content format
	PlainText MarkupKind = "plaintext"

	// Markdown markdown is supported as a content format
	Markdown MarkupKind = "markdown"
)
