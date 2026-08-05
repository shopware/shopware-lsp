package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type ThemeDefinitionProvider struct {
	themeIndexer *theme.ThemeConfigIndexer
}

func NewThemeDefinitionProvider(themeIndexer *theme.ThemeConfigIndexer) *ThemeDefinitionProvider {
	return &ThemeDefinitionProvider{themeIndexer: themeIndexer}
}

func (p *ThemeDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".scss":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.scssDefinition(ctx, params)
	case ".twig":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.twigDefinition(ctx, params)
	default:
		return []protocol.Location{}
	}
}

func (p *ThemeDefinitionProvider) scssDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if variable := scssquery.VariableAt(params.Node); variable != nil {
		locations, _ := p.themeIndexer.GetThemeConfigField(scssquery.VariableName(variable))

		var result []protocol.Location
		for _, location := range locations {
			result = append(result, protocol.Location{
				URI: uriutil.FileURI(location.Path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return result
	}

	return []protocol.Location{}
}

func (p *ThemeDefinitionProvider) twigDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {

	if twigquery.StringInFunction(params.Node, "theme_config") {
		nodeText := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))
		locations, _ := p.themeIndexer.GetThemeConfigField(nodeText)

		var result []protocol.Location
		for _, location := range locations {
			result = append(result, protocol.Location{
				URI: uriutil.FileURI(location.Path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return result
	}

	return []protocol.Location{}
}
