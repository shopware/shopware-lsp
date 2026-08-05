package parsekit

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// DebugAsserts enables the LIFO / marker-completion safety-net assertions that
// ludtwig guards behind #[cfg(debug_assertions)]. It is off by default (so the
// production parser never panics on these) and is flipped on in tests.
var DebugAsserts = false

// eventKind discriminates the event variants replayed by the sink.
type eventKind uint8

const (
	// evtPlaceholder is an unfilled slot created by start(); a later complete()
	// rewrites it into evtStartNode.
	evtPlaceholder eventKind = iota
	// evtStartNode opens a node of the given kind. forwardParent, when >0, is a
	// relative offset (in events) to another StartNode this node should wrap.
	evtStartNode
	// evtAddToken emits the next lexer token under the given kind.
	evtAddToken
	// evtAddNextNTokensAs fuses the next n lexer tokens into one tree token.
	evtAddNextNTokensAs
	// evtFinishNode closes the current node.
	evtFinishNode
	// evtExplicitlyConsumeTrivia forces trailing trivia inside the open node.
	evtExplicitlyConsumeTrivia
)

// event is one entry in the parser's event stream.
type event struct {
	// data stores the forward-parent offset for evtStartNode and the token
	// count for evtAddNextNTokensAs. Those variants are mutually exclusive.
	data uint32
	// nodeKind is the SyntaxKind for evtStartNode / evtAddToken /
	// evtAddNextNTokensAs.
	nodeKind cst.Kind
	kind     eventKind
}

// eventCollection accumulates events in a guaranteed-valid order (a FinishNode
// for every StartNode, markers completed LIFO). Port of Rust's EventCollection.
type eventCollection struct {
	events      []event
	childCounts []uint8
	// nodeCount is maintained while completing markers so the sink can size the
	// CST node slab without rescanning the event stream.
	nodeCount uint32
	// openMarkers tracks open marker positions to enforce LIFO completion. Only
	// maintained when DebugAsserts is true.
	openMarkers  []uint32
	markerBlocks [][]Marker
	markerBlock  int
	markerOffset int
}

const (
	// Reuse medium generated PHP files while excluding the roughly
	// million-event extremes. Bound the whole pool by bytes rather than
	// multiplying any one outlier by the worker count.
	maxPooledEvents     = 1 << 19
	maxPooledMarkers    = 1 << 18
	maxPooledEventBytes = 8 << 20
)

type eventCollectionPool struct {
	mu          sync.Mutex
	collections []*eventCollection
	bytes       int
	maxBytes    int
	limit       int
}

func newEventCollectionPool(size int) *eventCollectionPool {
	size = max(1, size)
	return &eventCollectionPool{
		collections: make([]*eventCollection, 0, size),
		maxBytes:    maxPooledEventBytes,
		limit:       size,
	}
}

var parserEventCollections = newEventCollectionPool(
	min(runtime.GOMAXPROCS(0), 8),
)

func newEventCollection() *eventCollection {
	return parserEventCollections.get(0, 0)
}

func newEventCollectionCap(eventCapacity, markerCapacity int) *eventCollection {
	return parserEventCollections.get(eventCapacity, markerCapacity)
}

func (pool *eventCollectionPool) get(
	eventCapacity,
	markerCapacity int,
) *eventCollection {
	pool.mu.Lock()
	best := -1
	bestWaste := int64(^uint64(0) >> 1)
	bestCoverage := int64(-1)
	for index, candidate := range pool.collections {
		candidateEvents := cap(candidate.events)
		candidateMarkers := candidate.markerCapacity()
		if candidateEvents >= eventCapacity &&
			candidateMarkers >= markerCapacity {
			waste := int64(candidateEvents-eventCapacity)*8 +
				int64(candidateMarkers-markerCapacity)*8 +
				int64(max(0, cap(candidate.childCounts)-eventCapacity))
			if waste < bestWaste {
				best = index
				bestWaste = waste
			}
			continue
		}
		if bestWaste != int64(^uint64(0)>>1) {
			continue
		}
		coverage := int64(min(candidateEvents, eventCapacity))*8 +
			int64(min(candidateMarkers, markerCapacity))*8 +
			int64(min(cap(candidate.childCounts), eventCapacity))
		if coverage > bestCoverage {
			best = index
			bestCoverage = coverage
		}
	}

	var collection *eventCollection
	if best >= 0 {
		collection = pool.collections[best]
		last := len(pool.collections) - 1
		pool.collections[best] = pool.collections[last]
		pool.collections[last] = nil
		pool.collections = pool.collections[:last]
		pool.bytes -= collectionStorageBytes(collection)
	}
	pool.mu.Unlock()

	if collection == nil {
		collection = &eventCollection{}
	}
	collection.reset(eventCapacity, markerCapacity)
	return collection
}

func (pool *eventCollectionPool) put(collection *eventCollection) {
	if collection == nil ||
		cap(collection.events) > maxPooledEvents ||
		collection.markerCapacity() > maxPooledMarkers {
		return
	}
	collection.reset(0, 0)
	storageBytes := collectionStorageBytes(collection)
	if storageBytes > pool.maxBytes {
		return
	}
	pool.mu.Lock()
	if len(pool.collections) < pool.limit &&
		pool.bytes+storageBytes <= pool.maxBytes {
		pool.collections = append(pool.collections, collection)
		pool.bytes += storageBytes
	}
	pool.mu.Unlock()
}

func (pool *eventCollectionPool) clear() {
	pool.mu.Lock()
	clear(pool.collections)
	pool.collections = pool.collections[:0]
	pool.bytes = 0
	pool.mu.Unlock()
}

func collectionStorageBytes(collection *eventCollection) int {
	if collection == nil {
		return 0
	}
	return cap(collection.events)*int(unsafe.Sizeof(event{})) +
		cap(collection.childCounts) +
		collection.markerCapacity()*int(unsafe.Sizeof(Marker{}))
}

func (c *eventCollection) reset(eventCapacity, markerCapacity int) {
	c.events = c.events[:0]
	if cap(c.events) < eventCapacity {
		c.events = make([]event, 0, eventCapacity)
	}
	c.childCounts = c.childCounts[:0]
	c.nodeCount = 0
	c.openMarkers = c.openMarkers[:0]
	c.markerBlock = 0
	c.markerOffset = 0
	if markerCapacity > 0 &&
		(len(c.markerBlocks) == 0 || len(c.markerBlocks[0]) < markerCapacity) {
		c.markerBlocks = [][]Marker{make([]Marker, markerCapacity)}
	}
}

func (c *eventCollection) directChildCountBuffer() []uint8 {
	eventCount := len(c.events)
	if cap(c.childCounts) < eventCount {
		c.childCounts = make([]uint8, eventCount)
	} else {
		c.childCounts = c.childCounts[:eventCount]
		clear(c.childCounts)
	}
	return c.childCounts
}

func (c *eventCollection) markerCapacity() int {
	capacity := 0
	for _, block := range c.markerBlocks {
		capacity += len(block)
	}
	return capacity
}

func (c *eventCollection) allocMarker() *Marker {
	if len(c.markerBlocks) == 0 {
		c.markerBlocks = append(c.markerBlocks, make([]Marker, 64))
	}
	if c.markerOffset == len(c.markerBlocks[c.markerBlock]) {
		size := 64
		if len(c.markerBlocks[c.markerBlock]) > 0 {
			size = len(c.markerBlocks[c.markerBlock])
		}
		c.markerBlock++
		c.markerOffset = 0
		if c.markerBlock == len(c.markerBlocks) {
			c.markerBlocks = append(c.markerBlocks, make([]Marker, size))
		}
	}
	marker := &c.markerBlocks[c.markerBlock][c.markerOffset]
	c.markerOffset++
	return marker
}

// ReleaseTransientBuffers drops idle parser event and marker slabs. Scanner
// batches call this after all workers finish so the running LSP does not retain
// cold-index scratch memory.
func ReleaseTransientBuffers() {
	parserEventCollections.clear()
	parserTokenBuffers.clear()
}

func (c *eventCollection) addToken(kind cst.Kind) {
	c.events = append(c.events, event{kind: evtAddToken, nodeKind: kind})
}

func (c *eventCollection) addNextNTokensAs(n int, kind cst.Kind) {
	c.events = append(c.events, event{
		data:     uint32(n),
		nodeKind: kind,
		kind:     evtAddNextNTokensAs,
	})
}

func (c *eventCollection) explicitlyConsumeTrivia() {
	c.events = append(c.events, event{kind: evtExplicitlyConsumeTrivia})
}

func (c *eventCollection) intoEventList() []event {
	return c.events
}

// start pushes a placeholder event and returns a marker referencing it. The
// node kind is decided later, at complete().
func (c *eventCollection) start() *Marker {
	pos := uint32(len(c.events))
	c.events = append(c.events, event{kind: evtPlaceholder})
	if DebugAsserts {
		c.openMarkers = append(c.openMarkers, pos)
	}
	marker := c.allocMarker()
	*marker = Marker{pos: pos}
	return marker
}

// complete rewrites the placeholder at the marker into a StartNode of kind and
// appends a matching FinishNode. Returns a completedMarker for later preceding.
func (c *eventCollection) complete(m *Marker, kind cst.Kind) CompletedMarker {
	m.completed = true

	if DebugAsserts {
		if len(c.openMarkers) == 0 || c.openMarkers[len(c.openMarkers)-1] != m.pos {
			panic("parsekit: Inner Markers must be closed before outer Markers!")
		}
		c.openMarkers = c.openMarkers[:len(c.openMarkers)-1]
	}

	if c.events[int(m.pos)].kind != evtPlaceholder {
		panic(fmt.Sprintf("parsekit: complete called on non-placeholder event at %d", m.pos))
	}
	c.events[int(m.pos)] = event{kind: evtStartNode, nodeKind: kind}
	c.events = append(c.events, event{kind: evtFinishNode})
	c.nodeCount++

	return CompletedMarker{pos: m.pos}
}

// precede starts a NEW marker and records it as the forward-parent of the given
// completed marker's node, so completing the new marker retroactively wraps the
// already-completed node. Port of Rust's precede.
func (c *eventCollection) precede(cm CompletedMarker) *Marker {
	newM := c.start()
	e := &c.events[int(cm.pos)]
	if e.kind != evtStartNode {
		panic("parsekit: precede called on a non-StartNode event")
	}
	e.data = newM.pos - cm.pos
	return newM
}

// Marker marks the start of a node and must be completed. If DebugAsserts is on
// and a marker is never completed, assertMarkersCompleted (called at parse end)
// panics.
type Marker struct {
	pos       uint32
	completed bool
}

// CompletedMarker is returned by complete() and can be preceded to wrap the
// completed node in another.
type CompletedMarker struct {
	pos uint32
}

// assertMarkersCompleted panics if any marker was left open. Ported from Rust's
// Marker Drop guard; called at the end of parsing when DebugAsserts is on.
func (c *eventCollection) assertMarkersCompleted() {
	if DebugAsserts && len(c.openMarkers) != 0 {
		panic("parsekit: Markers need to be completed!")
	}
}
