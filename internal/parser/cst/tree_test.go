package cst

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// buildSample builds this tree over the source
// "{% block a %} hi {% endblock %}" (lengths approximated for the test; the
// exact source is used so Text() slices are meaningful):
//
//	ROOT
//	  TWIG_BLOCK
//	    TWIG_STARTING_BLOCK  "{% block a %}"
//	      TK_CURLY_PERCENT   "{%"
//	      TK_WHITESPACE      " "
//	      TK_BLOCK           "block"
//	      TK_WHITESPACE      " "
//	      TK_WORD            "a"
//	      TK_WHITESPACE      " "
//	      TK_PERCENT_CURLY   "%}"
//	    BODY                 " hi "
//	      HTML_TEXT
//	        TK_WHITESPACE    " "
//	        TK_WORD          "hi"
//	        TK_WHITESPACE    " "
//	    TWIG_ENDING_BLOCK    "{% endblock %}"
//	      TK_CURLY_PERCENT   "{%"
//	      TK_WHITESPACE      " "
//	      TK_ENDBLOCK        "endblock"
//	      TK_WHITESPACE      " "
//	      TK_PERCENT_CURLY   "%}"
const sampleSrc = "{% block a %} hi {% endblock %}"

func buildSample(t *testing.T) *Tree {
	t.Helper()
	b := NewBuilder(sampleSrc)
	tok := func(k Kind, s, e uint32) { b.Token(k, TextRange{s, e}) }
	b.StartNode(kRoot)
	b.StartNode(kTwigBlock)
	b.StartNode(kTwigStartingBlock)
	tok(tkCurlyPercent, 0, 2)   // {%
	tok(tkWhitespace, 2, 3)     // " "
	tok(tkBlock, 3, 8)          // block
	tok(tkWhitespace, 8, 9)     // " "
	tok(tkWord, 9, 10)          // a
	tok(tkWhitespace, 10, 11)   // " "
	tok(tkPercentCurly, 11, 13) // %}
	b.FinishNode()              // TWIG_STARTING_BLOCK
	b.StartNode(kBody)
	b.StartNode(kHtmlText)
	tok(tkWhitespace, 13, 14) // " "
	tok(tkWord, 14, 16)       // hi
	tok(tkWhitespace, 16, 17) // " "
	b.FinishNode()            // HTML_TEXT
	b.FinishNode()            // BODY
	b.StartNode(kTwigEndingBlock)
	tok(tkCurlyPercent, 17, 19) // {%
	tok(tkWhitespace, 19, 20)   // " "
	tok(tkEndblock, 20, 28)     // endblock
	tok(tkWhitespace, 28, 29)   // " "
	tok(tkPercentCurly, 29, 31) // %}
	b.FinishNode()              // TWIG_ENDING_BLOCK
	b.FinishNode()              // TWIG_BLOCK
	b.FinishNode()              // ROOT
	return b.Finish()
}

func TestBuilderSpansAndText(t *testing.T) {
	tree := buildSample(t)
	if tree.Source != sampleSrc {
		t.Fatalf("Source mismatch")
	}
	root := tree.Root
	if root.Kind() != kRoot {
		t.Fatalf("root kind = %v", root.Kind())
	}
	if root.Parent() != nil {
		t.Fatalf("root parent = %p, want nil", root.Parent())
	}
	if got := root.Range(); got != (TextRange{0, 31}) {
		t.Fatalf("root range = %v", got)
	}
	// Losslessness: root text equals source.
	if root.Text() != sampleSrc {
		t.Fatalf("root.Text() = %q", root.Text())
	}
	block := root.FirstChild().(*Node)
	if block.Kind() != kTwigBlock || block.Text() != sampleSrc {
		t.Fatalf("block = %v %q", block.Kind(), block.Text())
	}
	if block.Parent() != root {
		t.Fatalf("block parent = %p, want root %p", block.Parent(), root)
	}
	token := block.FirstToken()
	if token == nil || token.Parent() == nil || token.Text() != "{%" {
		t.Fatalf("first token or its compact parent/source link is invalid")
	}
}

func TestCSTElementsStayCompact(t *testing.T) {
	t.Parallel()

	headerSize := unsafe.Sizeof(elementHeader{})
	if size := unsafe.Sizeof(Node{}); size != 32 {
		t.Fatalf(
			"Node size = %d bytes, want compact 32-byte representation",
			size,
		)
	}
	if size := unsafe.Sizeof(Token{}); size != 16 {
		t.Fatalf("Token size = %d bytes, want 16", size)
	}
	if size := unsafe.Sizeof(elementRef{}); size != unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("elementRef size = %d bytes, want one machine word", size)
	}
	if size := unsafe.Sizeof(childList{}); size != unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("childList size = %d bytes, want one machine word", size)
	}
	if offset := unsafe.Offsetof(Node{}.children); offset != 24 {
		t.Fatalf(
			"Node child offset = %d bytes, want 24",
			offset,
		)
	}
	if size := unsafe.Sizeof(Token{}); size != headerSize {
		t.Fatalf("Token/header size = %d/%d bytes", size, headerSize)
	}
}

func TestBuilderRetainsFullRangesThroughCompactLengthOverflow(t *testing.T) {
	t.Parallel()

	source := strings.Repeat("x", int(elementLengthOverflow)+1)
	b := NewBuilderCapacities(source, 2, 1, 3)
	b.StartNodeCapacity(kRoot, 1)
	b.StartNodeCapacity(kBody, 1)
	b.Token(tkWord, TextRange{Start: 0, End: uint32(len(source))})
	b.FinishNode()
	b.FinishNode()
	tree := b.Finish()
	runtime.GC()

	body := tree.Root.FirstChild().(*Node)
	token := body.FirstToken()
	expected := TextRange{Start: 0, End: uint32(len(source))}
	require.Equal(t, expected, tree.Root.Range())
	require.Equal(t, expected, body.Range())
	require.Equal(t, expected, token.Range())
	require.Equal(t, source, tree.Root.Text())
	require.Equal(t, source, token.Text())
	require.Len(
		t,
		(*rootNodeStorage)(unsafe.Pointer(tree.Root)).rangeOverflow,
		3,
	)
}

func TestBuilderElementTypeComesFromBuilderOperation(t *testing.T) {
	t.Parallel()

	b := NewBuilder("x")
	b.StartNode(kRoot)
	// Even a node-classified kind remains a Token when emitted through Token.
	// The compact header keeps this explicit bit instead of inferring the
	// concrete element type from the global kind registry.
	b.Token(kBody, TextRange{Start: 0, End: 1})
	b.FinishNode()

	child := b.Finish().Root.Child(0)
	require.IsType(t, (*Token)(nil), child)
	require.Equal(t, kBody, child.Kind())
}

func TestDynamicBuilderRetainsMultipleElementSlabs(t *testing.T) {
	t.Parallel()

	const tokenCount = 500
	const nodeCount = 300
	source := strings.Repeat("x", tokenCount)
	b := NewBuilder(source)
	b.StartNode(kRoot)
	for index := range tokenCount {
		b.Token(tkWord, TextRange{
			Start: uint32(index),
			End:   uint32(index + 1),
		})
	}
	for range nodeCount {
		b.StartNode(kError)
		b.FinishNode()
	}
	b.FinishNode()
	tree := b.Finish()
	runtime.GC()

	require.Equal(t, tokenCount+nodeCount, tree.Root.ChildCount())
	lastToken := tree.Root.Child(tokenCount - 1).(*Token)
	require.Equal(t, "x", lastToken.Text())
	require.Same(t, tree.Root, lastToken.Parent())
	lastNode := tree.Root.Child(tokenCount + nodeCount - 1).(*Node)
	require.Equal(t, kError, lastNode.Kind())
	require.Same(t, tree.Root, lastNode.Parent())
}

func TestReleasedTreeStorageIsClearedAndReused(t *testing.T) {
	ReleaseTransientBuffers()
	t.Cleanup(ReleaseTransientBuffers)

	build := func() *Tree {
		builder := NewBuilderCapacities("abc", 2, 2, 3)
		builder.StartNodeCapacity(kRoot, 2)
		builder.StartNodeCapacity(kBody, 1)
		builder.Token(tkWord, TextRange{Start: 0, End: 1})
		builder.FinishNode()
		builder.Token(tkWord, TextRange{Start: 1, End: 3})
		builder.FinishNode()
		return builder.Finish()
	}

	first := build()
	firstStorage := first.Root.rootStorage()
	firstNodeBlock := unsafe.SliceData(firstStorage.nodeBlocks[0])
	firstTokenBlock := unsafe.SliceData(firstStorage.tokenBlocks[0])
	firstChildBlock := unsafe.SliceData(firstStorage.childBlocks[0])

	first.ReleaseTransientStorage()
	require.Nil(t, first.Root)
	require.Empty(t, first.Source)
	require.Nil(t, firstStorage.nodeBlocks)
	require.Nil(t, firstStorage.tokenBlocks)
	require.Nil(t, firstStorage.childBlocks)

	second := build()
	secondStorage := second.Root.rootStorage()
	require.Equal(
		t,
		firstNodeBlock,
		unsafe.SliceData(secondStorage.nodeBlocks[0]),
	)
	require.Equal(
		t,
		firstTokenBlock,
		unsafe.SliceData(secondStorage.tokenBlocks[0]),
	)
	require.Equal(
		t,
		firstChildBlock,
		unsafe.SliceData(secondStorage.childBlocks[0]),
	)
	require.Equal(t, "abc", second.Root.Text())
}

func TestBuilderChildCapacityHintCanGrow(t *testing.T) {
	b := NewBuilder("abc")
	b.StartNodeHint(kRoot, 2)
	b.Token(tkWord, TextRange{0, 1})
	b.Token(tkWord, TextRange{1, 2})
	b.Token(tkWord, TextRange{2, 3})
	b.FinishNode()
	tree := b.Finish()

	if got := len(tree.Root.Children()); got != 3 {
		t.Fatalf("child count = %d want 3", got)
	}
	if got := cap(tree.Root.Children()); got < 3 {
		t.Fatalf("child capacity = %d want at least 3", got)
	}
}

func TestBuilderExactChildrenShareOneArena(t *testing.T) {
	b := NewBuilderCapacities("abc", 2, 2, 3)
	b.StartNodeCapacity(kRoot, 2)
	b.StartNodeCapacity(kBody, 1)
	b.Token(tkWord, TextRange{0, 1})
	b.FinishNode()
	b.Token(tkWord, TextRange{1, 3})
	b.FinishNode()
	tree := b.Finish()

	rootChildren := tree.Root.Children()
	if len(rootChildren) != 2 || cap(rootChildren) != 2 {
		t.Fatalf(
			"root children len/cap = %d/%d want 2/2",
			len(rootChildren),
			cap(rootChildren),
		)
	}
	body := rootChildren[0].(*Node)
	if len(body.Children()) != 1 || cap(body.Children()) != 1 {
		t.Fatalf(
			"body children len/cap = %d/%d want 1/1",
			len(body.Children()),
			cap(body.Children()),
		)
	}
	if got := tree.Root.Text(); got != "abc" {
		t.Fatalf("root text = %q want abc", got)
	}
}

func TestBuilderExactChildrenRejectsMismatchedShape(t *testing.T) {
	b := NewBuilderCapacities("a", 1, 1, 2)
	b.StartNodeCapacity(kRoot, 2)
	b.Token(tkWord, TextRange{0, 1})

	defer func() {
		if recover() == nil {
			t.Fatal("expected mismatched exact child capacity to panic")
		}
	}()
	b.FinishNode()
}

func TestChildIterators(t *testing.T) {
	tree := buildSample(t)
	block := tree.Root.FirstChild().(*Node)

	var nodeKinds []Kind
	for n := range block.ChildNodes() {
		nodeKinds = append(nodeKinds, n.Kind())
	}
	want := []Kind{kTwigStartingBlock, kBody, kTwigEndingBlock}
	if !slices.Equal(nodeKinds, want) {
		t.Fatalf("ChildNodes = %v want %v", nodeKinds, want)
	}
	nodeKinds = nodeKinds[:0]
	cursor := block.ChildNodeCursor()
	for cursor.Next() {
		nodeKinds = append(nodeKinds, cursor.Node().Kind())
	}
	if !slices.Equal(nodeKinds, want) {
		t.Fatalf("ChildNodeCursor = %v want %v", nodeKinds, want)
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		cursor := block.ChildNodeCursor()
		for cursor.Next() {
			_ = cursor.Node().Kind()
		}
	}); allocations != 0 {
		t.Fatalf("ChildNodeCursor allocations = %v want 0", allocations)
	}
	// TWIG_BLOCK has no direct token children.
	for tok := range block.ChildTokens() {
		t.Fatalf("unexpected direct token %v", tok.Kind())
	}

	start := block.FirstChild().(*Node)
	var tokKinds []Kind
	for tok := range start.ChildTokens() {
		tokKinds = append(tokKinds, tok.Kind())
	}
	wantTok := []Kind{tkCurlyPercent, tkWhitespace, tkBlock, tkWhitespace, tkWord, tkWhitespace, tkPercentCurly}
	if !slices.Equal(tokKinds, wantTok) {
		t.Fatalf("ChildTokens = %v want %v", tokKinds, wantTok)
	}
	tokKinds = tokKinds[:0]
	tokenCursor := start.ChildTokenCursor()
	for tokenCursor.Next() {
		tokKinds = append(tokKinds, tokenCursor.Token().Kind())
	}
	if !slices.Equal(tokKinds, wantTok) {
		t.Fatalf("ChildTokenCursor = %v want %v", tokKinds, wantTok)
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		cursor := start.ChildTokenCursor()
		for cursor.Next() {
			_ = cursor.Token().Kind()
		}
	}); allocations != 0 {
		t.Fatalf("ChildTokenCursor allocations = %v want 0", allocations)
	}
}

func TestFirstLastChildAndSiblings(t *testing.T) {
	tree := buildSample(t)
	block := tree.Root.FirstChild().(*Node)
	first := block.FirstChild().(*Node)
	last := block.LastChild().(*Node)
	if first.Kind() != kTwigStartingBlock || last.Kind() != kTwigEndingBlock {
		t.Fatalf("first/last = %v %v", first.Kind(), last.Kind())
	}
	// Siblings.
	body := first.NextSibling().(*Node)
	if body.Kind() != kBody {
		t.Fatalf("next sibling = %v", body.Kind())
	}
	if body.PrevSibling().(*Node).Kind() != kTwigStartingBlock {
		t.Fatalf("prev sibling wrong")
	}
	if last.NextSibling() != nil {
		t.Fatalf("last should have no next sibling")
	}
	if first.PrevSibling() != nil {
		t.Fatalf("first should have no prev sibling")
	}
	// Token siblings.
	start := first
	toks := start.childValues()
	firstTok := toks[0].element().(*Token)
	if firstTok.PrevSibling() != nil {
		t.Fatalf("first token prev should be nil")
	}
	if firstTok.NextSibling().(*Token).Kind() != tkWhitespace {
		t.Fatalf("token next sibling wrong")
	}
}

func TestFirstLastToken(t *testing.T) {
	tree := buildSample(t)
	ft := tree.Root.FirstToken()
	if ft == nil || ft.Kind() != tkCurlyPercent || ft.Range().Start != 0 {
		t.Fatalf("FirstToken = %+v", ft)
	}
	lt := tree.Root.LastToken()
	if lt == nil || lt.Kind() != tkPercentCurly || lt.Range().End != 31 {
		t.Fatalf("LastToken = %+v", lt)
	}
}

func TestAncestors(t *testing.T) {
	tree := buildSample(t)
	htmlText := tree.Root.FirstChild().(*Node). // TWIG_BLOCK
							childAt(1).element().(*Node). // BODY
							childAt(0).element().(*Node)  // HTML_TEXT
	var kinds []Kind
	for a := range htmlText.Ancestors() {
		kinds = append(kinds, a.Kind())
	}
	want := []Kind{kHtmlText, kBody, kTwigBlock, kRoot}
	if !slices.Equal(kinds, want) {
		t.Fatalf("Ancestors = %v want %v", kinds, want)
	}
}

func TestDescendantsPreorder(t *testing.T) {
	tree := buildSample(t)
	body := tree.Root.FirstChild().(*Node).childAt(1).element().(*Node) // BODY
	var kinds []Kind
	for e := range body.Descendants() {
		kinds = append(kinds, e.Kind())
	}
	want := []Kind{kBody, kHtmlText, tkWhitespace, tkWord, tkWhitespace}
	if !slices.Equal(kinds, want) {
		t.Fatalf("Descendants = %v want %v", kinds, want)
	}
}

func TestWalkEnterLeave(t *testing.T) {
	tree := buildSample(t)
	body := tree.Root.FirstChild().(*Node).childAt(1).element().(*Node) // BODY

	type step struct {
		ev   WalkEvent
		kind Kind
	}
	var got []step
	for ev, e := range body.Walk() {
		got = append(got, step{ev, e.Kind()})
	}
	want := []step{
		{WalkEnter, kBody},
		{WalkEnter, kHtmlText},
		{WalkEnter, tkWhitespace}, // token: enter only
		{WalkEnter, tkWord},
		{WalkEnter, tkWhitespace},
		{WalkLeave, kHtmlText},
		{WalkLeave, kBody},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Walk = %v want %v", got, want)
	}
}

func TestWalkEarlyStop(t *testing.T) {
	tree := buildSample(t)
	count := 0
	for range tree.Root.Walk() {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("early break did not stop: %d", count)
	}
}

func TestChildOfKind(t *testing.T) {
	tree := buildSample(t)
	block := tree.Root.FirstChild().(*Node)
	if e := block.ChildOfKind(kBody); e == nil || e.Kind() != kBody {
		t.Fatalf("ChildOfKind(Body) = %v", e)
	}
	if e := block.ChildOfKind(kHtmlText); e != nil {
		t.Fatalf("ChildOfKind(HtmlText) should be nil (not direct child)")
	}
	start := block.FirstChild().(*Node)
	if tok := start.ChildTokenOfKind(tkWord); tok == nil || tok.Text() != "a" {
		t.Fatalf("ChildTokenOfKind(TkWord) = %v", tok)
	}
	if tok := start.ChildTokenOfKind(tkEndblock); tok != nil {
		t.Fatalf("ChildTokenOfKind(TkEndblock) should be nil")
	}
}

func TestRangeTrimmedTrivia(t *testing.T) {
	tree := buildSample(t)
	body := tree.Root.FirstChild().(*Node).childAt(1).element().(*Node) // BODY
	htmlText := body.childAt(0).element().(*Node)
	// HTML_TEXT starts with a whitespace token (13..14); trimming removes it.
	if got := htmlText.RangeTrimmedTrivia(); got != (TextRange{14, 17}) {
		t.Fatalf("RangeTrimmedTrivia = %v want 14..17", got)
	}
	// TWIG_STARTING_BLOCK starts with a non-trivia token, so range is unchanged.
	start := tree.Root.FirstChild().(*Node).FirstChild().(*Node)
	if got := start.RangeTrimmedTrivia(); got != start.Range() {
		t.Fatalf("RangeTrimmedTrivia(start) = %v want %v", got, start.Range())
	}
}

func TestRangeTrimmedTriviaAllTrivia(t *testing.T) {
	// A node made entirely of trivia: start advances only while new_start < end.
	b := NewBuilder("   ")
	b.StartNode(kRoot)
	b.StartNode(kHtmlText)
	b.Token(tkWhitespace, TextRange{0, 1})
	b.Token(tkWhitespace, TextRange{1, 2})
	b.Token(tkWhitespace, TextRange{2, 3})
	b.FinishNode()
	b.FinishNode()
	tree := b.Finish()
	ht := tree.Root.FirstChild().(*Node)
	// First trivia: newStart=1 (<3) -> start=1. Second: newStart=2 (<3) -> start=2.
	// Third: newStart=3 (not <3) -> unchanged. Result 2..3.
	if got := ht.RangeTrimmedTrivia(); got != (TextRange{2, 3}) {
		t.Fatalf("RangeTrimmedTrivia all-trivia = %v want 2..3", got)
	}
}

func TestTokenAtOffset(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root
	// Offset 0 -> "{%" (0..2).
	if tok := root.TokenAtOffset(0); tok == nil || tok.Kind() != tkCurlyPercent {
		t.Fatalf("TokenAtOffset(0) = %v", tok)
	}
	// Offset 9 -> word "a" (9..10).
	if tok := root.TokenAtOffset(9); tok == nil || tok.Text() != "a" {
		t.Fatalf("TokenAtOffset(9) = %v", tok)
	}
	// Boundary at 2: token [2,3) whitespace begins -> prefer the right token.
	if tok := root.TokenAtOffset(2); tok == nil || tok.Range() != (TextRange{2, 3}) {
		t.Fatalf("TokenAtOffset(2) = %v want 2..3", tok)
	}
	// Offset at EOF (31) is outside -> nil.
	if tok := root.TokenAtOffset(31); tok != nil {
		t.Fatalf("TokenAtOffset(31) = %v want nil", tok)
	}
	// Last valid offset 30 -> "%}" (29..31).
	if tok := root.TokenAtOffset(30); tok == nil || tok.Range() != (TextRange{29, 31}) {
		t.Fatalf("TokenAtOffset(30) = %v", tok)
	}
}

func TestNilSafety(t *testing.T) {
	var n *Node
	if n.Children() != nil {
		t.Fatal("nil Children")
	}
	if n.FirstChild() != nil || n.LastChild() != nil {
		t.Fatal("nil first/last child")
	}
	if n.FirstToken() != nil || n.LastToken() != nil {
		t.Fatal("nil first/last token")
	}
	if n.ChildOfKind(kRoot) != nil || n.ChildTokenOfKind(tkWord) != nil {
		t.Fatal("nil child of kind")
	}
	if n.TokenAtOffset(0) != nil {
		t.Fatal("nil TokenAtOffset")
	}
	for range n.ChildNodes() {
		t.Fatal("nil ChildNodes yielded")
	}
	for range n.Descendants() {
		t.Fatal("nil Descendants yielded")
	}
	if n.RangeTrimmedTrivia() != (TextRange{}) {
		t.Fatal("nil RangeTrimmedTrivia")
	}
}

func TestEmptyNodeSpan(t *testing.T) {
	// An empty node placed after a token should have a zero-width span at the
	// current position.
	b := NewBuilder("ab")
	b.StartNode(kRoot)
	b.Token(tkWord, TextRange{0, 2})
	b.StartNode(kError) // empty node
	b.FinishNode()
	b.FinishNode()
	tree := b.Finish()
	empty := tree.Root.childAt(1).element().(*Node)
	if empty.Range() != (TextRange{2, 2}) {
		t.Fatalf("empty node span = %v want 2..2", empty.Range())
	}
	if empty.Text() != "" {
		t.Fatalf("empty node text = %q", empty.Text())
	}
}

func TestDescendantForRange(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root

	// Sub-range inside a single token → that token ("blo" inside "block"@3..8).
	el := root.DescendantForRange(TextRange{3, 6})
	tok, ok := el.(*Token)
	if !ok || tok.Kind() != tkBlock {
		t.Fatalf("range in token = %v", el)
	}

	// Range spanning two tokens of the starting block → the starting block node.
	el = root.DescendantForRange(TextRange{3, 10}) // "block a"
	node, ok := el.(*Node)
	if !ok || node.Kind() != kTwigStartingBlock {
		t.Fatalf("range across tokens = %v (%v)", el, el.Kind())
	}

	// Range spanning starting block and body → TWIG_BLOCK.
	el = root.DescendantForRange(TextRange{5, 15})
	if el == nil || el.Kind() != kTwigBlock {
		t.Fatalf("range across children = %v", el)
	}

	// Empty range on a boundary prefers the right side: offset 13 is the
	// boundary between STARTING_BLOCK and BODY.
	el = root.DescendantForRange(TextRange{13, 13})
	tok, ok = el.(*Token)
	if !ok || tok.Range().Start != 13 {
		t.Fatalf("empty range boundary = %v", el)
	}
	if got := tok.AncestorOfKind(kBody); got == nil {
		t.Fatal("boundary token should be inside BODY")
	}

	// Empty range at EOF boundary → root itself.
	end := root.Range().End
	if el = root.DescendantForRange(TextRange{end, end}); el != Element(root) {
		t.Fatalf("EOF range = %v", el)
	}

	// Outside and inverted ranges → nil.
	if el = root.DescendantForRange(TextRange{end, end + 1}); el != nil {
		t.Fatalf("outside range = %v", el)
	}
	if el = root.DescendantForRange(TextRange{10, 3}); el != nil {
		t.Fatalf("inverted range = %v", el)
	}
	var nilNode *Node
	if el = nilNode.DescendantForRange(TextRange{0, 0}); el != nil {
		t.Fatalf("nil receiver = %v", el)
	}
}

func TestNodeAtOffset(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root

	// Offset inside "hi" → HTML_TEXT.
	if n := root.NodeAtOffset(14); n == nil || n.Kind() != kHtmlText {
		t.Fatalf("NodeAtOffset(14) = %v", n)
	}
	// Offset inside "endblock" → TWIG_ENDING_BLOCK.
	if n := root.NodeAtOffset(20); n == nil || n.Kind() != kTwigEndingBlock {
		t.Fatalf("NodeAtOffset(20) = %v", n)
	}
	// EOF boundary → root itself; outside → nil.
	if n := root.NodeAtOffset(root.Range().End); n != root {
		t.Fatalf("NodeAtOffset(EOF) = %v", n)
	}
	if n := root.NodeAtOffset(root.Range().End + 5); n != nil {
		t.Fatalf("NodeAtOffset(outside) = %v", n)
	}
}

func TestAncestorOfKind(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root

	text := root.NodeAtOffset(14) // HTML_TEXT
	if a := text.AncestorOfKind(kTwigBlock); a == nil || a.Kind() != kTwigBlock {
		t.Fatalf("AncestorOfKind(TwigBlock) = %v", a)
	}
	// Excludes self.
	if a := text.AncestorOfKind(kHtmlText); a != nil {
		t.Fatalf("AncestorOfKind should exclude self, got %v", a)
	}
	if a := root.AncestorOfKind(kTwigBlock); a != nil {
		t.Fatalf("root has no ancestors, got %v", a)
	}
	tok := root.TokenAtOffset(14)
	if a := tok.AncestorOfKind(kBody); a == nil || a.Kind() != kBody {
		t.Fatalf("token AncestorOfKind(Body) = %v", a)
	}
	var nilTok *Token
	if a := nilTok.AncestorOfKind(kBody); a != nil {
		t.Fatalf("nil token = %v", a)
	}
}
