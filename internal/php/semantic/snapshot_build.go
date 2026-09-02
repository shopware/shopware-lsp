package semantic

import (
	"slices"
	"strings"
	"sync"
)

// Snapshot is an immutable workspace-wide semantic generation.
type Snapshot struct {
	Revision uint64

	base *Snapshot

	// Document overlays replace exactly one path. Keeping that path and its
	// compact indexes inline avoids constructing a bundle of one-entry maps for
	// every file while binding and inferring a cold workspace.
	overlayPath       string
	overlayReferences *workspaceDocument
	referenceQueries  *referenceQueryCache

	symbols workspaceSymbolIndex
	// Overlay symbols already live in expandedData. Store compact indexes
	// instead of another pointer per map entry.
	expanded          expandedSymbolIndex
	expandedData      []Symbol
	overrides         map[SymbolID]*Symbol
	overrideData      []Symbol
	classes           symbolNameIndex
	functions         symbolNameIndex
	constants         symbolNameIndex
	members           map[SymbolID]map[string]SymbolID
	memberAlternates  map[SymbolID]map[string][]SymbolID
	compactMembers    compactMemberIndex
	globals           []SymbolID
	dynamicNames      *normalizedNameCache
	pathRefs          map[string]*workspaceDocument
	reverseReferences *reverseReferenceIndex
	reverseHierarchy  *reverseHierarchyIndex
	functionContracts map[string][]indexedCallContract
	methodContracts   map[string][]indexedCallContract
}

type ReferenceLocation struct {
	Path       string
	RangeStart uint32
	RangeEnd   uint32
}

type compactReferenceLocation struct {
	pathID     uint32
	rangeStart uint32
	rangeEnd   uint32
}

type compactReferenceSpan struct {
	start uint32
	count uint32
}

type reverseReferenceIndex struct {
	once       sync.Once
	paths      []string
	references map[SymbolID]uint32
	spans      []compactReferenceSpan
	locations  []compactReferenceLocation
}

// referenceQueryCache keeps only reference targets requested from a document
// overlay. Building a complete reverse index for every open document would
// multiply the workspace graph's retained memory, while rescanning the graph
// on every repeated LSP request makes warm references linear in workspace
// size.
type referenceQueryCache struct {
	mu      sync.Mutex
	entries map[SymbolID]*referenceQueryCacheEntry
}

type referenceQueryCacheEntry struct {
	ready     chan struct{}
	locations []ReferenceLocation
}

type symbolNameIndex struct {
	primary    map[string]SymbolID
	alternates map[string][]SymbolID
}

// compactMemberIndex is the immutable member lookup used by published
// workspace snapshots. Names and symbol pointers are contiguous per container,
// avoiding one retained Go map allocation for every class-like declaration.
// Document overlays remain map-backed because they are small and short-lived.
type compactMemberIndex struct {
	containers map[SymbolID]compactMemberContainer
	names      []string
	valueSpans []compactMemberValueSpan
	values     []*workspaceSymbol
}

type compactMemberContainer struct {
	start uint32
	count uint32
}

type compactMemberValueSpan struct {
	start uint32
	count uint32
}

type workspaceMemberBuildEntry struct {
	name   string
	symbol *workspaceSymbol
	order  uint32
}

type symbolNameIndexKind uint8

const (
	classNameIndex symbolNameIndexKind = iota
	functionNameIndex
	constantNameIndex
)

func (i *symbolNameIndex) add(name string, id SymbolID) bool {
	if i.primary == nil {
		i.primary = make(map[string]SymbolID)
	}
	primary, exists := i.primary[name]
	if !exists {
		i.primary[name] = id
		return true
	}
	if primary == id || containsSymbolID(i.alternates[name], id) {
		return false
	}
	if i.alternates == nil {
		i.alternates = make(map[string][]SymbolID)
	}
	i.alternates[name] = append(i.alternates[name], id)
	return true
}

func (i *symbolNameIndex) ids(name string) []SymbolID {
	primary, exists := i.primary[name]
	if !exists {
		return nil
	}
	alternates := i.alternates[name]
	result := make([]SymbolID, 1, 1+len(alternates))
	result[0] = primary
	return append(result, alternates...)
}

func NewSnapshot(revision uint64, documents []*Document) *Snapshot {
	workspaceDocuments := make([]*workspaceDocument, 0, len(documents))
	for _, document := range documents {
		if document == nil {
			continue
		}
		workspaceDocuments = append(
			workspaceDocuments,
			packWorkspaceDocument(document.Clone()),
		)
	}
	return newSnapshot(revision, workspaceDocuments)
}

func newSnapshot(
	revision uint64,
	documents []*workspaceDocument,
) *Snapshot {
	return newSnapshotWithPathRefs(revision, documents, nil)
}

func newSnapshotWithPathRefs(
	revision uint64,
	documents []*workspaceDocument,
	pathRefs map[string]*workspaceDocument,
) *Snapshot {
	buildPathRefs := pathRefs == nil
	if buildPathRefs {
		pathRefs = make(map[string]*workspaceDocument, len(documents))
	}
	symbolCount := 0
	for _, document := range documents {
		if document != nil {
			symbolCount += len(document.Symbols)
		}
	}
	snapshot := &Snapshot{
		Revision:          revision,
		symbols:           newWorkspaceSymbolIndex(symbolCount),
		dynamicNames:      &normalizedNameCache{},
		pathRefs:          pathRefs,
		reverseReferences: &reverseReferenceIndex{},
		reverseHierarchy:  &reverseHierarchyIndex{},
	}
	snapshot.functionContracts, snapshot.methodContracts =
		indexCallContracts(documents)
	for _, document := range documents {
		if document == nil {
			continue
		}
		for index := range document.Symbols {
			symbol := &document.Symbols[index]
			snapshot.symbols.Set(symbol)
		}
	}
	snapshot.reserveSymbolIndexes()
	for _, document := range documents {
		if document == nil {
			continue
		}
		for index := range document.Symbols {
			symbol := &document.Symbols[index]
			indexed, found := snapshot.symbols.Get(symbol.ID)
			if !found || indexed != symbol {
				continue
			}
			container := symbol.container()
			if container != "" && isMemberSymbol(symbol.Kind) {
				continue
			}
			snapshot.indexSymbol(
				symbol.ID,
				symbol.Kind,
				symbol.name(),
				symbol.fullyQualified(),
				container,
			)
		}
	}
	snapshot.buildWorkspaceMemberIndex(documents)
	if buildPathRefs {
		for _, document := range documents {
			if document == nil {
				continue
			}
			snapshot.pathRefs[document.Path] = document
		}
	}
	return snapshot
}

func (s *Snapshot) reserveSymbolIndexes() {
	classCount := 0
	functionCount := 0
	constantCount := 0
	s.symbols.Range(func(_ SymbolID, symbol *workspaceSymbol) bool {
		container := symbol.container()
		if container != "" && isMemberSymbol(symbol.Kind) {
			return true
		}
		if container != "" {
			return true
		}
		switch symbol.Kind {
		case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
			classCount++
		case FunctionSymbol:
			functionCount++
		case GlobalConstantSymbol:
			constantCount++
		}
		return true
	})
	s.reserveSymbolIndexCapacities(
		nil,
		classCount,
		functionCount,
		constantCount,
	)
}

func (s *Snapshot) reserveExpandedSymbolIndexes(symbols []Symbol) {
	memberCounts := make(map[SymbolID]int)
	classCount := 0
	functionCount := 0
	constantCount := 0
	for index := range symbols {
		symbol := &symbols[index]
		if symbol.Container != "" && isMemberSymbol(symbol.Kind) {
			memberCounts[symbol.Container]++
			continue
		}
		if symbol.Container != "" {
			continue
		}
		switch symbol.Kind {
		case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
			classCount++
		case FunctionSymbol:
			functionCount++
		case GlobalConstantSymbol:
			constantCount++
		}
	}
	s.reserveSymbolIndexCapacities(
		memberCounts,
		classCount,
		functionCount,
		constantCount,
	)
}

func (s *Snapshot) reserveSymbolIndexCapacities(
	memberCounts map[SymbolID]int,
	classCount,
	functionCount,
	constantCount int,
) {
	if len(memberCounts) != 0 {
		s.members = make(
			map[SymbolID]map[string]SymbolID,
			len(memberCounts),
		)
		for container, count := range memberCounts {
			s.members[container] = make(map[string]SymbolID, count)
		}
	}
	if classCount != 0 {
		s.classes.primary = make(map[string]SymbolID, classCount)
	}
	if functionCount != 0 {
		s.functions.primary = make(map[string]SymbolID, functionCount)
	}
	if constantCount != 0 {
		s.constants.primary = make(map[string]SymbolID, constantCount)
	}
	s.globals = make(
		[]SymbolID,
		0,
		classCount+functionCount+constantCount,
	)
}

func (s *Snapshot) addExpandedSymbol(symbol *Symbol, index int) {
	if symbol == nil {
		return
	}
	s.expanded.Set(symbol.ID, index, s.expandedData)
	delete(s.overrides, symbol.ID)
	s.indexSymbol(
		symbol.ID,
		symbol.Kind,
		symbol.Name,
		symbol.FullyQualified,
		symbol.Container,
	)
}

func (s *Snapshot) indexSymbol(
	id SymbolID,
	kind SymbolKind,
	name,
	fullyQualified string,
	container SymbolID,
) {
	if container != "" && isMemberSymbol(kind) {
		if s.members == nil {
			s.members = make(map[SymbolID]map[string]SymbolID)
		}
		if s.members[container] == nil {
			s.members[container] = make(map[string]SymbolID)
		}
		normalized := normalizeSymbolName(name, true)
		primary, exists := s.members[container][normalized]
		if !exists {
			s.members[container][normalized] = id
			return
		}
		if primary == id {
			return
		}
		if s.memberAlternates == nil {
			s.memberAlternates = make(
				map[SymbolID]map[string][]SymbolID,
			)
		}
		if s.memberAlternates[container] == nil {
			s.memberAlternates[container] = make(map[string][]SymbolID)
		}
		s.memberAlternates[container][normalized] = appendUniqueSymbolID(
			s.memberAlternates[container][normalized],
			id,
		)
		return
	}
	if container != "" {
		return
	}
	normalized := normalizeSymbolName(fullyQualified, false)
	switch kind {
	case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
		if s.classes.add(normalized, id) {
			s.globals = append(s.globals, id)
		}
	case FunctionSymbol:
		if s.functions.add(normalized, id) {
			s.globals = append(s.globals, id)
		}
	case GlobalConstantSymbol:
		if s.constants.add(fullyQualified, id) {
			s.globals = append(s.globals, id)
		}
	}
}

func appendUniqueSymbolID(ids []SymbolID, id SymbolID) []SymbolID {
	if containsSymbolID(ids, id) {
		return ids
	}
	return append(ids, id)
}

func containsSymbolID(ids []SymbolID, target SymbolID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func (s *Snapshot) buildWorkspaceMemberIndex(
	documents []*workspaceDocument,
) {
	if s == nil {
		return
	}
	memberCount := 0
	s.symbols.Range(func(_ SymbolID, symbol *workspaceSymbol) bool {
		if symbol.container() != "" && isMemberSymbol(symbol.Kind) {
			memberCount++
		}
		return true
	})
	if memberCount == 0 {
		return
	}
	// Normalized member names repeat heavily across unrelated containers.
	// Canonicalize them while building the immutable index so the retained
	// contiguous name slice does not own one lowercase allocation per member.
	// The hash table itself is construction-only and is released after this
	// method; cap the initial hint so unusually large workspaces grow in
	// proportion to their actual unique-name count.
	normalizedNames := newWorkspaceStringInterner(
		min(memberCount, 64<<10),
	)
	entries := make([]workspaceMemberBuildEntry, 0, memberCount)
	order := 0
	for _, document := range documents {
		if document == nil {
			continue
		}
		for index := range document.Symbols {
			symbol := &document.Symbols[index]
			indexed, found := s.symbols.Get(symbol.ID)
			if !found ||
				indexed != symbol ||
				symbol.container() == "" ||
				!isMemberSymbol(symbol.Kind) {
				continue
			}
			entries = append(entries, workspaceMemberBuildEntry{
				name: normalizedNames.Intern(
					normalizeSymbolName(symbol.name(), true),
				),
				symbol: symbol,
				order:  compactMemberUint32(order, "build order"),
			})
			order++
		}
	}
	slices.SortFunc(entries, func(
		left,
		right workspaceMemberBuildEntry,
	) int {
		if compared := strings.Compare(
			string(left.symbol.container()),
			string(right.symbol.container()),
		); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.name, right.name); compared != 0 {
			return compared
		}
		switch {
		case left.order < right.order:
			return -1
		case left.order > right.order:
			return 1
		default:
			return 0
		}
	})
	containerCount := 0
	nameCount := 0
	valueCount := 0
	for groupStart := 0; groupStart < len(entries); {
		groupEnd := workspaceMemberBuildGroupEnd(entries, groupStart)
		if groupStart == 0 ||
			entries[groupStart-1].symbol.container() !=
				entries[groupStart].symbol.container() {
			containerCount++
		}
		nameCount++
		for index := groupStart; index < groupEnd; index++ {
			if workspaceMemberBuildEntryUnique(
				entries,
				groupStart,
				index,
			) {
				valueCount++
			}
		}
		groupStart = groupEnd
	}
	memberIndex := compactMemberIndex{
		containers: make(
			map[SymbolID]compactMemberContainer,
			containerCount,
		),
		names:      make([]string, 0, nameCount),
		valueSpans: make([]compactMemberValueSpan, 0, nameCount),
		values:     make([]*workspaceSymbol, 0, valueCount),
	}
	containerStart := 0
	var currentContainer SymbolID
	for groupStart := 0; groupStart < len(entries); {
		groupEnd := workspaceMemberBuildGroupEnd(entries, groupStart)
		entry := entries[groupStart]
		container := entry.symbol.container()
		if currentContainer != "" && currentContainer != container {
			memberIndex.containers[currentContainer] =
				compactMemberContainer{
					start: compactMemberUint32(
						containerStart,
						"container start",
					),
					count: compactMemberUint32(
						len(memberIndex.names)-containerStart,
						"container count",
					),
				}
			containerStart = len(memberIndex.names)
		}
		currentContainer = container
		valueStart := len(memberIndex.values)
		for index := groupStart; index < groupEnd; index++ {
			if workspaceMemberBuildEntryUnique(
				entries,
				groupStart,
				index,
			) {
				memberIndex.values = append(
					memberIndex.values,
					entries[index].symbol,
				)
			}
		}
		memberIndex.names = append(memberIndex.names, entry.name)
		memberIndex.valueSpans = append(
			memberIndex.valueSpans,
			compactMemberValueSpan{
				start: compactMemberUint32(valueStart, "value start"),
				count: compactMemberUint32(
					len(memberIndex.values)-valueStart,
					"value count",
				),
			},
		)
		groupStart = groupEnd
	}
	if currentContainer != "" {
		memberIndex.containers[currentContainer] = compactMemberContainer{
			start: compactMemberUint32(containerStart, "container start"),
			count: compactMemberUint32(
				len(memberIndex.names)-containerStart,
				"container count",
			),
		}
	}
	s.compactMembers = memberIndex
}

func workspaceMemberBuildGroupEnd(
	entries []workspaceMemberBuildEntry,
	start int,
) int {
	entry := entries[start]
	end := start + 1
	for end < len(entries) &&
		entries[end].symbol.container() == entry.symbol.container() &&
		entries[end].name == entry.name {
		end++
	}
	return end
}

func workspaceMemberBuildEntryUnique(
	entries []workspaceMemberBuildEntry,
	groupStart,
	index int,
) bool {
	id := entries[index].symbol.ID
	for previous := groupStart; previous < index; previous++ {
		if entries[previous].symbol.ID == id {
			return false
		}
	}
	return true
}

func compactMemberUint32(value int, label string) uint32 {
	if value < 0 || uint64(value) > uint64(^uint32(0)) {
		panic("semantic: compact member " + label + " exceeds uint32")
	}
	return uint32(value)
}

func (index *compactMemberIndex) valuesFor(
	container SymbolID,
	name string,
) []*workspaceSymbol {
	if index == nil || len(index.containers) == 0 {
		return nil
	}
	containerSpan, exists := index.containers[container]
	if !exists || containerSpan.count == 0 {
		return nil
	}
	start := int(containerSpan.start)
	end := start + int(containerSpan.count)
	offset, found := slices.BinarySearch(index.names[start:end], name)
	if !found {
		return nil
	}
	valueSpan := index.valueSpans[start+offset]
	valueStart := int(valueSpan.start)
	return index.values[valueStart : valueStart+int(valueSpan.count)]
}

func (index *compactMemberIndex) valuesForContainer(
	container SymbolID,
) []*workspaceSymbol {
	if index == nil || len(index.containers) == 0 {
		return nil
	}
	containerSpan, exists := index.containers[container]
	if !exists || containerSpan.count == 0 {
		return nil
	}
	entryStart := int(containerSpan.start)
	entryEnd := entryStart + int(containerSpan.count)
	first := index.valueSpans[entryStart]
	last := index.valueSpans[entryEnd-1]
	valueStart := int(first.start)
	valueEnd := int(last.start) + int(last.count)
	return index.values[valueStart:valueEnd]
}
