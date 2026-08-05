package parsekit

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// source is a trivia-skipping cursor over the flat lexer token slice. Every
// read/peek first eats leading trivia. Port of Rust's Source.
type source struct {
	tokens []Token
	cursor int
}

func newSource(tokens []Token) *source {
	return &source{tokens: tokens}
}

// nextToken eats leading trivia and returns the next token (advancing the
// cursor), or nil at EOF.
func (s *source) nextToken() *Token {
	s.eatTrivia()
	if s.cursor >= len(s.tokens) {
		return nil
	}
	tok := &s.tokens[s.cursor]
	s.cursor++
	return tok
}

// nextNTokens eats leading trivia then takes the next n RAW tokens (no trivia
// skipping between them) and advances the cursor by n. Returns the slice of
// tokens actually available (may be shorter than n at EOF).
func (s *source) nextNTokens(n int) []Token {
	s.eatTrivia()
	end := s.cursor + n
	if end > len(s.tokens) {
		end = len(s.tokens)
	}
	toks := s.tokens[s.cursor:end]
	s.cursor += n
	return toks
}

// peekKind eats leading trivia and returns the kind of the current token, or
// (KindNone, false) at EOF.
func (s *source) peekKind() (cst.Kind, bool) {
	s.eatTrivia()
	return s.peekKindRaw()
}

// peekToken eats leading trivia and returns the current token, or nil at EOF.
func (s *source) peekToken() *Token {
	s.eatTrivia()
	return s.peekTokenRaw()
}

// peekNthToken eats leading trivia then returns the n-th RAW token from the
// cursor without skipping trivia between tokens. Used to fuse adjacent lexer
// tokens; for n==0 prefer peekToken. Returns nil if out of range.
func (s *source) peekNthToken(n int) *Token {
	s.eatTrivia()
	idx := s.cursor + n
	if idx < 0 || idx >= len(s.tokens) {
		return nil
	}
	return &s.tokens[idx]
}

// atFollowing eats leading trivia then compares the upcoming non-trivia kinds
// against set in order. It returns true if the first len(set) non-trivia kinds
// match; a prefix of the token stream matching the whole set counts, but an
// empty upcoming stream does not (returns false when already at end).
func (s *source) atFollowing(set []cst.Kind) bool {
	s.eatTrivia()
	if s.cursor == len(s.tokens) {
		return false
	}
	setIdx := 0
	tokIdx := s.cursor
	for {
		if setIdx >= len(set) {
			// set exhausted (whether or not tokens remain) => match
			return true
		}
		// advance to next non-trivia token
		for tokIdx < len(s.tokens) && s.tokens[tokIdx].Kind.IsTrivia() {
			tokIdx++
		}
		if tokIdx >= len(s.tokens) {
			// tokens exhausted but set still has entries
			return false
		}
		if s.tokens[tokIdx].Kind != set[setIdx] {
			return false
		}
		setIdx++
		tokIdx++
	}
}

// FollowingContent is one entry for [Parser.AtFollowingContent]: a kind and an
// optional exact text match (MatchText true = require text == Text).
type FollowingContent struct {
	Kind      cst.Kind
	MatchText bool
	Text      string
}

// atFollowingContent is like atFollowing but also optionally matches token text.
func (s *source) atFollowingContent(set []FollowingContent) bool {
	s.eatTrivia()
	if s.cursor == len(s.tokens) {
		return false
	}
	setIdx := 0
	tokIdx := s.cursor
	for {
		if setIdx >= len(set) {
			return true
		}
		for tokIdx < len(s.tokens) && s.tokens[tokIdx].Kind.IsTrivia() {
			tokIdx++
		}
		if tokIdx >= len(s.tokens) {
			return false
		}
		want := set[setIdx]
		tok := &s.tokens[tokIdx]
		if tok.Kind != want.Kind {
			return false
		}
		if want.MatchText && tok.Text() != want.Text {
			return false
		}
		setIdx++
		tokIdx++
	}
}

// peekNextNonTriviaKind eats leading trivia then returns the kind of the next
// non-trivia token AFTER the current one. Returns (KindNone, false) if none.
func (s *source) peekNextNonTriviaKind() (cst.Kind, bool) {
	s.eatTrivia()
	pos := s.cursor + 1
	for pos < len(s.tokens) {
		k := s.tokens[pos].Kind
		if !k.IsTrivia() {
			return k, true
		}
		pos++
	}
	return cst.KindNone, false
}

// lastTokenRange returns the range of the very last lexer token, or (zero,
// false) if there are no tokens at all.
func (s *source) lastTokenRange() (cst.TextRange, bool) {
	if len(s.tokens) == 0 {
		return cst.TextRange{}, false
	}
	return s.tokens[len(s.tokens)-1].Range(), true
}

// getPos returns the raw cursor position (used by the grammar for backtrack-free
// position checks).
func (s *source) getPos() int {
	return s.cursor
}

func (s *source) eatTrivia() {
	for s.cursor < len(s.tokens) && s.tokens[s.cursor].Kind.IsTrivia() {
		s.cursor++
	}
}

func (s *source) peekKindRaw() (cst.Kind, bool) {
	if s.cursor < len(s.tokens) {
		return s.tokens[s.cursor].Kind, true
	}
	return cst.KindNone, false
}

func (s *source) peekTokenRaw() *Token {
	if s.cursor < len(s.tokens) {
		return &s.tokens[s.cursor]
	}
	return nil
}
