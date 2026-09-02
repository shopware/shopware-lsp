package semantic

import (
	"maps"
	"slices"
)

func (s *Snapshot) CloneSymbols() map[SymbolID]Symbol {
	if s == nil {
		return nil
	}
	result := make(map[SymbolID]Symbol)
	if s.base != nil {
		for id, symbol := range s.base.CloneSymbols() {
			if s.overlayPath == "" || s.overlayPath != symbol.Path {
				result[id] = symbol
			}
		}
	}
	s.symbols.Range(func(id SymbolID, symbol *workspaceSymbol) bool {
		if symbol != nil {
			result[id] = symbol.materialize()
		}
		return true
	})
	s.expanded.Range(s.expandedData, func(id SymbolID, index uint32) bool {
		if uint64(index) < uint64(len(s.expandedData)) {
			result[id] = s.expandedData[index]
		}
		return true
	})
	for id, symbol := range s.overrides {
		if symbol != nil {
			result[id] = *symbol
		}
	}
	return result
}

// WithUpdatedSymbols overlays values for declarations already present in the
// generation. Index keys and reverse references remain shared because stable
// declaration IDs, names, and ranges do not change during type fixed-point
// analysis.
func (s *Snapshot) WithUpdatedSymbols(document *Document) *Snapshot {
	return s.withUpdatedSymbols(document, nil)
}

// WithUpdatedFunctionReturns overlays function-like declarations during local
// fixed-point inference without copying unrelated declarations.
func (s *Snapshot) WithUpdatedFunctionReturns(document *Document) *Snapshot {
	return s.withUpdatedSymbols(document, func(symbol *Symbol) bool {
		return symbol.Kind == MethodSymbol ||
			symbol.Kind == FunctionSymbol ||
			symbol.Kind == ClosureSymbol
	})
}

func (s *Snapshot) withUpdatedSymbols(
	document *Document,
	include func(*Symbol) bool,
) *Snapshot {
	if s == nil || document == nil {
		return s
	}
	result := *s
	result.overrides = maps.Clone(s.overrides)
	if result.overrides == nil {
		result.overrides = make(map[SymbolID]*Symbol)
	}
	count := 0
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		if include != nil && !include(symbol) {
			continue
		}
		if _, exists := s.SymbolView(symbol.ID); exists {
			count++
		}
	}
	result.overrideData = make([]Symbol, 0, count)
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		if include != nil && !include(symbol) {
			continue
		}
		if _, exists := s.SymbolView(symbol.ID); exists {
			result.overrideData = append(result.overrideData, *symbol)
			updated := &result.overrideData[len(result.overrideData)-1]
			result.overrides[updated.ID] = updated
		}
	}
	return &result
}

// WithDocument returns an overlay generation in which declarations from the
// supplied document replace persisted declarations from the same path.
func (s *Snapshot) WithDocument(document *Document) *Snapshot {
	return s.withDocument(document, true)
}

// WithDeclarations returns an overlay containing the supplied document's
// declarations but not its reference graph. This is sufficient while linking
// and inferring one document and avoids packing references that background
// indexing immediately discards.
func (s *Snapshot) WithDeclarations(document *Document) *Snapshot {
	return s.withDocument(document, false)
}

func (s *Snapshot) withDocument(
	document *Document,
	includeReferences bool,
) *Snapshot {
	if document == nil {
		return s
	}
	result := &Snapshot{
		Revision:         s.Revision,
		base:             s,
		overlayPath:      document.Path,
		expanded:         newExpandedSymbolIndex(len(document.Symbols)),
		expandedData:     document.Symbols,
		dynamicNames:     s.dynamicNames,
		reverseHierarchy: &reverseHierarchyIndex{},
	}
	if includeReferences {
		result.referenceQueries = &referenceQueryCache{}
	}
	if len(document.CallContracts) > 0 {
		contractDocument := &workspaceDocument{
			Path:          document.Path,
			CallContracts: cloneCallContracts(document.CallContracts),
		}
		result.functionContracts, result.methodContracts =
			indexCallContracts([]*workspaceDocument{contractDocument})
	}
	result.reserveExpandedSymbolIndexes(document.Symbols)
	for index := range document.Symbols {
		result.addExpandedSymbol(&document.Symbols[index], index)
	}
	if includeReferences && len(document.References) > 0 {
		referenceDocument := &workspaceDocument{Path: document.Path}
		referenceDocument.packReferences(document.References)
		result.overlayReferences = referenceDocument
	}
	return result
}

// HasDocument reports whether path contributes a document to this generation.
func (s *Snapshot) HasDocument(path string) bool {
	for current := s; current != nil; current = current.base {
		if current.overlayPath != "" && current.overlayPath == path {
			return true
		}
		if _, found := current.pathRefs[path]; found {
			return true
		}
	}
	return false
}

func (s *Snapshot) ensureReverseReferences() {
	if s == nil || s.reverseReferences == nil {
		return
	}
	index := s.reverseReferences
	index.once.Do(func() {
		index.references = make(map[SymbolID][]compactReferenceLocation)
		paths := make([]string, 0, len(s.pathRefs))
		for path := range s.pathRefs {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		index.paths = paths
		for pathID, path := range paths {
			s.addReferences(s.pathRefs[path], uint32(pathID))
		}
	})
}

func (s *Snapshot) addReferences(
	document *workspaceDocument,
	pathID uint32,
) {
	if document == nil {
		return
	}
	for index := range document.References {
		reference := &document.References[index]
		rng := reference.rangeValue(document)
		for _, target := range s.referenceTargetsPacked(document, reference) {
			s.reverseReferences.references[target] = append(
				s.reverseReferences.references[target],
				compactReferenceLocation{
					pathID:     pathID,
					rangeStart: rng.Start,
					rangeEnd:   rng.End,
				},
			)
		}
	}
}
