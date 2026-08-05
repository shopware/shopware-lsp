package semantic

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceSymbolIndexPreservesLastDeclarationPrecedence(
	t *testing.T,
) {
	t.Parallel()

	first := &workspaceSymbol{ID: "duplicate"}
	second := &workspaceSymbol{ID: "duplicate"}
	index := newWorkspaceSymbolIndex(2)
	index.Set(first)
	index.Set(second)

	actual, found := index.Get("duplicate")
	require.True(t, found)
	require.Same(t, second, actual)
	require.Equal(t, 1, index.Len())
}

func TestWorkspaceSymbolIndexGrowsAndVisitsEveryUniqueSymbol(t *testing.T) {
	t.Parallel()

	const count = 10_000
	symbols := make([]workspaceSymbol, count)
	index := newWorkspaceSymbolIndex(1)
	for symbolIndex := range symbols {
		symbols[symbolIndex].ID = SymbolID(
			"symbol:" + strconv.Itoa(symbolIndex),
		)
		index.Set(&symbols[symbolIndex])
	}

	require.Equal(t, count, index.Len())
	for symbolIndex := range symbols {
		actual, found := index.Get(symbols[symbolIndex].ID)
		require.True(t, found)
		require.Same(t, &symbols[symbolIndex], actual)
	}
	_, found := index.Get("missing")
	require.False(t, found)

	visited := make(map[SymbolID]struct{}, count)
	index.Range(func(id SymbolID, symbol *workspaceSymbol) bool {
		require.Equal(t, id, symbol.ID)
		visited[id] = struct{}{}
		return true
	})
	require.Len(t, visited, count)

	visits := 0
	index.Range(func(SymbolID, *workspaceSymbol) bool {
		visits++
		return false
	})
	require.Equal(t, 1, visits)
}

func TestWorkspaceSymbolIndexCapacityKeepsBoundedLoad(t *testing.T) {
	t.Parallel()

	require.Zero(t, workspaceSymbolIndexCapacity(0))
	for _, count := range []int{1, 6, 7, 100, 10_000} {
		capacity := workspaceSymbolIndexCapacity(count)
		require.Zero(t, capacity&(capacity-1))
		require.LessOrEqual(t, count*4, capacity*3)
		if capacity > 8 {
			require.Greater(t, count*4, capacity/2*3)
		}
	}
}

var (
	benchmarkWorkspaceSymbol      *workspaceSymbol
	benchmarkWorkspaceSymbolFound bool
)

func BenchmarkWorkspaceSymbolIndexLookup(b *testing.B) {
	const count = 16_384
	symbols := make([]workspaceSymbol, count)
	index := newWorkspaceSymbolIndex(count)
	mapIndex := make(map[SymbolID]*workspaceSymbol, count)
	for symbolIndex := range symbols {
		symbol := &symbols[symbolIndex]
		symbol.ID = SymbolID("9:app\\service::method:$parameter-" +
			strconv.Itoa(symbolIndex))
		index.Set(symbol)
		mapIndex[symbol.ID] = symbol
	}
	miss := SymbolID("9:app\\service::method:$missing")

	b.Run("compact_hit", func(b *testing.B) {
		b.ReportAllocs()
		for operation := range b.N {
			hit := symbols[operation&(count-1)].ID
			benchmarkWorkspaceSymbol,
				benchmarkWorkspaceSymbolFound = index.Get(hit)
		}
	})
	b.Run("map_hit", func(b *testing.B) {
		b.ReportAllocs()
		for operation := range b.N {
			hit := symbols[operation&(count-1)].ID
			benchmarkWorkspaceSymbol,
				benchmarkWorkspaceSymbolFound = mapIndex[hit]
		}
	})
	b.Run("compact_miss", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkWorkspaceSymbol,
				benchmarkWorkspaceSymbolFound = index.Get(miss)
		}
	})
	b.Run("map_miss", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkWorkspaceSymbol,
				benchmarkWorkspaceSymbolFound = mapIndex[miss]
		}
	})
}
