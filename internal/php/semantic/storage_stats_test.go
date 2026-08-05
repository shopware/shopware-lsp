package semantic

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceStorageStatsCountsRetainedTables(t *testing.T) {
	t.Parallel()

	path := "/service.php"
	document := &Document{
		Path: path,
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           path,
		}},
		References: []Reference{referenceWithTargets(Reference{
			Name:     "run",
			Resolved: "service::run",
			Receiver: types.Named("App\\Service"),
			Kind:     MemberName,
		}, []string{"App\\Service"}, []SymbolID{"service::run"})},
	}
	snapshot := NewSnapshot(1, []*Document{document})

	stats := snapshot.WorkspaceStorageStats()

	require.Equal(t, 1, stats.Documents)
	require.Equal(t, 1, stats.Symbols)
	require.Equal(t, 1, stats.References)
	require.Equal(t, 1, stats.SymbolIndexEntries)
	require.Equal(t, 1, stats.PathIndexEntries)
	require.Equal(t, 1, stats.ClassNames)
	require.Equal(t, 1, stats.ClassIDs)
	require.Equal(t, 1, stats.UniqueClassIDs)
	require.Equal(t, 0, stats.FunctionNames)
	require.Equal(t, 0, stats.FunctionIDs)
	require.Equal(t, 0, stats.UniqueFunctionIDs)
	require.Equal(t, 0, stats.ConstantNames)
	require.Equal(t, 0, stats.ConstantIDs)
	require.Equal(t, 0, stats.UniqueConstantIDs)
	require.Equal(t, 0, stats.MemberContainers)
	require.Equal(t, 0, stats.MemberNames)
	require.Equal(t, 0, stats.MemberIDs)
	require.Equal(t, 0, stats.UniqueMemberIDs)
	require.Equal(t, 0, stats.MemberDuplicateIDs)
	require.Equal(t, 1, stats.GlobalIDs)
	require.Equal(t, 1, stats.UniqueGlobalIDs)
	require.Equal(t, 1, stats.NoSideTables)
	require.Equal(t, 3, stats.ReferenceStrings)
	require.Equal(t, 3, stats.ReferenceStringCapacity)
	require.Equal(t, 1, stats.ReferenceTypes)
	require.Equal(t, 2, stats.ReferenceValues)
	require.Equal(t, 1, stats.MaxQualified)
	require.Equal(t, 1, stats.MaxCandidates)
	require.Equal(t, 1, stats.SymbolsUsingDocumentPath)
	require.Equal(t, 4, stats.SymbolStringSlots)
	require.Equal(t, 1, stats.SymbolStringTables)
	require.Equal(t, 2, stats.SymbolStringTableValues)
	require.Equal(t, 2, stats.SymbolStringTableCapacity)
	require.Equal(t, 7, stats.InternedStringSlots)
	require.Equal(t, 6, stats.UniqueInternedStrings)
	require.Equal(t, 5, stats.TypeSlots)
	require.Equal(t, 2, stats.UniqueTypeKeys)
}
