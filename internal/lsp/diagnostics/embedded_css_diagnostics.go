package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	invalidEmbeddedCSSCode lsp.DiagnosticID = "symfony.php.embedded_css.invalid"
	embeddedCSSPrefix      string           = "@media all { "
	embeddedCSSSuffix      string           = " {} }"
)

// EmbeddedCSSAnalyzer validates typed DomCrawler CSS selector
// arguments through the native lossless SCSS parser. The wrapper mirrors the
// reference plugin's CSS injection host and lets the stylesheet grammar check
// nested selector delimiters and strings.
type EmbeddedCSSAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedCSSAnalyzer(
	phpIndex *php.PHPIndex,
) *EmbeddedCSSAnalyzer {
	return &EmbeddedCSSAnalyzer{phpIndex: phpIndex}
}

func (p *EmbeddedCSSAnalyzer) Analyze(
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
	for _, selector := range php.EmbeddedCSSSelectors(
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
			embeddedCSSDiagnostics(selector, document.LineIndex)...,
		)
	}
	return result, nil
}

func embeddedCSSDiagnostics(
	selector php.EmbeddedPHPString,
	_ *cst.LineIndex,
) []lsp.Problem {
	parsed := scssparser.Parse(
		embeddedCSSPrefix + selector.Value + embeddedCSSSuffix,
	)
	for _, parseError := range parsed.Errors {
		embeddedRange, relevant := embeddedCSSErrorRange(
			parseError.Range,
			uint32(len(selector.Value)),
		)
		if !relevant {
			continue
		}
		hostRange := embeddedDiagnosticHostRange(selector, embeddedRange)
		// The synthetic rule suffix can cascade one unbalanced selector
		// delimiter into additional closing-block errors. Report the first
		// selector error only.
		return []lsp.Problem{{
			Range: hostRange,
			Message: fmt.Sprintf(
				"Invalid CSS selector: %s",
				parseError.Message(),
			),
			Severity: protocol.DiagnosticSeverityError,
			Source:   "symfony",
			ID:       invalidEmbeddedCSSCode,
		}}
	}
	return nil
}

func embeddedCSSErrorRange(
	wrapped cst.TextRange,
	selectorLength uint32,
) (cst.TextRange, bool) {
	prefixLength := uint32(len(embeddedCSSPrefix))
	selectorEnd := prefixLength + selectorLength
	if wrapped.End < prefixLength {
		return cst.TextRange{}, false
	}
	start := min(max(wrapped.Start, prefixLength), selectorEnd)
	end := min(max(wrapped.End, prefixLength), selectorEnd)
	return cst.TextRange{
		Start: start - prefixLength,
		End:   end - prefixLength,
	}, true
}
