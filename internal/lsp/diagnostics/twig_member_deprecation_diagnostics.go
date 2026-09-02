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
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const deprecatedTwigMemberCode lsp.DiagnosticID = "twig.member.deprecated"

type TwigMemberDeprecationAnalyzer struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigMemberDeprecationAnalyzer(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigMemberDeprecationAnalyzer {
	return &TwigMemberDeprecationAnalyzer{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigMemberDeprecationAnalyzer) Analyze(
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
	accessResolver := twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndex,
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	seen := make(map[string]struct{})
	var result []lsp.Problem
	for accessor := range twigquery.IterateNodes(root, twigsyntax.TwigAccessor) {
		if ctx.Err() != nil {
			return nil, nil
		}
		resolution, ok := accessResolver.ResolveAccessor(
			templatePath,
			root,
			accessor,
		)
		if !ok {
			continue
		}
		for _, member := range resolution.Members {
			symbol := member.Symbol
			if !symbol.Flags.Has(semantic.DeprecatedFlag) {
				continue
			}
			key := string(symbol.ID) + ":" +
				resolution.NameNode.RangeTrimmedTrivia().String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, lsp.Problem{
				Range:    resolution.NameNode.RangeTrimmedTrivia(),
				Message:  deprecatedTwigMemberMessage(snapshot, symbol),
				Source:   "twig",
				Severity: protocol.DiagnosticSeverityHint,
				ID:       deprecatedTwigMemberCode,
				Tags: []protocol.DiagnosticTag{
					protocol.DiagnosticTagDeprecated,
				},
			})
		}
	}
	return result, nil
}

func deprecatedTwigMemberMessage(
	snapshot interface {
		Symbol(semantic.SymbolID) (semantic.Symbol, bool)
	},
	symbol semantic.Symbol,
) string {
	className := ""
	if container, ok := snapshot.Symbol(symbol.Container); ok {
		className = container.Name
	}
	if className == "" {
		className = strings.TrimPrefix(
			strings.Split(symbol.FullyQualified, "::")[0],
			"\\",
		)
		if separator := strings.LastIndex(className, "\\"); separator >= 0 {
			className = className[separator+1:]
		}
	}
	switch symbol.Kind {
	case semantic.MethodSymbol:
		return fmt.Sprintf(
			"Method '%s::%s' is deprecated",
			className,
			symbol.Name,
		)
	case semantic.PropertySymbol:
		return fmt.Sprintf(
			"Field '%s::$%s' is deprecated",
			className,
			strings.TrimPrefix(symbol.Name, "$"),
		)
	default:
		return fmt.Sprintf(
			"Element '%s::%s' is deprecated",
			className,
			symbol.Name,
		)
	}
}
