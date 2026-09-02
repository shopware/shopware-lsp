package semantic

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandedSymbolIndexLooksUpAndReplacesDataIndexes(t *testing.T) {
	t.Parallel()

	const symbolCount = 100
	data := make([]Symbol, symbolCount, symbolCount+1)
	index := newExpandedSymbolIndex(symbolCount)
	for dataIndex := range data {
		data[dataIndex].ID = SymbolID("symbol-" + strconv.Itoa(dataIndex))
		index.Set(data[dataIndex].ID, dataIndex, data)
	}

	require.Equal(t, symbolCount, index.Len())
	for dataIndex := range data {
		foundIndex, found := index.Get(data[dataIndex].ID, data)
		require.True(t, found)
		require.Equal(t, uint32(dataIndex), foundIndex)
	}
	_, found := index.Get("missing", data)
	require.False(t, found)

	data = append(data, Symbol{ID: "symbol-10", Name: "replacement"})
	index.Set(data[len(data)-1].ID, len(data)-1, data)
	require.Equal(t, symbolCount, index.Len())
	foundIndex, found := index.Get("symbol-10", data)
	require.True(t, found)
	require.Equal(t, uint32(len(data)-1), foundIndex)

	visited := make(map[SymbolID]uint32, symbolCount)
	index.Range(data, func(id SymbolID, dataIndex uint32) bool {
		visited[id] = dataIndex
		return true
	})
	require.Len(t, visited, symbolCount)
	require.Equal(t, uint32(len(data)-1), visited["symbol-10"])
}
