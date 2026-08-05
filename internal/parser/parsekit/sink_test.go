package parsekit

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TestSinkEatWhitespace ports sink.rs sink_eat_whitespace: leading trivia goes
// inside the node containing the next token, trailing trivia at EOF lands in
// ROOT.
func TestSinkEatWhitespace(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWhitespace, "  "},
		tok{tkLineBreak, "\n"},
		tok{tkWord, "hello"},
		tok{tkLineBreak, "\n"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
		tok{tkWord, "world"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
	)
	tree, _ := runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Bump()
		p.Bump()
		p.Complete(m, kRoot)
	})
	want := "ROOT@0..20\n" +
		"  TK_WHITESPACE@0..2 \"  \"\n" +
		"  TK_LINE_BREAK@2..3 \"\\n\"\n" +
		"  TK_WORD@3..8 \"hello\"\n" +
		"  TK_LINE_BREAK@8..9 \"\\n\"\n" +
		"  TK_LINE_BREAK@9..10 \"\\n\"\n" +
		"  TK_WHITESPACE@10..12 \"  \"\n" +
		"  TK_WORD@12..17 \"world\"\n" +
		"  TK_LINE_BREAK@17..18 \"\\n\"\n" +
		"  TK_WHITESPACE@18..20 \"  \""
	if tree != want {
		t.Fatalf("trivia attachment mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

// TestSinkForwardParentHandling ports sink.rs sink_forward_parent_handling:
// forward-parent chain resolution plus leading trivia landing deep inside the
// innermost node.
func TestSinkForwardParentHandling(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWhitespace, "  "},
		tok{tkLineBreak, "\n"},
		tok{tkWord, "hello"},
		tok{tkLineBreak, "\n"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
		tok{tkWord, "world"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
	)
	tree, _ := runGrammar(t, tokens, source, func(p *Parser) {
		outer := p.Start()
		p.Bump() // hello

		inner := p.Start()
		p.Bump() // world
		innerCompleted := p.Complete(inner, kHtmlString)
		innerWrapper := p.Precede(innerCompleted)
		innerWrapperCompleted := p.Complete(innerWrapper, kBody)
		outerWrapper := p.Precede(innerWrapperCompleted)
		p.Complete(outerWrapper, kError)

		p.Complete(outer, kRoot)
	})
	want := "ROOT@0..20\n" +
		"  TK_WHITESPACE@0..2 \"  \"\n" +
		"  TK_LINE_BREAK@2..3 \"\\n\"\n" +
		"  TK_WORD@3..8 \"hello\"\n" +
		"  ERROR@8..17\n" +
		"    BODY@8..17\n" +
		"      HTML_STRING@8..17\n" +
		"        TK_LINE_BREAK@8..9 \"\\n\"\n" +
		"        TK_LINE_BREAK@9..10 \"\\n\"\n" +
		"        TK_WHITESPACE@10..12 \"  \"\n" +
		"        TK_WORD@12..17 \"world\"\n" +
		"  TK_LINE_BREAK@17..18 \"\\n\"\n" +
		"  TK_WHITESPACE@18..20 \"  \""
	if tree != want {
		t.Fatalf("forward-parent mismatch:\n got: %q\nwant: %q", tree, want)
	}
}

func TestSinkPreallocatesExactChildrenWithForwardParents(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWhitespace, " "},
		tok{tkWord, "hello"},
		tok{tkWhitespace, " "},
		tok{tkWord, "world"},
		tok{tkWhitespace, " "},
	)
	p := newTestParser(tokens)
	root := p.Start()
	p.Bump()
	inner := p.Start()
	p.Bump()
	completed := p.Complete(inner, kHtmlString)
	wrapper := p.Precede(completed)
	p.Complete(wrapper, kBody)
	p.Complete(root, kRoot)

	tree, _ := p.Finish(source)
	for element := range tree.Root.Descendants() {
		node, ok := element.(*cst.Node)
		if !ok {
			continue
		}
		if node.ChildCount() != node.ChildCapacity() {
			t.Fatalf(
				"%s child len/cap = %d/%d, want exact",
				node.Kind(),
				node.ChildCount(),
				node.ChildCapacity(),
			)
		}
	}
}

func TestSinkSpillsLargeDirectChildCounts(t *testing.T) {
	const childCount = int(directChildCountMask) + 73
	pairs := make([]tok, childCount)
	for index := range pairs {
		pairs[index] = tok{tkWord, "x"}
	}
	tokens, source := buildTokens(pairs...)
	p := newTestParser(tokens)
	root := p.Start()
	for range childCount {
		p.Bump()
	}
	p.Complete(root, kRoot)

	tree, errors := p.Finish(source)
	if len(errors) != 0 {
		t.Fatalf("unexpected errors: %v", errors)
	}
	if tree.Root.ChildCount() != childCount ||
		tree.Root.ChildCapacity() != childCount {
		t.Fatalf(
			"large root child len/cap = %d/%d, want %d",
			tree.Root.ChildCount(),
			tree.Root.ChildCapacity(),
			childCount,
		)
	}
}

// TestSinkNotAllConsumedPanics ports sink.rs sink_non_reported_token_by_parser:
// the sink asserts every lexer token was consumed.
func TestSinkNotAllConsumedPanics(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "hello"},
		tok{tkWhitespace, " "},
		tok{tkWord, "world"},
	)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic for unconsumed tokens")
		}
		if !strings.Contains(toStr(r), "did not consume all tokens") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	runGrammar(t, tokens, source, func(p *Parser) {
		m := p.Start()
		p.Bump() // only "hello"; "world" left unconsumed
		p.Complete(m, kRoot)
	})
}

// TestExplicitlyConsumeTrivia checks that trailing trivia is pulled INSIDE the
// open node when ExplicitlyConsumeTrivia is emitted before Complete.
func TestExplicitlyConsumeTrivia(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "a"},
		tok{tkWhitespace, " "},
		tok{tkWord, "b"},
	)
	// Without ExplicitlyConsumeTrivia the whitespace after "a" would fall
	// outside the inner node; with it, the whitespace is consumed inside.
	treeWith, _ := runGrammar(t, tokens, source, func(p *Parser) {
		root := p.Start()
		inner := p.Start()
		p.Bump() // a
		p.ExplicitlyConsumeTrivia()
		p.Complete(inner, kBody)
		p.Bump() // b
		p.Complete(root, kRoot)
	})
	wantWith := "ROOT@0..3\n" +
		"  BODY@0..2\n" +
		"    TK_WORD@0..1 \"a\"\n" +
		"    TK_WHITESPACE@1..2 \" \"\n" +
		"  TK_WORD@2..3 \"b\""
	if treeWith != wantWith {
		t.Fatalf("explicitlyConsumeTrivia mismatch:\n got: %q\nwant: %q", treeWith, wantWith)
	}

	// Control: same shape without ExplicitlyConsumeTrivia keeps the whitespace
	// outside the BODY (attached before the next token "b").
	treeWithout, _ := runGrammar(t, tokens, source, func(p *Parser) {
		root := p.Start()
		inner := p.Start()
		p.Bump() // a
		p.Complete(inner, kBody)
		p.Bump() // b
		p.Complete(root, kRoot)
	})
	wantWithout := "ROOT@0..3\n" +
		"  BODY@0..1\n" +
		"    TK_WORD@0..1 \"a\"\n" +
		"  TK_WHITESPACE@1..2 \" \"\n" +
		"  TK_WORD@2..3 \"b\""
	if treeWithout != wantWithout {
		t.Fatalf("control mismatch:\n got: %q\nwant: %q", treeWithout, wantWithout)
	}
}
