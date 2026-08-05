package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testDocumentHighlightProvider struct {
	highlights []protocol.DocumentHighlight
}

func (p testDocumentHighlightProvider) GetDocumentHighlights(
	_ context.Context,
	_ *DocumentHighlightRequest,
) ([]protocol.DocumentHighlight, error) {
	return p.highlights, nil
}

func TestDocumentHighlightsAggregateSortAndPreferSpecificKind(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.js"
	server.documentManager.OpenDocument(uri, "first\nsecond\n", 1)
	firstRange := protocol.Range{
		Start: protocol.Position{Line: 0},
		End:   protocol.Position{Line: 0, Character: 5},
	}
	secondRange := protocol.Range{
		Start: protocol.Position{Line: 1},
		End:   protocol.Position{Line: 1, Character: 6},
	}
	server.RegisterDocumentHighlightProvider(testDocumentHighlightProvider{
		highlights: []protocol.DocumentHighlight{
			{Range: secondRange, Kind: protocol.DocumentHighlightRead},
			{Range: firstRange, Kind: protocol.DocumentHighlightText},
		},
	})
	server.RegisterDocumentHighlightProvider(testDocumentHighlightProvider{
		highlights: []protocol.DocumentHighlight{
			{Range: firstRange, Kind: protocol.DocumentHighlightWrite},
		},
	})

	highlights, err := server.documentHighlights(
		context.Background(),
		&protocol.DocumentHighlightParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{},
		},
	)
	require.NoError(t, err)
	require.Len(t, highlights, 2)
	assert.Equal(t, firstRange, highlights[0].Range)
	assert.Equal(t, protocol.DocumentHighlightWrite, highlights[0].Kind)
	assert.Equal(t, secondRange, highlights[1].Range)
}
