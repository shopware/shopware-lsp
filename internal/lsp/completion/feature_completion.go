package completion

import (
	"context"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/feature"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
)

type FeatureCompletionProvider struct {
	featureIndex *feature.FeatureIndexer
}

func NewFeatureCompletionProvider(featureIndexer *feature.FeatureIndexer) *FeatureCompletionProvider {
	return &FeatureCompletionProvider{featureIndex: featureIndexer}
}

func (p *FeatureCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	matches := false
	switch filepath.Ext(params.TextDocument.URI) {
	case ".twig":
		matches = params.Node != nil && twigquery.StringInFunction(params.Node, "feature")
	case ".scss":
		matches = params.Node != nil && scssquery.StringInFunction(params.Node, "feature")
	case ".php":
		matches = params.Node != nil &&
			phpquery.StringInCall(params.Node, 0, "Feature::isActive")
	case ".js", ".ts":
		matches = params.Node != nil && jsquery.StringInCall(
			params.Node,
			0,
			"Feature.isActive",
			"Shopware.Feature.isActive",
		)
	}

	if matches {
		completionItems := []protocol.CompletionItem{}
		features, _ := p.featureIndex.GetAllFeatures()
		for _, feature := range features {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: feature.Name,
				Kind:  int(protocol.FunctionCompletion),
			})
		}

		return completionItems
	}

	return nil
}

func (p *FeatureCompletionProvider) GetTriggerCharacters() []string {
	return []string{}
}
