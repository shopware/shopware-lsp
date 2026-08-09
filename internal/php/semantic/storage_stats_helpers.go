package semantic

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func addMemberContainerStorageStats(
	count int,
	stats *WorkspaceStorageStats,
) {
	if stats == nil {
		return
	}
	stats.MaxMembersPerContainer = max(stats.MaxMembersPerContainer, count)
	switch {
	case count == 1:
		stats.MemberContainers1++
	case count <= 4:
		stats.MemberContainers2To4++
	case count <= 8:
		stats.MemberContainers5To8++
	case count <= 16:
		stats.MemberContainers9To16++
	case count <= 32:
		stats.MemberContainers17To32++
	default:
		stats.MemberContainersOver32++
	}
}

func addWorkspaceSymbolRangeStats(
	symbol *workspaceSymbol,
	stats *WorkspaceStorageStats,
) {
	if symbol == nil || stats == nil {
		return
	}
	ranges := symbol.ranges()
	rng := ranges.Range
	selection := ranges.SelectionRange
	body := ranges.BodyRange
	rangeLength, rangeOK := workspaceSymbolRangeLength(rng)
	stats.MaxSymbolRangeLength = max(
		stats.MaxSymbolRangeLength,
		rangeLength,
	)
	selectionOK := true
	if selection == (cst.TextRange{}) {
		stats.SymbolMissingSelections++
	} else {
		offset, length, ok := workspaceSymbolSubrangeStats(rng.Start, selection)
		stats.MaxSymbolSelectionOffset = max(
			stats.MaxSymbolSelectionOffset,
			offset,
		)
		stats.MaxSymbolSelectionLength = max(
			stats.MaxSymbolSelectionLength,
			length,
		)
		selectionOK = ok
	}
	bodyOK := true
	if body == (cst.TextRange{}) {
		stats.SymbolMissingBodies++
	} else {
		offset, length, ok := workspaceSymbolSubrangeStats(rng.Start, body)
		stats.MaxSymbolBodyOffset = max(stats.MaxSymbolBodyOffset, offset)
		stats.MaxSymbolBodyLength = max(stats.MaxSymbolBodyLength, length)
		bodyOK = ok
	}
	if rangeOK && selectionOK && bodyOK {
		stats.SymbolCompactRanges++
	} else {
		stats.SymbolFullRanges++
	}
}

func workspaceSymbolRangeLength(rng cst.TextRange) (int, bool) {
	if rng.End < rng.Start {
		return 0, false
	}
	length := rng.End - rng.Start
	return int(length), length < workspaceCompactRangeMissing
}

func workspaceSymbolSubrangeStats(
	base uint32,
	rng cst.TextRange,
) (offset, length int, compact bool) {
	if rng.Start < base || rng.End < rng.Start {
		return 0, 0, false
	}
	rawOffset := rng.Start - base
	rawLength := rng.End - rng.Start
	return int(rawOffset),
		int(rawLength),
		rawOffset < workspaceCompactRangeMissing &&
			rawLength < workspaceCompactRangeMissing
}

func addHierarchyPairStats(
	names []string,
	typeValues []types.Type,
	stats *WorkspaceStorageStats,
) {
	if stats == nil {
		return
	}
	paired := min(len(names), len(typeValues))
	stats.HierarchyPairedNameTypes += paired
	for index := range paired {
		if names[index] == typeValues[index].Name() {
			stats.HierarchyExactNamedTypePairs++
		}
	}
}

func addStorageString(
	unique map[string]struct{},
	value string,
	stats *WorkspaceStorageStats,
) {
	if value == "" {
		return
	}
	stats.SymbolStringSlots++
	if _, exists := unique[value]; exists {
		return
	}
	unique[value] = struct{}{}
	stats.UniqueSymbolStringBytes += len(value)
}

func addInternedStorageString(
	unique map[string]struct{},
	value string,
	stats *WorkspaceStorageStats,
) {
	if value == "" {
		return
	}
	stats.InternedStringSlots++
	if _, exists := unique[value]; exists {
		return
	}
	unique[value] = struct{}{}
	stats.UniqueInternedBytes += len(value)
}

func addStorageType(
	unique map[string]struct{},
	value types.Type,
	stats *WorkspaceStorageStats,
) {
	stats.TypeSlots++
	key := value.Key()
	if _, exists := unique[key]; exists {
		return
	}
	unique[key] = struct{}{}
	stats.UniqueTypeBytes += len(key)
}
