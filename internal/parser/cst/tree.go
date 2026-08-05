package cst

import (
	"iter"
	"unsafe"
)

// Element is either a Node or a Token. It is the union type ludtwig calls
// SyntaxElement (rowan::NodeOrToken).
type Element interface {
	Kind() Kind
	Range() TextRange
	Parent() *Node // nil for the root
	Text() string  // zero-copy slice of source

	// isElement is a private marker to keep the interface closed to Node/Token.
	isElement()
}

const (
	elementTokenFlag      = uint16(1 << 15)
	elementLengthMask     = elementTokenFlag - 1
	elementLengthOverflow = elementLengthMask
)

// elementHeader is the common prefix of Node and Token. Ordinary element
// lengths fit in 15 bits beside the token flag; the uncommon larger range
// lives in the root-owned overflow table. Keeping the header to 16 bytes makes
// every Token 16 bytes while preserving 32-bit source positions and the public
// TextRange API.
type elementHeader struct {
	parentOrSource unsafe.Pointer
	start          uint32
	lengthAndFlags uint16
	kind           Kind
}

type elementRef struct {
	pointer unsafe.Pointer
}

type childList struct {
	data *elementRef
}

func childListWithStorage(storage []elementRef) childList {
	if cap(storage) == 0 {
		return childList{}
	}
	return childList{data: &storage[:cap(storage)][0]}
}

func (children childList) values(length uint32) []elementRef {
	if length == 0 {
		return nil
	}
	return unsafe.Slice(children.data, length)
}

func (children childList) at(length uint32, index int) elementRef {
	return unsafe.Slice(children.data, length)[index]
}

func nodeRef(node *Node) elementRef {
	return elementRef{pointer: unsafe.Pointer(node)}
}

func tokenRef(token *Token) elementRef {
	return elementRef{pointer: unsafe.Pointer(token)}
}

func (r elementRef) header() *elementHeader {
	if r.pointer == nil {
		return nil
	}
	return (*elementHeader)(r.pointer)
}

func (r elementRef) element() Element {
	header := r.header()
	if header == nil {
		return nil
	}
	if header.isToken() {
		return (*Token)(r.pointer)
	}
	return (*Node)(r.pointer)
}

func (r elementRef) node() (*Node, bool) {
	header := r.header()
	if header == nil || header.isToken() {
		return nil, false
	}
	return (*Node)(r.pointer), true
}

func (r elementRef) token() (*Token, bool) {
	header := r.header()
	if header == nil || !header.isToken() {
		return nil, false
	}
	return (*Token)(r.pointer), true
}

// Node is a composite element with children. Unlike rowan we build a single
// immutable tree with absolute offsets in the sink (no green/red split), so
// parent pointers and ranges are stored directly.
type Node struct {
	// Keep this prefix identical to elementHeader.
	// Non-root nodes store their parent in parentOrSource. The root points to
	// itself and is the prefix of rootNodeStorage. Direct-child capacity is
	// needed only while building and lives in the Builder stack.
	parentOrSource unsafe.Pointer
	start          uint32
	lengthAndFlags uint16
	kind           Kind

	childCount uint32
	children   childList
}

// Token is a leaf element holding a slice of the source text.
type Token struct {
	// Keep this prefix identical to elementHeader.
	parentOrSource unsafe.Pointer
	start          uint32
	lengthAndFlags uint16
	kind           Kind
}

// rootNodeStorage extends only the root node with the shared source pointer
// and rare full-width ranges. A pointer to the embedded Node keeps this entire
// allocation alive.
type rootNodeStorage struct {
	Node
	source        *string
	rangeOverflow map[unsafe.Pointer]TextRange
	nodeBlocks    [][]Node
	tokenBlocks   [][]Token
	childBlocks   [][]elementRef
}

func (n *Node) isElement()  {}
func (t *Token) isElement() {}

// --- Node basics ---

// Kind returns the node's syntax kind.
func (n *Node) Kind() Kind { return n.kind }

// Range returns the node's absolute byte range.
func (n *Node) Range() TextRange {
	return (*elementHeader)(unsafe.Pointer(n)).textRange()
}

// Parent returns the node's parent, or nil for the root.
func (n *Node) Parent() *Node {
	if n == nil || n.parentOrSource == nil ||
		n.parentOrSource == unsafe.Pointer(n) {
		return nil
	}
	return (*Node)(n.parentOrSource)
}

func (n *Node) rootStorage() *rootNodeStorage {
	for n != nil {
		if n.parentOrSource == unsafe.Pointer(n) {
			return (*rootNodeStorage)(unsafe.Pointer(n))
		}
		if n.parentOrSource == nil {
			return nil
		}
		n = (*Node)(n.parentOrSource)
	}
	return nil
}

func (n *Node) source() *string {
	root := n.rootStorage()
	if root == nil {
		return nil
	}
	return root.source
}

// Text returns the exact source slice covered by this node.
func (n *Node) Text() string {
	source := n.source()
	if source == nil {
		return ""
	}
	textRange := n.Range()
	return (*source)[textRange.Start:textRange.End]
}

func (n *Node) childLength() int {
	if n == nil {
		return 0
	}
	return int(n.childCount)
}

func (n *Node) childValues() []elementRef {
	if n == nil {
		return nil
	}
	return n.children.values(n.childCount)
}

func (n *Node) childAt(index int) elementRef {
	return n.children.at(n.childCount, index)
}

// Children returns a materialized copy of the direct children. Prefer
// ChildElements when iterating to avoid allocating the compatibility slice.
func (n *Node) Children() []Element {
	if n == nil || n.childLength() == 0 {
		return nil
	}
	children := make([]Element, n.childLength())
	for index, child := range n.childValues() {
		children[index] = child.element()
	}
	return children
}

// ChildElements iterates direct children (nodes and tokens) in order without
// materializing an interface slice.
func (n *Node) ChildElements() iter.Seq[Element] {
	return func(yield func(Element) bool) {
		if n == nil {
			return
		}
		for _, child := range n.childValues() {
			if !yield(child.element()) {
				return
			}
		}
	}
}

// ChildCount returns the number of direct child elements.
func (n *Node) ChildCount() int {
	if n == nil {
		return 0
	}
	return n.childLength()
}

// ChildCapacity reports the exact number of retained direct-child slots.
// Builder-only spare capacity is discarded when the immutable tree is sealed.
func (n *Node) ChildCapacity() int {
	if n == nil {
		return 0
	}
	return n.childLength()
}

// Child returns the direct child at index, or nil when index is out of bounds.
func (n *Node) Child(index int) Element {
	if n == nil || index < 0 || index >= n.childLength() {
		return nil
	}
	return n.childAt(index).element()
}

// --- Token basics ---

// Kind returns the token's syntax kind.
func (t *Token) Kind() Kind { return t.kind }

// Range returns the token's absolute byte range.
func (t *Token) Range() TextRange {
	return (*elementHeader)(unsafe.Pointer(t)).textRange()
}

// Parent returns the token's parent node.
func (t *Token) Parent() *Node {
	if t == nil {
		return nil
	}
	return (*Node)(t.parentOrSource)
}

// Text returns the exact source slice covered by this token.
func (t *Token) Text() string {
	parent := t.Parent()
	if parent == nil {
		return ""
	}
	source := parent.source()
	if source == nil {
		return ""
	}
	textRange := t.Range()
	return (*source)[textRange.Start:textRange.End]
}

func (header *elementHeader) textRange() TextRange {
	if header == nil {
		return TextRange{}
	}
	length := header.lengthAndFlags & elementLengthMask
	if length != elementLengthOverflow {
		return TextRange{
			Start: header.start,
			End:   header.start + uint32(length),
		}
	}
	root := header.rootStorage()
	if root == nil {
		return TextRange{
			Start: header.start,
			End:   header.start + uint32(length),
		}
	}
	if textRange, found := root.rangeOverflow[unsafe.Pointer(header)]; found {
		return textRange
	}
	return TextRange{
		Start: header.start,
		End:   header.start + uint32(length),
	}
}

func (header *elementHeader) isToken() bool {
	return header != nil && header.lengthAndFlags&elementTokenFlag != 0
}

func (header *elementHeader) rootStorage() *rootNodeStorage {
	if header == nil {
		return nil
	}
	if header.isToken() {
		if header.parentOrSource == nil {
			return nil
		}
		return (*Node)(header.parentOrSource).rootStorage()
	}
	return (*Node)(unsafe.Pointer(header)).rootStorage()
}

// --- Child iterators ---

// ChildNodeCursor walks direct child nodes without allocating an iterator
// closure. The cursor is a value and is safe to use with a nil parent.
type ChildNodeCursor struct {
	parent  *Node
	index   int
	current *Node
}

// ChildNodeCursor returns a zero-allocation cursor over direct child nodes.
func (n *Node) ChildNodeCursor() ChildNodeCursor {
	return ChildNodeCursor{parent: n}
}

// Next advances to the next direct child node.
func (cursor *ChildNodeCursor) Next() bool {
	if cursor.parent == nil {
		return false
	}
	for cursor.index < cursor.parent.childLength() {
		child, ok := cursor.parent.childAt(cursor.index).node()
		cursor.index++
		if ok {
			cursor.current = child
			return true
		}
	}
	cursor.current = nil
	return false
}

// Node returns the current direct child node.
func (cursor *ChildNodeCursor) Node() *Node {
	return cursor.current
}

// ChildTokenCursor walks direct child tokens without allocating an iterator
// closure. The cursor is a value and is safe to use with a nil parent.
type ChildTokenCursor struct {
	parent  *Node
	index   int
	current *Token
}

// ChildTokenCursor returns a zero-allocation cursor over direct child tokens.
func (n *Node) ChildTokenCursor() ChildTokenCursor {
	return ChildTokenCursor{parent: n}
}

// Next advances to the next direct child token.
func (cursor *ChildTokenCursor) Next() bool {
	if cursor.parent == nil {
		return false
	}
	for cursor.index < cursor.parent.childLength() {
		token, ok := cursor.parent.childAt(cursor.index).token()
		cursor.index++
		if ok {
			cursor.current = token
			return true
		}
	}
	cursor.current = nil
	return false
}

// Token returns the current direct child token.
func (cursor *ChildTokenCursor) Token() *Token {
	return cursor.current
}

// ChildNodes iterates the direct child nodes (skipping tokens).
func (n *Node) ChildNodes() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n == nil {
			return
		}
		for _, c := range n.childValues() {
			if child, ok := c.node(); ok {
				if !yield(child) {
					return
				}
			}
		}
	}
}

// ChildTokens iterates the direct child tokens (skipping nodes).
func (n *Node) ChildTokens() iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		if n == nil {
			return
		}
		for _, c := range n.childValues() {
			if tok, ok := c.token(); ok {
				if !yield(tok) {
					return
				}
			}
		}
	}
}

// FirstChild returns the first direct child element, or nil.
func (n *Node) FirstChild() Element {
	if n == nil || n.childLength() == 0 {
		return nil
	}
	return n.childAt(0).element()
}

// LastChild returns the last direct child element, or nil.
func (n *Node) LastChild() Element {
	if n == nil || n.childLength() == 0 {
		return nil
	}
	return n.childAt(n.childLength() - 1).element()
}

// --- Siblings ---

// NextSibling returns the sibling after this node, or nil.
func (n *Node) NextSibling() Element {
	return nextSibling(n.Parent(), unsafe.Pointer(n))
}

// PrevSibling returns the sibling before this node, or nil.
func (n *Node) PrevSibling() Element {
	return prevSibling(n.Parent(), unsafe.Pointer(n))
}

// NextSibling returns the sibling after this token, or nil.
func (t *Token) NextSibling() Element {
	return nextSibling(t.Parent(), unsafe.Pointer(t))
}

// PrevSibling returns the sibling before this token, or nil.
func (t *Token) PrevSibling() Element {
	return prevSibling(t.Parent(), unsafe.Pointer(t))
}

func nextSibling(parent *Node, target unsafe.Pointer) Element {
	if parent == nil || target == nil {
		return nil
	}
	children := parent.childValues()
	for index := range children {
		if children[index].pointer != target {
			continue
		}
		if index+1 == len(children) {
			return nil
		}
		return children[index+1].element()
	}
	return nil
}

func prevSibling(parent *Node, target unsafe.Pointer) Element {
	if parent == nil || target == nil {
		return nil
	}
	children := parent.childValues()
	for index := range children {
		if children[index].pointer != target {
			continue
		}
		if index == 0 {
			return nil
		}
		return children[index-1].element()
	}
	return nil
}

// --- Deep first/last token ---

// FirstToken returns the leftmost token in the subtree, or nil for an empty node.
func (n *Node) FirstToken() *Token {
	if n == nil {
		return nil
	}
	for _, c := range n.childValues() {
		if child, ok := c.token(); ok {
			return child
		}
		if child, ok := c.node(); ok {
			if t := child.FirstToken(); t != nil {
				return t
			}
		}
	}
	return nil
}

// LastToken returns the rightmost token in the subtree, or nil for an empty node.
func (n *Node) LastToken() *Token {
	if n == nil {
		return nil
	}
	for i := n.childLength() - 1; i >= 0; i-- {
		if child, ok := n.childAt(i).token(); ok {
			return child
		}
		if child, ok := n.childAt(i).node(); ok {
			if t := child.LastToken(); t != nil {
				return t
			}
		}
	}
	return nil
}

// --- Ancestors / descendants ---

// Ancestors iterates this node and every parent up to the root (inclusive of
// self), matching rowan's SyntaxNode::ancestors.
func (n *Node) Ancestors() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for cur := n; cur != nil; cur = cur.Parent() {
			if !yield(cur) {
				return
			}
		}
	}
}

// Descendants iterates all elements in the subtree in preorder, including this
// node itself (nodes and tokens).
func (n *Node) Descendants() iter.Seq[Element] {
	return func(yield func(Element) bool) {
		if n == nil {
			return
		}
		var walk func(e Element) bool
		walk = func(e Element) bool {
			if !yield(e) {
				return false
			}
			if node, ok := e.(*Node); ok {
				for _, c := range node.childValues() {
					if !walk(c.element()) {
						return false
					}
				}
			}
			return true
		}
		walk(n)
	}
}

// --- Walk with enter/leave ---

// WalkEvent distinguishes entering from leaving a node during a Walk.
type WalkEvent uint8

const (
	// WalkEnter is emitted when descending into an element.
	WalkEnter WalkEvent = iota
	// WalkLeave is emitted when ascending out of a node.
	WalkLeave
)

// Walk performs a preorder traversal emitting Enter before descending into an
// element and Leave after its children. Tokens (leaves) get an Enter only,
// matching rowan's PreorderWithTokens.
func (n *Node) Walk() iter.Seq2[WalkEvent, Element] {
	return func(yield func(WalkEvent, Element) bool) {
		if n == nil {
			return
		}
		var walk func(e Element) bool
		walk = func(e Element) bool {
			node, isNode := e.(*Node)
			if !yield(WalkEnter, e) {
				return false
			}
			if isNode {
				for _, c := range node.childValues() {
					if !walk(c.element()) {
						return false
					}
				}
				if !yield(WalkLeave, e) {
					return false
				}
			}
			return true
		}
		walk(n)
	}
}

// --- Kind lookups ---

// ChildOfKind returns the first direct child element of the given kind, or nil.
func (n *Node) ChildOfKind(k Kind) Element {
	if n == nil {
		return nil
	}
	for _, c := range n.childValues() {
		if c.header().kind == k {
			return c.element()
		}
	}
	return nil
}

// ChildTokenOfKind returns the first direct child token of the given kind, or
// nil. Ports rowan::ast::support::token.
func (n *Node) ChildTokenOfKind(k Kind) *Token {
	if n == nil {
		return nil
	}
	for _, c := range n.childValues() {
		if tok, ok := c.token(); ok && tok.kind == k {
			return tok
		}
	}
	return nil
}

// --- Trivia-trimmed range ---

// RangeTrimmedTrivia returns the node's range with leading trivia tokens
// removed. Trivia may be nested inside the node's first child, as happens when
// a parser precedes an expression with lossless whitespace or comments.
func (n *Node) RangeTrimmedTrivia() TextRange {
	if n == nil {
		return TextRange{}
	}
	r := n.Range()
	for element := range n.Descendants() {
		tok, ok := element.(*Token)
		if !ok {
			continue
		}
		if !tok.kind.IsTrivia() {
			break
		}
		newStart := tok.Range().End
		if newStart < r.End {
			r.Start = newStart
		}
	}
	return r
}

// --- Offset lookup ---

// TokenAtOffset returns the token at the given byte offset, descending into the
// tree. On a boundary between two tokens the right (later) token is preferred.
// Returns nil if the offset is outside the node's range.
func (n *Node) TokenAtOffset(off uint32) *Token {
	if n == nil {
		return nil
	}
	nodeRange := n.Range()
	if off < nodeRange.Start || off >= nodeRange.End {
		return nil
	}
	cur := n
	for {
		var next *Node
		var found *Token
		for _, c := range cur.childValues() {
			r := c.header().textRange()
			if off < r.Start || off >= r.End {
				continue
			}
			if child, ok := c.token(); ok {
				found = child
			} else if child, ok := c.node(); ok {
				next = child
			}
			break
		}
		if next != nil {
			cur = next
			continue
		}
		return found
	}
}

// DescendantForRange returns the smallest element (node or token) whose range
// fully contains r.
// For an empty range on a boundary between two elements the right (later) side
// is preferred, consistent with TokenAtOffset. Zero-width nodes (e.g. an empty
// BODY) are never returned. Returns n itself when no child contains r, and nil
// when r is inverted or lies outside n's range.
func (n *Node) DescendantForRange(r TextRange) Element {
	if n == nil {
		return nil
	}
	nodeRange := n.Range()
	if r.Start > r.End ||
		r.Start < nodeRange.Start ||
		r.End > nodeRange.End {
		return nil
	}
	var result Element = n
	cur := n
descend:
	for {
		for _, c := range cur.childValues() {
			cr := c.header().textRange()
			// Containment requires Start <= r.Start < End (right-preference for
			// zero-width r on a boundary, and skips zero-width children) plus
			// r.End <= End.
			if cr.Start <= r.Start && r.Start < cr.End && r.End <= cr.End {
				result = c.element()
				if child, ok := c.node(); ok {
					cur = child
					continue descend
				}
				return result
			}
		}
		return result
	}
}

// NodeAtOffset returns the smallest node containing the byte offset: the node
// enclosing the token at that position (right token preferred on boundaries).
// Returns n itself for an offset on n's end boundary, and nil outside n.
func (n *Node) NodeAtOffset(off uint32) *Node {
	switch e := n.DescendantForRange(TextRange{Start: off, End: off}).(type) {
	case *Token:
		return e.Parent()
	case *Node:
		return e
	}
	return nil
}

// AncestorOfKind returns the nearest ancestor of the given kind (excluding n
// itself), or nil.
func (n *Node) AncestorOfKind(k Kind) *Node {
	if n == nil {
		return nil
	}
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.kind == k {
			return p
		}
	}
	return nil
}

// AncestorOfKind returns the nearest ancestor node of the given kind, or nil.
func (t *Token) AncestorOfKind(k Kind) *Node {
	parent := t.Parent()
	if parent == nil {
		return nil
	}
	if parent.kind == k {
		return parent
	}
	return parent.AncestorOfKind(k)
}

// --- Tree ---

// Tree is a parsed template: the source plus its root node.
type Tree struct {
	Source string
	Root   *Node
}

// ReleaseTransientStorage makes a scanner-owned tree unusable and returns its
// cleared element slabs to the bounded transient pools. Ordinary parser and
// editor callers should let trees follow normal garbage-collection lifetime.
func (tree *Tree) ReleaseTransientStorage() {
	if tree == nil || tree.Root == nil {
		return
	}
	root := tree.Root.rootStorage()
	tree.Root = nil
	tree.Source = ""
	releaseTreeStorage(root)
}
