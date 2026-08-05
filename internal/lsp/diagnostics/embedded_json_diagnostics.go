package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const invalidEmbeddedJSONCode lsp.DiagnosticID = "symfony.php.embedded_json.invalid"

// EmbeddedJSONAnalyzer brings the reference plugin's JsonResponse
// language injection to portable LSP clients by validating the decoded host
// string with the native lossless JSON parser.
type EmbeddedJSONAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedJSONAnalyzer(
	phpIndex *php.PHPIndex,
) *EmbeddedJSONAnalyzer {
	return &EmbeddedJSONAnalyzer{phpIndex: phpIndex}
}

func (p *EmbeddedJSONAnalyzer) Analyze(
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
	for _, literal := range php.EmbeddedJSONLiterals(
		p.phpIndex,
		path,
		document.Version,
		string(document.Text),
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		result = append(
			result,
			embeddedJSONDiagnostics(literal, document.LineIndex)...,
		)
	}
	return result, nil
}

func embeddedJSONDiagnostics(
	literal php.EmbeddedPHPString,
	_ *cst.LineIndex,
) []lsp.Problem {
	parsed := jsonparser.Parse(literal.Value)
	result := make([]lsp.Problem, 0, len(parsed.Errors))
	for _, parseError := range parsed.Errors {
		hostRange := embeddedDiagnosticHostRange(
			literal,
			parseError.Range,
		)
		result = append(result, lsp.Problem{
			Range: hostRange,
			Message: fmt.Sprintf(
				"Invalid JSON: %s",
				parseError.Message(),
			),
			Severity: protocol.DiagnosticSeverityError,
			Source:   "symfony",
			ID:       invalidEmbeddedJSONCode,
		})
	}
	return result
}

func embeddedDiagnosticHostRange(
	literal php.EmbeddedPHPString,
	embeddedRange cst.TextRange,
) cst.TextRange {
	hostRange := literal.SourceRange(embeddedRange)
	if hostRange.Start != hostRange.End {
		return hostRange
	}
	switch {
	case hostRange.End < literal.ContentRange.End:
		hostRange.End++
	case hostRange.Start > literal.ContentRange.Start:
		hostRange.Start--
	}
	return hostRange
}
