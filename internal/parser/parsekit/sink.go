package parsekit

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// sink replays the parser's event stream into a cst.Builder, re-attaching
// trivia (which the parser never sees) and resolving forwardParent chains. Port
// of Rust's Sink.
type sink struct {
	tokens      []Token
	cursor      int
	events      []event
	childCounts []uint8
	// Direct child counts fit inline for almost every syntax node. Large file,
	// class, or statement-list nodes spill here instead of making every event
	// pay for a 32-bit counter.
	childCountOverflow map[int]uint32
	errors             []Error
	builder            *cst.Builder
}

func newSink(
	source string,
	tokens []Token,
	events []event,
	childCounts []uint8,
	errors []Error,
	nodeCount int,
) *sink {
	s := &sink{
		tokens:      tokens,
		events:      events,
		childCounts: childCounts,
		errors:      errors,
	}
	treeTokenCount, childCount := s.countTreeShape()
	s.builder = cst.NewBuilderCapacities(
		source,
		nodeCount,
		treeTokenCount,
		childCount,
	)
	return s
}

const (
	forwardStartConsumed = uint8(1 << 7)
	directChildCountMask = forwardStartConsumed - 1
)

type treeShapeCounter struct {
	tokens        []Token
	events        []event
	childCounts   []uint8
	overflow      map[int]uint32
	stack         []int
	forwardStarts []int
	cursor        int
	treeTokens    int
	totalChildren int
}

func (s *sink) countTreeShape() (int, int) {
	if len(s.childCounts) != len(s.events) {
		panic("parser sink: child-count buffer does not match event stream")
	}
	counter := treeShapeCounter{
		tokens:      s.tokens,
		events:      s.events,
		childCounts: s.childCounts,
		stack:       make([]int, 0, 64),
	}
	for idx, e := range counter.events {
		if counter.childCounts[idx]&forwardStartConsumed != 0 {
			continue
		}
		if e.kind == evtAddToken ||
			e.kind == evtAddNextNTokensAs ||
			idx == len(counter.events)-1 {
			counter.consumeTrivia()
		}

		switch e.kind {
		case evtStartNode:
			counter.forwardStarts = append(counter.forwardStarts, idx)
			walkIdx := idx
			for fp := e.data; fp != 0; {
				walkIdx += int(fp)
				if walkIdx < 0 || walkIdx >= len(counter.events) {
					panic("parser sink: forward_parent points outside event stream")
				}
				forward := counter.events[walkIdx]
				if forward.kind != evtStartNode {
					panic("parser sink: forward_parent points at a non-StartNode event")
				}
				counter.childCounts[walkIdx] |= forwardStartConsumed
				counter.forwardStarts = append(
					counter.forwardStarts,
					walkIdx,
				)
				fp = forward.data
			}
			for i := len(counter.forwardStarts) - 1; i >= 0; i-- {
				counter.startNode(counter.forwardStarts[i])
			}
			counter.forwardStarts = counter.forwardStarts[:0]

		case evtAddToken:
			counter.addToken()
			counter.cursor++

		case evtAddNextNTokensAs:
			counter.addToken()
			counter.cursor += int(e.data)

		case evtExplicitlyConsumeTrivia:
			counter.consumeTrivia()

		case evtFinishNode:
			if len(counter.stack) == 0 {
				panic("parser sink: FinishNode has no matching StartNode")
			}
			counter.stack = counter.stack[:len(counter.stack)-1]

		case evtPlaceholder:
		}
	}
	if counter.cursor != len(counter.tokens) {
		panic(fmt.Sprintf(
			"parser sink: Parser did not consume all tokens while sizing the tree (%d of %d)! This is a bug in the parsing logic!",
			counter.cursor,
			len(counter.tokens),
		))
	}
	if len(counter.stack) != 0 {
		panic("parser sink: unclosed nodes while sizing the tree")
	}
	s.childCountOverflow = counter.overflow
	return counter.treeTokens, counter.totalChildren
}

func (c *treeShapeCounter) startNode(eventIndex int) {
	if len(c.stack) > 0 {
		c.addChild()
	}
	c.stack = append(c.stack, eventIndex)
}

func (c *treeShapeCounter) addToken() {
	c.addChild()
	c.treeTokens++
}

func (c *treeShapeCounter) addChild() {
	if len(c.stack) == 0 {
		panic("parser sink: child emitted outside a node")
	}
	index := c.stack[len(c.stack)-1]
	if count, ok := c.overflow[index]; ok {
		if count == ^uint32(0) {
			panic("parser sink: node has too many direct children")
		}
		c.overflow[index] = count + 1
		c.totalChildren++
		return
	}
	value := c.childCounts[index]
	count := value & directChildCountMask
	if count == directChildCountMask {
		if c.overflow == nil {
			c.overflow = make(map[int]uint32)
		}
		c.overflow[index] = uint32(count) + 1
	} else {
		c.childCounts[index] = value&forwardStartConsumed | (count + 1)
	}
	c.totalChildren++
}

func (c *treeShapeCounter) consumeTrivia() {
	for c.cursor < len(c.tokens) {
		if !c.tokens[c.cursor].Kind.IsTrivia() {
			return
		}
		c.addToken()
		c.cursor++
	}
}

type forwardNode struct {
	event event
	index int
}

// finish replays all events and returns the built tree plus the error list.
func (s *sink) finish() (*cst.Tree, []Error) {
	var forwardNodes []forwardNode

	for idx := 0; idx < len(s.events); idx++ {
		e := s.events[idx]
		if e.kind == evtAddToken || e.kind == evtAddNextNTokensAs || idx == len(s.events)-1 {
			// consume trivia before any token event or the last event
			s.consumeTrivia()
		}

		// take the event, replacing it with a placeholder so a forward-parent
		// chain walk that revisits it is a no-op.
		s.events[idx] = event{kind: evtPlaceholder}

		switch e.kind {
		case evtStartNode:
			// Walk the forward_parent chain, collecting kinds and nulling
			// visited events, then start the collected nodes in reverse order
			// (outermost forward-parent first).
			forwardNodes = append(forwardNodes, forwardNode{
				event: e,
				index: idx,
			})
			walkIdx := idx
			fp := e.data
			for fp != 0 {
				walkIdx += int(fp)
				fe := s.events[walkIdx]
				if fe.kind != evtStartNode {
					panic("parser sink: forward_parent points at a non-StartNode event")
				}
				s.events[walkIdx] = event{kind: evtPlaceholder}
				forwardNodes = append(forwardNodes, forwardNode{
					event: fe,
					index: walkIdx,
				})
				fp = fe.data
			}
			for i := len(forwardNodes) - 1; i >= 0; i-- {
				s.builder.StartNodeCapacity(
					forwardNodes[i].event.nodeKind,
					s.directChildCount(forwardNodes[i].index),
				)
			}
			forwardNodes = forwardNodes[:0]

		case evtAddToken:
			s.tokenAs(e.nodeKind)

		case evtAddNextNTokensAs:
			s.nextNTokensAs(int(e.data), e.nodeKind)

		case evtExplicitlyConsumeTrivia:
			s.consumeTrivia()

		case evtFinishNode:
			s.builder.FinishNode()

		case evtPlaceholder:
			// skipped (either an unfilled placeholder or a nulled forward-parent)
		}
	}

	if s.cursor != len(s.tokens) {
		panic(fmt.Sprintf(
			"parser sink: Parser did not consume all tokens (%d of %d)! This is a bug in the parsing logic!",
			s.cursor, len(s.tokens)))
	}

	return s.builder.Finish(), s.errors
}

func (s *sink) directChildCount(eventIndex int) int {
	if count, ok := s.childCountOverflow[eventIndex]; ok {
		return int(count)
	}
	return int(s.childCounts[eventIndex] & directChildCountMask)
}

// consumeTrivia emits all pending trivia tokens (with their original kinds) into
// the currently open node.
func (s *sink) consumeTrivia() {
	for s.cursor < len(s.tokens) {
		if !s.tokens[s.cursor].Kind.IsTrivia() {
			break
		}
		s.token()
	}
}

// token emits the current token with its original lexer kind.
func (s *sink) token() {
	tok := &s.tokens[s.cursor]
	s.builder.Token(tok.Kind, tok.Range())
	s.cursor++
}

// tokenAs emits the current token under the kind the parser specified (the text
// and range come from the original lexer token).
func (s *sink) tokenAs(kind cst.Kind) {
	tok := &s.tokens[s.cursor]
	s.builder.Token(kind, tok.Range())
	s.cursor++
}

// nextNTokensAs fuses the next n lexer tokens into one tree token spanning
// tokens[cursor].start .. tokens[cursor+n-1].end.
func (s *sink) nextNTokensAs(n int, kind cst.Kind) {
	start := s.tokens[s.cursor].Range().Start
	end := s.tokens[s.cursor+n-1].Range().End
	s.builder.Token(kind, cst.TextRange{Start: start, End: end})
	s.cursor += n
}
