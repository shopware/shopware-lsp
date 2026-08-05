package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/feature"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type FeatureDefinitionProvider struct {
	featureIndex *feature.FeatureIndexer
}

func NewFeatureDefinitionProvider(featureIndexer *feature.FeatureIndexer) *FeatureDefinitionProvider {
	return &FeatureDefinitionProvider{featureIndex: featureIndexer}
}

func (p *FeatureDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".twig":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.twigDefinition(ctx, params)
	case ".php":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.phpDefinition(ctx, params)
	case ".scss":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.scssDefinition(ctx, params)
	case ".js", ".ts":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.javascriptDefinition(params)
	default:
		return []protocol.Location{}
	}
}

func (p *FeatureDefinitionProvider) javascriptDefinition(params *lsp.DefinitionRequest) []protocol.Location {
	if !jsquery.StringInCall(
		params.Node,
		0,
		"Feature.isActive",
		"Shopware.Feature.isActive",
	) {
		return []protocol.Location{}
	}
	featureName := jsquery.StringValue(params.Node)
	features, _ := p.featureIndex.GetFeatureByName(featureName)
	locations := make([]protocol.Location, 0, len(features))
	for _, current := range features {
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(current.File),
			Range: protocol.Range{
				Start: protocol.Position{Line: current.Line - 1},
				End:   protocol.Position{Line: current.Line - 1},
			},
		})
	}
	return locations
}

func (p *FeatureDefinitionProvider) twigDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if twigquery.StringInFunction(params.Node, "feature") {
		featureName := twigquery.StringValue(twigquery.LiteralStringAt(params.Node))
		features, _ := p.featureIndex.GetFeatureByName(featureName)

		var locations []protocol.Location
		for _, feature := range features {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(feature.File),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	return []protocol.Location{}
}

func (p *FeatureDefinitionProvider) phpDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	// Check for PHP feature flag usage patterns
	if phpquery.StringInCall(params.Node, 0, "Feature::isActive") {
		featureName := phpquery.StringValue(params.Node)
		features, _ := p.featureIndex.GetFeatureByName(featureName)

		var locations []protocol.Location
		for _, feature := range features {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(feature.File),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	return []protocol.Location{}
}

func (p *FeatureDefinitionProvider) scssDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if stringNode := scssquery.StringArgumentInFunction(params.Node, "feature"); stringNode != nil {
		featureName := scssquery.StringValue(stringNode)
		features, _ := p.featureIndex.GetFeatureByName(featureName)

		var locations []protocol.Location
		for _, feature := range features {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(feature.File),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      feature.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return locations
	}

	return []protocol.Location{}
}
