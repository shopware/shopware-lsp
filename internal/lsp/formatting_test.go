package lsp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

type testDocumentFormattingProvider struct {
	formatted string
	handled   bool
}

func TestFormattingProtocolRoute(t *testing.T) {
	t.Parallel()
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/example.html.twig"
	server.documentManager.OpenDocument(uri, "unformatted", 1)
	server.RegisterDocumentFormattingProvider(testDocumentFormattingProvider{
		formatted: "formatted", handled: true,
	})
	params, err := json.Marshal(protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Options: protocol.FormattingOptions{
			TabSize: 4, InsertSpaces: true,
		},
	})
	require.NoError(t, err)
	raw := json.RawMessage(params)

	result, err := server.handle(context.Background(), nil, &jsonrpc2.Request{
		Method: "textDocument/formatting",
		Params: &raw,
	})
	require.NoError(t, err)
	edits, ok := result.([]protocol.TextEdit)
	require.True(t, ok)
	require.Len(t, edits, 1)
	require.Equal(t, "formatted", edits[0].NewText)
}

func (provider testDocumentFormattingProvider) FormatDocument(
	_ context.Context,
	_ *DocumentFormattingRequest,
) (string, bool, error) {
	return provider.formatted, provider.handled, nil
}

func TestFormattingUsesCurrentDocumentAndReturnsOneFullUTF16Edit(t *testing.T) {
	t.Parallel()
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/example.html.twig"
	server.documentManager.OpenDocument(uri, "😀\nunformatted", 7)
	server.RegisterDocumentFormattingProvider(testDocumentFormattingProvider{
		formatted: "formatted", handled: true,
	})

	edits, err := server.formatting(context.Background(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Options: protocol.FormattingOptions{
			TabSize: 4, InsertSpaces: true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   protocol.Position{Line: 1, Character: 11},
		},
		NewText: "formatted",
	}}, edits)
}

func TestFormattingCapabilityRequiresProvider(t *testing.T) {
	t.Parallel()
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	_, found := server.serverCapabilities()["documentFormattingProvider"]
	require.False(t, found)

	server.RegisterDocumentFormattingProvider(testDocumentFormattingProvider{})
	require.Equal(t, true, server.serverCapabilities()["documentFormattingProvider"])

	server.configurationMu.Lock()
	server.effectiveConfiguration.Features["formatting"] = false
	server.configurationMu.Unlock()
	_, found = server.serverCapabilities()["documentFormattingProvider"]
	require.False(t, found)
}
