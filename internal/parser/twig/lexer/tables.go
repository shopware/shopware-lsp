package lexer

import "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"

// punctuationEntry pairs a literal punctuation/delimiter string with its kind.
type punctuationEntry struct {
	text string
	kind syntax.Kind
}

// punctuation lists every `#[token]` literal made purely of punctuation /
// delimiter characters. It is grouped by first byte in punctuationByFirst and,
// within each group, sorted longest-first so maximal munch picks the longest
// matching literal (e.g. `<=>` before `<=` before `<`, `{%-` before `{%`).
var punctuation = []punctuationEntry{
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
	{"<!--", syntax.TkLessThanExclamationMarkMinusMinus},
	{">", syntax.TkGreaterThan},
	{">=", syntax.TkGreaterThanEqual},
	{"=>", syntax.TkEqualGreaterThan},
	{"/>", syntax.TkSlashGreaterThan},
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

// punctuationByFirst maps a leading byte to the punctuation entries that begin
// with it, sorted longest-first. Built once at package init.
var punctuationByFirst = buildPunctuationIndex()

func buildPunctuationIndex() map[byte][]punctuationEntry {
	idx := make(map[byte][]punctuationEntry)
	for _, e := range punctuation {
		b := e.text[0]
		idx[b] = append(idx[b], e)
	}
	// Sort each bucket longest-first (insertion sort is fine, buckets are tiny).
	for b, list := range idx {
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && len(list[j].text) > len(list[j-1].text); j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
		idx[b] = list
	}
	return idx
}

// lookupPunctuation returns the longest punctuation/delimiter literal matching
// at source[pos].
func lookupPunctuation(source string, pos int) (syntax.Kind, int, bool) {
	for _, e := range punctuationByFirst[source[pos]] {
		if hasLiteral(source, pos, e.text) {
			return e.kind, len(e.text), true
		}
	}
	return 0, 0, false
}

// matchPunctuation is lookupPunctuation with an unknown fallback, used for the
// `#`/`&` first bytes that also start words / escapes.
func matchPunctuation(source string, pos int) (syntax.Kind, int) {
	if k, n, ok := lookupPunctuation(source, pos); ok {
		return k, n
	}
	return unknown(source, pos)
}

// keywords maps case-sensitive keyword text to its token kind. These are the
// logos `#[token("...")]` attributes without the `ignore(case)` flag. The
// multi-word keywords are handled separately (matchMultiWord), and TK_NOT /
// TK_IS carry a boundary callback, so `not` and `is` are still classified here
// for the standalone case.
var keywords = map[string]syntax.Kind{
	"block":         syntax.TkBlock,
	"endblock":      syntax.TkEndblock,
	"if":            syntax.TkIf,
	"elseif":        syntax.TkElseIf,
	"else":          syntax.TkElse,
	"endif":         syntax.TkEndif,
	"apply":         syntax.TkApply,
	"endapply":      syntax.TkEndapply,
	"autoescape":    syntax.TkAutoescape,
	"endautoescape": syntax.TkEndautoescape,
	"cache":         syntax.TkCache,
	"endcache":      syntax.TkEndcache,
	"deprecated":    syntax.TkDeprecated,
	"do":            syntax.TkDo,
	"embed":         syntax.TkEmbed,
	"endembed":      syntax.TkEndembed,
	"extends":       syntax.TkExtends,
	"flush":         syntax.TkFlush,
	"for":           syntax.TkFor,
	"endfor":        syntax.TkEndfor,
	"from":          syntax.TkFrom,
	"import":        syntax.TkImport,
	"macro":         syntax.TkMacro,
	"endmacro":      syntax.TkEndmacro,
	"sandbox":       syntax.TkSandbox,
	"endsandbox":    syntax.TkEndsandbox,
	"set":           syntax.TkSet,
	"endset":        syntax.TkEndset,
	"use":           syntax.TkUse,
	"verbatim":      syntax.TkVerbatim,
	"endverbatim":   syntax.TkEndverbatim,
	"only":          syntax.TkOnly,
	"with":          syntax.TkWith,
	"endwith":       syntax.TkEndwith,
	"ttl":           syntax.TkTtl,
	"tags":          syntax.TkTags,
	"props":         syntax.TkProps,
	"component":     syntax.TkComponent,
	"endcomponent":  syntax.TkEndcomponent,

	"not":     syntax.TkNot,
	"or":      syntax.TkOr,
	"and":     syntax.TkAnd,
	"b-or":    syntax.TkBinaryOr,
	"b-xor":   syntax.TkBinaryXor,
	"b-and":   syntax.TkBinaryAnd,
	"in":      syntax.TkIn,
	"matches": syntax.TkMatches,
	"is":      syntax.TkIs,

	"even":     syntax.TkEven,
	"odd":      syntax.TkOdd,
	"defined":  syntax.TkDefined,
	"as":       syntax.TkAs,
	"constant": syntax.TkConstant,
	"empty":    syntax.TkEmpty,
	"iterable": syntax.TkIterable,

	"max":     syntax.TkMax,
	"min":     syntax.TkMin,
	"range":   syntax.TkRange,
	"cycle":   syntax.TkCycle,
	"random":  syntax.TkRandom,
	"date":    syntax.TkDate,
	"include": syntax.TkInclude,
	"source":  syntax.TkSource,

	"trans":    syntax.TkTrans,
	"endtrans": syntax.TkEndtrans,

	"sw_extends":                syntax.TkSwExtends,
	"sw_silent_feature_call":    syntax.TkSwSilentFeatureCall,
	"endsw_silent_feature_call": syntax.TkEndswSilentFeatureCall,
	"sw_include":                syntax.TkSwInclude,
	"return":                    syntax.TkReturn,
	"sw_icon":                   syntax.TkSwIcon,
	"sw_thumbnails":             syntax.TkSwThumbnails,
	"style":                     syntax.TkStyle,
	"sw_embed":                  syntax.TkSwEmbed,
	"sw_end_embed":              syntax.TkSwEndEmbed,
	"sw_use":                    syntax.TkSwUse,
	"sw_import":                 syntax.TkSwImport,
	"sw_from":                   syntax.TkSwFrom,
}
