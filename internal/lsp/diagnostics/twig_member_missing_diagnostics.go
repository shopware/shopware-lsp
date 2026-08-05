package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingTwigMemberCode lsp.DiagnosticID = "twig.member.missing"

type TwigMemberMissingAnalyzer struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigMemberMissingAnalyzer(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigMemberMissingAnalyzer {
	return &TwigMemberMissingAnalyzer{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigMemberMissingAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".twig") ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	templatePath, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	root := document.SyntaxTree.Root
	resolver := twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndex,
	}
	var result []lsp.Problem
	for _, accessor := range twigquery.Nodes(
		root,
		twigsyntax.TwigAccessor,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		resolution, definite := resolver.InspectAccessor(
			templatePath,
			root,
			accessor,
		)
		if !definite || len(resolution.Members) != 0 {
			continue
		}
		result = append(result, lsp.Problem{
			Range: resolution.NameNode.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Field or method '%s' not found on %s",
				resolution.Name,
				resolution.Receiver.String(),
			),
			Source:   "twig",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       missingTwigMemberCode,
		})
	}
	return result, nil
}
