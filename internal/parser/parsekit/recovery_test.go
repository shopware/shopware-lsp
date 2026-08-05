package parsekit

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// varCloseSet mirrors the twig var close tokens: }} -}} ~}}
var varCloseSet = []cst.Kind{tkCloseCurlyCurly, tkMinusCloseCurlyCurly, tkTildeCloseCurlyCurly}

// TestRecoverBailAtSyncPoint: when the current unexpected token is itself a sync
// point (in the recovery set or the general recovery set), RecoverExpect must
// bail WITHOUT consuming it and WITHOUT creating an ERROR node — only an error
// record.
func TestRecoverBailAtSyncPoint(t *testing.T) {
	// Grammar: expect a TK_WORD, but we are sitting on `{%` (a general sync
	// point). The `{%` must be left for the outer parser and wrapped in nothing.
	tokens, source := buildTokens(
		tok{tkCurlyPercent, "{%"},
		tok{tkWord, "leftover"},
	)
	tree, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Expect(tkWord, nil) // no local recovery set; {% is general sync
		// consume the rest so the sink's all-consumed assert passes
		for !p.AtEnd() {
			p.Bump()
		}
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(errs), errs)
	}
	if got := errs[0].String(); got != "error at 0..2: expected word but found {%" {
		t.Fatalf("error message mismatch: %q", got)
	}
	// No ERROR node: the {% was bumped by the trailing loop, not wrapped.
	want := "ROOT@0..10\n" +
		"  TK_CURLY_PERCENT@0..2 \"{%\"\n" +
		"  TK_WORD@2..10 \"leftover\""
	if tree != want {
		t.Fatalf("tree mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestRecoverSkipThenFindExpected: garbage before the expected token is wrapped
// in an ERROR node, then the expected token is consumed normally (expect still
// succeeds after error).
func TestRecoverSkipThenFindExpected(t *testing.T) {
	// expect TK_WORD while sitting on garbage (TK_NUMBER), then TK_WORD follows.
	tokens, source := buildTokens(
		tok{tkNumber, "42"},
		tok{tkWord, "ok"},
	)
	tree, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Expect(tkWord, nil)
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	if got := errs[0].String(); got != "error at 0..2: expected word but found number" {
		t.Fatalf("error message mismatch: %q", got)
	}
	want := "ROOT@0..4\n" +
		"  ERROR@0..2\n" +
		"    TK_NUMBER@0..2 \"42\"\n" +
		"  TK_WORD@2..4 \"ok\""
	if tree != want {
		t.Fatalf("tree mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestRecoverSkipToEOF: expected token never appears; garbage up to EOF is
// wrapped in an ERROR node and expect returns nil.
func TestRecoverSkipToEOF(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkNumber, "42"},
		tok{tkNumber, "43"},
	)
	tree, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Expect(tkWord, nil)
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	if got := errs[0].String(); got != "error at 0..2: expected word but found number" {
		t.Fatalf("error message mismatch: %q", got)
	}
	want := "ROOT@0..4\n" +
		"  ERROR@0..4\n" +
		"    TK_NUMBER@0..2 \"42\"\n" +
		"    TK_NUMBER@2..4 \"43\""
	if tree != want {
		t.Fatalf("tree mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestRecoverStopsAtLocalRecoverySet: garbage is skipped only until a token in
// the local recovery set, which is left unconsumed.
func TestRecoverStopsAtLocalRecoverySet(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkNumber, "42"},
		tok{tkColon, ":"}, // local recovery set member
		tok{tkWord, "rest"},
	)
	tree, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Expect(tkWord, []cst.Kind{tkColon})
		// consume remainder
		for !p.AtEnd() {
			p.Bump()
		}
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	want := "ROOT@0..7\n" +
		"  ERROR@0..2\n" +
		"    TK_NUMBER@0..2 \"42\"\n" +
		"  TK_COLON@2..3 \":\"\n" +
		"  TK_WORD@3..7 \"rest\""
	if tree != want {
		t.Fatalf("tree mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestExpectAnyErrorMessage: ExpectAny lists all accepted kinds joined by " or ".
func TestExpectAnyErrorMessage(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkNumber, "42"},
	)
	_, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.ExpectAny(varCloseSet, nil)
		// nothing consumed by recover (42 is not a sync point -> wrapped),
		// actually 42 is not sync so it gets wrapped in ERROR to EOF.
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	// ludtwig's SyntaxKind Display prints the raw delimiter text (unquoted),
	// joined by " or " (parser.rs expect_any).
	want := `error at 0..2: expected }} or -}} or ~}} but found number`
	if got := errs[0].String(); got != want {
		t.Fatalf("expectAny message mismatch:\n got: %q\nwant: %q", got, want)
	}
}
