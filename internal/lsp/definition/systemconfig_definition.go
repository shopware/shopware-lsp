package definition

import (
	"context"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/systemconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SystemConfigDefinitionProvider struct {
	indexer  *systemconfig.SystemConfigIndexer
	phpIndex *php.PHPIndex
}

func NewSystemConfigDefinitionProvider(indexer *systemconfig.SystemConfigIndexer, phpIndex *php.PHPIndex) *SystemConfigDefinitionProvider {
	return &SystemConfigDefinitionProvider{
		indexer:  indexer,
		phpIndex: phpIndex,
	}
}

func (s *SystemConfigDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if filepath.Ext(params.TextDocument.URI) == ".twig" {
		if params.Node == nil || !twigquery.StringInFunction(params.Node, "config") {
			return nil
		}
		value := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))

		entries, err := s.indexer.GetSystemConfigEntry(value)
		if err != nil {
			return nil
		}

		locations := make([]protocol.Location, 0, len(entries))
		for _, entry := range entries {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(entry.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      entry.Line - 1, // LSP uses 0-based line numbers
						Character: 0,
					},
					End: protocol.Position{
						Line:      entry.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	if filepath.Ext(params.TextDocument.URI) == ".php" {
		if params.Node == nil {
			return nil
		}
		return s.phpDefinition(ctx, params)
	}

	return nil
}

func (s *SystemConfigDefinitionProvider) phpDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if s.phpIndex.IsMethodCalledOnClass(ctx, params.Node, params.DocumentContent, "Shopware\\Core\\System\\SystemConfig\\SystemConfigService") {
		if s.phpIndex.IsMethodCalledName(ctx, params.Node, params.DocumentContent, "get", "getInt", "getString", "getFloat", "getBool", "set") {
			value := phpquery.StringValue(params.Node)

			entries, err := s.indexer.GetSystemConfigEntry(value)
			if err != nil {
				return nil
			}

			locations := make([]protocol.Location, 0, len(entries))
			for _, entry := range entries {
				locations = append(locations, protocol.Location{
					URI: uriutil.FileURI(entry.FilePath),
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      entry.Line - 1, // LSP uses 0-based line numbers
							Character: 0,
						},
						End: protocol.Position{
							Line:      entry.Line - 1,
							Character: 0,
						},
					},
				})
			}

			return locations
		}

		if s.phpIndex.IsMethodCalledName(ctx, params.Node, params.DocumentContent, "getDomain") {
			value := phpquery.StringValue(params.Node)

			entries, err := s.indexer.GetAllSystemConfigEntries()
			if err != nil {
				return nil
			}

			locations := make([]protocol.Location, 0, len(entries))
			for _, entry := range entries {
				if entry.Namespace != value {
					continue
				}

				locations = append(locations, protocol.Location{
					URI: uriutil.FileURI(entry.FilePath),
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      entry.Line - 1, // LSP uses 0-based line numbers
							Character: 0,
						},
						End: protocol.Position{
							Line:      entry.Line - 1,
							Character: 0,
						},
					},
				})

				break
			}

			return locations
		}
	}

	return nil
}
