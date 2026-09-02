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
		paths := make([]string, 0, len(s.pathRefs))
		referenceCount := 0
		for path := range s.pathRefs {
			paths = append(paths, path)
			referenceCount += len(s.pathRefs[path].References)
		}
		slices.Sort(paths)
		index.paths = paths
		builder := newReverseReferenceBuilder(
			s,
			index,
			referenceCount,
		)
		for pathID, path := range paths {
			builder.addReferences(
				s.pathRefs[path],
				uint32(pathID),
			)
			if builder.failed {
				break
			}
		}
		builder.finish()
	})
}

type reverseReferenceBuildBucket struct {
	head  uint32
	tail  uint32
	count uint32
}

type reverseReferenceBuilder struct {
	index     *reverseReferenceIndex
	resolver  packedReferenceTargetResolver
	buckets   []reverseReferenceBuildBucket
	locations []compactReferenceLocation
	next      []uint32
	failed    bool
}

func newReverseReferenceBuilder(
	snapshot *Snapshot,
	index *reverseReferenceIndex,
	referenceCount int,
) *reverseReferenceBuilder {
	return &reverseReferenceBuilder{
		index: index,
		resolver: packedReferenceTargetResolver{
			snapshot: snapshot,
		},
		locations: make(
			[]compactReferenceLocation,
			0,
			referenceCount,
		),
		next: make([]uint32, 0, referenceCount),
	}
}

func (b *reverseReferenceBuilder) addReferences(
	document *workspaceDocument,
	pathID uint32,
) {
	if document == nil || b == nil || b.failed {
		return
	}
	for index := range document.References {
		reference := &document.References[index]
		rng := reference.rangeValue(document)
		for _, target := range b.resolver.resolve(document, reference) {
			b.addLocation(
				target,
				compactReferenceLocation{
					pathID:     pathID,
					rangeStart: rng.Start,
					rangeEnd:   rng.End,
				},
			)
		}
	}
}

func (b *reverseReferenceBuilder) addLocation(
	target SymbolID,
	location compactReferenceLocation,
) {
	if target == "" || b == nil || b.index == nil {
		return
	}
	if uint64(len(b.locations)) >= uint64(^uint32(0)) {
		b.failed = true
		return
	}
	if b.index.references == nil {
		b.index.references = make(map[SymbolID]uint32)
	}
	bucketID := b.index.references[target]
	if bucketID == 0 {
		if uint64(len(b.buckets)) >= uint64(^uint32(0)) {
			b.failed = true
			return
		}
		b.buckets = append(b.buckets, reverseReferenceBuildBucket{})
		bucketID = uint32(len(b.buckets))
		b.index.references[target] = bucketID
	}
	bucket := &b.buckets[bucketID-1]
	locationID := uint32(len(b.locations)) + 1
	b.locations = append(b.locations, location)
	b.next = append(b.next, 0)
	if bucket.tail == 0 {
		bucket.head = locationID
	} else {
		b.next[bucket.tail-1] = locationID
	}
	bucket.tail = locationID
	if bucket.count == ^uint32(0) {
		b.failed = true
		return
	}
	bucket.count++
}

func (b *reverseReferenceBuilder) finish() {
	if b == nil || b.index == nil {
		return
	}
	if b.failed {
		b.index.references = nil
		b.index.spans = nil
		b.index.locations = nil
		return
	}
	if len(b.buckets) == 0 {
		return
	}
	b.index.spans = make([]compactReferenceSpan, len(b.buckets))
	b.index.locations = make(
		[]compactReferenceLocation,
		len(b.locations),
	)
	var destination uint32
	for bucketIndex, bucket := range b.buckets {
		b.index.spans[bucketIndex] = compactReferenceSpan{
			start: destination,
			count: bucket.count,
		}
		locationID := bucket.head
		for range bucket.count {
			if locationID == 0 ||
				uint64(locationID) > uint64(len(b.locations)) {
				b.index.references = nil
				b.index.spans = nil
				b.index.locations = nil
				return
			}
			source := locationID - 1
			b.index.locations[destination] = b.locations[source]
			destination++
			locationID = b.next[source]
		}
	}
}
