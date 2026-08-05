package cst

// This file defines a small self-contained language used by the tree and debug
// tests. It exercises the kind registry the same way a real language does,
// without depending on any concrete language package (which would import cst and
// create a cycle). The kinds and their SCREAMING_SNAKE names mirror the twig
// kinds the original syntax-package tests used, so the golden DebugTree output
// is unchanged.

const (
	tkWhitespace Kind = iota
	tkLineBreak
	tkCurlyPercent
	tkPercentCurly
	tkBlock
	tkEndblock
	tkWord
	tkUnknown

	// First composite node.
	kRoot
	kTwigBlock
	kTwigStartingBlock
	kTwigEndingBlock
	kBody
	kHtmlText
	kError

	testKindCount // sentinel: number of kinds in this test language
)

func init() {
	names := make([]string, testKindCount)
	names[tkWhitespace] = "TK_WHITESPACE"
	names[tkLineBreak] = "TK_LINE_BREAK"
	names[tkCurlyPercent] = "TK_CURLY_PERCENT"
	names[tkPercentCurly] = "TK_PERCENT_CURLY"
	names[tkBlock] = "TK_BLOCK"
	names[tkEndblock] = "TK_ENDBLOCK"
	names[tkWord] = "TK_WORD"
	names[tkUnknown] = "TK_UNKNOWN"
	names[kRoot] = "ROOT"
	names[kTwigBlock] = "TWIG_BLOCK"
	names[kTwigStartingBlock] = "TWIG_STARTING_BLOCK"
	names[kTwigEndingBlock] = "TWIG_ENDING_BLOCK"
	names[kBody] = "BODY"
	names[kHtmlText] = "HTML_TEXT"
	names[kError] = "ERROR"

	RegisterLanguage(LanguageSpec{
		Name:        "cst-test",
		Base:        0,
		KindNames:   names,
		FirstNode:   kRoot,
		TriviaKinds: []Kind{tkWhitespace, tkLineBreak},
	})
}
