package semantic

import "hash/maphash"

// workspaceSymbolIndex is the immutable ID lookup for a published workspace
// generation. Slots retain only symbol pointers: the key already lives in the
// pointed-to workspaceSymbol, so a separate map key would duplicate one string
// header for every declaration.
//
// The index is built before publication and is never mutated afterwards.
// Snapshot overlays keep their small mutable maps.
type workspaceSymbolIndex struct {
	slots []*workspaceSymbol
	seed  maphash.Seed
	count uint32
}

func newWorkspaceSymbolIndex(expected int) workspaceSymbolIndex {
	capacity := workspaceSymbolIndexCapacity(expected)
	if capacity == 0 {
		return workspaceSymbolIndex{}
	}
	return workspaceSymbolIndex{
		slots: make([]*workspaceSymbol, capacity),
		seed:  maphash.MakeSeed(),
	}
}

func workspaceSymbolIndexCapacity(expected int) int {
	if expected <= 0 {
		return 0
	}
	// Keep the linear-probing table at or below a 75% load factor.
	required := expected + (expected+2)/3
	capacity := 8
	for capacity < required {
		capacity <<= 1
	}
	return capacity
}

func (index *workspaceSymbolIndex) Len() int {
	if index == nil {
		return 0
	}
	return int(index.count)
}

func (index *workspaceSymbolIndex) Get(
	id SymbolID,
) (*workspaceSymbol, bool) {
	if index == nil || len(index.slots) == 0 {
		return nil, false
	}
	slotIndex, found := index.findSlot(id)
	if !found {
		return nil, false
	}
	return index.slots[slotIndex], true
}

func (index *workspaceSymbolIndex) Set(symbol *workspaceSymbol) {
	if index == nil || symbol == nil {
		return
	}
	if len(index.slots) == 0 {
		*index = newWorkspaceSymbolIndex(1)
	}
	slotIndex, found := index.findSlot(symbol.ID)
	if found {
		index.slots[slotIndex] = symbol
		return
	}
	if (uint64(index.count)+1)*4 > uint64(len(index.slots))*3 {
		index.grow()
		slotIndex, _ = index.findSlot(symbol.ID)
	}
	index.slots[slotIndex] = symbol
	index.count++
}

func (index *workspaceSymbolIndex) Range(
	visit func(SymbolID, *workspaceSymbol) bool,
) {
	if index == nil || visit == nil {
		return
	}
	for _, symbol := range index.slots {
		if symbol != nil && !visit(symbol.ID, symbol) {
			return
		}
	}
}

func (index *workspaceSymbolIndex) findSlot(id SymbolID) (int, bool) {
	mask := uint64(len(index.slots) - 1)
	hash := maphash.String(index.seed, string(id))
	slotIndex := hash & mask
	// An odd secondary step visits every slot in the power-of-two table and
	// avoids the long primary clusters produced by linear probing.
	step := (hash>>32 | 1) & mask
	for {
		symbol := index.slots[slotIndex]
		if symbol == nil {
			return int(slotIndex), false
		}
		if symbol.ID == id {
			return int(slotIndex), true
		}
		slotIndex = (slotIndex + step) & mask
	}
}

func (index *workspaceSymbolIndex) grow() {
	previous := index.slots
	index.slots = make([]*workspaceSymbol, max(8, len(previous)*2))
	index.count = 0
	for _, symbol := range previous {
		if symbol != nil {
			index.Set(symbol)
		}
	}
}
