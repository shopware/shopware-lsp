package semantic

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/php/types"
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

	symbols workspaceSymbolIndex
	// Overlay symbols already live in expandedData. Store compact indexes
	// instead of another pointer per map entry.
	expanded          map[SymbolID]uint32
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
	dynamicNames      *sync.Map
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

type reverseReferenceIndex struct {
	once       sync.Once
	paths      []string
	references map[SymbolID][]compactReferenceLocation
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
		dynamicNames:      &sync.Map{},
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
	if index < 0 || uint64(index) > uint64(^uint32(0)) {
		panic("semantic: expanded symbol index exceeds uint32")
	}
	if s.expanded == nil {
		s.expanded = make(map[SymbolID]uint32)
	}
	s.expanded[symbol.ID] = uint32(index)
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
				name:   normalizeSymbolName(symbol.name(), true),
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

func (s *Snapshot) Symbol(id SymbolID) (Symbol, bool) {
	symbol, ok := s.SymbolView(id)
	if !ok {
		return Symbol{}, false
	}
	return symbol.Materialize(), true
}

// SymbolView returns a lightweight immutable symbol owned by this snapshot.
func (s *Snapshot) SymbolView(id SymbolID) (SymbolView, bool) {
	if s == nil {
		return SymbolView{}, false
	}
	if symbol, ok := s.overrides[id]; ok {
		return expandedView(symbol), true
	}
	if index, ok := s.expanded[id]; ok &&
		uint64(index) < uint64(len(s.expandedData)) {
		return expandedView(&s.expandedData[index]), true
	}
	symbol, ok := s.symbols.Get(id)
	if ok && symbol != nil {
		return workspaceView(symbol), true
	}
	if s.base == nil {
		return SymbolView{}, false
	}
	symbolValue, ok := s.base.SymbolView(id)
	if !ok {
		return SymbolView{}, false
	}
	if s.overlayPath != "" && s.overlayPath == symbolValue.Path() {
		return SymbolView{}, false
	}
	return symbolValue, true
}

func (s *Snapshot) Classes(name string) []Symbol {
	return s.lookup(s.classIDs(name))
}

func (s *Snapshot) ClassViews(name string) []SymbolView {
	return s.namedViews(s.lowerName(name, false), classNameIndex)
}

// VisitClassViews visits matching classes without materializing a result
// slice. Returning false stops the traversal; the result reports whether all
// matching declarations were visited.
func (s *Snapshot) VisitClassViews(
	name string,
	visit func(SymbolView) bool,
) bool {
	if s == nil {
		return true
	}
	return s.visitNamedViews(
		s.lowerName(name, false),
		classNameIndex,
		visit,
	)
}

func (s *Snapshot) Functions(name string) []Symbol {
	return s.lookup(s.namedIDs(
		s.lowerName(name, false),
		functionNameIndex,
	))
}

func (s *Snapshot) FunctionViews(name string) []SymbolView {
	return s.namedViews(
		s.lowerName(name, false),
		functionNameIndex,
	)
}

// VisitFunctionViews is the non-materializing counterpart of FunctionViews.
func (s *Snapshot) VisitFunctionViews(
	name string,
	visit func(SymbolView) bool,
) bool {
	if s == nil {
		return true
	}
	return s.visitNamedViews(
		s.lowerName(name, false),
		functionNameIndex,
		visit,
	)
}

// VisitFunctionCallContracts visits dynamic return contracts for a resolved
// function name. Open-document overlays shadow persisted contracts from the
// same path without copying the base snapshot's contract indexes.
func (s *Snapshot) VisitFunctionCallContracts(
	name string,
	visit func(CallContract) bool,
) bool {
	if s == nil || visit == nil {
		return true
	}
	key := functionCallContractKey(name)
	return s.visitCallContracts(func(current *Snapshot) []indexedCallContract {
		return current.functionContracts[key]
	}, visit)
}

// VisitMethodCallContracts visits contracts whose declaring class is a
// supertype of receiver. This makes metadata declared for an interface or
// parent class apply to concrete implementations without duplicating records.
func (s *Snapshot) VisitMethodCallContracts(
	receiver types.Type,
	name string,
	visit func(CallContract) bool,
) bool {
	if s == nil || visit == nil || receiver.IsUnknown() {
		return true
	}
	key := methodCallContractKey(name)
	relations := s.Relations()
	return s.visitCallContracts(func(current *Snapshot) []indexedCallContract {
		return current.methodContracts[key]
	}, func(contract CallContract) bool {
		if !relations.IsSubtype(
			receiver,
			types.Named(contract.Target.Class),
		) {
			return true
		}
		return visit(contract)
	})
}

func (s *Snapshot) visitCallContracts(
	entries func(*Snapshot) []indexedCallContract,
	visit func(CallContract) bool,
) bool {
	var shadowedPaths map[string]struct{}
	for current := s; current != nil; current = current.base {
		for _, entry := range entries(current) {
			if _, shadowed := shadowedPaths[entry.path]; shadowed {
				continue
			}
			if !visit(entry.contract) {
				return false
			}
		}
		if current.overlayPath != "" {
			if shadowedPaths == nil {
				shadowedPaths = make(map[string]struct{})
			}
			shadowedPaths[current.overlayPath] = struct{}{}
		}
	}
	return true
}

func (s *Snapshot) Constants(name string) []Symbol {
	return s.lookup(s.namedIDs(
		strings.TrimPrefix(name, "\\"),
		constantNameIndex,
	))
}

func (s *Snapshot) ConstantViews(name string) []SymbolView {
	return s.namedViews(
		strings.TrimPrefix(name, "\\"),
		constantNameIndex,
	)
}

// VisitConstantViews is the non-materializing counterpart of ConstantViews.
func (s *Snapshot) VisitConstantViews(
	name string,
	visit func(SymbolView) bool,
) bool {
	if s == nil {
		return true
	}
	return s.visitNamedViews(
		strings.TrimPrefix(name, "\\"),
		constantNameIndex,
		visit,
	)
}

func (s *Snapshot) Members(container SymbolID, name string) []Symbol {
	if s == nil {
		return nil
	}
	return s.lookup(s.memberIDs(container, name))
}

func (s *Snapshot) MemberViews(container SymbolID, name string) []SymbolView {
	if s == nil {
		return nil
	}
	normalized := s.lowerName(name, true)
	var result []SymbolView
	direct := s.hasDirectWorkspaceMembers()
	for current := s; current != nil; current = current.base {
		for _, symbol := range current.compactMembers.valuesFor(
			container,
			normalized,
		) {
			if direct {
				result = append(result, workspaceView(symbol))
			} else {
				result = s.appendVisibleView(result, symbol.ID)
			}
		}
		primary, exists := current.members[container][normalized]
		if !exists {
			continue
		}
		result = s.appendVisibleView(result, primary)
		for _, alternate := range current.memberAlternates[container][normalized] {
			result = s.appendVisibleView(result, alternate)
		}
	}
	return result
}

// VisitMemberViews visits named members without materializing a result slice.
func (s *Snapshot) VisitMemberViews(
	container SymbolID,
	name string,
	visit func(SymbolView) bool,
) bool {
	if s == nil || visit == nil {
		return true
	}
	normalized := s.lowerName(name, true)
	var seen inlineSymbolIDSet
	direct := s.hasDirectWorkspaceMembers()
	for current := s; current != nil; current = current.base {
		for _, symbol := range current.compactMembers.valuesFor(
			container,
			normalized,
		) {
			if direct {
				if !visit(workspaceView(symbol)) {
					return false
				}
				continue
			}
			if !s.visitVisibleID(&seen, symbol.ID, visit) {
				return false
			}
		}
		primary, exists := current.members[container][normalized]
		if !exists {
			continue
		}
		if !s.visitVisibleID(&seen, primary, visit) {
			return false
		}
		for _, alternate := range current.memberAlternates[container][normalized] {
			if !s.visitVisibleID(&seen, alternate, visit) {
				return false
			}
		}
	}
	return true
}

func (s *Snapshot) MembersOf(container SymbolID) []Symbol {
	views := s.MemberViewsOf(container)
	result := make([]Symbol, len(views))
	for index, symbol := range views {
		result[index] = symbol.Materialize()
	}
	return result
}

func (s *Snapshot) MemberViewsOf(container SymbolID) []SymbolView {
	if s == nil {
		return nil
	}
	if s.hasDirectWorkspaceMembers() {
		values := s.compactMembers.valuesForContainer(container)
		if len(values) == 0 {
			return nil
		}
		result := make([]SymbolView, len(values))
		for index, symbol := range values {
			result[index] = workspaceView(symbol)
		}
		s.sortMemberViews(result)
		return result
	}
	capacity := 0
	for current := s; current != nil; current = current.base {
		capacity += len(
			current.compactMembers.valuesForContainer(container),
		)
		capacity += len(current.members[container])
		for _, alternates := range current.memberAlternates[container] {
			capacity += len(alternates)
		}
	}
	if capacity == 0 {
		return nil
	}
	result := make([]SymbolView, 0, capacity)
	seen := make(map[SymbolID]struct{}, capacity)
	appendView := func(id SymbolID) {
		if _, exists := seen[id]; exists {
			return
		}
		symbol, exists := s.SymbolView(id)
		if !exists {
			return
		}
		seen[id] = struct{}{}
		result = append(result, symbol)
	}
	for current := s; current != nil; current = current.base {
		for _, symbol := range current.compactMembers.valuesForContainer(
			container,
		) {
			appendView(symbol.ID)
		}
		for name, primary := range current.members[container] {
			appendView(primary)
			for _, id := range current.memberAlternates[container][name] {
				appendView(id)
			}
		}
	}
	s.sortMemberViews(result)
	return result
}

func (s *Snapshot) sortMemberViews(result []SymbolView) {
	slices.SortFunc(result, func(left, right SymbolView) int {
		if left.Kind() != right.Kind() {
			return int(left.Kind()) - int(right.Kind())
		}
		if compared := strings.Compare(
			s.lowerName(left.Name(), true),
			s.lowerName(right.Name(), true),
		); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.Path(), right.Path()); compared != 0 {
			return compared
		}
		if left.Range().Start < right.Range().Start {
			return -1
		}
		if left.Range().Start > right.Range().Start {
			return 1
		}
		return 0
	})
}

func (s *Snapshot) hasDirectWorkspaceMembers() bool {
	return s != nil &&
		s.base == nil &&
		len(s.expanded) == 0 &&
		len(s.overrides) == 0
}

func (s *Snapshot) SymbolsIn(path string) []Symbol {
	if s == nil {
		return nil
	}
	if s.overlayPath != "" && s.overlayPath == path {
		result := make([]Symbol, 0, len(s.expandedData))
		for index := range s.expandedData {
			if symbol, ok := s.Symbol(s.expandedData[index].ID); ok {
				result = append(result, symbol)
			}
		}
		return result
	}
	if document, exists := s.pathRefs[path]; exists {
		result := make([]Symbol, len(document.Symbols))
		for index := range document.Symbols {
			result[index] = document.Symbols[index].materialize()
		}
		return result
	}
	if s.base != nil {
		return s.base.SymbolsIn(path)
	}
	return nil
}

func (s *Snapshot) ReferencesTo(id SymbolID) []ReferenceLocation {
	if s == nil {
		return nil
	}
	if s.base != nil {
		var result []ReferenceLocation
		s.visitPathReferences(func(path string, document *workspaceDocument) {
			for index := range document.References {
				reference := &document.References[index]
				rng := reference.rangeValue(document)
				for _, target := range s.referenceTargetsPacked(
					document,
					reference,
				) {
					if target == id {
						result = append(result, ReferenceLocation{
							Path:       path,
							RangeStart: rng.Start,
							RangeEnd:   rng.End,
						})
						break
					}
				}
			}
		})
		return result
	}
	s.ensureReverseReferences()
	index := s.reverseReferences
	if index == nil {
		return nil
	}
	var result []ReferenceLocation
	for _, location := range index.references[id] {
		result = append(result, ReferenceLocation{
			Path:       index.paths[location.pathID],
			RangeStart: location.rangeStart,
			RangeEnd:   location.rangeEnd,
		})
	}
	return result
}

func (s *Snapshot) AllSymbols() []Symbol {
	if s == nil {
		return nil
	}
	var result []Symbol
	seen := make(map[SymbolID]struct{})
	for current := s; current != nil; current = current.base {
		current.symbols.Range(func(id SymbolID, _ *workspaceSymbol) bool {
			if _, exists := seen[id]; exists {
				return true
			}
			seen[id] = struct{}{}
			if symbol, ok := s.Symbol(id); ok {
				result = append(result, symbol)
			}
			return true
		})
		for id := range current.expanded {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			if symbol, ok := s.Symbol(id); ok {
				result = append(result, symbol)
			}
		}
	}
	return result
}

// GlobalSymbols returns class-like, function, and constant declarations
// without scanning locals and members in the workspace symbol table.
func (s *Snapshot) GlobalSymbols() []Symbol {
	views := s.GlobalSymbolViews()
	result := make([]Symbol, len(views))
	for index := range views {
		result[index] = views[index].Materialize()
	}
	return result
}

// GlobalSymbolViews returns lightweight class-like, function, and constant
// declaration views without materializing complete public symbols.
func (s *Snapshot) GlobalSymbolViews() []SymbolView {
	if s == nil {
		return nil
	}
	ids := s.idsChain(func(snapshot *Snapshot) []SymbolID {
		return snapshot.globals
	})
	result := make([]SymbolView, 0, len(ids))
	seen := make(map[SymbolID]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if symbol, ok := s.SymbolView(id); ok {
			result = append(result, symbol)
		}
	}
	return result
}

// GlobalClassViews filters global declarations before materialization so
// callers that need only classes do not allocate public function/constant
// symbols and their optional side data.
func (s *Snapshot) GlobalClassViews() []SymbolView {
	globals := s.GlobalSymbolViews()
	result := globals[:0]
	for _, symbol := range globals {
		switch symbol.Kind() {
		case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
			result = append(result, symbol)
		}
	}
	return result
}

func (s *Snapshot) IsSubtypeOf(candidate, target string) bool {
	target = s.classAliasCanonicalName(target)
	normalizedTarget := s.lowerName(target, false)
	if s.lowerName(candidate, false) == normalizedTarget {
		return true
	}
	return s.isSubtypeOf(candidate, normalizedTarget, make(map[string]struct{}))
}

func (s *Snapshot) classAliasCanonicalName(name string) string {
	visited := make(map[string]struct{})
	for name != "" {
		key := s.lowerName(name, false)
		if _, exists := visited[key]; exists {
			return name
		}
		visited[key] = struct{}{}
		aliasTarget := ""
		s.VisitClassViews(name, func(view SymbolView) bool {
			if !view.Flags().Has(ClassAliasFlag) {
				return true
			}
			_, extends, _ := view.HierarchyNames()
			if len(extends) == 1 {
				aliasTarget = extends[0]
				return false
			}
			return true
		})
		if aliasTarget == "" {
			return name
		}
		name = aliasTarget
	}
	return name
}

func (s *Snapshot) isSubtypeOf(
	candidate,
	normalizedTarget string,
	visited map[string]struct{},
) bool {
	normalized := s.lowerName(candidate, false)
	if _, exists := visited[normalized]; exists {
		return false
	}
	visited[normalized] = struct{}{}
	found := false
	s.VisitClassViews(candidate, func(classView SymbolView) bool {
		class := classView.Materialize()
		for _, parent := range class.Extends {
			if s.lowerName(parent, false) == normalizedTarget ||
				s.isSubtypeOf(parent, normalizedTarget, visited) {
				found = true
				return false
			}
		}
		for _, parent := range class.Implements {
			if s.lowerName(parent, false) == normalizedTarget ||
				s.isSubtypeOf(parent, normalizedTarget, visited) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func (s *Snapshot) Relations() types.Relations {
	return types.Relations{Hierarchy: s}
}

// CallableSignature returns the effective __invoke contract for an object.
// It follows traits and parents while preserving generic class arguments.
func (s *Snapshot) CallableSignature(
	candidate types.Type,
) (types.Type, bool) {
	return s.callableSignature(candidate, make(map[string]struct{}))
}

func (s *Snapshot) callableSignature(
	candidate types.Type,
	visited map[string]struct{},
) (types.Type, bool) {
	if candidate.Kind() != types.ObjectKind || candidate.Name() == "" {
		return types.Unknown(), false
	}
	if _, exists := visited[candidate.Key()]; exists {
		return types.Unknown(), false
	}
	visited[candidate.Key()] = struct{}{}

	result := types.Unknown()
	found := false
	s.VisitClassViews(candidate.Name(), func(classView SymbolView) bool {
		class := classView.Materialize()
		templates := classTemplateBindings(class, candidate)
		s.VisitMemberViews(
			class.ID,
			"__invoke",
			func(memberView SymbolView) bool {
				member := memberView.Materialize()
				if member.Kind != MethodSymbol {
					return true
				}
				parameters := make(
					[]types.CallableParameter,
					len(member.Parameters),
				)
				for index, parameter := range member.Parameters {
					parameters[index] = types.CallableParameter{
						Name:        parameter.Name,
						Type:        types.Substitute(parameter.Type, templates),
						Optional:    parameter.Optional,
						Variadic:    parameter.Flags.Has(VariadicFlag),
						ByReference: parameter.Flags.Has(ByReferenceFlag),
					}
				}
				returnType := types.Substitute(member.ReturnType, templates)
				result = types.Callable(parameters, returnType)
				found = true
				return false
			},
		)
		if found {
			return false
		}
		for _, trait := range class.Traits {
			if signature, ok := s.callableSignature(
				types.Named(trait),
				visited,
			); ok {
				result, found = signature, true
				return false
			}
		}
		for _, parent := range classParentTypes(class) {
			parent = types.Substitute(parent, templates)
			if signature, ok := s.callableSignature(parent, visited); ok {
				result, found = signature, true
				return false
			}
		}
		return true
	})
	return result, found
}

// ResolveTypeAlias expands a nominal PHPDoc alias through the declaring
// class's synthetic alias member.
func (s *Snapshot) ResolveTypeAlias(value types.Type) (types.Type, bool) {
	className, alias, ok := types.PHPDocAliasParts(value)
	if !ok {
		return types.Unknown(), false
	}
	resolved := types.Unknown()
	found := false
	s.VisitClassViews(className, func(classView SymbolView) bool {
		s.VisitMemberViews(
			classView.ID(),
			alias,
			func(aliasView SymbolView) bool {
				if aliasView.Kind() != TypeAliasSymbol {
					return true
				}
				resolved = aliasView.Materialize().Type
				found = !resolved.IsUnknown()
				return !found
			},
		)
		return !found
	})
	return resolved, found
}

func (s *Snapshot) TemplateVariance(name string, index int) types.Variance {
	result := types.Invariant
	s.VisitClassViews(name, func(classView SymbolView) bool {
		class := classView.Materialize()
		if index < 0 || index >= len(class.Templates) {
			return true
		}
		template := class.Templates[index]
		switch {
		case template.Covariant:
			result = types.Covariant
		case template.Contravariant:
			result = types.Contravariant
		}
		return false
	})
	return result
}

func (s *Snapshot) AsSupertype(
	candidate types.Type,
	target string,
) (types.Type, bool) {
	if candidate.Kind() != types.ObjectKind || candidate.Name() == "" {
		return types.Unknown(), false
	}
	return s.asSupertype(candidate, target, make(map[string]struct{}))
}

func (s *Snapshot) asSupertype(
	candidate types.Type,
	target string,
	visited map[string]struct{},
) (types.Type, bool) {
	normalizedTarget := s.lowerName(target, false)
	if s.lowerName(candidate.Name(), false) == normalizedTarget {
		return candidate, true
	}
	if _, exists := visited[candidate.Key()]; exists {
		return types.Unknown(), false
	}
	visited[candidate.Key()] = struct{}{}
	result := types.Unknown()
	found := false
	s.VisitClassViews(candidate.Name(), func(classView SymbolView) bool {
		class := classView.Materialize()
		templates := classTemplateBindings(class, candidate)
		for _, parent := range classParentTypes(class) {
			parent = types.Substitute(parent, templates)
			parent = inheritExplicitArguments(class, candidate, parent)
			if s.lowerName(parent.Name(), false) == normalizedTarget {
				result = parent
				found = true
				return false
			}
			if projected, ok := s.asSupertype(parent, target, visited); ok {
				result = projected
				found = true
				return false
			}
		}
		return true
	})
	return result, found
}

func classTemplateBindings(
	class Symbol,
	candidate types.Type,
) map[string]types.Type {
	if len(class.Templates) == 0 {
		return nil
	}
	templates := make(map[string]types.Type, len(class.Templates))
	for index, template := range class.Templates {
		switch {
		case index < candidate.ArgumentCount():
			templates[template.Name] = candidate.Argument(index)
		case !template.Default.IsUnknown():
			templates[template.Name] = template.Default
		}
	}
	return templates
}

// inheritExplicitArguments preserves a PHPDoc specialization across
// non-template bridge classes. Projects commonly annotate concrete collection
// subclasses as CollectionSubclass<Element> even when the subclass itself does
// not redeclare the template inherited from a generic ancestor.
func inheritExplicitArguments(
	class Symbol,
	candidate types.Type,
	parent types.Type,
) types.Type {
	if len(class.Templates) != 0 || candidate.ArgumentCount() == 0 ||
		parent.Kind() != types.ObjectKind || parent.Name() == "" {
		return parent
	}

	arguments := parent.Arguments()
	if len(arguments) < candidate.ArgumentCount() {
		arguments = append(
			arguments,
			make([]types.Type, candidate.ArgumentCount()-len(arguments))...,
		)
	}
	for index, argument := range candidate.Arguments() {
		arguments[index] = argument
	}
	return types.Named(parent.Name(), arguments...)
}

func classParentTypes(class Symbol) []types.Type {
	declared := append(
		append([]types.Type(nil), class.ExtendsTypes...),
		class.ImplementsTypes...,
	)
	names := append(append([]string(nil), class.Extends...), class.Implements...)
	result := append([]types.Type(nil), declared...)
	for _, name := range names {
		found := false
		for _, value := range declared {
			if strings.EqualFold(value.Name(), name) {
				found = true
				break
			}
		}
		if !found {
			result = append(result, types.Named(name))
		}
	}
	return result
}

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
	for id, index := range s.expanded {
		if uint64(index) < uint64(len(s.expandedData)) {
			result[id] = s.expandedData[index]
		}
	}
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
		expanded:         make(map[SymbolID]uint32, len(document.Symbols)),
		expandedData:     document.Symbols,
		dynamicNames:     s.dynamicNames,
		reverseHierarchy: &reverseHierarchyIndex{},
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

func cloneReferences(references []Reference) []Reference {
	result := slices.Clone(references)
	for index := range result {
		if result[index].targets == nil {
			continue
		}
		result[index].targets = &referenceTargets{
			qualified: slices.Clone(
				result[index].targets.qualified,
			),
			candidates: slices.Clone(
				result[index].targets.candidates,
			),
		}
	}
	return result
}

func (s *Snapshot) visitPathReferences(
	visitor func(path string, document *workspaceDocument),
) {
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
	normalized := strings.ToLower(name)
	actual, _ := s.dynamicNames.LoadOrStore(name, normalized)
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

func (s *Snapshot) referenceTargetsPacked(
	document *workspaceDocument,
	reference *workspaceReference,
) []SymbolID {
	if s == nil || document == nil || reference == nil {
		return nil
	}
	name := document.referenceString(reference.nameIndex())
	resolvedID := SymbolID(
		document.referenceString(reference.resolvedIndex()),
	)
	valueStart := int(reference.valueStart(document))
	qualifiedEnd := valueStart + int(reference.qualifiedCount())
	var candidates []SymbolID
	switch reference.kind() {
	case ClassName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			candidates = append(
				candidates,
				s.classIDs(document.referenceValue(valueIndex))...,
			)
		}
	case FunctionName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			normalized := s.lowerName(
				document.referenceValue(valueIndex),
				false,
			)
			resolved := s.namedIDs(normalized, functionNameIndex)
			candidates = append(candidates, resolved...)
			if len(resolved) > 0 {
				break
			}
		}
	case ConstantName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			normalized := strings.TrimPrefix(
				document.referenceValue(valueIndex),
				"\\",
			)
			resolved := s.namedIDs(normalized, constantNameIndex)
			candidates = append(candidates, resolved...)
			if len(resolved) > 0 {
				break
			}
		}
	case MemberName:
		return s.memberReferenceTargets(
			document.referenceType(reference.receiverIndex()),
			name,
			reference.targetKind(),
		)
	default:
		if resolvedID != "" {
			if _, exists := s.Symbol(resolvedID); exists {
				return []SymbolID{resolvedID}
			}
		}
	}
	if len(candidates) == 0 && resolvedID != "" {
		if _, exists := s.Symbol(resolvedID); exists {
			candidates = append(candidates, resolvedID)
		}
	}
	if len(candidates) == 0 {
		candidateEnd := qualifiedEnd + int(reference.candidateCount())
		for valueIndex := qualifiedEnd; valueIndex < candidateEnd; valueIndex++ {
			candidate := SymbolID(document.referenceValue(valueIndex))
			if _, exists := s.Symbol(candidate); exists {
				candidates = append(candidates, candidate)
			}
		}
	}
	return uniqueReferenceTargets(candidates)
}

func uniqueReferenceTargets(candidates []SymbolID) []SymbolID {
	if len(candidates) < 2 {
		return candidates
	}
	result := make([]SymbolID, 0, len(candidates))
	seen := make(map[SymbolID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func (s *Snapshot) memberReferenceTargets(
	receiver types.Type,
	name string,
	targetKind SymbolKind,
) []SymbolID {
	var result []SymbolID
	seen := make(map[SymbolID]struct{})
	visited := make(map[SymbolID]struct{})
	var resolveType func(types.Type)
	resolveType = func(value types.Type) {
		switch value.Kind() {
		case types.UnionKind, types.IntersectionKind:
			for index := range value.ArgumentCount() {
				resolveType(value.Argument(index))
			}
		case types.ObjectKind:
			for _, classID := range s.classIDs(value.Name()) {
				class, exists := s.SymbolView(classID)
				if !exists {
					continue
				}
				s.collectMemberTargets(
					class,
					name,
					targetKind,
					seen,
					visited,
					&result,
				)
			}
		}
	}
	resolveType(receiver)
	return result
}

func (s *Snapshot) collectMemberTargets(
	class SymbolView,
	name string,
	targetKind SymbolKind,
	seen,
	visited map[SymbolID]struct{},
	result *[]SymbolID,
) {
	classID := class.ID()
	if _, exists := visited[classID]; exists {
		return
	}
	visited[classID] = struct{}{}
	for _, memberID := range s.memberIDs(classID, name) {
		member, exists := s.SymbolView(memberID)
		if !exists {
			continue
		}
		matches := member.Kind() == targetKind
		if targetKind == ClassConstantSymbol && member.Kind() == EnumCaseSymbol {
			matches = true
		}
		if !matches {
			continue
		}
		if _, exists := seen[member.ID()]; exists {
			continue
		}
		seen[member.ID()] = struct{}{}
		*result = append(*result, member.ID())
	}
	collectRelated := func(related []string) {
		for _, relatedName := range related {
			for _, parentID := range s.classIDs(relatedName) {
				parent, exists := s.SymbolView(parentID)
				if !exists {
					continue
				}
				s.collectMemberTargets(parent, name, targetKind, seen, visited, result)
			}
		}
	}
	traits, extends, implements := class.hierarchyNames()
	collectRelated(traits)
	collectRelated(extends)
	collectRelated(implements)
}
