package symbol

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type WorkspaceSymbolProvider interface {
	WorkspaceSymbols(
		ctx context.Context,
		query string,
	) ([]protocol.SymbolInformation, error)
}

// CatalogWorkspaceSymbolProvider uses the persisted FTS catalog once initial
// indexing completes. During a cold index it delegates to the existing live
// providers, preserving workspace-symbol availability during startup.
type CatalogWorkspaceSymbolProvider struct {
	catalog         *indexer.WorkspaceSymbolCatalog
	fallbacks       []WorkspaceSymbolProvider
	excludedDomains []string
}

func NewCatalogWorkspaceSymbolProvider(
	catalog *indexer.WorkspaceSymbolCatalog,
	fallbacks ...WorkspaceSymbolProvider,
) *CatalogWorkspaceSymbolProvider {
	return &CatalogWorkspaceSymbolProvider{
		catalog:   catalog,
		fallbacks: fallbacks,
	}
}

// ExcludeDomains removes catalog domains at query time. Fallback providers
// are already framework-specific and remain available during cold indexing.
func (provider *CatalogWorkspaceSymbolProvider) ExcludeDomains(domains ...string) {
	if provider == nil {
		return
	}
	provider.excludedDomains = append([]string(nil), domains...)
}

func (provider *CatalogWorkspaceSymbolProvider) WorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, error) {
	if provider == nil {
		return nil, nil
	}
	if provider.catalog != nil {
		ready, err := provider.catalog.Ready(ctx)
		if err != nil {
			return nil, err
		}
		if ready {
			symbols, err := provider.catalog.QueryWithOptions(
				ctx,
				query,
				500,
				indexer.WorkspaceSymbolQueryOptions{
					ExcludedDomains: provider.excludedDomains,
				},
			)
			if err != nil {
				return nil, err
			}
			result := make([]protocol.SymbolInformation, 0, len(symbols))
			for _, current := range symbols {
				result = append(result, catalogProtocolSymbol(current))
			}
			// PHP declarations are the first migrated domain. A miss still
			// delegates to the legacy framework providers until their indexers
			// publish into the catalog as well.
			if len(result) > 0 || len(provider.fallbacks) == 0 {
				return result, nil
			}
		}
	}

	var result []protocol.SymbolInformation
	for _, fallback := range provider.fallbacks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		symbols, err := fallback.WorkspaceSymbols(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("fallback workspace symbols: %w", err)
		}
		result = append(result, symbols...)
	}
	return result, nil
}

func catalogProtocolSymbol(
	symbol indexer.WorkspaceSymbol,
) protocol.SymbolInformation {
	return protocol.SymbolInformation{
		Name:          symbol.Name,
		Kind:          protocol.SymbolKind(symbol.Kind),
		ContainerName: symbol.ContainerName,
		Priority:      symbol.Priority,
		Location: protocol.Location{
			URI: uriutil.FileURI(symbol.Path),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      symbol.Range.Start.Line,
					Character: symbol.Range.Start.Character,
				},
				End: protocol.Position{
					Line:      symbol.Range.End.Line,
					Character: symbol.Range.End.Character,
				},
			},
		},
	}
}
