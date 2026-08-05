package cst

import "unsafe"

// Builder constructs an immutable Tree top-down, mirroring rowan's
// GreenNodeBuilder API (StartNode / Token / FinishNode / Finish). Unlike rowan
// we compute absolute offsets and parent pointers directly here — there is no
// separate green/red split (see DESIGN.md).
type Builder struct {
	source      *string
	stack       []builderFrame // in-progress node stack
	rootStorage *rootNodeStorage
	pos         uint32 // running byte offset: end of the last-added token

	nodeBlocks  [][]Node
	tokenBlocks [][]Token
	childBlocks [][]elementRef
	nodeOffset  int
	tokenOffset int
	childOffset int
	nodeBlock   int
	tokenBlock  int
	childBlock  int

	exactChildren bool
}

type builderFrame struct {
	node          *Node
	childCapacity uint32
}

// NewBuilder returns a Builder over the given source. All token text is a
// zero-copy slice of source.
func NewBuilder(source string) *Builder {
	return NewBuilderCapacity(source, 0, 0)
}

// NewBuilderCapacity constructs a builder with slab capacity for the expected
// number of nodes and tokens. Elements are served from stable slabs instead of
// one heap allocation per CST element.
func NewBuilderCapacity(source string, nodeCapacity, tokenCapacity int) *Builder {
	return newBuilderCapacities(
		source,
		nodeCapacity,
		tokenCapacity,
		0,
		false,
	)
}

// NewBuilderCapacities constructs a builder with stable slabs for nodes,
// tokens, and direct-child references. childCapacity is shared by every node;
// callers using it must start nodes with their exact direct-child capacity.
func NewBuilderCapacities(
	source string,
	nodeCapacity,
	tokenCapacity,
	childCapacity int,
) *Builder {
	return newBuilderCapacities(
		source,
		nodeCapacity,
		tokenCapacity,
		childCapacity,
		true,
	)
}

func newBuilderCapacities(
	source string,
	nodeCapacity,
	tokenCapacity,
	childCapacity int,
	exactChildren bool,
) *Builder {
	b := &Builder{
		source:        &source,
		exactChildren: exactChildren,
	}
	if nodeCapacity > 0 {
		// The root needs its source/overflow sidecar and is allocated
		// separately. The node slab holds only ordinary descendants.
		nodeCapacity--
		if nodeCapacity > 0 {
			b.nodeBlocks = append(
				b.nodeBlocks,
				acquireNodeBlock(nodeCapacity),
			)
		}
	}
	if tokenCapacity > 0 {
		b.tokenBlocks = append(
			b.tokenBlocks,
			acquireTokenBlock(tokenCapacity),
		)
	}
	if childCapacity > 0 {
		b.childBlocks = append(
			b.childBlocks,
			acquireChildBlock(childCapacity),
		)
	}
	return b
}

func (b *Builder) allocNode() *Node {
	if len(b.nodeBlocks) == 0 || b.nodeOffset == len(b.nodeBlocks[b.nodeBlock]) {
		size := 64
		if len(b.nodeBlocks) > 0 {
			size = len(b.nodeBlocks[b.nodeBlock]) * 2
		}
		b.nodeBlocks = append(b.nodeBlocks, acquireNodeBlock(size))
		b.nodeBlock = len(b.nodeBlocks) - 1
		b.nodeOffset = 0
	}
	node := &b.nodeBlocks[b.nodeBlock][b.nodeOffset]
	b.nodeOffset++
	return node
}

func (b *Builder) allocToken() *Token {
	if len(b.tokenBlocks) == 0 || b.tokenOffset == len(b.tokenBlocks[b.tokenBlock]) {
		size := 64
		if len(b.tokenBlocks) > 0 {
			size = len(b.tokenBlocks[b.tokenBlock]) * 2
		}
		b.tokenBlocks = append(b.tokenBlocks, acquireTokenBlock(size))
		b.tokenBlock = len(b.tokenBlocks) - 1
		b.tokenOffset = 0
	}
	token := &b.tokenBlocks[b.tokenBlock][b.tokenOffset]
	b.tokenOffset++
	return token
}

func (b *Builder) allocChildren(size int) []elementRef {
	if size == 0 {
		return nil
	}
	if len(b.childBlocks) == 0 ||
		b.childOffset+size > len(b.childBlocks[b.childBlock]) {
		blockSize := size
		if len(b.childBlocks) > 0 &&
			len(b.childBlocks[b.childBlock]) > blockSize {
			blockSize = len(b.childBlocks[b.childBlock])
		}
		b.childBlocks = append(
			b.childBlocks,
			acquireChildBlock(blockSize),
		)
		b.childBlock = len(b.childBlocks) - 1
		b.childOffset = 0
	}
	start := b.childOffset
	b.childOffset += size
	return b.childBlocks[b.childBlock][start:b.childOffset]
}

// StartNode begins a new node of the given kind. It becomes the current parent
// until the matching FinishNode.
func (b *Builder) StartNode(kind Kind) {
	b.startNode(kind, 0)
}

// StartNodeCapacity begins a node with space reserved for its direct children.
func (b *Builder) StartNodeCapacity(kind Kind, childCapacity int) {
	if childCapacity < 0 {
		panic("cst: Builder.StartNodeCapacity called with negative capacity")
	}
	b.startNode(kind, childCapacity)
}

// StartNodeHint begins a dynamically growing node while reserving a likely
// number of direct children. Unlike StartNodeCapacity, the hint is not an
// exactness contract.
func (b *Builder) StartNodeHint(kind Kind, childCapacity int) {
	if childCapacity < 0 {
		panic("cst: Builder.StartNodeHint called with negative capacity")
	}
	b.startNode(kind, childCapacity)
}

func (b *Builder) startNode(kind Kind, childCapacity int) {
	if uint64(childCapacity) > uint64(^uint32(0)) {
		panic("cst: direct-child capacity exceeds uint32")
	}
	var n *Node
	if len(b.stack) == 0 && b.rootStorage == nil {
		b.rootStorage = &rootNodeStorage{
			source: b.source,
		}
		n = &b.rootStorage.Node
		n.parentOrSource = unsafe.Pointer(n)
	} else {
		n = b.allocNode()
	}
	parentOrSource := n.parentOrSource
	*n = Node{
		parentOrSource: parentOrSource,
		kind:           kind,
	}
	if childCapacity > 0 {
		n.children = childListWithStorage(b.allocChildren(childCapacity))
	}
	if len(b.stack) > 0 {
		parent := &b.stack[len(b.stack)-1]
		n.parentOrSource = unsafe.Pointer(parent.node)
		b.appendChild(parent, nodeRef(n))
	}
	b.stack = append(b.stack, builderFrame{
		node:          n,
		childCapacity: uint32(childCapacity),
	})
}

// Token appends a token of the given kind spanning span. The token text is
// source[span.Start:span.End].
func (b *Builder) Token(kind Kind, span TextRange) {
	if len(b.stack) == 0 {
		panic("cst: Builder.Token called with no open node")
	}
	parent := &b.stack[len(b.stack)-1]
	t := b.allocToken()
	*t = Token{
		kind:           kind,
		parentOrSource: unsafe.Pointer(parent.node),
		lengthAndFlags: elementTokenFlag,
	}
	b.setElementRange((*elementHeader)(unsafe.Pointer(t)), span)
	b.appendChild(parent, tokenRef(t))
	b.pos = span.End
}

func (b *Builder) appendChild(
	parent *builderFrame,
	child elementRef,
) {
	if parent == nil || parent.node == nil {
		panic("cst: child emitted outside a node")
	}
	count := parent.node.childCount
	if count == parent.childCapacity {
		capacity := max(uint32(4), parent.childCapacity*2)
		if capacity < parent.childCapacity {
			panic("cst: direct-child capacity overflow")
		}
		storage := b.allocChildren(int(capacity))
		copy(storage, parent.node.childValues())
		parent.node.children = childListWithStorage(storage)
		parent.childCapacity = capacity
	}
	parent.node.children.values(parent.childCapacity)[count] = child
	parent.node.childCount++
}

// FinishNode closes the current node, computing its span from its children.
func (b *Builder) FinishNode() {
	if len(b.stack) == 0 {
		panic("cst: Builder.FinishNode called with empty node stack")
	}
	frame := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	n := frame.node
	if b.exactChildren && n.childCount != frame.childCapacity {
		panic("cst: exact direct-child capacity did not match emitted children")
	}

	if n.childLength() > 0 {
		first := n.childAt(0).header().textRange()
		last := n.childAt(n.childLength() - 1).header().textRange()
		b.setElementRange((*elementHeader)(unsafe.Pointer(n)), TextRange{
			Start: first.Start,
			End:   last.End,
		})
	} else {
		// Empty node: zero-width at the running byte offset (end of the last
		// token added), mirroring rowan where an empty node sits at the current
		// text position.
		b.setElementRange((*elementHeader)(unsafe.Pointer(n)), TextRange{
			Start: b.pos,
			End:   b.pos,
		})
	}
}

func (b *Builder) setElementRange(
	header *elementHeader,
	textRange TextRange,
) {
	header.start = textRange.Start
	length := textRange.Len()
	flags := header.lengthAndFlags & elementTokenFlag
	if length < uint32(elementLengthOverflow) {
		header.lengthAndFlags = flags | uint16(length)
		return
	}
	header.lengthAndFlags = flags | elementLengthOverflow
	if b.rootStorage.rangeOverflow == nil {
		b.rootStorage.rangeOverflow = make(map[unsafe.Pointer]TextRange)
	}
	b.rootStorage.rangeOverflow[unsafe.Pointer(header)] = textRange
}

// Finish returns the completed Tree. The node stack must be empty.
func (b *Builder) Finish() *Tree {
	if len(b.stack) != 0 {
		panic("cst: Builder.Finish called with non-empty node stack")
	}
	var root *Node
	if b.rootStorage != nil {
		root = &b.rootStorage.Node
		b.rootStorage.nodeBlocks = b.nodeBlocks
		b.rootStorage.tokenBlocks = b.tokenBlocks
		b.rootStorage.childBlocks = b.childBlocks
	}
	return &Tree{Source: *b.source, Root: root}
}
