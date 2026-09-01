package semantic

import (
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

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
	// Analysis normally has one open-document overlay. Keep that path inline so
	// every call-contract lookup does not allocate a map merely to shadow the
	// current document's persisted metadata. A map is only needed for the rare
	// chain containing distinct document overlays.
	shadowedPath := ""
	var shadowedPaths map[string]struct{}
	for current := s; current != nil; current = current.base {
		for _, entry := range entries(current) {
			if entry.path == shadowedPath {
				continue
			}
			if _, shadowed := shadowedPaths[entry.path]; shadowed {
				continue
			}
			if !visit(entry.contract) {
				return false
			}
		}
		if current.overlayPath != "" {
			switch {
			case shadowedPath == "":
				shadowedPath = current.overlayPath
			case shadowedPath == current.overlayPath:
			case shadowedPaths == nil:
				shadowedPaths = map[string]struct{}{
					shadowedPath:        {},
					current.overlayPath: {},
				}
			default:
				shadowedPaths[current.overlayPath] = struct{}{}
			}
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
		target, found := s.SymbolView(id)
		if !found {
			return nil
		}
		var result []ReferenceLocation
		s.visitPathReferences(func(path string, document *workspaceDocument) {
			for index := range document.References {
				reference := &document.References[index]
				if !s.referenceMayTargetPacked(document, reference, target) {
					continue
				}
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
		slices.SortFunc(result, func(left, right ReferenceLocation) int {
			if compared := strings.Compare(left.Path, right.Path); compared != 0 {
				return compared
			}
			if left.RangeStart < right.RangeStart {
				return -1
			}
			if left.RangeStart > right.RangeStart {
				return 1
			}
			if left.RangeEnd < right.RangeEnd {
				return -1
			}
			if left.RangeEnd > right.RangeEnd {
				return 1
			}
			return 0
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
