package cst

import "strconv"

// Kind is the syntax kind of a node or token. It is a globally unique uint16
// shared across every registered language: each language reserves a contiguous
// range of kind values via [RegisterLanguage], so a single tree can, in
// principle, mix kinds from several languages (e.g. SCSS embedded in a twig
// <style> body). The methods [Kind.String], [Kind.TokenText], [Kind.IsTrivia]
// and [Kind.IsToken] are backed by flat lookup tables materialized at
// registration time, so they are O(1) with no per-call branching on language.
type Kind uint16

// KindNone is a sentinel for "no kind" (e.g. reaching EOF in an error). It is
// reserved and can never be claimed by a language range.
const KindNone Kind = 0xFFFF

// LanguageSpec describes the kind inventory of one language. Kinds are indexed
// from Base: the kind value Base+i has debug name KindNames[i] and diagnostic
// text TokenTexts[i]. Kinds in [Base, FirstNode) — where FirstNode is a
// kind value, not an index — are tokens; kinds in [FirstNode, Base+len) are
// composite nodes.
type LanguageSpec struct {
	// Name is a human-readable language name (e.g. "twig"). Must be non-empty.
	Name string
	// Base is the first kind value owned by this language. Ranges of distinct
	// languages must not overlap and must not include KindNone.
	Base Kind
	// KindNames holds the SCREAMING_SNAKE debug name of each kind, indexed from
	// Base. Its length defines the language's range: [Base, Base+len(KindNames)).
	KindNames []string
	// TokenTexts holds the diagnostic display text of each kind, indexed from
	// Base. An empty string means "use the KindName". May be shorter than
	// KindNames (missing entries are treated as "").
	TokenTexts []string
	// FirstNode is the first kind value that is a composite node; kinds below it
	// (down to Base) are tokens. Must lie within [Base, Base+len(KindNames)].
	FirstNode Kind
	// TriviaKinds lists the kinds that count as trivia (whitespace/line breaks).
	// Each must lie within the language's range.
	TriviaKinds []Kind
}

// maxKind tracks the highest registered kind value so the lookup tables can be
// sized exactly. It never reaches KindNone.
var (
	kindNames  []string // Kind -> debug name
	tokenTexts []string // Kind -> diagnostic text ("" = use name)
	isTrivia   []bool   // Kind -> trivia?
	isTokenTab []bool   // Kind -> token (vs node)?
	registered []Kind   // Base of each registered language, for overlap checks
	regNames   []string // Name of each registered language, parallel to registered
	regEnds    []Kind   // exclusive end of each registered language range
)

// RegisterLanguage records a language's kind inventory and materializes the
// flat lookup tables used by the Kind methods. It is intended to be called from
// package init functions only and panics on any inconsistency:
//
//   - an empty Name,
//   - a range that overlaps another registered language (or includes KindNone),
//   - a FirstNode outside the language's range,
//   - a trivia kind outside the language's range.
func RegisterLanguage(spec LanguageSpec) {
	if spec.Name == "" {
		panic("cst: RegisterLanguage: empty language name")
	}
	n := len(spec.KindNames)
	if n == 0 {
		panic("cst: RegisterLanguage: language " + spec.Name + " has no kinds")
	}
	base := uint32(spec.Base)
	end := base + uint32(n) // exclusive, in uint32 space to detect overflow
	// The range must not reach KindNone (0xFFFF) or wrap past uint16.
	if end > uint32(KindNone) {
		panic("cst: RegisterLanguage: language " + spec.Name + " range [" +
			strconv.FormatUint(uint64(base), 10) + ".." + strconv.FormatUint(uint64(end), 10) +
			") reaches or exceeds the reserved KindNone (65535)")
	}
	// Overlap check against every previously registered language.
	for i, ob := range registered {
		os, oe := uint32(ob), uint32(regEnds[i])
		if base < oe && os < end {
			panic("cst: RegisterLanguage: language " + spec.Name + " range [" +
				strconv.FormatUint(uint64(base), 10) + ".." + strconv.FormatUint(uint64(end), 10) +
				") overlaps language " + regNames[i] + " range [" +
				strconv.FormatUint(uint64(os), 10) + ".." + strconv.FormatUint(uint64(oe), 10) + ")")
		}
	}
	// FirstNode must fall within the range (a language may be all tokens, in
	// which case FirstNode == end).
	fn := uint32(spec.FirstNode)
	if fn < base || fn > end {
		panic("cst: RegisterLanguage: language " + spec.Name + " FirstNode " +
			strconv.FormatUint(uint64(fn), 10) + " is outside its range [" +
			strconv.FormatUint(uint64(base), 10) + ".." + strconv.FormatUint(uint64(end), 10) + ")")
	}
	// Trivia kinds must fall within the range.
	for _, tk := range spec.TriviaKinds {
		v := uint32(tk)
		if v < base || v >= end {
			panic("cst: RegisterLanguage: language " + spec.Name + " trivia kind " +
				strconv.FormatUint(uint64(v), 10) + " is outside its range [" +
				strconv.FormatUint(uint64(base), 10) + ".." + strconv.FormatUint(uint64(end), 10) + ")")
		}
	}

	// Grow the flat tables to cover this range.
	if int(end) > len(kindNames) {
		grow(int(end))
	}
	for i := 0; i < n; i++ {
		k := int(base) + i
		kindNames[k] = spec.KindNames[i]
		if i < len(spec.TokenTexts) {
			tokenTexts[k] = spec.TokenTexts[i]
		}
		isTokenTab[k] = Kind(k) < spec.FirstNode
	}
	for _, tk := range spec.TriviaKinds {
		isTrivia[tk] = true
	}

	registered = append(registered, spec.Base)
	regNames = append(regNames, spec.Name)
	regEnds = append(regEnds, Kind(end))
}

// grow extends the flat lookup tables to length n, preserving existing entries.
func grow(n int) {
	nk := make([]string, n)
	copy(nk, kindNames)
	kindNames = nk
	nt := make([]string, n)
	copy(nt, tokenTexts)
	tokenTexts = nt
	tr := make([]bool, n)
	copy(tr, isTrivia)
	isTrivia = tr
	it := make([]bool, n)
	copy(it, isTokenTab)
	isTokenTab = it
}

// IsTrivia reports whether the kind is trivia (whitespace or line break).
// Comments are not trivia. Unregistered kinds are never trivia.
func (k Kind) IsTrivia() bool {
	if int(k) < len(isTrivia) {
		return isTrivia[k]
	}
	return false
}

// IsToken reports whether the kind is a token kind (as opposed to a composite
// node kind). Unregistered kinds — including KindNone — report false.
func (k Kind) IsToken() bool {
	if int(k) < len(isTokenTab) {
		return isTokenTab[k]
	}
	return false
}

// String returns the kind's SCREAMING_SNAKE debug name (e.g. "TK_WORD",
// "TWIG_BLOCK", "ROOT"). The reserved [KindNone] renders as "NONE"; any other
// unregistered kind renders as "KIND(<n>)".
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		if s := kindNames[k]; s != "" {
			return s
		}
	}
	if k == KindNone {
		return "NONE"
	}
	return "KIND(" + strconv.FormatUint(uint64(k), 10) + ")"
}

// TokenText returns the human display text used in diagnostics ("{%", "word",
// ...). Kinds without diagnostic text fall back to their debug name, and
// unregistered kinds fall back to the same "KIND(<n>)" form as String.
func (k Kind) TokenText() string {
	if int(k) < len(tokenTexts) {
		if t := tokenTexts[k]; t != "" {
			return t
		}
	}
	return k.String()
}
