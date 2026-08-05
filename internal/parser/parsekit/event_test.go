package parsekit

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

func TestEventStaysCompact(t *testing.T) {
	t.Parallel()

	if size := unsafe.Sizeof(event{}); size > 8 {
		t.Fatalf("event size = %d bytes, want at most 8", size)
	}
}

func TestTokenStaysCompact(t *testing.T) {
	t.Parallel()

	require.Equal(t, uintptr(16), unsafe.Sizeof(Token{}))
}

func TestParserBufferStatsLifecycle(t *testing.T) {
	tokens, source := buildTokens(
		tok{tkWord, "left"},
		tok{tkAnd, " and"},
	)
	parser := newTestParser(tokens)
	root := parser.Start()
	parser.Bump()
	parser.Bump()
	parser.Complete(root, kRoot)

	stats := parser.BufferStats()
	require.Equal(t, 2, stats.Tokens)
	require.GreaterOrEqual(t, stats.TokenCapacity, stats.Tokens)
	require.Equal(t, 4, stats.Events)
	require.GreaterOrEqual(t, stats.EventCapacity, stats.Events)
	require.Equal(t, 1, stats.Nodes)
	require.GreaterOrEqual(t, stats.MarkerCapacity, stats.Nodes)

	_, errors := parser.Finish(source)
	require.Empty(t, errors)
	require.Equal(t, BufferStats{}, parser.BufferStats())
}

func TestLongTokenPreservesSourceRangeAndText(t *testing.T) {
	t.Parallel()

	source := "prefix" + strings.Repeat("x", int(longTokenLength)+17) + "suffix"
	rng := cst.TextRange{
		Start: uint32(len("prefix")),
		End:   uint32(len(source) - len("suffix")),
	}
	token := NewToken(1, &source, rng)

	require.Equal(t, rng, token.Range())
	require.Equal(t, source[rng.Start:rng.End], token.Text())
	require.Equal(t, 17+int(longTokenLength), len(token.Text()))
}

// TestMarkerLeakAsserts: with DebugAsserts on, an uncompleted marker is caught
// by assertMarkersCompleted.
func TestMarkerLeakAsserts(t *testing.T) {
	c := newEventCollection()
	c.start() // never completed
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic for leaked marker")
		}
		if !strings.Contains(toStr(r), "Markers need to be completed") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	c.assertMarkersCompleted()
}

// TestLIFOCompletionAsserts: completing an outer marker while an inner marker is
// still open must panic (LIFO enforcement).
func TestLIFOCompletionAsserts(t *testing.T) {
	c := newEventCollection()
	outer := c.start()
	c.addToken(tkWord)
	inner := c.start()
	c.addToken(tkWord)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected LIFO panic")
		}
		if !strings.Contains(toStr(r), "Inner Markers must be closed before outer Markers") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	c.complete(outer, kRoot) // outer before inner -> panic
	_ = inner
}

// TestEventCollectionMarkers ports event.rs event_collection_markers: nested
// start/complete produces the expected linear event list.
func TestEventCollectionMarkers(t *testing.T) {
	c := newEventCollection()
	mOuter := c.start()
	c.addToken(tkWord)
	mInner := c.start()
	c.addToken(tkAnd)
	c.complete(mInner, kBody)
	c.addToken(tkApply)
	c.complete(mOuter, kRoot)

	want := []event{
		{kind: evtStartNode, nodeKind: kRoot},
		{kind: evtAddToken, nodeKind: tkWord},
		{kind: evtStartNode, nodeKind: kBody},
		{kind: evtAddToken, nodeKind: tkAnd},
		{kind: evtFinishNode},
		{kind: evtAddToken, nodeKind: tkApply},
		{kind: evtFinishNode},
	}
	assertEvents(t, c.intoEventList(), want)
}

// TestEventCollectionPrecede ports event.rs event_collection_precede_example:
// forward_parent offsets are recorded correctly.
func TestEventCollectionPrecede(t *testing.T) {
	c := newEventCollection()
	startTag := c.start()
	c.addToken(tkLessThan)
	c.addToken(tkWord)
	completedStart := c.complete(startTag, kHtmlStartingTag)

	tag := c.precede(completedStart)
	completedTag := c.complete(tag, kHtmlTag)

	rootM := c.precede(completedTag)
	c.complete(rootM, kRoot)

	want := []event{
		{kind: evtStartNode, nodeKind: kHtmlStartingTag, data: 4},
		{kind: evtAddToken, nodeKind: tkLessThan},
		{kind: evtAddToken, nodeKind: tkWord},
		{kind: evtFinishNode},
		{kind: evtStartNode, nodeKind: kHtmlTag, data: 2},
		{kind: evtFinishNode},
		{kind: evtStartNode, nodeKind: kRoot},
		{kind: evtFinishNode},
	}
	assertEvents(t, c.intoEventList(), want)
}

func TestEventCollectionPoolReusesAndClearsBoundedSlabs(t *testing.T) {
	pool := newEventCollectionPool(1)
	first := pool.get(128, 64)
	first.events = append(first.events, event{kind: evtAddToken})
	first.childCounts = append(first.childCounts, 7)
	first.nodeCount = 7
	marker := first.allocMarker()
	marker.completed = true
	pool.put(first)
	require.Len(t, pool.collections, 1)
	require.Positive(t, pool.bytes)

	reused := pool.get(64, 32)
	require.Same(t, first, reused)
	require.Zero(t, pool.bytes)
	require.Empty(t, reused.events)
	require.Empty(t, reused.childCounts)
	require.Zero(t, reused.nodeCount)
	require.Zero(t, reused.markerBlock)
	require.Zero(t, reused.markerOffset)
	require.GreaterOrEqual(t, cap(reused.events), 128)
	require.GreaterOrEqual(t, cap(reused.childCounts), 1)
	require.GreaterOrEqual(t, reused.markerCapacity(), 64)

	pool.put(reused)
	pool.clear()
	require.Empty(t, pool.collections)
	require.Zero(t, pool.bytes)

	oversized := &eventCollection{
		events: make([]event, 0, maxPooledEvents+1),
	}
	pool.put(oversized)
	require.Empty(t, pool.collections)
}

func TestEventCollectionPoolSelectsSmallestSufficientSlabs(t *testing.T) {
	pool := newEventCollectionPool(2)
	small := pool.get(128, 64)
	large := pool.get(512, 256)
	pool.put(small)
	pool.put(large)

	reusedLarge := pool.get(256, 128)
	require.Same(t, large, reusedLarge)
	reusedSmall := pool.get(64, 32)
	require.Same(t, small, reusedSmall)
}

func TestEventCollectionPoolBoundsAggregateStorage(t *testing.T) {
	pool := newEventCollectionPool(2)
	newCollection := func() *eventCollection {
		return &eventCollection{
			events:      make([]event, 0, 32),
			childCounts: make([]uint8, 0, 32),
			markerBlocks: [][]Marker{
				make([]Marker, 16),
			},
		}
	}
	first := newCollection()
	pool.maxBytes = collectionStorageBytes(first)
	pool.put(first)
	pool.put(newCollection())

	require.Len(t, pool.collections, 1)
	require.Equal(t, pool.maxBytes, pool.bytes)
}

func TestTokenBufferPoolReusesAndBoundsOwnedSlices(t *testing.T) {
	pool := newTokenBufferPool(1)
	source := "<?php"
	first := make([]Token, 0, 128)
	first = append(first, NewToken(
		1,
		&source,
		cst.TextRange{Start: 0, End: uint32(len(source))},
	))
	firstData := unsafe.SliceData(first)
	pool.put(first)
	require.Len(t, pool.buffers, 1)
	require.Equal(t, cap(first), pool.items)

	reused := pool.get(64)
	require.Empty(t, reused)
	require.Zero(t, pool.items)
	require.GreaterOrEqual(t, cap(reused), 128)
	reused = reused[:1]
	require.Equal(t, firstData, unsafe.SliceData(reused))

	pool.put(reused)
	pool.clear()
	require.Empty(t, pool.buffers)
	require.Zero(t, pool.items)

	pool.put(make([]Token, 0, maxPooledTokens+1))
	require.Empty(t, pool.buffers)

	largeSource := strings.Repeat("x", int(longTokenLength)+1)
	large := []Token{NewToken(
		1,
		&largeSource,
		cst.TextRange{Start: 0, End: uint32(len(largeSource))},
	)}
	pool.put(large)
	require.Len(t, pool.buffers, 1)
	require.Equal(t, Token{}, pool.buffers[0][:1][0])
}

func TestTokenBufferPoolBoundsAggregateStorage(t *testing.T) {
	pool := newTokenBufferPool(2)
	pool.maxItems = 12
	pool.put(make([]Token, 0, 8))
	pool.put(make([]Token, 0, 8))
	require.Len(t, pool.buffers, 1)
	require.Equal(t, 8, pool.items)

	replacement := pool.get(12)
	require.Empty(t, pool.buffers)
	require.Zero(t, pool.items)
	require.Equal(t, 12, cap(replacement))
	pool.put(replacement)
	require.Len(t, pool.buffers, 1)
	require.Equal(t, 12, pool.items)
}

func TestTokenBufferPoolSelectsSmallestSufficientSlice(t *testing.T) {
	pool := newTokenBufferPool(2)
	small := make([]Token, 0, 16)
	large := make([]Token, 0, 64)
	smallData := unsafe.SliceData(small[:1])
	largeData := unsafe.SliceData(large[:1])
	pool.put(small)
	pool.put(large)

	reusedLarge := pool.get(48)
	require.Equal(t, largeData, unsafe.SliceData(reusedLarge[:1]))
	reusedSmall := pool.get(8)
	require.Equal(t, smallData, unsafe.SliceData(reusedSmall[:1]))
}

func TestTokenBufferPoolReplacesUndersizedSlice(t *testing.T) {
	pool := newTokenBufferPool(1)
	pool.put(make([]Token, 0, 16))

	resized := pool.get(32)
	require.Equal(t, 32, cap(resized))
	require.Empty(t, pool.buffers)

	pool.put(resized)
	require.Len(t, pool.buffers, 1)
	require.Equal(t, 32, cap(pool.buffers[0]))
}

func assertEvents(t *testing.T, got, want []event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count mismatch: got %d want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d mismatch:\n got: %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}
