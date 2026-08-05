package cst

import (
	"sync"
)

// Scanner indexing owns syntax trees for a bounded preparation/persistence
// batch. Recycling their exact slabs at that lifecycle boundary avoids asking
// the runtime for the same pointer-bearing storage again in the next batch.
const maxTransientCSTBlocks = 256

type transientBlockPool[T any] struct {
	mu             sync.Mutex
	blocks         [][]T
	items          int
	maxBlockItems  int
	maxPooledItems int
}

func newTransientBlockPool[T any](
	maxBlockItems,
	maxPooledItems int,
) transientBlockPool[T] {
	return transientBlockPool[T]{
		maxBlockItems:  maxBlockItems,
		maxPooledItems: maxPooledItems,
	}
}

func (pool *transientBlockPool[T]) get(length int) []T {
	if length <= 0 {
		return nil
	}
	pool.mu.Lock()
	best := -1
	for index, block := range pool.blocks {
		if cap(block) < length || cap(block)-length > length {
			continue
		}
		if best == -1 || cap(block) < cap(pool.blocks[best]) {
			best = index
		}
	}
	if best >= 0 {
		block := pool.blocks[best]
		last := len(pool.blocks) - 1
		pool.blocks[best] = pool.blocks[last]
		pool.blocks[last] = nil
		pool.blocks = pool.blocks[:last]
		pool.items -= cap(block)
		pool.mu.Unlock()
		return block[:length]
	}
	pool.mu.Unlock()
	return make([]T, length)
}

func (pool *transientBlockPool[T]) put(block []T) {
	blockItems := cap(block)
	if blockItems == 0 || blockItems > pool.maxBlockItems {
		return
	}
	block = block[:blockItems]
	clear(block)
	pool.mu.Lock()
	if len(pool.blocks) < maxTransientCSTBlocks &&
		pool.items+blockItems <= pool.maxPooledItems {
		pool.blocks = append(pool.blocks, block)
		pool.items += blockItems
	}
	pool.mu.Unlock()
}

func (pool *transientBlockPool[T]) release() {
	pool.mu.Lock()
	pool.blocks = nil
	pool.items = 0
	pool.mu.Unlock()
}

var (
	transientNodeBlocks = newTransientBlockPool[Node](
		1<<16,
		1<<17,
	)
	transientTokenBlocks = newTransientBlockPool[Token](
		1<<17,
		1<<18,
	)
	transientChildBlocks = newTransientBlockPool[elementRef](
		1<<18,
		1<<19,
	)
)

func acquireNodeBlock(length int) []Node {
	return transientNodeBlocks.get(length)
}

func acquireTokenBlock(length int) []Token {
	return transientTokenBlocks.get(length)
}

func acquireChildBlock(length int) []elementRef {
	return transientChildBlocks.get(length)
}

func releaseTreeStorage(root *rootNodeStorage) {
	if root == nil {
		return
	}
	nodeBlocks := root.nodeBlocks
	tokenBlocks := root.tokenBlocks
	childBlocks := root.childBlocks
	root.nodeBlocks = nil
	root.tokenBlocks = nil
	root.childBlocks = nil
	root.Node = Node{}
	root.source = nil
	root.rangeOverflow = nil
	for _, block := range nodeBlocks {
		transientNodeBlocks.put(block)
	}
	for _, block := range tokenBlocks {
		transientTokenBlocks.put(block)
	}
	for _, block := range childBlocks {
		transientChildBlocks.put(block)
	}
}

// ReleaseTransientBuffers drops every idle CST slab retained for scanner
// reuse. Active trees are unaffected.
func ReleaseTransientBuffers() {
	transientNodeBlocks.release()
	transientTokenBlocks.release()
	transientChildBlocks.release()
}
