package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/twig/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// This file is the thin twig-specific layer over the language-agnostic
// [parsekit.Parser] engine. It keeps the twig recovery sets and predicates and
// re-exposes the engine's exported API under the lowercase names the seven
// grammar files use, so those files compile unchanged.

// Error is a single parse diagnostic. It aliases [parsekit.Error].
type Error = parsekit.Error

// marker and completedMarker alias the engine types so the grammar keeps
// referring to them by their old unexported names.
type (
	marker          = parsekit.Marker
	completedMarker = parsekit.CompletedMarker
)

// errorBuilder wraps [parsekit.ErrorBuilder] so the grammar can keep calling the
// lowercase atToken method (a method cannot be added to the aliased engine type
// directly). newErrorBuilder returns this wrapper; addError unwraps it.
type errorBuilder struct {
	parsekit.ErrorBuilder
}

// newErrorBuilder creates a builder describing what was expected.
func newErrorBuilder(expected string) errorBuilder {
	return errorBuilder{parsekit.NewErrorBuilder(expected)}
}

// atToken pins the range and found kind explicitly to the given token.
func (b errorBuilder) atToken(tok *lexer.Token) errorBuilder {
	return errorBuilder{b.AtToken(tok)}
}

// Recovery sets, ported from parser.rs.

// generalRecoverySet holds the tokens that can begin a new top-level element.
// It is always active in addition to any local recovery set: whatever garbage
// the parser is in, one of these plausibly starts the next parseable element.
var generalRecoverySet = []syntax.Kind{
	syntax.TkCurlyPercent, syntax.TkCurlyPercentMinus, syntax.TkCurlyPercentTilde,
	syntax.TkOpenCurlyCurly, syntax.TkOpenCurlyCurlyMinus, syntax.TkOpenCurlyCurlyTilde,
	syntax.TkOpenCurlyHashtag, syntax.TkOpenCurlyHashtagMinus, syntax.TkOpenCurlyHashtagTilde,
	syntax.TkLessThan, syntax.TkLessThanExclamationMarkMinusMinus, syntax.TkLessThanExclamationMark,
}

// twigBlockOpenSet holds the twig block open tokens: {% {%- {%~
var twigBlockOpenSet = []syntax.Kind{
	syntax.TkCurlyPercent, syntax.TkCurlyPercentMinus, syntax.TkCurlyPercentTilde,
}

// twigBlockCloseSet holds the twig block close tokens: %} -%} ~%}
var twigBlockCloseSet = []syntax.Kind{
	syntax.TkPercentCurly, syntax.TkMinusPercentCurly, syntax.TkTildePercentCurly,
}

// twigVarOpenSet holds the twig var open tokens: {{ {{- {{~
var twigVarOpenSet = []syntax.Kind{
	syntax.TkOpenCurlyCurly, syntax.TkOpenCurlyCurlyMinus, syntax.TkOpenCurlyCurlyTilde,
}

// twigVarCloseSet holds the twig var close tokens: }} -}} ~}}
var twigVarCloseSet = []syntax.Kind{
	syntax.TkCloseCurlyCurly, syntax.TkMinusCloseCurlyCurly, syntax.TkTildeCloseCurlyCurly,
}

// twigCommentOpenSet holds the twig comment open tokens: {# {#- {#~
var twigCommentOpenSet = []syntax.Kind{
	syntax.TkOpenCurlyHashtag, syntax.TkOpenCurlyHashtagMinus, syntax.TkOpenCurlyHashtagTilde,
}

// twigCommentCloseSet holds the twig comment close tokens: #} -#} ~#}
var twigCommentCloseSet = []syntax.Kind{
	syntax.TkHashtagCloseCurly, syntax.TkMinusHashtagCloseCurly, syntax.TkTildeHashtagCloseCurly,
}

// parser is the twig-specific wrapper over the engine. It embeds
// *parsekit.Parser and re-exposes its methods under the lowercase names the
// grammar uses.
type parser struct {
	*parsekit.Parser
}

// newParser builds a parser over the given lexer tokens, configuring the engine
// with the twig general recovery set and ERROR node kind.
func newParser(tokens []lexer.Token) *parser {
	return newParserWithOwnership(tokens, false)
}

func newOwnedParser(tokens []lexer.Token) *parser {
	return newParserWithOwnership(tokens, true)
}

func newParserWithOwnership(tokens []lexer.Token, owned bool) *parser {
	newEngine := parsekit.New
	if owned {
		newEngine = parsekit.NewOwned
	}
	return &parser{
		Parser: newEngine(tokens, parsekit.Config{
			GeneralRecoverySet: generalRecoverySet,
			ErrorKind:          syntax.Error,
			EventsPerToken:     5,
		}),
	}
}

// -- peeking / position ------------------------------------------------------

func (p *parser) peekToken() *lexer.Token { return p.PeekToken() }

// peekNthToken is raw lookahead (does not skip trivia between tokens); used to
// fuse adjacent lexer tokens.
func (p *parser) peekNthToken(n int) *lexer.Token { return p.PeekNthToken(n) }

func (p *parser) getPos() int { return p.GetPos() }

func (p *parser) peekNextNonTriviaKind() (syntax.Kind, bool) { return p.PeekNextNonTriviaKind() }

// -- predicates --------------------------------------------------------------

func (p *parser) at(kind syntax.Kind) bool { return p.At(kind) }

func (p *parser) atSet(set []syntax.Kind) bool { return p.AtSet(set) }

func (p *parser) atEnd() bool { return p.AtEnd() }

func (p *parser) atFollowing(set []syntax.Kind) bool { return p.AtFollowing(set) }

func (p *parser) atFollowingContent(set []followingContent) bool {
	pk := make([]parsekit.FollowingContent, len(set))
	for i, fc := range set {
		pk[i] = parsekit.FollowingContent{Kind: fc.kind, MatchText: fc.matchText, Text: fc.text}
	}
	return p.AtFollowingContent(pk)
}

// followingContent is one entry for atFollowingContent: a kind and an optional
// exact text match (matchText true = require text == Text). It stays a distinct
// twig-side struct (rather than an alias for parsekit.FollowingContent) so the
// grammar can keep constructing it with lowercase field names.
type followingContent struct {
	kind      syntax.Kind
	matchText bool
	text      string
}

// atTwigTag efficiently checks whether the parser is at a `{% keyword` sequence
// (including the {%- and {%~ whitespace-control variants).
func (p *parser) atTwigTag(keyword syntax.Kind) bool {
	if !p.atTwigBlockOpen() {
		return false
	}
	k, ok := p.peekNextNonTriviaKind()
	return ok && k == keyword
}

func (p *parser) atTwigWordTag(name string) bool {
	for _, opening := range twigBlockOpenSet {
		if p.atFollowingContent([]followingContent{
			{kind: opening},
			{kind: syntax.TkWord, matchText: true, text: name},
		}) {
			return true
		}
	}
	return false
}

func (p *parser) atTwigBlockOpen() bool  { return p.atSet(twigBlockOpenSet) }
func (p *parser) atTwigBlockClose() bool { return p.atSet(twigBlockCloseSet) }
func (p *parser) atTwigVarOpen() bool    { return p.atSet(twigVarOpenSet) }
func (p *parser) atTwigCommentOpen() bool {
	return p.atSet(twigCommentOpenSet)
}

// -- consumption -------------------------------------------------------------

func (p *parser) bump() *lexer.Token { return p.Bump() }

func (p *parser) bumpAs(kind syntax.Kind) lexer.Token { return p.BumpAs(kind) }

func (p *parser) bumpNextNAs(n int, kind syntax.Kind) []lexer.Token { return p.BumpNextNAs(n, kind) }

func (p *parser) explicitlyConsumeTrivia() { p.ExplicitlyConsumeTrivia() }

// -- markers -----------------------------------------------------------------

func (p *parser) start() *marker { return p.Start() }

func (p *parser) complete(m *marker, kind syntax.Kind) completedMarker { return p.Complete(m, kind) }

func (p *parser) precede(cm completedMarker) *marker { return p.Precede(cm) }

// -- expect / recovery -------------------------------------------------------

func (p *parser) expect(kind syntax.Kind, recoverySet []syntax.Kind) *lexer.Token {
	return p.Expect(kind, recoverySet)
}

func (p *parser) expectAny(kinds []syntax.Kind, recoverySet []syntax.Kind) *lexer.Token {
	return p.ExpectAny(kinds, recoverySet)
}

func (p *parser) recover(recoverySet []syntax.Kind) { p.Recover(recoverySet) }

func (p *parser) addError(b errorBuilder) { p.AddError(b.ErrorBuilder) }
