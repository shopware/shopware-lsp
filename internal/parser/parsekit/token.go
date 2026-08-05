// Package parsekit is the language-agnostic parser engine extracted from the
// twig parser: the trivia-skipping token cursor, the event/marker machinery,
// the sink that replays events into a [cst.Tree] (re-attaching trivia and
// resolving forward-parent chains), the recovery loop and the diagnostic types.
//
// A language builds on parsekit by defining its kinds (via [cst.RegisterLanguage]),
// a lexer that produces []Token, and a grammar that drives a [Parser]. The twig
// parser is the first such language; see package parser.
package parsekit

import (
	"sync"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// Token is a single lexeme produced by a lexer. All ordinary tokens from one
// lexer run share one source-string header. A 32-bit start and 16-bit length
// keep the hot token representation to two machine words while preserving
// 32-bit source offsets. A single token of 65,535 bytes or more stores a
// zero-copy substring header instead; those tokens are rare comments, strings,
// or embedded text rather than the identifiers and punctuation that dominate
// parser buffers.
type Token struct {
	source *string
	start  uint32
	length uint16
	Kind   cst.Kind
}

const (
	// Generated container files can exceed the ordinary 64-Ki-token working
	// set. Reuse medium outliers, but keep the aggregate token pool at its
	// former eight-MiB envelope and let extreme generated files be collected.
	maxPooledTokens     = 1 << 18
	maxPooledTokenItems = 1 << 19
	longTokenLength     = ^uint16(0)
)

type tokenBufferPool struct {
	mu       sync.Mutex
	buffers  [][]Token
	items    int
	maxItems int
	limit    int
}

func newTokenBufferPool(size int) *tokenBufferPool {
	size = max(1, size)
	return &tokenBufferPool{
		buffers:  make([][]Token, 0, size),
		maxItems: maxPooledTokenItems,
		limit:    size,
	}
}

var parserTokenBuffers = newTokenBufferPool(
	parserEventCollections.limit,
)

// AcquireTokenBuffer returns an empty parser-owned token slice with at least
// the requested capacity. It must only be passed to a Parser created with
// NewOwned.
func AcquireTokenBuffer(capacity int) []Token {
	return parserTokenBuffers.get(capacity)
}

func (pool *tokenBufferPool) get(capacity int) []Token {
	pool.mu.Lock()
	best := -1
	bestCapacity := int(^uint(0) >> 1)
	for index, candidate := range pool.buffers {
		candidateCapacity := cap(candidate)
		if candidateCapacity >= capacity && candidateCapacity < bestCapacity {
			best = index
			bestCapacity = candidateCapacity
		}
	}
	if best >= 0 {
		tokens := pool.buffers[best]
		last := len(pool.buffers) - 1
		pool.buffers[best] = pool.buffers[last]
		pool.buffers[last] = nil
		pool.buffers = pool.buffers[:last]
		pool.items -= cap(tokens)
		pool.mu.Unlock()
		return tokens[:0]
	}
	// Make room for a reusable replacement before allocating it. Oversized
	// requests are deliberately not allowed to displace bounded buffers.
	for capacity <= maxPooledTokens &&
		len(pool.buffers) > 0 &&
		(len(pool.buffers) == pool.limit ||
			pool.items+capacity > pool.maxItems) {
		smallest := 0
		for index := 1; index < len(pool.buffers); index++ {
			if cap(pool.buffers[index]) < cap(pool.buffers[smallest]) {
				smallest = index
			}
		}
		pool.items -= cap(pool.buffers[smallest])
		last := len(pool.buffers) - 1
		pool.buffers[smallest] = pool.buffers[last]
		pool.buffers[last] = nil
		pool.buffers = pool.buffers[:last]
	}
	pool.mu.Unlock()
	return make([]Token, 0, capacity)
}

func (pool *tokenBufferPool) put(tokens []Token) {
	if cap(tokens) > maxPooledTokens {
		return
	}
	// The backing array otherwise retains every source header written by the
	// lexer even after the slice length becomes zero.
	clear(tokens)
	tokens = tokens[:0]
	tokenItems := cap(tokens)
	pool.mu.Lock()
	if len(pool.buffers) < pool.limit &&
		pool.items+tokenItems <= pool.maxItems {
		pool.buffers = append(pool.buffers, tokens)
		pool.items += tokenItems
	}
	pool.mu.Unlock()
}

func (pool *tokenBufferPool) clear() {
	pool.mu.Lock()
	clear(pool.buffers)
	pool.buffers = pool.buffers[:0]
	pool.items = 0
	pool.mu.Unlock()
}

// NewToken constructs a zero-copy token backed by source.
func NewToken(kind cst.Kind, source *string, rng cst.TextRange) Token {
	length := rng.End - rng.Start
	if length >= uint32(longTokenLength) {
		if source == nil {
			panic("parsekit: long token requires source text")
		}
		text := (*source)[rng.Start:rng.End]
		return Token{
			source: &text,
			start:  rng.Start,
			length: longTokenLength,
			Kind:   kind,
		}
	}
	return Token{
		source: source,
		start:  rng.Start,
		length: uint16(length),
		Kind:   kind,
	}
}

// Range returns the token's half-open byte range in the complete source.
func (t Token) Range() cst.TextRange {
	end := t.start + uint32(t.length)
	if t.length == longTokenLength && t.source != nil {
		end = t.start + uint32(len(*t.source))
	}
	return cst.TextRange{Start: t.start, End: end}
}

// Text returns the exact source slice covered by the token.
func (t Token) Text() string {
	if t.source == nil {
		return ""
	}
	if t.length == longTokenLength {
		return *t.source
	}
	return (*t.source)[t.start : t.start+uint32(t.length)]
}
