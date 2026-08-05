package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testDocumentSymbolProvider struct {
	symbols []protocol.DocumentSymbol
}

func (p testDocumentSymbolProvider) GetDocumentSymbols(
	_ context.Context,
	_ *DocumentSymbolRequest,
) ([]protocol.DocumentSymbol, error) {
	return p.symbols, nil
}

func TestDocumentSymbolsAggregateSortAndDeduplicateProviders(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.js"
	server.documentManager.OpenDocument(uri, "first\nsecond\n", 1)
	first := protocol.DocumentSymbol{
		Name: "first", Kind: protocol.SymbolClass,
		Range: protocol.Range{
			Start: protocol.Position{Line: 0},
			End:   protocol.Position{Line: 1},
		},
		SelectionRange: protocol.Range{
			Start: protocol.Position{Line: 0},
			End:   protocol.Position{Line: 0, Character: 5},
		},
	}
	second := protocol.DocumentSymbol{
		Name: "second", Kind: protocol.SymbolMethod,
		Range: protocol.Range{
			Start: protocol.Position{Line: 1},
			End:   protocol.Position{Line: 1, Character: 6},
		},
		SelectionRange: protocol.Range{
			Start: protocol.Position{Line: 1},
			End:   protocol.Position{Line: 1, Character: 6},
		},
	}
	server.RegisterDocumentSymbolProvider(testDocumentSymbolProvider{
		symbols: []protocol.DocumentSymbol{second, first},
	})
	server.RegisterDocumentSymbolProvider(testDocumentSymbolProvider{
		symbols: []protocol.DocumentSymbol{first},
	})

	symbols, err := server.documentSymbols(
		context.Background(),
		&protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		},
	)
	require.NoError(t, err)
	require.Len(t, symbols, 2)
	assert.Equal(t, []string{"first", "second"}, []string{
		symbols[0].Name, symbols[1].Name,
	})
}
