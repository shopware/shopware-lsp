package lsp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type testInlayHintProvider struct {
	called bool
}

func (p *testInlayHintProvider) GetInlayHints(
	_ context.Context,
	request *InlayHintRequest,
) ([]protocol.InlayHint, error) {
	p.called = request.Document != nil
	return []protocol.InlayHint{{
		Label: "test",
		Kind:  protocol.InlayHintKindParameter,
	}}, nil
}

func TestServerCollectsInlayHintsForOpenDocument(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	provider := &testInlayHintProvider{}
	server.RegisterInlayHintProvider(provider)
	server.documentManager.OpenDocument(
		"file:///project/services.yaml",
		"services: {}",
		1,
	)
	params := &protocol.InlayHintParams{}
	params.TextDocument.URI = "file:///project/services.yaml"
	params.Range.End = protocol.Position{Line: 1}
	hints, err := server.inlayHints(context.Background(), params)
	require.NoError(t, err)
	require.True(t, provider.called)
	require.Len(t, hints, 1)
	require.Equal(t, "test", hints[0].Label)
}
