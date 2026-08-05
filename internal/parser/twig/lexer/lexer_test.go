package lexer

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// tok is a compact expectation for a single lexed token.
type tok struct {
	kind  syntax.Kind
	text  string
	start uint32
	end   uint32
}

// assertTokens lexes input and asserts kind, text and range of every token.
func assertTokens(t *testing.T, input string, want []tok) {
	t.Helper()
	got := Lex(input)
	if len(got) != len(want) {
		t.Fatalf("token count mismatch for %q: got %d, want %d\ngot: %v", input, len(got), len(want), dump(got))
	}
	for i := range want {
		g := got[i]
		w := want[i]
		if g.Kind != w.kind {
			t.Errorf("token %d kind mismatch for %q: got %s, want %s", i, input, g.Kind, w.kind)
		}
		if g.Text() != w.text {
			t.Errorf("token %d text mismatch for %q: got %q, want %q", i, input, g.Text(), w.text)
		}
		rng := g.Range()
		if rng.Start != w.start || rng.End != w.end {
			t.Errorf("token %d range mismatch for %q: got %d..%d, want %d..%d", i, input, rng.Start, rng.End, w.start, w.end)
		}
	}
}

func dump(toks []Token) []string {
	out := make([]string, len(toks))
	for i, tk := range toks {
		out[i] = tk.Kind.String() + "@" + tk.Range().String() + " " + tk.Text()
	}
	return out
}

// single is a convenience for the very common "whole input is one token" case
// (ports the Rust check_token / check_regex helpers, which assert kind, text
// and full range).
func single(t *testing.T, input string, kind syntax.Kind) {
	t.Helper()
	assertTokens(t, input, []tok{{kind, input, 0, uint32(len(input))}})
}

func TestLexSimpleOutput(t *testing.T) {
	// Ports lexer.rs `lex_simple_output`.
	assertTokens(t, "</div>", []tok{
		{syntax.TkLessThanSlash, "</", 0, 2},
		{syntax.TkWord, "div", 2, 5},
		{syntax.TkGreaterThan, ">", 5, 6},
	})
}

func BenchmarkLexCaseInsensitiveKeywords(b *testing.B) {
	source := strings.Repeat(
		"ordinary DOCTYPE TRUE False NONE null LUDTWIG-IGNORE-FILE Ludtwig-Ignore ",
		1000,
	)
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	for b.Loop() {
		_ = Lex(source)
	}
}

func TestLexSimpleExpression(t *testing.T) {
	// Ports lexer.rs `lex_simple_expression`.
	assertTokens(t, "{{ not a }}", []tok{
		{syntax.TkOpenCurlyCurly, "{{", 0, 2},
		{syntax.TkWhitespace, " ", 2, 3},
		{syntax.TkNot, "not", 3, 6},
		{syntax.TkWhitespace, " ", 6, 7},
		{syntax.TkWord, "a", 7, 8},
		{syntax.TkWhitespace, " ", 8, 9},
		{syntax.TkCloseCurlyCurly, "}}", 9, 11},
	})
}

func TestLexHashtagCurlyCurly(t *testing.T) {
	// Ports lexer.rs `lex_hashtag_curly_curly`.
	assertTokens(t, "#{{", []tok{
		{syntax.TkHashtag, "#", 0, 1},
		{syntax.TkOpenCurlyCurly, "{{", 1, 3},
	})
}

func TestLexWhitespace(t *testing.T) {
	single(t, "   ", syntax.TkWhitespace)
	single(t, " \t  ", syntax.TkWhitespace)
	single(t, "\t", syntax.TkWhitespace)
}

func TestLexLineBreak(t *testing.T) {
	single(t, "\n", syntax.TkLineBreak)
	single(t, "\n\n", syntax.TkLineBreak)
	single(t, "\r\n", syntax.TkLineBreak)
	single(t, "\r\n\r\n", syntax.TkLineBreak)
	single(t, "\r\n\n\r\n", syntax.TkLineBreak)
}

func TestLexWord(t *testing.T) {
	for _, w := range []string{
		"hello", "hello123", "camelCase", "kebab-case", "snake_case",
		"#hello123", "@hello123", "block1", "block_", "blocks", "_blank", "$special",
	} {
		single(t, w, syntax.TkWord)
	}
}

func TestLexTwigComponentName(t *testing.T) {
	single(t, "twig:a", syntax.TkTwigComponentName)
	single(t, "twig:a:b:c:d", syntax.TkTwigComponentName)
	single(t, "twig:my:component:name", syntax.TkTwigComponentName)
	// `twig` alone is a plain word.
	single(t, "twig", syntax.TkWord)
}

func TestLexNumber(t *testing.T) {
	for _, n := range []string{
		"123", "0.0", "3.123456789", "3e+2", "3e-2", "10E-7", "10E+6", "1.23E+10", "42.3",
	} {
		single(t, n, syntax.TkNumber)
	}
}

func TestLexHTMLEscapeCharacter(t *testing.T) {
	for _, e := range []string{
		"&NewLine;", "&nbsp;", "&#39;", "&#8721;", "&sup3;", "&#x00B3;", "&#10;",
	} {
		single(t, e, syntax.TkHtmlEscapeCharacter)
	}
}

func TestLexPunctuationAndDelimiters(t *testing.T) {
	// Ports the individual per-token tests in lexer.rs.
	cases := []struct {
		text string
		kind syntax.Kind
	}{
		{".", syntax.TkDot},
		{"..", syntax.TkDoubleDot},
		{"...", syntax.TkTripleDot},
		{",", syntax.TkComma},
		{":", syntax.TkColon},
		{";", syntax.TkSemicolon},
		{"!", syntax.TkExclamationMark},
		{"!=", syntax.TkExclamationMarkEquals},
		{"!==", syntax.TkExclamationMarkDoubleEquals},
		{"?", syntax.TkQuestionMark},
		{"??", syntax.TkDoubleQuestionMark},
		{"%", syntax.TkPercent},
		{"~", syntax.TkTilde},
		{"|", syntax.TkSinglePipe},
		{"||", syntax.TkDoublePipe},
		{"&", syntax.TkAmpersand},
		{"&&", syntax.TkDoubleAmpersand},
		{"/", syntax.TkForwardSlash},
		{"//", syntax.TkDoubleForwardSlash},
		{"\\", syntax.TkBackwardSlash},
		{"(", syntax.TkOpenParenthesis},
		{")", syntax.TkCloseParenthesis},
		{"{", syntax.TkOpenCurly},
		{"}", syntax.TkCloseCurly},
		{"[", syntax.TkOpenSquare},
		{"]", syntax.TkCloseSquare},
		{"<", syntax.TkLessThan},
		{"<=", syntax.TkLessThanEqual},
		{"<=>", syntax.TkLessThanEqualGreaterThan},
		{"</", syntax.TkLessThanSlash},
		{"<!", syntax.TkLessThanExclamationMark},
		{">", syntax.TkGreaterThan},
		{">=", syntax.TkGreaterThanEqual},
		{"=>", syntax.TkEqualGreaterThan},
		{"/>", syntax.TkSlashGreaterThan},
		{"<!--", syntax.TkLessThanExclamationMarkMinusMinus},
		{"-->", syntax.TkMinusMinusGreaterThan},
		{"=", syntax.TkEqual},
		{"==", syntax.TkDoubleEqual},
		{"===", syntax.TkTripleEqual},
		{"+", syntax.TkPlus},
		{"-", syntax.TkMinus},
		{"*", syntax.TkStar},
		{"**", syntax.TkDoubleStar},
		{"\"", syntax.TkDoubleQuotes},
		{"'", syntax.TkSingleQuotes},
		{"`", syntax.TkGraveAccentQuotes},
		{"{%", syntax.TkCurlyPercent},
		{"{%-", syntax.TkCurlyPercentMinus},
		{"{%~", syntax.TkCurlyPercentTilde},
		{"%}", syntax.TkPercentCurly},
		{"-%}", syntax.TkMinusPercentCurly},
		{"~%}", syntax.TkTildePercentCurly},
		{"{{", syntax.TkOpenCurlyCurly},
		{"{{-", syntax.TkOpenCurlyCurlyMinus},
		{"{{~", syntax.TkOpenCurlyCurlyTilde},
		{"}}", syntax.TkCloseCurlyCurly},
		{"-}}", syntax.TkMinusCloseCurlyCurly},
		{"~}}", syntax.TkTildeCloseCurlyCurly},
		{"{#", syntax.TkOpenCurlyHashtag},
		{"{#-", syntax.TkOpenCurlyHashtagMinus},
		{"{#~", syntax.TkOpenCurlyHashtagTilde},
		{"#}", syntax.TkHashtagCloseCurly},
		{"-#}", syntax.TkMinusHashtagCloseCurly},
		{"~#}", syntax.TkTildeHashtagCloseCurly},
		{"#", syntax.TkHashtag},
	}
	for _, c := range cases {
		single(t, c.text, c.kind)
	}
}

func TestLexCaseInsensitiveKeywords(t *testing.T) {
	// DOCTYPE / true / false / none / null / ludtwig-ignore(-file) ignore case.
	single(t, "DOCTYPE", syntax.TkDoctype)
	single(t, "doctype", syntax.TkDoctype)
	single(t, "DocType", syntax.TkDoctype)
	single(t, "true", syntax.TkTrue)
	single(t, "TRUE", syntax.TkTrue)
	single(t, "True", syntax.TkTrue)
	single(t, "false", syntax.TkFalse)
	single(t, "FALSE", syntax.TkFalse)
	single(t, "none", syntax.TkNone)
	single(t, "NONE", syntax.TkNone)
	single(t, "null", syntax.TkNull)
	single(t, "NULL", syntax.TkNull)
	single(t, "ludtwig-ignore-file", syntax.TkLudtwigIgnoreFile)
	single(t, "LUDTWIG-IGNORE-FILE", syntax.TkLudtwigIgnoreFile)
	single(t, "ludtwig-ignore", syntax.TkLudtwigIgnore)
	single(t, "Ludtwig-Ignore", syntax.TkLudtwigIgnore)
}

func TestLexKeywords(t *testing.T) {
	// Ports every single-word keyword test in lexer.rs.
	cases := []struct {
		text string
		kind syntax.Kind
	}{
		{"block", syntax.TkBlock},
		{"endblock", syntax.TkEndblock},
		{"if", syntax.TkIf},
		{"elseif", syntax.TkElseIf},
		{"else", syntax.TkElse},
		{"endif", syntax.TkEndif},
		{"apply", syntax.TkApply},
		{"endapply", syntax.TkEndapply},
		{"autoescape", syntax.TkAutoescape},
		{"endautoescape", syntax.TkEndautoescape},
		{"cache", syntax.TkCache},
		{"endcache", syntax.TkEndcache},
		{"deprecated", syntax.TkDeprecated},
		{"do", syntax.TkDo},
		{"embed", syntax.TkEmbed},
		{"endembed", syntax.TkEndembed},
		{"extends", syntax.TkExtends},
		{"flush", syntax.TkFlush},
		{"for", syntax.TkFor},
		{"endfor", syntax.TkEndfor},
		{"from", syntax.TkFrom},
		{"import", syntax.TkImport},
		{"macro", syntax.TkMacro},
		{"endmacro", syntax.TkEndmacro},
		{"sandbox", syntax.TkSandbox},
		{"endsandbox", syntax.TkEndsandbox},
		{"set", syntax.TkSet},
		{"endset", syntax.TkEndset},
		{"use", syntax.TkUse},
		{"verbatim", syntax.TkVerbatim},
		{"endverbatim", syntax.TkEndverbatim},
		{"only", syntax.TkOnly},
		{"with", syntax.TkWith},
		{"endwith", syntax.TkEndwith},
		{"ttl", syntax.TkTtl},
		{"tags", syntax.TkTags},
		{"props", syntax.TkProps},
		{"component", syntax.TkComponent},
		{"endcomponent", syntax.TkEndcomponent},
		{"not", syntax.TkNot},
		{"or", syntax.TkOr},
		{"and", syntax.TkAnd},
		{"b-or", syntax.TkBinaryOr},
		{"b-xor", syntax.TkBinaryXor},
		{"b-and", syntax.TkBinaryAnd},
		{"in", syntax.TkIn},
		{"matches", syntax.TkMatches},
		{"is", syntax.TkIs},
		{"even", syntax.TkEven},
		{"odd", syntax.TkOdd},
		{"defined", syntax.TkDefined},
		{"as", syntax.TkAs},
		{"constant", syntax.TkConstant},
		{"empty", syntax.TkEmpty},
		{"iterable", syntax.TkIterable},
		{"max", syntax.TkMax},
		{"min", syntax.TkMin},
		{"range", syntax.TkRange},
		{"cycle", syntax.TkCycle},
		{"random", syntax.TkRandom},
		{"date", syntax.TkDate},
		{"include", syntax.TkInclude},
		{"source", syntax.TkSource},
		{"trans", syntax.TkTrans},
		{"endtrans", syntax.TkEndtrans},
		{"sw_extends", syntax.TkSwExtends},
		{"sw_silent_feature_call", syntax.TkSwSilentFeatureCall},
		{"endsw_silent_feature_call", syntax.TkEndswSilentFeatureCall},
		{"sw_include", syntax.TkSwInclude},
		{"return", syntax.TkReturn},
		{"sw_icon", syntax.TkSwIcon},
		{"sw_thumbnails", syntax.TkSwThumbnails},
		{"style", syntax.TkStyle},
		{"sw_embed", syntax.TkSwEmbed},
		{"sw_end_embed", syntax.TkSwEndEmbed},
		{"sw_use", syntax.TkSwUse},
		{"sw_import", syntax.TkSwImport},
		{"sw_from", syntax.TkSwFrom},
	}
	for _, c := range cases {
		single(t, c.text, c.kind)
	}
}

func TestLexMultiWordKeywords(t *testing.T) {
	// The space-containing keywords lex as a SINGLE token.
	single(t, "not in", syntax.TkNotIn)
	single(t, "is not", syntax.TkIsNot)
	single(t, "starts with", syntax.TkStartsWith)
	single(t, "ends with", syntax.TkEndsWith)
	single(t, "same as", syntax.TkSameAs)
	single(t, "divisible by", syntax.TkDivisibleBy)
	single(t, "ignore missing", syntax.TkIgnoreMissing)
}

// TestLexMultiWordBoundaries covers the cb_not / cb_is boundary tricks and the
// longest-match behaviour of the other multi-word keywords.
func TestLexMultiWordBoundaries(t *testing.T) {
	// `not insider` must NOT fuse to TK_NOT_IN: the char after " in" is 's'.
	assertTokens(t, "not insider", []tok{
		{syntax.TkNot, "not", 0, 3},
		{syntax.TkWhitespace, " ", 3, 4},
		{syntax.TkWord, "insider", 4, 11},
	})
	// `not inside` likewise stays TK_NOT + word.
	assertTokens(t, "not inside", []tok{
		{syntax.TkNot, "not", 0, 3},
		{syntax.TkWhitespace, " ", 3, 4},
		{syntax.TkWord, "inside", 4, 10},
	})
	// `notin` is longer than `not`, so it is a single word (maximal munch).
	single(t, "notin", syntax.TkWord)
	// `not in` at EOF fuses.
	single(t, "not in", syntax.TkNotIn)
	// `not in ` (trailing space) fuses, then whitespace.
	assertTokens(t, "not in ", []tok{
		{syntax.TkNotIn, "not in", 0, 6},
		{syntax.TkWhitespace, " ", 6, 7},
	})
	// `not in\n` fuses; '\n' is a valid boundary.
	assertTokens(t, "not in\n", []tok{
		{syntax.TkNotIn, "not in", 0, 6},
		{syntax.TkLineBreak, "\n", 6, 7},
	})
	// `not in\t` fuses; '\t' is a valid boundary.
	assertTokens(t, "not in\t", []tok{
		{syntax.TkNotIn, "not in", 0, 6},
		{syntax.TkWhitespace, "\t", 6, 7},
	})
	// `not iny` does not fuse (char after " in" is 'y').
	assertTokens(t, "not iny", []tok{
		{syntax.TkNot, "not", 0, 3},
		{syntax.TkWhitespace, " ", 3, 4},
		{syntax.TkWord, "iny", 4, 7},
	})

	// `is nothing` must NOT fuse to TK_IS_NOT: char after " not" is 'h'.
	assertTokens(t, "is nothing", []tok{
		{syntax.TkIs, "is", 0, 2},
		{syntax.TkWhitespace, " ", 2, 3},
		{syntax.TkWord, "nothing", 3, 10},
	})
	// `is not` at EOF fuses.
	single(t, "is not", syntax.TkIsNot)
	// `is not ` fuses then whitespace.
	assertTokens(t, "is not ", []tok{
		{syntax.TkIsNot, "is not", 0, 6},
		{syntax.TkWhitespace, " ", 6, 7},
	})

	// The other multi-word keywords are pure longest-match literals: no
	// trailing boundary check. `starts withheld` -> `starts with` + `held`.
	assertTokens(t, "starts withheld", []tok{
		{syntax.TkStartsWith, "starts with", 0, 11},
		{syntax.TkWord, "held", 11, 15},
	})
	// `starts wit` (incomplete) -> word + ws + word.
	assertTokens(t, "starts wit", []tok{
		{syntax.TkWord, "starts", 0, 6},
		{syntax.TkWhitespace, " ", 6, 7},
		{syntax.TkWord, "wit", 7, 10},
	})
	// Two spaces break the literal `starts with`.
	assertTokens(t, "starts  with", []tok{
		{syntax.TkWord, "starts", 0, 6},
		{syntax.TkWhitespace, "  ", 6, 8},
		{syntax.TkWith, "with", 8, 12},
	})
	// `ignore foo` -> `ignore` is just a word (ignore is not a keyword alone).
	assertTokens(t, "ignore foo", []tok{
		{syntax.TkWord, "ignore", 0, 6},
		{syntax.TkWhitespace, " ", 6, 7},
		{syntax.TkWord, "foo", 7, 10},
	})
	single(t, "divisible by", syntax.TkDivisibleBy)
	single(t, "same as", syntax.TkSameAs)
}

// TestLexKeywordWordBoundary makes sure keywords are only recognised when the
// whole scanned word matches (maximal munch): `blocks` and `block_` are words.
func TestLexKeywordWordBoundary(t *testing.T) {
	single(t, "blocks", syntax.TkWord)
	single(t, "block_", syntax.TkWord)
	single(t, "block1", syntax.TkWord)
	single(t, "iffy", syntax.TkWord)
	single(t, "ifs", syntax.TkWord)
}

// TestLexUnknown covers un-lexable input: one TkUnknown token per UTF-8 rune.
func TestLexUnknown(t *testing.T) {
	// A single multi-byte rune (`€` is 3 bytes) is one TkUnknown token.
	single(t, "€", syntax.TkUnknown)
	// A lone '\r' is not a line break and becomes TkUnknown.
	single(t, "\r", syntax.TkUnknown)
	// Consecutive unknown runes split into one token each (`€` is 3 bytes,
	// `£` is 2 bytes).
	assertTokens(t, "€£", []tok{
		{syntax.TkUnknown, "€", 0, 3},
		{syntax.TkUnknown, "£", 3, 5},
	})
	// Unknown surrounded by known tokens.
	assertTokens(t, "a€b", []tok{
		{syntax.TkWord, "a", 0, 1},
		{syntax.TkUnknown, "€", 1, 4},
		{syntax.TkWord, "b", 4, 5},
	})
}

// TestLexNumberBoundaries exercises the number regex edge cases (mandatory
// exponent sign, fractional part needing digits).
func TestLexNumberBoundaries(t *testing.T) {
	// Missing exponent sign: `3e2` -> number `3` then word `e2`.
	assertTokens(t, "3e2", []tok{
		{syntax.TkNumber, "3", 0, 1},
		{syntax.TkWord, "e2", 1, 3},
	})
	// Trailing dot with no fraction: `3.` -> number `3` then dot.
	assertTokens(t, "3.", []tok{
		{syntax.TkNumber, "3", 0, 1},
		{syntax.TkDot, ".", 1, 2},
	})
	// Exponent sign but no digits: `3e+` -> number `3`, word `e`, plus.
	assertTokens(t, "3e+", []tok{
		{syntax.TkNumber, "3", 0, 1},
		{syntax.TkWord, "e", 1, 2},
		{syntax.TkPlus, "+", 2, 3},
	})
	// Range operator: `1..5` -> number, `..`, number.
	assertTokens(t, "1..5", []tok{
		{syntax.TkNumber, "1", 0, 1},
		{syntax.TkDoubleDot, "..", 1, 3},
		{syntax.TkNumber, "5", 3, 4},
	})
}

// TestLexTwigComponentBoundaries exercises the twig:component-name matcher.
func TestLexTwigComponentBoundaries(t *testing.T) {
	// `twiggy` is a plain word (no colon after `twig`).
	single(t, "twiggy", syntax.TkWord)
	// `twigx:a` -> `twigx` is a word (base is not exactly `twig`).
	assertTokens(t, "twigx:a", []tok{
		{syntax.TkWord, "twigx", 0, 5},
		{syntax.TkColon, ":", 5, 6},
		{syntax.TkWord, "a", 6, 7},
	})
	// Empty segment `twig::a` -> word `twig`, two colons, word.
	assertTokens(t, "twig::a", []tok{
		{syntax.TkWord, "twig", 0, 4},
		{syntax.TkColon, ":", 4, 5},
		{syntax.TkColon, ":", 5, 6},
		{syntax.TkWord, "a", 6, 7},
	})
	// Trailing colon: `twig:a:` -> component `twig:a` then colon.
	assertTokens(t, "twig:a:", []tok{
		{syntax.TkTwigComponentName, "twig:a", 0, 6},
		{syntax.TkColon, ":", 6, 7},
	})
	// Kebab and digits inside segments are allowed.
	single(t, "twig:my-comp:v2", syntax.TkTwigComponentName)
}

// TestLexAmpersandBoundaries checks the `&` vs `&&` vs escape interplay.
func TestLexAmpersandBoundaries(t *testing.T) {
	single(t, "&&", syntax.TkDoubleAmpersand)
	single(t, "&", syntax.TkAmpersand)
	// `&amp` without a semicolon is not an escape: `&` then word.
	assertTokens(t, "&amp", []tok{
		{syntax.TkAmpersand, "&", 0, 1},
		{syntax.TkWord, "amp", 1, 4},
	})
	// `&nbsp;` is a full escape.
	single(t, "&nbsp;", syntax.TkHtmlEscapeCharacter)
}

// TestLexAllTokensChainedTogether ports lexer.rs `lex_all_tokens_chained_together`:
// each token followed by a space, verifying kinds in a long stream. It also
// verifies the losslessness invariant (concatenated text == source).
func TestLexAllTokensChainedTogether(t *testing.T) {
	type entry struct {
		text string
		kind syntax.Kind
	}
	entries := []entry{
		{"\n", syntax.TkLineBreak},
		{"word", syntax.TkWord},
		{"twig:my:component:name", syntax.TkTwigComponentName},
		{"42.3", syntax.TkNumber},
		{"&#10;", syntax.TkHtmlEscapeCharacter},
		{".", syntax.TkDot},
		{"..", syntax.TkDoubleDot},
		{"...", syntax.TkTripleDot},
		{",", syntax.TkComma},
		{":", syntax.TkColon},
		{";", syntax.TkSemicolon},
		{"!", syntax.TkExclamationMark},
		{"!=", syntax.TkExclamationMarkEquals},
		{"!==", syntax.TkExclamationMarkDoubleEquals},
		{"?", syntax.TkQuestionMark},
		{"??", syntax.TkDoubleQuestionMark},
		{"%", syntax.TkPercent},
		{"~", syntax.TkTilde},
		{"|", syntax.TkSinglePipe},
		{"||", syntax.TkDoublePipe},
		{"&", syntax.TkAmpersand},
		{"&&", syntax.TkDoubleAmpersand},
		{"/", syntax.TkForwardSlash},
		{"//", syntax.TkDoubleForwardSlash},
		{"\\", syntax.TkBackwardSlash},
		{"(", syntax.TkOpenParenthesis},
		{")", syntax.TkCloseParenthesis},
		{"{", syntax.TkOpenCurly},
		{"}", syntax.TkCloseCurly},
		{"[", syntax.TkOpenSquare},
		{"]", syntax.TkCloseSquare},
		{"<", syntax.TkLessThan},
		{"<=", syntax.TkLessThanEqual},
		{"<=>", syntax.TkLessThanEqualGreaterThan},
		{"</", syntax.TkLessThanSlash},
		{"<!", syntax.TkLessThanExclamationMark},
		{"doctype", syntax.TkDoctype},
		{">", syntax.TkGreaterThan},
		{">=", syntax.TkGreaterThanEqual},
		{"=>", syntax.TkEqualGreaterThan},
		{"/>", syntax.TkSlashGreaterThan},
		{"<!--", syntax.TkLessThanExclamationMarkMinusMinus},
		{"-->", syntax.TkMinusMinusGreaterThan},
		{"=", syntax.TkEqual},
		{"==", syntax.TkDoubleEqual},
		{"===", syntax.TkTripleEqual},
		{"+", syntax.TkPlus},
		{"-", syntax.TkMinus},
		{"*", syntax.TkStar},
		{"**", syntax.TkDoubleStar},
		{"\"", syntax.TkDoubleQuotes},
		{"'", syntax.TkSingleQuotes},
		{"`", syntax.TkGraveAccentQuotes},
		{"{%", syntax.TkCurlyPercent},
		{"{%-", syntax.TkCurlyPercentMinus},
		{"{%~", syntax.TkCurlyPercentTilde},
		{"%}", syntax.TkPercentCurly},
		{"-%}", syntax.TkMinusPercentCurly},
		{"~%}", syntax.TkTildePercentCurly},
		{"{{", syntax.TkOpenCurlyCurly},
		{"{{-", syntax.TkOpenCurlyCurlyMinus},
		{"{{~", syntax.TkOpenCurlyCurlyTilde},
		{"}}", syntax.TkCloseCurlyCurly},
		{"-}}", syntax.TkMinusCloseCurlyCurly},
		{"~}}", syntax.TkTildeCloseCurlyCurly},
		{"{#", syntax.TkOpenCurlyHashtag},
		{"{#-", syntax.TkOpenCurlyHashtagMinus},
		{"{#~", syntax.TkOpenCurlyHashtagTilde},
		{"#", syntax.TkHashtag},
		{"#}", syntax.TkHashtagCloseCurly},
		{"-#}", syntax.TkMinusHashtagCloseCurly},
		{"~#}", syntax.TkTildeHashtagCloseCurly},
		{"true", syntax.TkTrue},
		{"false", syntax.TkFalse},
		{"block", syntax.TkBlock},
		{"endblock", syntax.TkEndblock},
		{"if", syntax.TkIf},
		{"elseif", syntax.TkElseIf},
		{"else", syntax.TkElse},
		{"endif", syntax.TkEndif},
		{"apply", syntax.TkApply},
		{"endapply", syntax.TkEndapply},
		{"autoescape", syntax.TkAutoescape},
		{"endautoescape", syntax.TkEndautoescape},
		{"cache", syntax.TkCache},
		{"endcache", syntax.TkEndcache},
		{"deprecated", syntax.TkDeprecated},
		{"do", syntax.TkDo},
		{"embed", syntax.TkEmbed},
		{"endembed", syntax.TkEndembed},
		{"extends", syntax.TkExtends},
		{"flush", syntax.TkFlush},
		{"for", syntax.TkFor},
		{"endfor", syntax.TkEndfor},
		{"from", syntax.TkFrom},
		{"import", syntax.TkImport},
		{"macro", syntax.TkMacro},
		{"endmacro", syntax.TkEndmacro},
		{"sandbox", syntax.TkSandbox},
		{"endsandbox", syntax.TkEndsandbox},
		{"set", syntax.TkSet},
		{"endset", syntax.TkEndset},
		{"use", syntax.TkUse},
		{"verbatim", syntax.TkVerbatim},
		{"endverbatim", syntax.TkEndverbatim},
		{"only", syntax.TkOnly},
		{"ignore missing", syntax.TkIgnoreMissing},
		{"with", syntax.TkWith},
		{"endwith", syntax.TkEndwith},
		{"ttl", syntax.TkTtl},
		{"tags", syntax.TkTags},
		{"props", syntax.TkProps},
		{"component", syntax.TkComponent},
		{"endcomponent", syntax.TkEndcomponent},
		{"not", syntax.TkNot},
		{"not in", syntax.TkNotIn},
		{"or", syntax.TkOr},
		{"and", syntax.TkAnd},
		{"b-or", syntax.TkBinaryOr},
		{"b-xor", syntax.TkBinaryXor},
		{"b-and", syntax.TkBinaryAnd},
		{"in", syntax.TkIn},
		{"matches", syntax.TkMatches},
		{"starts with", syntax.TkStartsWith},
		{"ends with", syntax.TkEndsWith},
		{"is", syntax.TkIs},
		{"is not", syntax.TkIsNot},
		{"even", syntax.TkEven},
		{"odd", syntax.TkOdd},
		{"defined", syntax.TkDefined},
		{"same as", syntax.TkSameAs},
		{"as", syntax.TkAs},
		{"none", syntax.TkNone},
		{"null", syntax.TkNull},
		{"divisible by", syntax.TkDivisibleBy},
		{"constant", syntax.TkConstant},
		{"empty", syntax.TkEmpty},
		{"iterable", syntax.TkIterable},
		{"max", syntax.TkMax},
		{"min", syntax.TkMin},
		{"range", syntax.TkRange},
		{"cycle", syntax.TkCycle},
		{"random", syntax.TkRandom},
		{"date", syntax.TkDate},
		{"include", syntax.TkInclude},
		{"source", syntax.TkSource},
		{"sw_extends", syntax.TkSwExtends},
		{"sw_silent_feature_call", syntax.TkSwSilentFeatureCall},
		{"endsw_silent_feature_call", syntax.TkEndswSilentFeatureCall},
		{"sw_include", syntax.TkSwInclude},
		{"return", syntax.TkReturn},
		{"sw_icon", syntax.TkSwIcon},
		{"sw_thumbnails", syntax.TkSwThumbnails},
		{"style", syntax.TkStyle},
		{"ludtwig-ignore-file", syntax.TkLudtwigIgnoreFile},
		{"ludtwig-ignore", syntax.TkLudtwigIgnore},
		{"€", syntax.TkUnknown},
		{"trans", syntax.TkTrans},
		{"endtrans", syntax.TkEndtrans},
		{"sw_embed", syntax.TkSwEmbed},
		{"sw_end_embed", syntax.TkSwEndEmbed},
		{"sw_use", syntax.TkSwUse},
		{"sw_import", syntax.TkSwImport},
		{"sw_from", syntax.TkSwFrom},
	}

	var sb strings.Builder
	var want []syntax.Kind
	for _, e := range entries {
		sb.WriteString(e.text)
		sb.WriteString(" ")
		want = append(want, e.kind, syntax.TkWhitespace)
	}
	source := sb.String()

	got := Lex(source)
	if len(got) != len(want) {
		t.Fatalf("token count mismatch: got %d, want %d", len(got), len(want))
	}
	var reconstructed strings.Builder
	for i, tk := range got {
		if tk.Kind != want[i] {
			t.Errorf("token %d kind mismatch: got %s (%q), want %s", i, tk.Kind, tk.Text(), want[i])
		}
		reconstructed.WriteString(tk.Text())
	}
	if reconstructed.String() != source {
		t.Errorf("lossless invariant violated: reconstructed text != source")
	}
}

// TestLexLossless is a general property check: the concatenation of all token
// texts equals the source and ranges are contiguous.
func TestLexLossless(t *testing.T) {
	inputs := []string{
		"",
		"{% if foo.bar is not empty %}{{ x|title }}{% endif %}",
		"<div class=\"a\">text &nbsp; {# comment #}</div>",
		"€ not insider is nothing starts withheld",
		"\r lone carriage return",
	}
	for _, in := range inputs {
		toks := Lex(in)
		var sb strings.Builder
		var prevEnd uint32
		for i, tk := range toks {
			rng := tk.Range()
			if rng.Start != prevEnd {
				t.Errorf("input %q token %d: non-contiguous range, start %d != prev end %d", in, i, rng.Start, prevEnd)
			}
			prevEnd = rng.End
			sb.WriteString(tk.Text())
		}
		if sb.String() != in {
			t.Errorf("input %q: reconstructed %q != source", in, sb.String())
		}
		if len(in) > 0 && prevEnd != uint32(len(in)) {
			t.Errorf("input %q: final end %d != len %d", in, prevEnd, len(in))
		}
	}
}
