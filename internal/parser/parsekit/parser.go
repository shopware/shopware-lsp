package parsekit

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// Config carries the language-specific configuration of a [Parser].
type Config struct {
	// GeneralRecoverySet holds the tokens that can begin a new top-level element.
	// It is always active in addition to any local recovery set passed to the
	// recovery functions: whatever garbage the parser is in, one of these
	// plausibly starts the next parseable element.
	GeneralRecoverySet []cst.Kind
	// ErrorKind is the node kind used to wrap skipped garbage during recovery
	// (the language's ERROR node).
	ErrorKind cst.Kind
	// EventsPerToken controls event-stream preallocation for the language.
	// Zero uses the conservative default of two events per lexer token.
	EventsPerToken int
}

// Parser is the event-based parser engine over a trivia-skipping source. Port of
// Rust's Parser. It is language-agnostic: the token kinds and grammar live in a
// language package (e.g. twig's parser) that drives this engine.
type Parser struct {
	source             *source
	events             *eventCollection
	parseErrs          []Error
	generalRecoverySet []cst.Kind
	errorKind          cst.Kind
	ownsTokens         bool
}

// BufferStats reports the parser's current transient-buffer utilization.
// It is intended for allocation benchmarks and capacity tuning; callers should
// sample it after completing the root marker and before Finish releases owned
// buffers back to their bounded pools.
type BufferStats struct {
	Tokens         int
	TokenCapacity  int
	Events         int
	EventCapacity  int
	Nodes          int
	MarkerCapacity int
}

func (p *Parser) BufferStats() BufferStats {
	if p == nil || p.source == nil || p.events == nil {
		return BufferStats{}
	}
	return BufferStats{
		Tokens:         len(p.source.tokens),
		TokenCapacity:  cap(p.source.tokens),
		Events:         len(p.events.events),
		EventCapacity:  cap(p.events.events),
		Nodes:          int(p.events.nodeCount),
		MarkerCapacity: p.events.markerCapacity(),
	}
}

// New builds a parser over the given lexer tokens, preallocating event capacity
// at 2x the token count.
func New(tokens []Token, cfg Config) *Parser {
	return newParser(tokens, cfg, false)
}

// NewOwned builds a parser that assumes ownership of tokens and returns their
// backing buffer to the bounded transient pool after Finish.
func NewOwned(tokens []Token, cfg Config) *Parser {
	return newParser(tokens, cfg, true)
}

func newParser(tokens []Token, cfg Config, ownsTokens bool) *Parser {
	eventsPerToken := cfg.EventsPerToken
	if eventsPerToken <= 0 {
		eventsPerToken = 2
	}
	markerCapacity := len(tokens) / 2
	if markerCapacity < 64 {
		markerCapacity = 64
	}
	return &Parser{
		source:             newSource(tokens),
		events:             newEventCollectionCap(len(tokens)*eventsPerToken, markerCapacity),
		generalRecoverySet: cfg.GeneralRecoverySet,
		errorKind:          cfg.ErrorKind,
		ownsTokens:         ownsTokens,
	}
}

// -- peeking / position ------------------------------------------------------

// Peek returns the kind of the current non-trivia token, or (KindNone, false) at
// EOF.
func (p *Parser) Peek() (cst.Kind, bool) {
	return p.source.peekKind()
}

// PeekToken returns the current non-trivia token, or nil at EOF.
func (p *Parser) PeekToken() *Token {
	return p.source.peekToken()
}

// PeekNthToken is raw lookahead (does not skip trivia between tokens); used to
// fuse adjacent lexer tokens.
func (p *Parser) PeekNthToken(n int) *Token {
	return p.source.peekNthToken(n)
}

// GetPos returns the raw cursor position (used by the grammar for backtrack-free
// progress checks).
func (p *Parser) GetPos() int {
	return p.source.getPos()
}

// PeekNextNonTriviaKind returns the kind of the next non-trivia token AFTER the
// current one, or (KindNone, false) if none.
func (p *Parser) PeekNextNonTriviaKind() (cst.Kind, bool) {
	return p.source.peekNextNonTriviaKind()
}

// -- predicates --------------------------------------------------------------

// At reports whether the current non-trivia token is of kind.
func (p *Parser) At(kind cst.Kind) bool {
	k, ok := p.Peek()
	return ok && k == kind
}

// AtSet reports whether the current non-trivia token is any of set.
func (p *Parser) AtSet(set []cst.Kind) bool {
	k, ok := p.Peek()
	if !ok {
		return false
	}
	for _, s := range set {
		if s == k {
			return true
		}
	}
	return false
}

// AtEnd reports whether the parser has reached EOF.
func (p *Parser) AtEnd() bool {
	_, ok := p.Peek()
	return !ok
}

// AtFollowing reports whether the first len(set) upcoming non-trivia kinds match
// set in order.
func (p *Parser) AtFollowing(set []cst.Kind) bool {
	return p.source.atFollowing(set)
}

// AtFollowingContent is like AtFollowing but also optionally matches token text.
func (p *Parser) AtFollowingContent(set []FollowingContent) bool {
	return p.source.atFollowingContent(set)
}

// -- consumption -------------------------------------------------------------

// Bump takes the next non-trivia token and emits an AddToken event with its
// lexer kind. Panics at EOF (the grammar must check AtEnd first).
func (p *Parser) Bump() *Token {
	consumed := p.source.nextToken()
	if consumed == nil {
		panic("parsekit: bump called, but there are no more tokens!")
	}
	p.events.addToken(consumed.Kind)
	return consumed
}

// BumpAs consumes one token but records it under a different kind. This is how a
// mode-less lexer works: e.g. re-labeling a Twig keyword as TK_WORD in HTML
// text. Returns a token carrying the new kind but the original text/range.
func (p *Parser) BumpAs(kind cst.Kind) Token {
	consumed := p.source.nextToken()
	if consumed == nil {
		panic("parsekit: bump called, but there are no more tokens!")
	}
	p.events.addToken(kind)
	result := *consumed
	result.Kind = kind
	return result
}

// BumpNextNAs consumes n raw lexer tokens and emits an AddNextNTokensAs event;
// the sink concatenates their texts into a single tree token. Panics if fewer
// than n tokens are available.
func (p *Parser) BumpNextNAs(n int, kind cst.Kind) []Token {
	consumed := p.source.nextNTokens(n)
	if len(consumed) != n {
		panic("parsekit: bump_next_n_as called, but there are not enough tokens!")
	}
	p.events.addNextNTokensAs(n, kind)
	return consumed
}

// ExplicitlyConsumeTrivia emits an ExplicitlyConsumeTrivia event, forcing
// trailing trivia inside the currently open node. Call just before Complete in
// string-content parsers.
func (p *Parser) ExplicitlyConsumeTrivia() {
	p.events.explicitlyConsumeTrivia()
}

// -- markers -----------------------------------------------------------------

// Start opens a new node marker.
func (p *Parser) Start() *Marker {
	return p.events.start()
}

// Complete closes marker m as a node of kind and returns a completed marker.
func (p *Parser) Complete(m *Marker, kind cst.Kind) CompletedMarker {
	return p.events.complete(m, kind)
}

// Precede starts a new marker wrapping the already-completed node cm.
func (p *Parser) Precede(cm CompletedMarker) *Marker {
	return p.events.precede(cm)
}

// -- expect / recovery -------------------------------------------------------

// Expect consumes kind if the parser is at it, otherwise records an error and
// runs RecoverExpect. Returns the consumed token (possibly the expected token
// found after skipping garbage) or nil.
func (p *Parser) Expect(kind cst.Kind, recoverySet []cst.Kind) *Token {
	if p.At(kind) {
		return p.Bump()
	}
	p.AddError(NewErrorBuilder(kind.TokenText()))
	return p.RecoverExpect([]cst.Kind{kind}, recoverySet)
}

// ExpectAny is like Expect but accepts any of the given kinds. The error string
// lists all accepted kinds joined by " or ".
func (p *Parser) ExpectAny(kinds []cst.Kind, recoverySet []cst.Kind) *Token {
	if p.AtSet(kinds) {
		return p.Bump()
	}
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = k.TokenText()
	}
	p.AddError(NewErrorBuilder(strings.Join(parts, " or ")))
	return p.RecoverExpect(kinds, recoverySet)
}

// Recover skips to a sync point wrapping any garbage in an ERROR node. It is
// RecoverExpect with no expected kinds. Do not call inside parseMany-style
// loops: it may consume tokens belonging to future siblings.
func (p *Parser) Recover(recoverySet []cst.Kind) {
	p.RecoverExpect(nil, recoverySet)
}

// RecoverExpect is the core recovery loop. Control flow is ported EXACTLY from
// parser.rs:
//
//  1. Bail without consuming anything if the current token is itself a sync
//     point (at end, or in recoverySet / the general recovery set). No ERROR
//     node is created — only the earlier error record.
//  2. Otherwise open an ERROR marker and loop: bump the offending token; if now
//     at one of expectedKinds, complete the ERROR node and consume+return the
//     expected token (resync past garbage); if now at a sync point, complete the
//     ERROR node and return nil.
//
// The wrapping ERROR node uses the kind configured as [Config.ErrorKind].
func (p *Parser) RecoverExpect(expectedKinds []cst.Kind, recoverySet []cst.Kind) *Token {
	if p.AtEnd() || p.AtSet(recoverySet) || p.AtSet(p.generalRecoverySet) {
		return nil
	}

	errorM := p.Start()
	for {
		p.Bump()

		if len(expectedKinds) != 0 && p.AtSet(expectedKinds) {
			p.Complete(errorM, p.errorKind)
			return p.Bump()
		}

		if p.AtEnd() || p.AtSet(p.generalRecoverySet) || p.AtSet(recoverySet) {
			p.Complete(errorM, p.errorKind)
			return nil
		}
	}
}

// AddError records a parse error without bumping any tokens. Missing range/found
// on the builder are filled from the current peeked token, or from the last
// token's range at EOF (found stays unset -> KindNone). A truly empty input
// receives the zero-width range 0..0.
func (p *Parser) AddError(b ErrorBuilder) {
	if !b.hasRange || !b.hasFound {
		current := p.source.peekToken()
		if current != nil {
			if !b.hasRange {
				b.rng = current.Range()
				b.hasRange = true
			}
			if !b.hasFound {
				b.found = current.Kind
				b.hasFound = true
			}
		} else {
			// EOF: use the range of the very last token; found stays unset.
			if !b.hasRange {
				rng, ok := p.source.lastTokenRange()
				if !ok {
					rng = cst.TextRange{}
				}
				b.rng = rng
				b.hasRange = true
			}
			// b.hasFound stays false -> built as KindNone
		}
	}
	p.parseErrs = append(p.parseErrs, b.build())
}

// Finish runs the sink over the accumulated event stream, producing the lossless
// tree and the flat error list. When [DebugAsserts] is on, it first verifies all
// markers were completed.
func (p *Parser) Finish(source string) (*cst.Tree, []Error) {
	if DebugAsserts {
		p.events.assertMarkersCompleted()
	}
	s := newSink(
		source,
		p.source.tokens,
		p.events.intoEventList(),
		p.events.directChildCountBuffer(),
		p.parseErrs,
		int(p.events.nodeCount),
	)
	tree, errors := s.finish()
	parserEventCollections.put(p.events)
	p.events = nil
	if p.ownsTokens {
		parserTokenBuffers.put(p.source.tokens)
		p.source.tokens = nil
	}
	return tree, errors
}
