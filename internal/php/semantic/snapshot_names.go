package semantic

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// normalizedNameCache owns a bounded working set of query names for a
// published snapshot. A typed map avoids the interface and hash-trie
// allocations sync.Map incurs during cold inference, while FIFO replacement
// and the RW lock keep long-running LSP use bounded and repeated lookups
// allocation-free.
type normalizedNameCache struct {
	mu     sync.RWMutex
	values map[string]string
	order  []string
	next   int
}

const (
	// The entry bound retains a broad interactive working set without allowing
	// distinct workspace or client-provided names to grow for the process
	// lifetime. Very long malformed identifiers are normalized but not cached.
	normalizedNameCacheEntries      = 32 << 10
	normalizedNameCacheMaxNameBytes = 512
)

func (c *normalizedNameCache) load(name string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	normalized, found := c.values[name]
	c.mu.RUnlock()
	return normalized, found
}

func (c *normalizedNameCache) normalize(name string) string {
	if normalized, found := c.load(name); found {
		return normalized
	}
	if len(name) > normalizedNameCacheMaxNameBytes {
		return strings.ToLower(name)
	}
	// Query names commonly arrive as zero-copy slices of a complete source
	// document. Map keys own their backing strings, so clone only cache misses
	// before retaining them in the bounded cache.
	ownedName := strings.Clone(name)
	normalized := strings.ToLower(ownedName)

	c.mu.Lock()
	if existing, found := c.values[name]; found {
		c.mu.Unlock()
		return existing
	}
	if c.values == nil {
		c.values = make(map[string]string)
	}
	if len(c.values) == normalizedNameCacheEntries {
		delete(c.values, c.order[c.next])
		c.order[c.next] = ownedName
		c.next++
		if c.next == normalizedNameCacheEntries {
			c.next = 0
		}
	} else {
		c.order = append(c.order, ownedName)
	}
	c.values[ownedName] = normalized
	c.mu.Unlock()
	return normalized
}

func (c *normalizedNameCache) rangeValues(
	visit func(string, string) bool,
) {
	if c == nil || visit == nil {
		return
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for name, normalized := range c.values {
		if !visit(name, normalized) {
			return
		}
	}
}

// clear releases names accumulated while this cache's snapshot was the
// workspace generation used for linking and inference. Superseded snapshots
// can remain reachable briefly through in-flight overlays, but cache contents
// are an optimization rather than immutable semantic state: a later lookup
// can safely repopulate them.
func (c *normalizedNameCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.values = nil
	c.order = nil
	c.next = 0
	c.mu.Unlock()
}

type inlineSymbolIDSet struct {
	values   [4]SymbolID
	length   uint8
	overflow map[SymbolID]struct{}
}

func (s *inlineSymbolIDSet) add(id SymbolID) bool {
	for index := uint8(0); index < s.length; index++ {
		if s.values[index] == id {
			return false
		}
	}
	if s.length < uint8(len(s.values)) {
		s.values[s.length] = id
		s.length++
		return true
	}
	if s.overflow == nil {
		s.overflow = make(map[SymbolID]struct{})
	}
	if _, exists := s.overflow[id]; exists {
		return false
	}
	s.overflow[id] = struct{}{}
	return true
}

func (s *Snapshot) symbolNameIndex(
	kind symbolNameIndexKind,
) *symbolNameIndex {
	switch kind {
	case classNameIndex:
		return &s.classes
	case functionNameIndex:
		return &s.functions
	case constantNameIndex:
		return &s.constants
	default:
		panic("semantic: invalid symbol name index kind")
	}
}

func (s *Snapshot) appendVisibleID(
	result []SymbolID,
	id SymbolID,
) []SymbolID {
	if containsSymbolID(result, id) {
		return result
	}
	if s.base != nil {
		if _, visible := s.SymbolView(id); !visible {
			return result
		}
	}
	return append(result, id)
}

func (s *Snapshot) appendVisibleView(
	result []SymbolView,
	id SymbolID,
) []SymbolView {
	for _, existing := range result {
		if existing.ID() == id {
			return result
		}
	}
	symbol, visible := s.SymbolView(id)
	if !visible {
		return result
	}
	return append(result, symbol)
}

func (s *Snapshot) visitVisibleID(
	seen *inlineSymbolIDSet,
	id SymbolID,
	visit func(SymbolView) bool,
) bool {
	if !seen.add(id) {
		return true
	}
	symbol, visible := s.SymbolView(id)
	return !visible || visit(symbol)
}

func (s *Snapshot) lookup(ids []SymbolID) []Symbol {
	if len(ids) == 0 {
		return nil
	}
	result := make([]Symbol, 0, len(ids))
	for _, id := range ids {
		if symbol, ok := s.Symbol(id); ok {
			result = append(result, symbol)
		}
	}
	return result
}

func normalizeSymbolName(name string, member bool) string {
	if member {
		name = strings.TrimPrefix(name, "$")
	} else {
		name = strings.TrimPrefix(name, "\\")
	}
	if name == "" {
		return ""
	}
	if isLowerASCII(name) {
		return name
	}
	return strings.ToLower(name)
}

func (s *Snapshot) lowerName(name string, member bool) string {
	if member {
		name = strings.TrimPrefix(name, "$")
	} else {
		name = strings.TrimPrefix(name, "\\")
	}
	if isLowerASCII(name) {
		return name
	}
	if s.dynamicNames == nil {
		return strings.ToLower(name)
	}
	return s.dynamicNames.normalize(name)
}

func isLowerASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current >= 'A' && current <= 'Z' || current >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isMemberSymbol(kind SymbolKind) bool {
	switch kind {
	case MethodSymbol, PropertySymbol, ClassConstantSymbol, EnumCaseSymbol,
		TypeAliasSymbol:
		return true
	default:
		return false
	}
}
