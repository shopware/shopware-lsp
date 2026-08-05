package parsekit

import "testing"

// TestMarkerPrecedeWrapOrdering exercises Precede building a left-nested wrap
// chain (the HTML_STARTING_TAG -> HTML_TAG -> ROOT example from event.rs)
// through the full sink pipeline.
func TestMarkerPrecedeWrapOrdering(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkLessThan, "<"},
		tok{tkWord, "div"},
	)
	tree, errs := runGrammar(t, tokens, source, func(p *Parser) {
		startTag := p.Start()
		p.Bump() // <
		p.Bump() // div
		completedStart := p.Complete(startTag, kHtmlStartingTag)
		tag := p.Precede(completedStart)
		completedTag := p.Complete(tag, kHtmlTag)
		rootM := p.Precede(completedTag)
		p.Complete(rootM, kRoot)
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := "ROOT@0..4\n" +
		"  HTML_TAG@0..4\n" +
		"    HTML_STARTING_TAG@0..4\n" +
		"      TK_LESS_THAN@0..1 \"<\"\n" +
		"      TK_WORD@1..4 \"div\""
	if want != tree {
		t.Fatalf("wrap ordering mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestBumpAsRelabels checks BumpAs records a token under a different kind while
// keeping the original text.
func TestBumpAsRelabels(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkIf, "if"}, // keyword token in the lexer
	)
	tree, _ := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.BumpAs(tkWord) // re-label as plain word (HTML text context)
		p.Complete(m, kRoot)
	})
	want := "ROOT@0..2\n  TK_WORD@0..2 \"if\""
	if tree != want {
		t.Fatalf("bumpAs mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestBumpNextNAsFusion checks n adjacent lexer tokens fuse into one tree token
// spanning first.start..last.end.
func TestBumpNextNAsFusion(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "foo"},
		tok{tkColon, "-"},
		tok{tkWord, "bar"},
	)
	tree, _ := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.BumpNextNAs(3, tkWord)
		p.Complete(m, kRoot)
	})
	want := "ROOT@0..7\n  TK_WORD@0..7 \"foo-bar\""
	if tree != want {
		t.Fatalf("fusion mismatch:\n got: %q\nwant: %q", tree, want)
	}
}
