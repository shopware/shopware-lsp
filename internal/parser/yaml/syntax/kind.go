package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const yamlBase Kind = 8192

const (
	TkWhitespace Kind = yamlBase + iota
	TkComment
	TkIndent
	TkLineBreak
	TkDocumentStart
	TkDocumentEnd
	TkDirective
	TkDash
	TkColon
	TkComma
	TkOpenBracket
	TkCloseBracket
	TkOpenBrace
	TkCloseBrace
	TkPlainScalar
	TkSingleQuotedScalar
	TkDoubleQuotedScalar
	TkBlockScalar
	TkUnknown

	YamlStream
	YamlDocument
	YamlMapping
	YamlFlowMapping
	YamlPair
	YamlSequence
	YamlFlowSequence
	YamlSequenceItem
	YamlScalar
	YamlNull
	Error

	yamlKindCount
)

func init() {
	names := make([]string, int(yamlKindCount-yamlBase))
	texts := make([]string, len(names))

	set := func(kind Kind, name, text string) {
		index := int(kind - yamlBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "YAML_WHITESPACE", "whitespace")
	set(TkComment, "YAML_COMMENT", "comment")
	set(TkIndent, "YAML_INDENT", "indentation")
	set(TkLineBreak, "YAML_LINE_BREAK", "line break")
	set(TkDocumentStart, "YAML_DOCUMENT_START", "---")
	set(TkDocumentEnd, "YAML_DOCUMENT_END", "...")
	set(TkDirective, "YAML_DIRECTIVE", "directive")
	set(TkDash, "YAML_DASH", "-")
	set(TkColon, "YAML_COLON", ":")
	set(TkComma, "YAML_COMMA", ",")
	set(TkOpenBracket, "YAML_OPEN_BRACKET", "[")
	set(TkCloseBracket, "YAML_CLOSE_BRACKET", "]")
	set(TkOpenBrace, "YAML_OPEN_BRACE", "{")
	set(TkCloseBrace, "YAML_CLOSE_BRACE", "}")
	set(TkPlainScalar, "YAML_PLAIN_SCALAR_TOKEN", "plain scalar")
	set(TkSingleQuotedScalar, "YAML_SINGLE_QUOTED_SCALAR_TOKEN", "single-quoted scalar")
	set(TkDoubleQuotedScalar, "YAML_DOUBLE_QUOTED_SCALAR_TOKEN", "double-quoted scalar")
	set(TkBlockScalar, "YAML_BLOCK_SCALAR_TOKEN", "block scalar")
	set(TkUnknown, "YAML_UNKNOWN", "unknown token")

	set(YamlStream, "YAML_STREAM", "")
	set(YamlDocument, "YAML_DOCUMENT", "")
	set(YamlMapping, "YAML_MAPPING", "")
	set(YamlFlowMapping, "YAML_FLOW_MAPPING", "")
	set(YamlPair, "YAML_PAIR", "")
	set(YamlSequence, "YAML_SEQUENCE", "")
	set(YamlFlowSequence, "YAML_FLOW_SEQUENCE", "")
	set(YamlSequenceItem, "YAML_SEQUENCE_ITEM", "")
	set(YamlScalar, "YAML_SCALAR", "")
	set(YamlNull, "YAML_NULL", "")
	set(Error, "YAML_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:        "yaml",
		Base:        yamlBase,
		KindNames:   names,
		TokenTexts:  texts,
		FirstNode:   YamlStream,
		TriviaKinds: []Kind{TkWhitespace, TkComment},
	})
}
