package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const xpathBase Kind = 28672

const (
	TkWhitespace Kind = xpathBase + iota
	TkLineBreak
	TkName
	TkNumber
	TkString
	TkSlash
	TkDoubleSlash
	TkDot
	TkDoubleDot
	TkAt
	TkDollar
	TkColon
	TkDoubleColon
	TkPipe
	TkPlus
	TkMinus
	TkStar
	TkEquals
	TkNotEquals
	TkLess
	TkLessEquals
	TkGreater
	TkGreaterEquals
	TkOpenParen
	TkCloseParen
	TkOpenBracket
	TkCloseBracket
	TkComma
	TkUnknown

	XPathDocument
	XPathGroup
	XPathPredicate
	Error

	xpathKindCount
)

func init() {
	names := make([]string, int(xpathKindCount-xpathBase))
	texts := make([]string, len(names))
	set := func(kind Kind, name, text string) {
		index := int(kind - xpathBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "XPATH_WHITESPACE", "whitespace")
	set(TkLineBreak, "XPATH_LINE_BREAK", "line break")
	set(TkName, "XPATH_NAME", "name")
	set(TkNumber, "XPATH_NUMBER", "number")
	set(TkString, "XPATH_STRING", "string")
	set(TkSlash, "XPATH_SLASH", "/")
	set(TkDoubleSlash, "XPATH_DOUBLE_SLASH", "//")
	set(TkDot, "XPATH_DOT", ".")
	set(TkDoubleDot, "XPATH_DOUBLE_DOT", "..")
	set(TkAt, "XPATH_AT", "@")
	set(TkDollar, "XPATH_DOLLAR", "$")
	set(TkColon, "XPATH_COLON", ":")
	set(TkDoubleColon, "XPATH_DOUBLE_COLON", "::")
	set(TkPipe, "XPATH_PIPE", "|")
	set(TkPlus, "XPATH_PLUS", "+")
	set(TkMinus, "XPATH_MINUS", "-")
	set(TkStar, "XPATH_STAR", "*")
	set(TkEquals, "XPATH_EQUALS", "=")
	set(TkNotEquals, "XPATH_NOT_EQUALS", "!=")
	set(TkLess, "XPATH_LESS", "<")
	set(TkLessEquals, "XPATH_LESS_EQUALS", "<=")
	set(TkGreater, "XPATH_GREATER", ">")
	set(TkGreaterEquals, "XPATH_GREATER_EQUALS", ">=")
	set(TkOpenParen, "XPATH_OPEN_PAREN", "(")
	set(TkCloseParen, "XPATH_CLOSE_PAREN", ")")
	set(TkOpenBracket, "XPATH_OPEN_BRACKET", "[")
	set(TkCloseBracket, "XPATH_CLOSE_BRACKET", "]")
	set(TkComma, "XPATH_COMMA", ",")
	set(TkUnknown, "XPATH_UNKNOWN", "unknown token")

	set(XPathDocument, "XPATH_DOCUMENT", "")
	set(XPathGroup, "XPATH_GROUP", "")
	set(XPathPredicate, "XPATH_PREDICATE", "")
	set(Error, "XPATH_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:        "xpath",
		Base:        xpathBase,
		KindNames:   names,
		TokenTexts:  texts,
		FirstNode:   XPathDocument,
		TriviaKinds: []Kind{TkWhitespace, TkLineBreak},
	})
}
