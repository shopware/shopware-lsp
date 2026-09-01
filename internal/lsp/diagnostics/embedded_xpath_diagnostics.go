package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xpathparser "github.com/shopware/shopware-lsp/internal/parser/xpath"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const invalidEmbeddedXPathCode lsp.DiagnosticID = "symfony.php.embedded_xpath.invalid"

// EmbeddedXPathAnalyzer validates typed DomCrawler filterXPath()
// and evaluate() strings through the native lossless XPath frontend.
type EmbeddedXPathAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedXPathAnalyzer(
	phpIndex *php.PHPIndex,
) *EmbeddedXPathAnalyzer {
	return &EmbeddedXPathAnalyzer{phpIndex: phpIndex}
}

func (p *EmbeddedXPathAnalyzer) Analyze(
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
	var result []lsp.Problem
	for _, expression := range php.EmbeddedXPathExpressions(
		p.phpIndex,
		path,
		document.Version,
		document.SourceString(),
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		result = append(
			result,
			embeddedXPathDiagnostics(expression, document.LineIndex)...,
		)
	}
	return result, nil
}

func embeddedXPathDiagnostics(
	expression php.EmbeddedPHPString,
	_ *cst.LineIndex,
) []lsp.Problem {
	parsed := xpathparser.Parse(expression.Value)
	if len(parsed.Errors) == 0 {
		return nil
	}
	parseError := parsed.Errors[0]
	hostRange := embeddedDiagnosticHostRange(expression, parseError.Range)
	return []lsp.Problem{{
		Range: hostRange,
		Message: fmt.Sprintf(
			"Invalid XPath expression: %s",
			parseError.Message(),
		),
		Severity: protocol.DiagnosticSeverityError,
		Source:   "symfony",
		ID:       invalidEmbeddedXPathCode,
	}}
}
