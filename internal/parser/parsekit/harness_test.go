package parsekit

import (
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TestMain enables the debug safety-net assertions (LIFO marker completion,
// marker-leak detection) for the whole engine test suite.
func TestMain(m *testing.M) {
	DebugAsserts = true
	os.Exit(m.Run())
}

// tok is a (kind, text) pair used to build a synthetic token slice.
type tok struct {
	kind cst.Kind
	text string
}

// buildTokens turns a list of (kind, text) pairs into a token slice with
// contiguous ranges and returns the reconstructed source string. This lets
// hand-driven tests feed the parser/sink real ranges that slice cleanly out of
// the source the Builder holds.
func buildTokens(pairs ...tok) ([]Token, string) {
	var src []byte
	for _, p := range pairs {
		src = append(src, p.text...)
	}
	source := string(src)
	sourceRef := &source
	tokens := make([]Token, 0, len(pairs))
	pos := 0
	for _, p := range pairs {
		start := pos
		pos += len(p.text)
		tokens = append(tokens, NewToken(
			p.kind,
			sourceRef,
			cst.TextRange{Start: uint32(start), End: uint32(pos)},
		))
	}
	return tokens, source
}

// newTestParser builds an engine parser configured with the test language's
// general recovery set and ERROR node kind.
func newTestParser(tokens []Token) *Parser {
	return New(tokens, Config{GeneralRecoverySet: generalRecovery, ErrorKind: kError})
}

// runGrammar runs a hand-written grammar function against a token slice and
// returns the debug tree plus errors, exercising the real source/sink pipeline.
func runGrammar(t *testing.T, tokens []Token, source string, g func(p *Parser)) (string, []Error) {
	t.Helper()
	p := newTestParser(tokens)
	g(p)
	tree, errs := p.Finish(source)
	return cst.DebugTree(tree.Root), errs
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}
