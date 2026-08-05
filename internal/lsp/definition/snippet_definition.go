package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SnippetDefinitionProvider struct {
	snippetIndexer *snippet.SnippetIndexer
}

func NewSnippetDefinitionProvider(snippetIndexer *snippet.SnippetIndexer) *SnippetDefinitionProvider {
	return &SnippetDefinitionProvider{snippetIndexer: snippetIndexer}
}

func (s *SnippetDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".twig":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return s.twigDefinitions(ctx, params)
	case ".php":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return s.phpDefinitions(ctx, params)
	case ".js", ".ts":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return s.jsDefinitions(ctx, params)
	default:
		return []protocol.Location{}
	}
}

func (s *SnippetDefinitionProvider) twigDefinitions(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if params.Root != nil && params.LineIndex != nil &&
		params.DefinitionParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if reference, found := snippet.AdminTwigReferenceAtOffset(
			params.Root,
			offset,
		); found && reference.Key != "" {
			return s.adminSnippetLocations(reference.Key)
		}
	}
	// Check for frontend snippet pattern: {{ 'key'|trans }}
	if twigquery.StringInFilter(params.Node, "trans") {
		snippets, _ := s.snippetIndexer.GetFrontendSnippet(twigquery.StringValue(twigquery.LiteralStringAt(params.Node)))

		var locations []protocol.Location

		for _, snippet := range snippets {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(snippet.File),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      snippet.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      snippet.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	// Check for admin snippet pattern: {{ $tc('key') }} or {{ $t('key') }}
	if twigquery.StringInFunction(params.Node, "$tc", "$t") {
		return s.adminSnippetLocations(
			twigquery.StringValue(twigquery.LiteralStringAt(params.Node)),
		)
	}

	return []protocol.Location{}
}

func (s *SnippetDefinitionProvider) adminSnippetLocations(
	key string,
) []protocol.Location {
	snippets, _ := s.snippetIndexer.GetAdminSnippet(key)
	locations := make([]protocol.Location, 0, len(snippets))
	for _, current := range snippets {
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(current.File),
			Range: protocol.Range{
				Start: protocol.Position{
					Line: current.Line - 1,
				},
				End: protocol.Position{
					Line: current.Line - 1,
				},
			},
		})
	}
	return locations
}

func (s *SnippetDefinitionProvider) phpDefinitions(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if phpquery.StringInCall(params.Node, 0, "trans") {
		value := phpquery.StringValue(params.Node)
		snippets, _ := s.snippetIndexer.GetFrontendSnippet(value)

		var locations []protocol.Location
		for _, snippet := range snippets {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(snippet.File),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      snippet.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      snippet.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	return []protocol.Location{}
}

func (s *SnippetDefinitionProvider) jsDefinitions(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if snippet.AdminJavaScriptStringReference(params.Node) {
		value := jsquery.StringValue(params.Node)
		return s.adminSnippetLocations(value)
	}

	return []protocol.Location{}
}
