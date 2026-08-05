package semantic

import (
	"hash/maphash"
	"unsafe"
)

const (
	workspaceStringTableMinimumSize = 8
	workspaceStringTableLoadPercent = 75
	// Capacity is only a hint sourced from the persisted row count. Bound the
	// eager table so a damaged cache cannot turn its census into an oversized
	// startup allocation; ordinary growth still supports larger valid graphs.
	workspaceStringTableMaximumHint = 1 << 20
)

// workspaceStringInterner is a restore/batch-scoped canonical string set.
// Unlike map[string]string, each table slot retains only one string header.
// It also accepts borrowed byte lookups so the streaming decoder can probe the
// table before copying a newly encountered value.
//
// The table uses a per-instance seeded hash and linear probing. It is not safe
// for concurrent use; all current owners already serialize their build phase.
type workspaceStringInterner struct {
	seed          maphash.Seed
	entries       []string
	count         int
	expectedItems int
}

func newWorkspaceStringInterner(
	expectedItems int,
) *workspaceStringInterner {
	return &workspaceStringInterner{
		seed:          maphash.MakeSeed(),
		expectedItems: expectedItems,
	}
}

func (interner *workspaceStringInterner) Intern(value string) string {
	if value == "" {
		return ""
	}
	if interner == nil {
		return value
	}
	hash := maphash.String(interner.seed, value)
	if existing, found := interner.findString(value, hash); found {
		return existing
	}
	interner.insert(value, hash)
	return value
}

func (interner *workspaceStringInterner) InternCopy(
	value string,
	copyValue func(string) string,
) string {
	if value == "" {
		return ""
	}
	if interner == nil {
		return copyValue(value)
	}
	hash := maphash.String(interner.seed, value)
	if existing, found := interner.findString(value, hash); found {
		return existing
	}
	owned := copyValue(value)
	interner.insert(owned, hash)
	return owned
}

func (interner *workspaceStringInterner) LookupBytes(
	value []byte,
) (string, bool) {
	if len(value) == 0 {
		return "", true
	}
	if interner == nil || len(interner.entries) == 0 {
		return "", false
	}
	hash := maphash.Bytes(interner.seed, value)
	index := int(hash & uint64(len(interner.entries)-1))
	borrowed := unsafe.String(unsafe.SliceData(value), len(value))
	for {
		existing := interner.entries[index]
		if existing == "" {
			return "", false
		}
		if existing == borrowed {
			return existing, true
		}
		index = (index + 1) & (len(interner.entries) - 1)
	}
}

func (interner *workspaceStringInterner) InternBytes(
	value []byte,
	own func([]byte) string,
) string {
	if len(value) == 0 {
		return ""
	}
	if interner == nil {
		return own(value)
	}
	hash := maphash.Bytes(interner.seed, value)
	if len(interner.entries) > 0 {
		index := int(hash & uint64(len(interner.entries)-1))
		borrowed := unsafe.String(unsafe.SliceData(value), len(value))
		for {
			existing := interner.entries[index]
			if existing == "" {
				break
			}
			if existing == borrowed {
				return existing
			}
			index = (index + 1) & (len(interner.entries) - 1)
		}
	}
	owned := own(value)
	interner.insert(owned, hash)
	return owned
}

func (interner *workspaceStringInterner) findString(
	value string,
	hash uint64,
) (string, bool) {
	if len(interner.entries) == 0 {
		return "", false
	}
	index := int(hash & uint64(len(interner.entries)-1))
	for {
		existing := interner.entries[index]
		if existing == "" {
			return "", false
		}
		if existing == value {
			return existing, true
		}
		index = (index + 1) & (len(interner.entries) - 1)
	}
}

func (interner *workspaceStringInterner) insert(
	value string,
	hash uint64,
) {
	if len(interner.entries) == 0 {
		size := workspaceStringTableSize(interner.expectedItems)
		if size == 0 {
			size = workspaceStringTableMinimumSize
		}
		interner.resize(size)
	} else if (interner.count+1)*100 >
		len(interner.entries)*workspaceStringTableLoadPercent {
		interner.resize(len(interner.entries) * 2)
	}
	index := int(hash & uint64(len(interner.entries)-1))
	for interner.entries[index] != "" {
		index = (index + 1) & (len(interner.entries) - 1)
	}
	interner.entries[index] = value
	interner.count++
}

func (interner *workspaceStringInterner) resize(size int) {
	previous := interner.entries
	interner.entries = make([]string, size)
	interner.count = 0
	interner.expectedItems = 0
	for _, value := range previous {
		if value == "" {
			continue
		}
		interner.insert(value, maphash.String(interner.seed, value))
	}
}

func workspaceStringTableSize(expectedItems int) int {
	if expectedItems <= 0 {
		return 0
	}
	expectedItems = min(expectedItems, workspaceStringTableMaximumHint)
	required := expectedItems +
		(expectedItems*(100-workspaceStringTableLoadPercent)+
			workspaceStringTableLoadPercent-1)/
			workspaceStringTableLoadPercent
	size := workspaceStringTableMinimumSize
	for size < required {
		size *= 2
	}
	return size
}
