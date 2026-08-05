package symbol

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// DALWorkspaceSymbolProvider makes technical entity and field names searchable
// without requiring users to know their PHP Definition class names.
type DALWorkspaceSymbolProvider struct {
	index *dal.Index
}

func NewDALWorkspaceSymbolProvider(index *dal.Index) *DALWorkspaceSymbolProvider {
	return &DALWorkspaceSymbolProvider{index: index}
}

func (p *DALWorkspaceSymbolProvider) WorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, error) {
	if p == nil || p.index == nil {
		return nil, nil
	}
	definitions, err := p.index.Definitions()
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var result []protocol.SymbolInformation
	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		appendDALWorkspaceSymbol(
			&result,
			query,
			definition.Name,
			"Shopware DAL entity · "+definition.Class,
			definition.File,
			definition.Line,
			protocol.SymbolClass,
		)
		for _, field := range definition.Fields {
			appendDALWorkspaceSymbol(
				&result,
				query,
				field.Name,
				"Shopware DAL field · "+definition.Name+" · "+field.Type,
				definition.File,
				field.Line,
				protocol.SymbolField,
			)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		if result[left].ContainerName != result[right].ContainerName {
			return result[left].ContainerName < result[right].ContainerName
		}
		return result[left].Location.URI < result[right].Location.URI
	})
	return result, nil
}

func appendDALWorkspaceSymbol(
	result *[]protocol.SymbolInformation,
	query,
	name,
	container,
	path string,
	line int,
	kind protocol.SymbolKind,
) {
	if name == "" || path == "" {
		return
	}
	search := strings.ToLower(name + " " + container)
	if query != "" && !strings.Contains(search, query) {
		return
	}
	if line < 1 {
		line = 1
	}
	*result = append(*result, protocol.SymbolInformation{
		Name: name, ContainerName: container, Kind: kind,
		Location: protocol.Location{
			URI: uriutil.FileURI(path),
			Range: protocol.Range{
				Start: protocol.Position{Line: line - 1},
				End:   protocol.Position{Line: line - 1},
			},
		},
	})
}
