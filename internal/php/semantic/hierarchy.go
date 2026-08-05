package semantic

import (
	"slices"
	"strings"
	"sync"
)

// reverseHierarchyIndex is derived lazily from the immutable declaration
// graph. Type edges and method-implementation edges use separate guards so a
// type-hierarchy query does not pay to inspect every workspace method.
type reverseHierarchyIndex struct {
	typesOnce   sync.Once
	methodsOnce sync.Once

	subtypes        map[string][]SymbolID
	traitConsumers  map[string][]SymbolID
	classAliases    map[string][]SymbolID
	methodOverrides map[SymbolID][]SymbolID
}

// DirectSubtypes returns classes and interfaces that directly extend or
// implement name. Transitive hierarchy traversal intentionally remains a query
// concern so the snapshot does not retain a closure of every relationship.
func (s *Snapshot) DirectSubtypes(name string) []Symbol {
	if s == nil || name == "" {
		return nil
	}
	s.ensureTypeHierarchy()
	if s.reverseHierarchy == nil {
		return nil
	}
	return s.lookup(s.reverseHierarchy.subtypes[s.lowerName(name, false)])
}

// TraitConsumers returns classes that directly use the named trait.
func (s *Snapshot) TraitConsumers(name string) []Symbol {
	if s == nil || name == "" {
		return nil
	}
	s.ensureTypeHierarchy()
	if s.reverseHierarchy == nil {
		return nil
	}
	return s.lookup(s.reverseHierarchy.traitConsumers[s.lowerName(name, false)])
}

// ClassAliases returns synthetic class_alias declarations targeting name.
func (s *Snapshot) ClassAliases(name string) []Symbol {
	if s == nil || name == "" {
		return nil
	}
	s.ensureTypeHierarchy()
	if s.reverseHierarchy == nil {
		return nil
	}
	return s.lookup(s.reverseHierarchy.classAliases[s.lowerName(name, false)])
}

// ClassAliasTarget resolves the target relationship retained by a synthetic
// class_alias declaration.
func (s *Snapshot) ClassAliasTarget(id SymbolID) (Symbol, bool) {
	view, found := s.SymbolView(id)
	if !found || !view.Flags().Has(ClassAliasFlag) {
		return Symbol{}, false
	}
	_, extends, _ := view.HierarchyNames()
	if len(extends) != 1 {
		return Symbol{}, false
	}
	classes := s.Classes(extends[0])
	if len(classes) == 0 {
		return Symbol{}, false
	}
	return classes[0], true
}

// MethodOverrides returns workspace methods that implement or override the
// selected method declaration. A method is related to every matching ancestor
// declaration, including through an intermediate class that does not redeclare
// it; this is a declaration edge, not a materialized type-hierarchy closure.
func (s *Snapshot) MethodOverrides(id SymbolID) []Symbol {
	if s == nil || id == "" {
		return nil
	}
	s.ensureMethodHierarchy()
	if s.reverseHierarchy == nil {
		return nil
	}
	return s.lookup(s.reverseHierarchy.methodOverrides[id])
}

// DirectSupertypes returns the declarations named directly by a class or
// interface's extends and implements clauses. Trait use is exposed separately
// because it is composition rather than nominal subtyping.
func (s *Snapshot) DirectSupertypes(id SymbolID) []Symbol {
	view, found := s.SymbolView(id)
	if !found || !isClassLikeKind(view.Kind()) {
		return nil
	}
	_, extends, implements := view.HierarchyNames()
	names := make([]string, 0, len(extends)+len(implements))
	names = append(names, extends...)
	names = append(names, implements...)
	return s.classesNamed(names)
}

func (s *Snapshot) ensureTypeHierarchy() {
	if s == nil {
		return
	}
	if s.reverseHierarchy == nil {
		return
	}
	index := s.reverseHierarchy
	index.typesOnce.Do(func() {
		index.subtypes = make(map[string][]SymbolID)
		index.traitConsumers = make(map[string][]SymbolID)
		index.classAliases = make(map[string][]SymbolID)
		for _, class := range s.GlobalClassViews() {
			traits, extends, implements := class.HierarchyNames()
			if class.Flags().Has(ClassAliasFlag) && len(extends) == 1 {
				key := s.lowerName(extends[0], false)
				index.classAliases[key] = appendUniqueSymbolID(
					index.classAliases[key],
					class.ID(),
				)
				continue
			}
			for _, parent := range append(append([]string(nil), extends...), implements...) {
				key := s.lowerName(parent, false)
				index.subtypes[key] = appendUniqueSymbolID(index.subtypes[key], class.ID())
			}
			for _, trait := range traits {
				key := s.lowerName(trait, false)
				index.traitConsumers[key] = appendUniqueSymbolID(
					index.traitConsumers[key],
					class.ID(),
				)
			}
		}
		for key := range index.subtypes {
			s.sortHierarchyIDs(index.subtypes[key])
		}
		for key := range index.traitConsumers {
			s.sortHierarchyIDs(index.traitConsumers[key])
		}
		for key := range index.classAliases {
			s.sortHierarchyIDs(index.classAliases[key])
		}
	})
}

func (s *Snapshot) ensureMethodHierarchy() {
	if s == nil {
		return
	}
	if s.reverseHierarchy == nil {
		return
	}
	index := s.reverseHierarchy
	index.methodsOnce.Do(func() {
		index.methodOverrides = make(map[SymbolID][]SymbolID)
		for _, class := range s.GlobalClassViews() {
			for _, member := range s.MemberViewsOf(class.ID()) {
				if member.Kind() != MethodSymbol || member.Visibility() == Private {
					continue
				}
				s.indexAncestorMethodOverrides(index, class, member)
			}
		}
		for id := range index.methodOverrides {
			s.sortHierarchyIDs(index.methodOverrides[id])
		}
	})
}

func (s *Snapshot) indexAncestorMethodOverrides(
	index *reverseHierarchyIndex,
	class SymbolView,
	method SymbolView,
) {
	visited := make(map[string]struct{})
	var visitClass func(string)
	visitClass = func(name string) {
		key := s.lowerName(name, false)
		if key == "" {
			return
		}
		if _, exists := visited[key]; exists {
			return
		}
		visited[key] = struct{}{}
		s.VisitClassViews(name, func(parent SymbolView) bool {
			s.VisitMemberViews(parent.ID(), method.Name(), func(candidate SymbolView) bool {
				if candidate.Kind() == MethodSymbol && candidate.Visibility() != Private {
					index.methodOverrides[candidate.ID()] = appendUniqueSymbolID(
						index.methodOverrides[candidate.ID()],
						method.ID(),
					)
				}
				return true
			})
			traits, extends, implements := parent.HierarchyNames()
			for _, ancestor := range traits {
				visitClass(ancestor)
			}
			for _, ancestor := range extends {
				visitClass(ancestor)
			}
			for _, ancestor := range implements {
				visitClass(ancestor)
			}
			return true
		})
	}
	traits, extends, implements := class.HierarchyNames()
	for _, parent := range traits {
		visitClass(parent)
	}
	for _, parent := range extends {
		visitClass(parent)
	}
	for _, parent := range implements {
		visitClass(parent)
	}
}

func (s *Snapshot) classesNamed(names []string) []Symbol {
	var result []Symbol
	seen := make(map[SymbolID]struct{})
	for _, name := range names {
		s.VisitClassViews(name, func(view SymbolView) bool {
			if _, exists := seen[view.ID()]; exists {
				return true
			}
			seen[view.ID()] = struct{}{}
			result = append(result, view.Materialize())
			return true
		})
	}
	slices.SortFunc(result, compareHierarchySymbols)
	return result
}

func (s *Snapshot) sortHierarchyIDs(ids []SymbolID) {
	slices.SortFunc(ids, func(left, right SymbolID) int {
		leftSymbol, leftFound := s.SymbolView(left)
		rightSymbol, rightFound := s.SymbolView(right)
		if !leftFound || !rightFound {
			return strings.Compare(string(left), string(right))
		}
		return compareHierarchyViews(leftSymbol, rightSymbol)
	})
}

func compareHierarchySymbols(left, right Symbol) int {
	if compared := strings.Compare(left.Path, right.Path); compared != 0 {
		return compared
	}
	if left.SelectionRange.Start < right.SelectionRange.Start {
		return -1
	}
	if left.SelectionRange.Start > right.SelectionRange.Start {
		return 1
	}
	return strings.Compare(left.FullyQualified, right.FullyQualified)
}

func compareHierarchyViews(left, right SymbolView) int {
	if compared := strings.Compare(left.Path(), right.Path()); compared != 0 {
		return compared
	}
	if left.Range().Start < right.Range().Start {
		return -1
	}
	if left.Range().Start > right.Range().Start {
		return 1
	}
	return strings.Compare(left.FullyQualified(), right.FullyQualified())
}

func isClassLikeKind(kind SymbolKind) bool {
	switch kind {
	case ClassSymbol, InterfaceSymbol, TraitSymbol, EnumSymbol:
		return true
	default:
		return false
	}
}
