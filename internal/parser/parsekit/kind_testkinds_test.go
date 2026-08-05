package parsekit

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// This file defines a small self-contained language used by the parser-engine
// tests. It exercises the kind registry the same way a real language does,
// without depending on any concrete language package (which would create an
// import cycle). The kinds and their SCREAMING_SNAKE names mirror the twig kinds
// the original parser-package tests used, so the golden DebugTree output is
// unchanged.

const (
	tkWhitespace cst.Kind = iota
	tkLineBreak
	tkCurlyPercent
	tkWord
	tkNumber
	tkColon
	tkLessThan
	tkGreaterThan
	tkAnd
	tkApply
	tkIf
	tkCloseCurlyCurly
	tkMinusCloseCurlyCurly
	tkTildeCloseCurlyCurly

	// First composite node.
	kRoot
	kBody
	kHtmlStartingTag
	kHtmlTag
	kHtmlString
	kError

	testKindCount // sentinel: number of kinds in this test language
)

func init() {
	names := make([]string, testKindCount)
	names[tkWhitespace] = "TK_WHITESPACE"
	names[tkLineBreak] = "TK_LINE_BREAK"
	names[tkCurlyPercent] = "TK_CURLY_PERCENT"
	names[tkWord] = "TK_WORD"
	names[tkNumber] = "TK_NUMBER"
	names[tkColon] = "TK_COLON"
	names[tkLessThan] = "TK_LESS_THAN"
	names[tkGreaterThan] = "TK_GREATER_THAN"
	names[tkAnd] = "TK_AND"
	names[tkApply] = "TK_APPLY"
	names[tkIf] = "TK_IF"
	names[tkCloseCurlyCurly] = "TK_CLOSE_CURLY_CURLY"
	names[tkMinusCloseCurlyCurly] = "TK_MINUS_CLOSE_CURLY_CURLY"
	names[tkTildeCloseCurlyCurly] = "TK_TILDE_CLOSE_CURLY_CURLY"
	names[kRoot] = "ROOT"
	names[kBody] = "BODY"
	names[kHtmlStartingTag] = "HTML_STARTING_TAG"
	names[kHtmlTag] = "HTML_TAG"
	names[kHtmlString] = "HTML_STRING"
	names[kError] = "ERROR"

	texts := make([]string, testKindCount)
	texts[tkWord] = "word"
	texts[tkNumber] = "number"
	texts[tkCurlyPercent] = "{%"
	texts[tkCloseCurlyCurly] = "}}"
	texts[tkMinusCloseCurlyCurly] = "-}}"
	texts[tkTildeCloseCurlyCurly] = "~}}"

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:        "parsekit-test",
		Base:        0,
		KindNames:   names,
		TokenTexts:  texts,
		FirstNode:   kRoot,
		TriviaKinds: []cst.Kind{tkWhitespace, tkLineBreak},
	})
}

// generalRecovery mirrors the twig general recovery set relevant to these tests:
// {% is the only general sync point exercised.
var generalRecovery = []cst.Kind{tkCurlyPercent}
