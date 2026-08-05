package protocol

type SignatureHelpParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
	Context  struct {
		TriggerKind      int            `json:"triggerKind,omitempty"`
		TriggerCharacter string         `json:"triggerCharacter,omitempty"`
		IsRetrigger      bool           `json:"isRetrigger,omitempty"`
		ActiveHelp       *SignatureHelp `json:"activeSignatureHelp,omitempty"`
	} `json:"context,omitempty"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   *MarkupContent         `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

type ParameterInformation struct {
	Label         string         `json:"label"`
	Documentation *MarkupContent `json:"documentation,omitempty"`
}
