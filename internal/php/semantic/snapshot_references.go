package semantic

import (
	"slices"
)

func (c *referenceQueryCache) loadOrCompute(
	id SymbolID,
	compute func() []ReferenceLocation,
) []ReferenceLocation {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[SymbolID]*referenceQueryCacheEntry)
	}
	entry, exists := c.entries[id]
	if !exists {
		entry = &referenceQueryCacheEntry{ready: make(chan struct{})}
		c.entries[id] = entry
	}
	c.mu.Unlock()

	if exists {
		<-entry.ready
		return slices.Clone(entry.locations)
	}
	entry.locations = compute()
	close(entry.ready)
	return slices.Clone(entry.locations)
}

func cloneReferences(references []Reference) []Reference {
	result := slices.Clone(references)
	for index := range result {
		if result[index].targets == nil {
			continue
		}
		qualified := slices.Clone(result[index].targets.qualified)
		candidates := slices.Clone(result[index].CandidateIDs())
		result[index].targets = nil
		result[index].SetQualifiedNames(qualified)
		result[index].SetCandidateIDs(candidates)
	}
	return result
}

func (s *Snapshot) visitPathReferences(
	visitor func(path string, document *workspaceDocument),
) {
	// Request-time semantic snapshots normally contain one open-document
	// overlay over one published workspace generation. Avoid allocating and
	// filling a workspace-sized seen set for that overwhelmingly common shape.
	if s != nil && s.base != nil && s.base.base == nil && len(s.pathRefs) == 0 {
		if s.overlayReferences != nil {
			visitor(s.overlayPath, s.overlayReferences)
		}
		for path, document := range s.base.pathRefs {
			if path != s.overlayPath {
				visitor(path, document)
			}
		}
		return
	}
	seen := make(map[string]struct{})
	for current := s; current != nil; current = current.base {
		if current.overlayPath != "" {
			if _, exists := seen[current.overlayPath]; !exists {
				seen[current.overlayPath] = struct{}{}
				if current.overlayReferences != nil {
					visitor(current.overlayPath, current.overlayReferences)
				}
			}
		}
		for path, document := range current.pathRefs {
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			visitor(path, document)
		}
	}
}

func (s *Snapshot) idsChain(
	ids func(snapshot *Snapshot) []SymbolID,
) []SymbolID {
	if s == nil {
		return nil
	}
	if s.base == nil {
		return ids(s)
	}
	var result []SymbolID
	seen := make(map[SymbolID]struct{})
	for current := s; current != nil; current = current.base {
		for _, id := range ids(current) {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			if _, ok := s.Symbol(id); ok {
				result = append(result, id)
			}
		}
	}
	return result
}

func (s *Snapshot) classIDs(name string) []SymbolID {
	normalized := s.lowerName(name, false)
	return s.namedIDs(normalized, classNameIndex)
}

func (s *Snapshot) memberIDs(container SymbolID, name string) []SymbolID {
	normalized := s.lowerName(name, true)
	var result []SymbolID
	direct := s.hasDirectWorkspaceMembers()
	for current := s; current != nil; current = current.base {
		for _, symbol := range current.compactMembers.valuesFor(
			container,
			normalized,
		) {
			if direct {
				result = append(result, symbol.ID)
			} else {
				result = s.appendVisibleID(result, symbol.ID)
			}
		}
		primary, exists := current.members[container][normalized]
		if !exists {
			continue
		}
		result = s.appendVisibleID(result, primary)
		for _, alternate := range current.memberAlternates[container][normalized] {
			result = s.appendVisibleID(result, alternate)
		}
	}
	return result
}

func (s *Snapshot) namedIDs(
	name string,
	kind symbolNameIndexKind,
) []SymbolID {
	if s == nil {
		return nil
	}
	var result []SymbolID
	for current := s; current != nil; current = current.base {
		index := current.symbolNameIndex(kind)
		primary, exists := index.primary[name]
		if !exists {
			continue
		}
		result = s.appendVisibleID(result, primary)
		for _, alternate := range index.alternates[name] {
			result = s.appendVisibleID(result, alternate)
		}
	}
	return result
}

func (s *Snapshot) namedViews(
	name string,
	kind symbolNameIndexKind,
) []SymbolView {
	if s == nil {
		return nil
	}
	var result []SymbolView
	for current := s; current != nil; current = current.base {
		index := current.symbolNameIndex(kind)
		primary, exists := index.primary[name]
		if !exists {
			continue
		}
		result = s.appendVisibleView(result, primary)
		for _, alternate := range index.alternates[name] {
			result = s.appendVisibleView(result, alternate)
		}
	}
	return result
}

func (s *Snapshot) visitNamedViews(
	name string,
	kind symbolNameIndexKind,
	visit func(SymbolView) bool,
) bool {
	if s == nil || visit == nil {
		return true
	}
	var seen inlineSymbolIDSet
	for current := s; current != nil; current = current.base {
		index := current.symbolNameIndex(kind)
		primary, exists := index.primary[name]
		if !exists {
			continue
		}
		if !s.visitVisibleID(&seen, primary, visit) {
			return false
		}
		for _, alternate := range index.alternates[name] {
			if !s.visitVisibleID(&seen, alternate, visit) {
				return false
			}
		}
	}
	return true
}
