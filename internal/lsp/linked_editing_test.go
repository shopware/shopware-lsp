package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

type testLinkedEditingRangeProvider struct {
	result   *protocol.LinkedEditingRanges
	request  **LinkedEditingRangeRequest
	response error
}

func (p testLinkedEditingRangeProvider) GetLinkedEditingRanges(
	_ context.Context,
	request *LinkedEditingRangeRequest,
) (*protocol.LinkedEditingRanges, error) {
	if p.request != nil {
		*p.request = request
	}
	return p.result, p.response
}

func TestLinkedEditingRangesUseFirstCompleteProviderResult(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.html.twig"
	server.documentManager.OpenDocument(uri, `<sw-card></sw-card>`, 3)
	var received *LinkedEditingRangeRequest
	server.RegisterLinkedEditingRangeProvider(testLinkedEditingRangeProvider{
		result: &protocol.LinkedEditingRanges{Ranges: []protocol.Range{{}}},
	})
	expected := &protocol.LinkedEditingRanges{
		Ranges: []protocol.Range{
			{Start: protocol.Position{Character: 1}, End: protocol.Position{Character: 8}},
			{Start: protocol.Position{Character: 11}, End: protocol.Position{Character: 18}},
		},
		WordPattern: "[-A-Za-z0-9_]+",
	}
	server.RegisterLinkedEditingRangeProvider(testLinkedEditingRangeProvider{
		result: expected, request: &received,
	})

	result, err := server.linkedEditingRanges(
		context.Background(),
		&protocol.LinkedEditingRangeParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Character: 4},
		},
	)
	require.NoError(t, err)
	require.Equal(t, expected, result)
	require.NotNil(t, received)
	require.Equal(t, 3, received.Document.Version)
	require.NotNil(t, received.Node)
}
