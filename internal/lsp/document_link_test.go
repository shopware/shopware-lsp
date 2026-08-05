package lsp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type testDocumentLinkProvider struct {
	called bool
}

func (p *testDocumentLinkProvider) GetDocumentLinks(
	_ context.Context,
	request *DocumentLinkRequest,
) ([]protocol.DocumentLink, error) {
	p.called = request.Document != nil
	return []protocol.DocumentLink{{
		Target:  "file:///project/target.yaml",
		Tooltip: "Open target",
	}}, nil
}

func TestServerCollectsDocumentLinksForOpenDocument(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	provider := &testDocumentLinkProvider{}
	server.RegisterDocumentLinkProvider(provider)
	server.documentManager.OpenDocument(
		"file:///project/config.yaml",
		"imports: []",
		1,
	)
	params := &protocol.DocumentLinkParams{}
	params.TextDocument.URI = "file:///project/config.yaml"

	links, err := server.documentLinks(context.Background(), params)
	require.NoError(t, err)
	require.True(t, provider.called)
	require.Len(t, links, 1)
	require.Equal(t, "file:///project/target.yaml", links[0].Target)
}
