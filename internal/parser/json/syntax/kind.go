package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const jsonBase Kind = 4096

const (
	TkWhitespace Kind = jsonBase + iota
	TkLineBreak
	TkOpenBrace
	TkCloseBrace
	TkOpenBracket
	TkCloseBracket
	TkColon
	TkComma
	TkString
	TkNumber
	TkTrue
	TkFalse
	TkNull
	TkUnknown

	JsonDocument
	JsonObject
	JsonPair
	JsonArray
	JsonString
	JsonNumber
	JsonBoolean
	JsonNull
	Error

	jsonKindCount
)

func init() {
	names := make([]string, int(jsonKindCount-jsonBase))
	texts := make([]string, len(names))

	set := func(kind Kind, name, text string) {
		index := int(kind - jsonBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "JSON_WHITESPACE", "whitespace")
	set(TkLineBreak, "JSON_LINE_BREAK", "line break")
	set(TkOpenBrace, "JSON_OPEN_BRACE", "{")
	set(TkCloseBrace, "JSON_CLOSE_BRACE", "}")
	set(TkOpenBracket, "JSON_OPEN_BRACKET", "[")
	set(TkCloseBracket, "JSON_CLOSE_BRACKET", "]")
	set(TkColon, "JSON_COLON", ":")
	set(TkComma, "JSON_COMMA", ",")
	set(TkString, "JSON_STRING_TOKEN", "string")
	set(TkNumber, "JSON_NUMBER_TOKEN", "number")
	set(TkTrue, "JSON_TRUE", "true")
	set(TkFalse, "JSON_FALSE", "false")
	set(TkNull, "JSON_NULL", "null")
	set(TkUnknown, "JSON_UNKNOWN", "unknown token")

	set(JsonDocument, "JSON_DOCUMENT", "")
	set(JsonObject, "JSON_OBJECT", "")
	set(JsonPair, "JSON_PAIR", "")
	set(JsonArray, "JSON_ARRAY", "")
	set(JsonString, "JSON_STRING", "")
	set(JsonNumber, "JSON_NUMBER", "")
	set(JsonBoolean, "JSON_BOOLEAN", "")
	set(JsonNull, "JSON_NULL_VALUE", "")
	set(Error, "JSON_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:        "json",
		Base:        jsonBase,
		KindNames:   names,
		TokenTexts:  texts,
		FirstNode:   JsonDocument,
		TriviaKinds: []Kind{TkWhitespace, TkLineBreak},
	})
}
