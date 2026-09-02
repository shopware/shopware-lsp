package semantic

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

type packedReferenceTargetFilter struct {
	id                 string
	name               string
	fullyQualified     string
	caseInsensitive    bool
	trimVariablePrefix bool
	bloomMasks         [2][2]uint64
	bloomCount         uint8
}

func newPackedReferenceTargetFilter(
	target SymbolView,
) packedReferenceTargetFilter {
	kind := target.Kind()
	filter := packedReferenceTargetFilter{
		id:                 string(target.ID()),
		name:               strings.TrimPrefix(target.Name(), "$"),
		fullyQualified:     strings.TrimPrefix(target.FullyQualified(), "\\"),
		caseInsensitive:    isClassLikeKind(kind) || kind == FunctionSymbol || isMemberSymbol(kind),
		trimVariablePrefix: isMemberSymbol(kind) || kind == LocalSymbol || kind == ParameterSymbol,
	}
	filter.addBloomValue(filter.id)
	switch {
	case isClassLikeKind(kind), kind == FunctionSymbol,
		kind == GlobalConstantSymbol:
		filter.addBloomValue(filter.fullyQualified)
		if filter.fullyQualified == "" {
			filter.addBloomValue(filter.name)
		}
	case isMemberSymbol(kind):
		filter.addBloomValue(filter.name)
	}
	return filter
}

func (filter *packedReferenceTargetFilter) addBloomValue(value string) {
	hash := referenceBloomHash(value)
	if filter == nil || hash == 0 {
		return
	}
	first := uint64(1) << (hash & 63)
	second := uint64(1) << ((hash >> 17) & 63)
	for index := range filter.bloomCount {
		if filter.bloomMasks[index][0] == first &&
			filter.bloomMasks[index][1] == second {
			return
		}
	}
	index := filter.bloomCount
	filter.bloomMasks[index] = [2]uint64{first, second}
	filter.bloomCount++
}

// matchesDocument uses a derived Bloom filter to reject documents which
// cannot reference the target. Every packed reference contributes its name,
// resolved ID, qualified names, and fallback candidate IDs, so Bloom misses
// are definitive. False positives are handled by the full resolver below.
func (filter packedReferenceTargetFilter) matchesDocument(
	document *workspaceDocument,
) bool {
	if document == nil || len(document.References) == 0 {
		return false
	}
	if document.referenceBloom != [2]uint64{} {
		for index := range filter.bloomCount {
			mask := filter.bloomMasks[index]
			if document.referenceBloom[0]&mask[0] != 0 &&
				document.referenceBloom[1]&mask[1] != 0 {
				return true
			}
		}
		return false
	}
	// Preserve correctness for a hand-built or legacy packed document whose
	// derived filter has not been initialized.
	for stringIndex := 1; stringIndex <= document.referenceStringCount(); stringIndex++ {
		value := document.referenceString(uint32(stringIndex))
		if value == filter.id {
			return true
		}
		value = strings.TrimPrefix(value, "\\")
		if filter.trimVariablePrefix {
			value = strings.TrimPrefix(value, "$")
		}
		if filter.caseInsensitive {
			if filter.name != "" && strings.EqualFold(value, filter.name) ||
				filter.fullyQualified != "" && strings.EqualFold(
					value, filter.fullyQualified,
				) {
				return true
			}
			continue
		}
		if value == filter.name ||
			filter.fullyQualified != "" && value == filter.fullyQualified {
			return true
		}
	}
	return false
}

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
		return isClassLikeKind(target.Kind()) &&
			packedReferenceMayResolveGlobal(
				document, reference, target, true,
			)
	case FunctionName:
		return target.Kind() == FunctionSymbol &&
			packedReferenceMayResolveGlobal(
				document, reference, target, true,
			)
	case ConstantName:
		return target.Kind() == GlobalConstantSymbol &&
			packedReferenceMayResolveGlobal(
				document, reference, target, false,
			)
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

func packedReferenceMayResolveGlobal(
	document *workspaceDocument,
	reference *workspaceReference,
	target SymbolView,
	caseInsensitive bool,
) bool {
	if packedReferenceRecordsTarget(document, reference, target.ID()) {
		return true
	}
	targetName := strings.TrimPrefix(target.FullyQualified(), "\\")
	if targetName == "" {
		return false
	}
	valueStart := int(reference.valueStart(document))
	qualifiedEnd := valueStart + int(reference.qualifiedCount())
	for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
		candidate := strings.TrimPrefix(
			document.referenceValue(valueIndex), "\\",
		)
		if candidate == targetName ||
			caseInsensitive && strings.EqualFold(candidate, targetName) {
			return true
		}
	}
	return false
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

// packedReferenceTargetResolver reuses traversal state while resolving a
// sequence of packed references. Reverse-index construction and an open
// document reference query both visit many references against one immutable
// snapshot. Inline sets cover ordinary one-target and shallow-hierarchy cases;
// reusable overflow maps retain only the largest individual traversal instead
// of every symbol encountered across the workspace.
type packedReferenceTargetResolver struct {
	snapshot *Snapshot
	seen     inlineSymbolIDSet
	visited  inlineSymbolIDSet
	targets  []SymbolID
}

func (r *packedReferenceTargetResolver) resolve(
	document *workspaceDocument,
	reference *workspaceReference,
) []SymbolID {
	r.reset()
	snapshot := r.snapshot
	if snapshot == nil || document == nil || reference == nil {
		return nil
	}
	name := document.referenceString(reference.nameIndex())
	resolvedID := SymbolID(
		document.referenceString(reference.resolvedIndex()),
	)
	valueStart := int(reference.valueStart(document))
	qualifiedEnd := valueStart + int(reference.qualifiedCount())
	switch reference.kind() {
	case ClassName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			r.appendNamedTargets(
				snapshot.lowerName(
					document.referenceValue(valueIndex),
					false,
				),
				classNameIndex,
			)
		}
	case FunctionName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			normalized := snapshot.lowerName(
				document.referenceValue(valueIndex),
				false,
			)
			if r.appendNamedTargets(normalized, functionNameIndex) {
				break
			}
		}
	case ConstantName:
		for valueIndex := valueStart; valueIndex < qualifiedEnd; valueIndex++ {
			normalized := strings.TrimPrefix(
				document.referenceValue(valueIndex),
				"\\",
			)
			if r.appendNamedTargets(normalized, constantNameIndex) {
				break
			}
		}
	case MemberName:
		r.appendMemberReferenceTargets(
			document.referenceType(reference.receiverIndex()),
			name,
			reference.targetKind(),
		)
		return r.targets
	default:
		if r.appendExistingTarget(resolvedID) {
			return r.targets
		}
	}
	if len(r.targets) == 0 {
		r.appendExistingTarget(resolvedID)
	}
	if len(r.targets) == 0 {
		candidateEnd := qualifiedEnd + int(reference.candidateCount())
		for valueIndex := qualifiedEnd; valueIndex < candidateEnd; valueIndex++ {
			candidate := SymbolID(document.referenceValue(valueIndex))
			r.appendExistingTarget(candidate)
		}
	}
	return r.targets
}

func (r *packedReferenceTargetResolver) reset() {
	r.targets = r.targets[:0]
	r.seen.reset()
	r.visited.reset()
}

func (r *packedReferenceTargetResolver) appendTarget(id SymbolID) bool {
	if id == "" {
		return false
	}
	if !r.seen.add(id) {
		return false
	}
	r.targets = append(r.targets, id)
	return true
}

func (r *packedReferenceTargetResolver) appendExistingTarget(
	id SymbolID,
) bool {
	if id == "" || r.snapshot == nil {
		return false
	}
	if _, exists := r.snapshot.SymbolView(id); !exists {
		return false
	}
	return r.appendTarget(id)
}

func (r *packedReferenceTargetResolver) appendNamedTargets(
	name string,
	kind symbolNameIndexKind,
) bool {
	found := false
	r.snapshot.visitNamedViews(name, kind, func(view SymbolView) bool {
		found = true
		r.appendTarget(view.ID())
		return true
	})
	return found
}

func (r *packedReferenceTargetResolver) appendMemberReferenceTargets(
	receiver types.Type,
	name string,
	targetKind SymbolKind,
) {
	switch receiver.Kind() {
	case types.UnionKind, types.IntersectionKind:
		for index := range receiver.ArgumentCount() {
			r.appendMemberReferenceTargets(
				receiver.Argument(index),
				name,
				targetKind,
			)
		}
	case types.ObjectKind:
		r.collectMemberTargetsForClassName(
			receiver.Name(),
			name,
			targetKind,
		)
	}
}

func (r *packedReferenceTargetResolver) collectMemberTargetsForClassName(
	className,
	memberName string,
	targetKind SymbolKind,
) {
	if className == "" {
		return
	}
	r.snapshot.VisitClassViews(className, func(class SymbolView) bool {
		r.collectMemberTargets(class, memberName, targetKind)
		return true
	})
}

func (r *packedReferenceTargetResolver) collectMemberTargets(
	class SymbolView,
	name string,
	targetKind SymbolKind,
) {
	classID := class.ID()
	if !r.visited.add(classID) {
		return
	}
	r.snapshot.VisitMemberViews(classID, name, func(member SymbolView) bool {
		matches := member.Kind() == targetKind
		if targetKind == ClassConstantSymbol && member.Kind() == EnumCaseSymbol {
			matches = true
		}
		if matches {
			r.appendTarget(member.ID())
		}
		return true
	})
	collectRelated := func(related []string) {
		for _, relatedName := range related {
			r.collectMemberTargetsForClassName(
				relatedName,
				name,
				targetKind,
			)
		}
	}
	traits, extends, implements := class.hierarchyNames()
	collectRelated(traits)
	collectRelated(extends)
	collectRelated(implements)
}
