package parsekit

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TestErrorMessageFound ports parse_error.rs parse_error_display.
func TestErrorMessageFound(t *testing.T) {
	e := Error{
		Range:    cst.TextRange{Start: 3, End: 5},
		Found:    tkCurlyPercent,
		Expected: "word",
	}
	if got := e.String(); got != "error at 3..5: expected word but found {%" {
		t.Fatalf("String mismatch: %q", got)
	}
	if got := e.Message(); got != "expected word but found {%" {
		t.Fatalf("Message mismatch: %q", got)
	}
}

// TestErrorMessageEOF checks the KindNone (reached end of file) branch.
func TestErrorMessageEOF(t *testing.T) {
	e := Error{
		Range:    cst.TextRange{Start: 7, End: 9},
		Found:    cst.KindNone,
		Expected: "word",
	}
	if got := e.Message(); got != "expected word but reached end of file" {
		t.Fatalf("Message mismatch: %q", got)
	}
	if got := e.String(); got != "error at 7..9: expected word but reached end of file" {
		t.Fatalf("String mismatch: %q", got)
	}
}

// TestAddErrorEOFInference: at EOF, AddError fills range from the last token and
// leaves Found as KindNone (reached end of file).
func TestAddErrorEOFInference(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "only"},
	)
	_, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Bump() // consume the only token -> now at EOF
		// expect a word at EOF: error recorded with EOF inference
		p.Expect(tkWord, nil)
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	if errs[0].Found != cst.KindNone {
		t.Fatalf("expected Found=KindNone at EOF, got %v", errs[0].Found)
	}
	// range comes from the last token "only" (0..4)
	if errs[0].Range != (cst.TextRange{Start: 0, End: 4}) {
		t.Fatalf("expected last-token range 0..4, got %v", errs[0].Range)
	}
	if got := errs[0].String(); got != "error at 0..4: expected word but reached end of file" {
		t.Fatalf("String mismatch: %q", got)
	}
}

func TestAddErrorOnEmptyInputUsesZeroWidthRange(t *testing.T) {
	p := newTestParser(nil)
	m := p.Start()
	p.AddError(NewErrorBuilder("value"))
	p.Complete(m, kRoot)

	_, errors := p.Finish("")
	if len(errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(errors))
	}
	if errors[0].Range != (cst.TextRange{}) || errors[0].Found != cst.KindNone {
		t.Fatalf("empty-input error = %#v", errors[0])
	}
}

// TestAddErrorAtTokenPinning: AtToken pins range+found explicitly, so a later
// AddError does not overwrite them with the current token.
func TestAddErrorAtTokenPinning(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "first"},
		tok{tkNumber, "42"},
	)
	_, errs := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		pinned := *p.PeekToken() // "first" at 0..5
		p.Bump()                 // move past "first"; current token is now "42"
		// error about the already-consumed "first" token
		p.AddError(NewErrorBuilder("something").AtToken(&pinned))
		p.Bump() // consume 42
		p.Complete(m, kRoot)
	})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	if errs[0].Range != (cst.TextRange{Start: 0, End: 5}) {
		t.Fatalf("expected pinned range 0..5, got %v", errs[0].Range)
	}
	if errs[0].Found != tkWord {
		t.Fatalf("expected pinned Found=TkWord, got %v", errs[0].Found)
	}
}
