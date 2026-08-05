package protocol

// Color uses normalized RGBA components in the inclusive range [0, 1].
type Color struct {
	Red   float64 `json:"red"`
	Green float64 `json:"green"`
	Blue  float64 `json:"blue"`
	Alpha float64 `json:"alpha"`
}

type DocumentColorParams struct {
	TextDocument  TextDocumentIdentifier `json:"textDocument"`
	WorkDoneToken any                    `json:"workDoneToken,omitempty"`
}

type ColorInformation struct {
	Range Range `json:"range"`
	Color Color `json:"color"`
}

type ColorPresentationParams struct {
	TextDocument  TextDocumentIdentifier `json:"textDocument"`
	Color         Color                  `json:"color"`
	Range         Range                  `json:"range"`
	WorkDoneToken any                    `json:"workDoneToken,omitempty"`
}

type ColorPresentation struct {
	Label               string     `json:"label"`
	TextEdit            *TextEdit  `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit `json:"additionalTextEdits,omitempty"`
}
