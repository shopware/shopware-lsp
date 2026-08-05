package parsekit

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TestSourceSkipWhitespace ports source.rs source_skip_whitespace.
func TestSourceSkipWhitespace(t *testing.T) {
	tokens, _ := buildTokens(
		tok{tkWhitespace, "  "},
		tok{tkLineBreak, "\n"},
		tok{tkWord, "word"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
	)
	s := newSource(tokens)
	if k, ok := s.peekKind(); !ok || k != tkWord {
		t.Fatalf("peekKind = %v,%v", k, ok)
	}
	nt := s.nextToken()
	if nt == nil || nt.Kind != tkWord || nt.Text() != "word" {
		t.Fatalf("nextToken = %+v", nt)
	}
	if _, ok := s.peekKind(); ok {
		t.Fatalf("expected EOF after trailing trivia")
	}
	if s.nextToken() != nil {
		t.Fatalf("expected nil nextToken at EOF")
	}
}

// TestSourceAtFollowing ports source.rs source_at_following.
func TestSourceAtFollowing(t *testing.T) {
	tokens, _ := buildTokens(
		tok{tkWhitespace, "  "},
		tok{tkLineBreak, "\n"},
		tok{tkWord, "hello"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
		tok{tkLessThan, "<"},
		tok{tkLineBreak, "\n"},
		tok{tkGreaterThan, ">"},
		tok{tkWhitespace, "  "},
	)
	s := newSource(tokens)
	w, lt, gt := tkWord, tkLessThan, tkGreaterThan

	if !s.atFollowing([]cst.Kind{w, lt, gt}) {
		t.Fatal("atFollowing [w < >] should match")
	}
	if !s.atFollowing([]cst.Kind{w, lt}) {
		t.Fatal("atFollowing [w <] should match")
	}
	if !s.atFollowing([]cst.Kind{w}) {
		t.Fatal("atFollowing [w] should match")
	}
	if s.atFollowing([]cst.Kind{w, lt, gt, w}) {
		t.Fatal("atFollowing [w < > w] should NOT match")
	}
	if s.atFollowing([]cst.Kind{lt}) {
		t.Fatal("atFollowing [<] should NOT match")
	}
	if s.atFollowing([]cst.Kind{w, gt}) {
		t.Fatal("atFollowing [w >] should NOT match")
	}

	s.nextToken()
	s.nextToken()
	s.nextToken()
	if s.atFollowing([]cst.Kind{w}) {
		t.Fatal("atFollowing at EOF should NOT match")
	}
}

// TestSourceAtFollowingContent ports source.rs source_at_following_content.
func TestSourceAtFollowingContent(t *testing.T) {
	tokens, _ := buildTokens(
		tok{tkWhitespace, "  "},
		tok{tkLineBreak, "\n"},
		tok{tkWord, "hello"},
		tok{tkLineBreak, "\n"},
		tok{tkWhitespace, "  "},
		tok{tkLessThan, "<"},
		tok{tkLineBreak, "\n"},
		tok{tkGreaterThan, ">"},
		tok{tkWhitespace, "  "},
	)
	s := newSource(tokens)
	w, lt, gt := tkWord, tkLessThan, tkGreaterThan

	txt := func(k cst.Kind, text string) FollowingContent {
		return FollowingContent{Kind: k, MatchText: true, Text: text}
	}
	any := func(k cst.Kind) FollowingContent {
		return FollowingContent{Kind: k}
	}

	if !s.atFollowingContent([]FollowingContent{txt(w, "hello"), any(lt), any(gt)}) {
		t.Fatal("content [hello < >] should match")
	}
	if !s.atFollowingContent([]FollowingContent{any(w), any(lt), any(gt)}) {
		t.Fatal("content [w < >] should match")
	}
	if !s.atFollowingContent([]FollowingContent{txt(w, "hello"), any(lt)}) {
		t.Fatal("content [hello <] should match")
	}
	if !s.atFollowingContent([]FollowingContent{txt(w, "hello")}) {
		t.Fatal("content [hello] should match")
	}
	if !s.atFollowingContent([]FollowingContent{any(w), any(lt), txt(gt, ">")}) {
		t.Fatal("content [w < >text] should match")
	}
	if s.atFollowingContent([]FollowingContent{txt(w, "nonExistent")}) {
		t.Fatal("content [nonExistent] should NOT match")
	}

	s.nextToken()
	s.nextToken()
	s.nextToken()
	if s.atFollowingContent([]FollowingContent{any(w)}) {
		t.Fatal("content at EOF should NOT match")
	}
}

// TestPeekNextNonTriviaKind checks the lookahead past the current token.
func TestPeekNextNonTriviaKind(t *testing.T) {
	tokens, _ := buildTokens(
		tok{tkCurlyPercent, "{%"},
		tok{tkWhitespace, " "},
		tok{tkIf, "if"},
	)
	s := newSource(tokens)
	if k, ok := s.peekNextNonTriviaKind(); !ok || k != tkIf {
		t.Fatalf("peekNextNonTriviaKind = %v,%v want TkIf", k, ok)
	}
}
