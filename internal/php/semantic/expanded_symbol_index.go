package semantic

import "hash/maphash"

var expandedSymbolIndexSeed = maphash.MakeSeed()

// expandedSymbolIndex maps overlay symbol IDs back into Snapshot.expandedData
// without retaining another string header for every declaration. Slots store
// data indexes plus one so zero remains the empty sentinel.
type expandedSymbolIndex struct {
	slots []uint32
	count uint32
}

func newExpandedSymbolIndex(expected int) expandedSymbolIndex {
	capacity := workspaceSymbolIndexCapacity(expected)
	if capacity == 0 {
		return expandedSymbolIndex{}
	}
	return expandedSymbolIndex{slots: make([]uint32, capacity)}
}

func (index *expandedSymbolIndex) Len() int {
	if index == nil {
		return 0
	}
	return int(index.count)
}

func (index *expandedSymbolIndex) Get(
	id SymbolID,
	data []Symbol,
) (uint32, bool) {
	if index == nil || len(index.slots) == 0 {
		return 0, false
	}
	slotIndex, found := index.findSlot(id, data)
	if !found {
		return 0, false
	}
	return index.slots[slotIndex] - 1, true
}

func (index *expandedSymbolIndex) Set(
	id SymbolID,
	dataIndex int,
	data []Symbol,
) {
	if index == nil {
		return
	}
	if dataIndex < 0 || uint64(dataIndex) >= uint64(^uint32(0)) {
		panic("semantic: expanded symbol index exceeds compact range")
	}
	if len(index.slots) == 0 {
		*index = newExpandedSymbolIndex(max(1, len(data)))
	}
	slotIndex, found := index.findSlot(id, data)
	if found {
		index.slots[slotIndex] = uint32(dataIndex) + 1
		return
	}
	if (uint64(index.count)+1)*4 > uint64(len(index.slots))*3 {
		index.grow(data)
		slotIndex, _ = index.findSlot(id, data)
	}
	index.slots[slotIndex] = uint32(dataIndex) + 1
	index.count++
}

func (index *expandedSymbolIndex) Range(
	data []Symbol,
	visit func(SymbolID, uint32) bool,
) {
	if index == nil || visit == nil {
		return
	}
	for _, stored := range index.slots {
		if stored == 0 {
			continue
		}
		dataIndex := stored - 1
		if uint64(dataIndex) >= uint64(len(data)) {
			continue
		}
		if !visit(data[dataIndex].ID, dataIndex) {
			return
		}
	}
}

func (index *expandedSymbolIndex) findSlot(
	id SymbolID,
	data []Symbol,
) (int, bool) {
	mask := uint64(len(index.slots) - 1)
	hash := maphash.String(expandedSymbolIndexSeed, string(id))
	slotIndex := hash & mask
	step := (hash>>32 | 1) & mask
	for {
		stored := index.slots[slotIndex]
		if stored == 0 {
			return int(slotIndex), false
		}
		dataIndex := stored - 1
		if uint64(dataIndex) < uint64(len(data)) &&
			data[dataIndex].ID == id {
			return int(slotIndex), true
		}
		slotIndex = (slotIndex + step) & mask
	}
}

func (index *expandedSymbolIndex) grow(data []Symbol) {
	previous := index.slots
	index.slots = make([]uint32, max(8, len(previous)*2))
	index.count = 0
	for _, stored := range previous {
		if stored == 0 {
			continue
		}
		dataIndex := stored - 1
		if uint64(dataIndex) >= uint64(len(data)) {
			continue
		}
		index.Set(data[dataIndex].ID, int(dataIndex), data)
	}
}
