package protocol

// SemanticTokensParams identifies the document for a full semantic-token
// request.
type SemanticTokensParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	WorkDoneToken      interface{} `json:"workDoneToken,omitempty"`
	PartialResultToken interface{} `json:"partialResultToken,omitempty"`
}

// SemanticTokens is the relative, delta-encoded token stream defined by LSP.
type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

// SemanticTokensLegend maps numeric token types and modifier bit positions in
// SemanticTokens.Data to their protocol names.
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

const (
	SemanticTokenKeyword uint32 = iota
	SemanticTokenProperty
	SemanticTokenType
	SemanticTokenVariable
	SemanticTokenString
	SemanticTokenNumber
	SemanticTokenClass
	SemanticTokenFunction
	SemanticTokenOperator
)

var SemanticTokenTypes = []string{
	"keyword",
	"property",
	"type",
	"variable",
	"string",
	"number",
	"class",
	"function",
	"operator",
}

var SemanticTokenModifiers = []string{}
