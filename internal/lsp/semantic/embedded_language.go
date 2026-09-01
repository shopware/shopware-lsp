package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// EmbeddedLanguageProvider colors every supported Symfony language-injection
// signature in one PHP CST scan and one semantic analysis.
type EmbeddedLanguageProvider struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedLanguageProvider(
	phpIndex *php.PHPIndex,
) *EmbeddedLanguageProvider {
	return &EmbeddedLanguageProvider{phpIndex: phpIndex}
}

func (p *EmbeddedLanguageProvider) GetSemanticTokens(
	ctx context.Context,
	request *lsp.SemanticTokensRequest,
) ([]lsp.SemanticToken, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.Document.URI),
			".php",
		) {
		return nil, nil
	}
	document := request.Document
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	embedded := php.EmbeddedLanguageStrings(
		p.phpIndex,
		path,
		document.Version,
		document.SourceString(),
		document.SyntaxTree.Root,
	)
	var result []lsp.SemanticToken
	for _, literal := range embedded {
		if ctx.Err() != nil {
			return nil, nil
		}
		switch literal.Language {
		case php.EmbeddedLanguageJSON:
			result = append(
				result,
				embeddedJSONSemanticTokens(
					literal.EmbeddedPHPString,
				)...,
			)
		case php.EmbeddedLanguageCSS:
			result = append(
				result,
				embeddedCSSSemanticTokens(
					literal.EmbeddedPHPString,
				)...,
			)
		case php.EmbeddedLanguageXPath:
			result = append(
				result,
				embeddedXPathSemanticTokens(
					literal.EmbeddedPHPString,
				)...,
			)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Range.End < result[right].Range.End
	})
	return result, nil
}

var _ lsp.SemanticTokensProvider = (*EmbeddedLanguageProvider)(nil)
