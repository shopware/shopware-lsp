package protocol

// DocumentLinkParams identifies the open document whose static links should
// be resolved.
type DocumentLinkParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	WorkDoneToken      interface{} `json:"workDoneToken,omitempty"`
	PartialResultToken interface{} `json:"partialResultToken,omitempty"`
}

// DocumentLink is a clickable source range. Target is omitted when a client
// must call documentLink/resolve; Shopware LSP currently resolves eagerly.
type DocumentLink struct {
	Range   Range       `json:"range"`
	Target  string      `json:"target,omitempty"`
	Tooltip string      `json:"tooltip,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}
