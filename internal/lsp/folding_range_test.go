package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

type testFoldingRangeProvider struct {
	ranges []protocol.FoldingRange
}

func (p testFoldingRangeProvider) GetFoldingRanges(
	_ context.Context,
	_ *FoldingRangeRequest,
) ([]protocol.FoldingRange, error) {
	return p.ranges, nil
}

func TestFoldingRangesAggregateDeduplicateValidateAndSort(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.ts"
	server.documentManager.OpenDocument(uri, "first\nsecond\nthird\n", 1)
	outer := protocol.FoldingRange{StartLine: 0, EndLine: 2}
	inner := protocol.FoldingRange{StartLine: 0, EndLine: 1}
	server.RegisterFoldingRangeProvider(testFoldingRangeProvider{
		ranges: []protocol.FoldingRange{
			inner, {StartLine: 1, EndLine: 1}, outer,
		},
	})
	server.RegisterFoldingRangeProvider(testFoldingRangeProvider{
		ranges: []protocol.FoldingRange{outer},
	})

	ranges, err := server.foldingRanges(
		context.Background(),
		&protocol.FoldingRangeParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []protocol.FoldingRange{outer, inner}, ranges)
}
