package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

type testSelectionRangeProvider struct {
	ranges   []protocol.SelectionRange
	received **SelectionRangeRequest
}

func (p testSelectionRangeProvider) GetSelectionRanges(
	_ context.Context,
	request *SelectionRangeRequest,
) ([]protocol.SelectionRange, error) {
	if p.received != nil {
		*p.received = request
	}
	return p.ranges, nil
}

func TestSelectionRangesRequireOneResultPerPosition(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.ts"
	server.documentManager.OpenDocument(uri, "first\nsecond\n", 4)
	positions := []protocol.Position{{}, {Line: 1}}
	server.RegisterSelectionRangeProvider(testSelectionRangeProvider{
		ranges: []protocol.SelectionRange{{Range: protocol.Range{}}},
	})
	expected := []protocol.SelectionRange{
		{Range: protocol.Range{End: protocol.Position{Character: 5}}},
		{Range: protocol.Range{
			Start: protocol.Position{Line: 1},
			End:   protocol.Position{Line: 1, Character: 6},
		}},
	}
	var received *SelectionRangeRequest
	server.RegisterSelectionRangeProvider(testSelectionRangeProvider{
		ranges: expected, received: &received,
	})

	ranges, err := server.selectionRanges(
		context.Background(),
		&protocol.SelectionRangeParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Positions:    positions,
		},
	)
	require.NoError(t, err)
	require.Equal(t, expected, ranges)
	require.NotNil(t, received)
	require.Equal(t, 4, received.Document.Version)
}
