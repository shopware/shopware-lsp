package binder

import (
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// declarationSet tracks CST nodes which introduce symbols while a document is
// bound. The set is temporary and its keys already live in the immutable CST,
// so retaining node pointers avoids duplicating every NodeID in a Go map.
// Equality still uses the complete semantic identity rather than pointer
// identity so malformed and zero-width nodes retain the previous behavior.
type declarationSet struct {
	slots    []*phpsyntax.Node
	count    int
	expected int
}

func newDeclarationSet(expected int) declarationSet {
	return declarationSet{expected: max(0, expected)}
}

func (set *declarationSet) Add(node *phpsyntax.Node) {
	if set == nil || node == nil {
		return
	}
	if len(set.slots) == 0 {
		set.slots = make(
			[]*phpsyntax.Node,
			declarationSetCapacity(max(1, set.expected)),
		)
	}
	identity := semantic.NodeIdentity(node)
	slot, found := set.findSlot(identity)
	if found {
		return
	}
	if (set.count+1)*4 > len(set.slots)*3 {
		set.grow()
		slot, _ = set.findSlot(identity)
	}
	set.slots[slot] = node
	set.count++
}

func (set *declarationSet) Contains(node *phpsyntax.Node) bool {
	if set == nil || node == nil || len(set.slots) == 0 {
		return false
	}
	_, found := set.findSlot(semantic.NodeIdentity(node))
	return found
}

func (set *declarationSet) findSlot(identity semantic.NodeID) (int, bool) {
	mask := uint64(len(set.slots) - 1)
	hash := declarationIdentityHash(identity)
	slot := hash & mask
	step := (hash>>32 | 1) & mask
	for {
		stored := set.slots[slot]
		if stored == nil {
			return int(slot), false
		}
		if semantic.NodeIdentity(stored) == identity {
			return int(slot), true
		}
		slot = (slot + step) & mask
	}
}

func (set *declarationSet) grow() {
	previous := set.slots
	set.slots = make([]*phpsyntax.Node, len(previous)*2)
	set.count = 0
	for _, node := range previous {
		if node != nil {
			set.addRehashed(node)
		}
	}
}

func (set *declarationSet) addRehashed(node *phpsyntax.Node) {
	slot, found := set.findSlot(semantic.NodeIdentity(node))
	if found {
		return
	}
	set.slots[slot] = node
	set.count++
}

func declarationSetCapacity(expected int) int {
	if expected <= 0 {
		return 0
	}
	required := expected + (expected+2)/3
	capacity := 8
	for capacity < required {
		capacity <<= 1
	}
	return capacity
}

func declarationIdentityHash(identity semantic.NodeID) uint64 {
	value := uint64(identity.Start)<<32 | uint64(identity.End)
	value ^= uint64(identity.Kind) * 0x9e3779b97f4a7c15
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	return value ^ (value >> 31)
}
