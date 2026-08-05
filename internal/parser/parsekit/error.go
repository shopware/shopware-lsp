package parsekit

import (
	"strconv"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// Error is a single parse diagnostic. It is a port of ludtwig's ParseError.
//
// Range is the byte range of the offending token (or the last token's range at
// EOF). Found is the kind of the token that was actually encountered;
// [cst.KindNone] means the parser reached end of file. Expected is the
// human-readable description of what was expected (e.g. "word" or
// `"}}", "-}}" or "~}}"`).
type Error struct {
	Range    cst.TextRange
	Found    cst.Kind
	Expected string
}

// Message renders the diagnostic body, matching ludtwig's expected_message():
//
//	found != KindNone -> "expected <Expected> but found <found.TokenText()>"
//	found == KindNone -> "expected <Expected> but reached end of file"
func (e *Error) Message() string {
	if e.Found != cst.KindNone {
		return "expected " + e.Expected + " but found " + e.Found.TokenText()
	}
	return "expected " + e.Expected + " but reached end of file"
}

// String renders the full diagnostic, matching ludtwig's Display impl:
//
//	"error at <start>..<end>: <Message>"
func (e *Error) String() string {
	return "error at " +
		strconv.FormatUint(uint64(e.Range.Start), 10) + ".." +
		strconv.FormatUint(uint64(e.Range.End), 10) + ": " +
		e.Message()
}

// ErrorBuilder mirrors ludtwig's ParseErrorBuilder. range/found are optional
// (hasRange/hasFound flags) and are inferred from the current token or EOF when
// the error is added, unless pinned explicitly via AtToken.
type ErrorBuilder struct {
	rng      cst.TextRange
	hasRange bool
	found    cst.Kind
	hasFound bool
	expected string
}

// NewErrorBuilder creates a builder describing what was expected. The found
// token and range are filled in later by [Parser.AddError].
func NewErrorBuilder(expected string) ErrorBuilder {
	return ErrorBuilder{expected: expected}
}

// AtToken pins the range and found kind explicitly to the given token. Used by
// the grammar for errors about already-consumed tokens.
func (b ErrorBuilder) AtToken(tok *Token) ErrorBuilder {
	b.rng = tok.Range()
	b.hasRange = true
	b.found = tok.Kind
	b.hasFound = true
	return b
}

// build finalizes the builder into an Error. range must have been set. When
// found was never set (EOF), it is recorded as [cst.KindNone].
func (b ErrorBuilder) build() Error {
	found := cst.KindNone
	if b.hasFound {
		found = b.found
	}
	return Error{
		Range:    b.rng,
		Found:    found,
		Expected: b.expected,
	}
}
