package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

type testWorkspaceSymbolProvider struct {
	symbols []protocol.SymbolInformation
}

func (p testWorkspaceSymbolProvider) WorkspaceSymbols(
	context.Context,
	string,
) ([]protocol.SymbolInformation, error) {
	return append([]protocol.SymbolInformation(nil), p.symbols...), nil
}

func TestWorkspaceSymbolsAggregateSortAndDeduplicateProviders(t *testing.T) {
	alpha := protocol.SymbolInformation{
		Name: "alpha",
		Kind: protocol.SymbolFunction,
		Location: protocol.Location{
			URI: "file:///project/alpha.php",
		},
	}
	beta := protocol.SymbolInformation{
		Name: "beta",
		Kind: protocol.SymbolClass,
		Location: protocol.Location{
			URI: "file:///project/beta.php",
		},
	}
	server := NewServer(nil, "", "test")
	server.RegisterWorkspaceSymbolProvider(
		testWorkspaceSymbolProvider{
			symbols: []protocol.SymbolInformation{beta, alpha},
		},
	)
	server.RegisterWorkspaceSymbolProvider(
		testWorkspaceSymbolProvider{
			symbols: []protocol.SymbolInformation{alpha},
		},
	)
	result, err := server.workspaceSymbols(
		context.Background(),
		&protocol.WorkspaceSymbolParams{Query: "a"},
	)
	require.NoError(t, err)
	require.Equal(t, []protocol.SymbolInformation{alpha, beta}, result)
	require.NoError(t, server.CloseAll())
}
