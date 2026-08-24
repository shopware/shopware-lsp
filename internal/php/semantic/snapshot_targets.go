package semantic

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

// referenceMayTargetPacked cheaply rejects references which cannot resolve to
// target before the more expensive inheritance-aware resolution runs. Open
// document snapshots must resolve retained references against the overlay so
// newly added declarations remain visible, but an inherited method lookup does
// not need to traverse the hierarchy for every unrelated member name in the
// workspace.
func (s *Snapshot) referenceMayTargetPacked(
	document *workspaceDocument,
	reference *workspaceReference,
	target SymbolView,
) bool {
	if s == nil || document == nil || reference == nil {
		return false
	}
	switch reference.kind() {
	case ClassName:
		return isClassLikeKind(target.Kind())
	case FunctionName:
		return target.Kind() == FunctionSymbol
	case ConstantName:
		return target.Kind() == GlobalConstantSymbol
	case MemberName:
		if !memberReferenceKindMatches(reference.targetKind(), target.Kind()) {
			return false
		}
		return s.lowerName(
			document.referenceString(reference.nameIndex()),
			true,
		) == s.lowerName(target.Name(), true)
	case VariableName:
		return packedReferenceRecordsTarget(document, reference, target.ID())
	default:
		return true
	}
}

func memberReferenceKindMatches(referenceKind, targetKind SymbolKind) bool {
	if referenceKind == targetKind {
		return true
	}
	return referenceKind == ClassConstantSymbol && targetKind == EnumCaseSymbol
}

func packedReferenceRecordsTarget(
	document *workspaceDocument,
	reference *workspaceReference,
	target SymbolID,
) bool {
	if target == "" {
		return false
	}
	if SymbolID(document.referenceString(reference.resolvedIndex())) == target {
		return true
	}
	valueStart := int(reference.valueStart(document))
	candidateStart := valueStart + int(reference.qualifiedCount())
	candidateEnd := candidateStart + int(reference.candidateCount())
	for valueIndex := candidateStart; valueIndex < candidateEnd; valueIndex++ {
		if SymbolID(document.referenceValue(valueIndex)) == target {
			return true
		}
	}
	return false
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
