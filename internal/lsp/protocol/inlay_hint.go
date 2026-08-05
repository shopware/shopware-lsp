package protocol

type InlayHintParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range         Range       `json:"range"`
	WorkDoneToken interface{} `json:"workDoneToken,omitempty"`
}

type InlayHintKind int

const (
	InlayHintKindType      InlayHintKind = 1
	InlayHintKindParameter InlayHintKind = 2
)

type InlayHint struct {
	Position     Position      `json:"position"`
	Label        any           `json:"label"`
	Kind         InlayHintKind `json:"kind,omitempty"`
	Tooltip      string        `json:"tooltip,omitempty"`
	PaddingLeft  bool          `json:"paddingLeft,omitempty"`
	PaddingRight bool          `json:"paddingRight,omitempty"`
	Data         interface{}   `json:"data,omitempty"`
}

// InlayHintLabelPart is a clickable segment of an inlay label. Location is
// followed by clients when the segment is selected.
type InlayHintLabelPart struct {
	Value    string    `json:"value"`
	Tooltip  string    `json:"tooltip,omitempty"`
	Location *Location `json:"location,omitempty"`
	Command  *Command  `json:"command,omitempty"`
}
