package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// EmbeddedLanguageAnalyzer validates every supported Symfony
// language-injection signature in one PHP CST scan and one semantic analysis.
type EmbeddedLanguageAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedLanguageAnalyzer(
	phpIndex *php.PHPIndex,
) *EmbeddedLanguageAnalyzer {
	return &EmbeddedLanguageAnalyzer{phpIndex: phpIndex}
}

func (p *EmbeddedLanguageAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		document.LineIndex == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".php") {
		return nil, nil
	}
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
	var result []lsp.Problem
	for _, literal := range embedded {
		if ctx.Err() != nil {
			return nil, nil
		}
		switch literal.Language {
		case php.EmbeddedLanguageJSON:
			result = append(
				result,
				embeddedJSONDiagnostics(
					literal.EmbeddedPHPString,
					document.LineIndex,
				)...,
			)
		case php.EmbeddedLanguageCSS:
			result = append(
				result,
				embeddedCSSDiagnostics(
					literal.EmbeddedPHPString,
					document.LineIndex,
				)...,
			)
		case php.EmbeddedLanguageXPath:
			result = append(
				result,
				embeddedXPathDiagnostics(
					literal.EmbeddedPHPString,
					document.LineIndex,
				)...,
			)
		}
	}
	return result, nil
}
