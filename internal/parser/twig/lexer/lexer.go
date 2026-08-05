// Package lexer is a hand-written, mode-less, maximal-munch tokenizer for the
// combined HTML + Twig grammar. It is a faithful port of ludtwig-parser's
// `lexer.rs` together with the logos `#[token]` / `#[regex]` attributes declared
// on the `SyntaxKind` enum in `syntax/untyped.rs`.
//
// The lexer is completely context free ("dumb"): there is no HTML-mode vs
// Twig-mode switching. The whole file is tokenized with one flat token set that
// is the union of HTML punctuation, Twig delimiters, Twig keywords and generic
// words. The parser later decides contextually what each token means (and may
// re-label keyword tokens as words via `bump_as`).
//
// Any input the lexer cannot match becomes a TkUnknown token (never an error),
// which feeds the parser's error recovery.
package lexer

import (
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// Token is a single lexeme produced by Lex. It aliases [parsekit.Token] so
// lexer output feeds the parser engine directly. Text returns a zero-copy slice
// of the source string and Range holds its half-open byte offsets [Start, End).
type Token = parsekit.Token

// Lex tokenizes the whole source eagerly into a flat slice of tokens. It is
// total: every byte of the input ends up in exactly one token, so the
// concatenation of all token texts equals source.
func Lex(source string) []Token {
	return LexInto(source, nil)
}

// LexInto tokenizes source into the provided reusable destination.
func LexInto(source string, result []Token) []Token {
	// Pre-allocate roughly one token per 4 bytes, mirroring the Rust lexer.
	capacity := len(source) / 4
	if cap(result) < capacity {
		result = make([]Token, 0, capacity)
	} else {
		result = result[:0]
	}
	sourceRef := &source

	pos := 0
	for pos < len(source) {
		kind, length := next(source, pos)
		result = append(result, parsekit.NewToken(
			kind,
			sourceRef,
			syntax.TextRange{
				Start: uint32(pos),
				End:   uint32(pos + length),
			},
		))
		pos += length
	}

	return result
}

// next returns the kind and byte length of the token starting at source[pos].
// pos is guaranteed < len(source). length is always >= 1 so lexing terminates.
func next(source string, pos int) (syntax.Kind, int) {
	c := source[pos]

	switch {
	case c == ' ' || c == '\t':
		return syntax.TkWhitespace, scanWhitespace(source, pos)

	case c == '\n' || c == '\r':
		if n := scanLineBreak(source, pos); n > 0 {
			return syntax.TkLineBreak, n
		}
		// A lone '\r' (not part of "\r\n") is not matched by the line break
		// regex `((\n)|(\r\n))+`, so it falls through to TkUnknown.
		return unknown(source, pos)

	case c >= '0' && c <= '9':
		return syntax.TkNumber, scanNumber(source, pos)

	case isWordStartLetter(c):
		return classifyWord(source, pos)

	case c == '@' || c == '$':
		// Word regex requires `[@$]` to be followed by a letter.
		if n := scanWord(source, pos); n > 0 {
			return syntax.TkWord, n
		}
		return unknown(source, pos)

	case c == '_':
		// `_` alone is a valid word.
		return syntax.TkWord, scanWord(source, pos)

	case c == '#':
		// `#letter...` is a word; otherwise the `#`/`#}` punctuation applies.
		if n := scanWord(source, pos); n > 0 {
			return syntax.TkWord, n
		}
		return matchPunctuation(source, pos)

	case c == '&':
		// `&name;` / `&#123;` / `&#xAB;` is an HTML escape; otherwise the
		// `&`/`&&` punctuation applies.
		if n := scanHTMLEscape(source, pos); n > 0 {
			return syntax.TkHtmlEscapeCharacter, n
		}
		return matchPunctuation(source, pos)

	default:
		if k, n, ok := lookupPunctuation(source, pos); ok {
			return k, n
		}
		return unknown(source, pos)
	}
}

// unknown returns a single TkUnknown token spanning one UTF-8 rune. Consecutive
// unlexable runes therefore produce one TkUnknown token each (matching logos,
// which yields one error token per un-matched rune). See lex_all_tokens test:
// `€` lexes to a single TkUnknown.
func unknown(source string, pos int) (syntax.Kind, int) {
	_, size := utf8.DecodeRuneInString(source[pos:])
	if size == 0 {
		size = 1
	}
	return syntax.TkUnknown, size
}

func scanWhitespace(source string, pos int) int {
	i := pos
	for i < len(source) && (source[i] == ' ' || source[i] == '\t') {
		i++
	}
	return i - pos
}

// scanLineBreak matches the regex `((\n)|(\r\n))+`: one or more consecutive
// line breaks collapse into a single token. A lone '\r' does not match and
// returns 0.
func scanLineBreak(source string, pos int) int {
	i := pos
	for i < len(source) {
		if source[i] == '\n' {
			i++
		} else if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i += 2
		} else {
			break
		}
	}
	return i - pos
}

// scanNumber matches `[0-9]+(\.[0-9]+)?([Ee][+\-][0-9]+)?`. The exponent part
// requires an explicit sign; without one it is not consumed.
func scanNumber(source string, pos int) int {
	i := pos
	for i < len(source) && isDigit(source[i]) {
		i++
	}
	// Optional fractional part: only consumed if `.` is followed by a digit.
	if i < len(source) && source[i] == '.' && i+1 < len(source) && isDigit(source[i+1]) {
		i++ // '.'
		for i < len(source) && isDigit(source[i]) {
			i++
		}
	}
	// Optional exponent: [Ee] then a mandatory sign then one or more digits.
	if i < len(source) && (source[i] == 'e' || source[i] == 'E') {
		j := i + 1
		if j < len(source) && (source[j] == '+' || source[j] == '-') {
			j++
			if j < len(source) && isDigit(source[j]) {
				for j < len(source) && isDigit(source[j]) {
					j++
				}
				i = j
			}
		}
	}
	return i - pos
}

// scanHTMLEscape matches `\&(([a-zA-Z][a-zA-Z0-9]*)|(\#[0-9]+)|(\#x[0-9a-fA-F]+));`.
// Returns the byte length including the trailing `;`, or 0 if no match.
func scanHTMLEscape(source string, pos int) int {
	// source[pos] == '&'
	i := pos + 1
	if i >= len(source) {
		return 0
	}
	switch {
	case isLetter(source[i]):
		// [a-zA-Z][a-zA-Z0-9]*
		i++
		for i < len(source) && isAlnum(source[i]) {
			i++
		}
	case source[i] == '#':
		i++
		if i < len(source) && source[i] == 'x' {
			// #x[0-9a-fA-F]+
			i++
			start := i
			for i < len(source) && isHexDigit(source[i]) {
				i++
			}
			if i == start {
				return 0
			}
		} else {
			// #[0-9]+
			start := i
			for i < len(source) && isDigit(source[i]) {
				i++
			}
			if i == start {
				return 0
			}
		}
	default:
		return 0
	}
	if i < len(source) && source[i] == ';' {
		return i + 1 - pos
	}
	return 0
}

// scanWord matches the word regex
// `([a-zA-Z]|([@\#_\$][a-zA-Z])|_)[a-zA-Z0-9_\-]*`.
// Returns the byte length, or 0 if there is no valid word start at pos.
func scanWord(source string, pos int) int {
	c := source[pos]
	i := pos
	switch {
	case isLetter(c):
		i++
	case c == '@' || c == '#' || c == '_' || c == '$':
		if c == '_' {
			// The `_` alternative stands on its own (no following letter needed).
			i++
		} else {
			// `[@#$]` must be followed by a letter.
			if pos+1 >= len(source) || !isLetter(source[pos+1]) {
				return 0
			}
			i += 2
		}
	default:
		return 0
	}
	for i < len(source) && isWordContinue(source[i]) {
		i++
	}
	return i - pos
}

// classifyWord scans a word (or twig component name) starting at a letter and
// classifies it as a keyword, a case-insensitive keyword, a twig component
// name, a multi-word keyword or a plain TkWord.
func classifyWord(source string, pos int) (syntax.Kind, int) {
	wordLen := scanWord(source, pos)
	word := source[pos : pos+wordLen]

	// Twig component name: `twig(:[a-zA-Z0-9_\-]+)+`. Only the literal base
	// `twig` (scanned as a word that stops at the first `:`) can start one.
	if word == "twig" {
		if n := scanTwigComponentTail(source, pos+wordLen); n > 0 {
			return syntax.TkTwigComponentName, wordLen + n
		}
	}

	// Multi-word keyword tokens containing a literal space. These lex as a
	// single token. For `not` and `is` a boundary check (cb_not / cb_is) is
	// applied; the others are plain longest-match literals per logos.
	if k, n, ok := matchMultiWord(source, pos, word, wordLen); ok {
		return k, n
	}

	// Single-word keywords (case-sensitive), then the case-insensitive set.
	if k, ok := keywords[word]; ok {
		return k, wordLen
	}
	if k, ok := lookupCaseInsensitiveKeyword(word); ok {
		return k, wordLen
	}

	return syntax.TkWord, wordLen
}

// lookupCaseInsensitiveKeyword recognizes the small ignore-case keyword set
// without allocating a lowercased copy for every ordinary word.
func lookupCaseInsensitiveKeyword(word string) (syntax.Kind, bool) {
	switch len(word) {
	case len("true"):
		if equalFoldASCII(word, "true") {
			return syntax.TkTrue, true
		}
		if equalFoldASCII(word, "none") {
			return syntax.TkNone, true
		}
		if equalFoldASCII(word, "null") {
			return syntax.TkNull, true
		}
	case len("false"):
		if equalFoldASCII(word, "false") {
			return syntax.TkFalse, true
		}
	case len("doctype"):
		if equalFoldASCII(word, "doctype") {
			return syntax.TkDoctype, true
		}
	case len("ludtwig-ignore"):
		if equalFoldASCII(word, "ludtwig-ignore") {
			return syntax.TkLudtwigIgnore, true
		}
	case len("ludtwig-ignore-file"):
		if equalFoldASCII(word, "ludtwig-ignore-file") {
			return syntax.TkLudtwigIgnoreFile, true
		}
	}
	return 0, false
}

func equalFoldASCII(value, lowercase string) bool {
	for i := range value {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lowercase[i] {
			return false
		}
	}
	return true
}

// scanTwigComponentTail matches `(:[a-zA-Z0-9_\-]+)+` starting at pos (which
// should point at the first `:` after `twig`). Returns the consumed length, or
// 0 if there is not at least one full `:segment`.
func scanTwigComponentTail(source string, pos int) int {
	i := pos
	segments := 0
	for i < len(source) && source[i] == ':' {
		j := i + 1
		for j < len(source) && isComponentChar(source[j]) {
			j++
		}
		if j == i+1 {
			// Empty segment: this `:` is not part of the component name.
			break
		}
		i = j
		segments++
	}
	if segments == 0 {
		return 0
	}
	return i - pos
}

// matchMultiWord handles the space-containing keyword tokens. word is the first
// already-scanned word and wordLen its length. pos is the start of word.
func matchMultiWord(source string, pos int, word string, wordLen int) (syntax.Kind, int, bool) {
	after := pos + wordLen
	switch word {
	case "not":
		// cb_not: match ` in` only if followed by space/tab/'\n' or EOF.
		if n, ok := matchBoundedSuffix(source, after, " in"); ok {
			return syntax.TkNotIn, wordLen + n, true
		}
	case "is":
		// cb_is: match ` not` only if followed by space/tab/'\n' or EOF.
		if n, ok := matchBoundedSuffix(source, after, " not"); ok {
			return syntax.TkIsNot, wordLen + n, true
		}
	case "starts":
		if hasLiteral(source, after, " with") {
			return syntax.TkStartsWith, wordLen + len(" with"), true
		}
	case "ends":
		if hasLiteral(source, after, " with") {
			return syntax.TkEndsWith, wordLen + len(" with"), true
		}
	case "same":
		if hasLiteral(source, after, " as") {
			return syntax.TkSameAs, wordLen + len(" as"), true
		}
	case "divisible":
		if hasLiteral(source, after, " by") {
			return syntax.TkDivisibleBy, wordLen + len(" by"), true
		}
	case "ignore":
		if hasLiteral(source, after, " missing") {
			return syntax.TkIgnoreMissing, wordLen + len(" missing"), true
		}
	}
	return 0, 0, false
}

// matchBoundedSuffix ports the cb_not / cb_is boundary check: the suffix must
// be present at source[at:], and the character right after the suffix must be
// one of ' ', '\t', '\n', or the end of input.
func matchBoundedSuffix(source string, at int, suffix string) (int, bool) {
	if !hasLiteral(source, at, suffix) {
		return 0, false
	}
	end := at + len(suffix)
	if end >= len(source) {
		return len(suffix), true
	}
	switch source[end] {
	case ' ', '\t', '\n':
		return len(suffix), true
	}
	return 0, false
}

// hasLiteral reports whether source has the exact bytes lit starting at at.
func hasLiteral(source string, at int, lit string) bool {
	return at+len(lit) <= len(source) && source[at:at+len(lit)] == lit
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isAlnum(c byte) bool  { return isLetter(c) || isDigit(c) }
func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isWordStartLetter(c byte) bool { return isLetter(c) }
func isWordContinue(c byte) bool    { return isAlnum(c) || c == '_' || c == '-' }
func isComponentChar(c byte) bool   { return isAlnum(c) || c == '_' || c == '-' }
