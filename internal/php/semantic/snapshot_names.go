package semantic

import (
	"strings"
	"unicode/utf8"
)

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
	if normalized, ok := s.dynamicNames.Load(name); ok {
		return normalized.(string)
	}
	// Query names commonly arrive as zero-copy slices of a complete source
	// document. A sync.Map key owns its backing string, so caching that slice
	// directly would keep the complete source buffer alive for the lifetime of
	// the published snapshot. Clone only on a cache miss; repeated lookups stay
	// allocation-free.
	ownedName := strings.Clone(name)
	normalized := strings.ToLower(ownedName)
	actual, _ := s.dynamicNames.LoadOrStore(ownedName, normalized)
	return actual.(string)
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
