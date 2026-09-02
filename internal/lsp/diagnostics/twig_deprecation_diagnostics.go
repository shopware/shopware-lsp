package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
)

const (
	deprecatedTwigFunctionCode lsp.DiagnosticID = "twig.function.deprecated"
	deprecatedTwigFilterCode   lsp.DiagnosticID = "twig.filter.deprecated"
	deprecatedTwigTestCode     lsp.DiagnosticID = "twig.test.deprecated"
	deprecatedTwigTagCode      lsp.DiagnosticID = "twig.tag.deprecated"
)

type TwigDeprecationAnalyzer struct {
	twigIndex *twig.TwigIndexer
}

func NewTwigDeprecationAnalyzer(
	twigIndex *twig.TwigIndexer,
) *TwigDeprecationAnalyzer {
	return &TwigDeprecationAnalyzer{twigIndex: twigIndex}
}

func (p *TwigDeprecationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.twigIndex == nil || document == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".twig") ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	var result []lsp.Problem
	for call := range twigquery.IterateNodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigFunctionCall,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if twig.IsTestFunctionCall(call) {
			continue
		}
		name := twigquery.FunctionName(call)
		deprecated, message, err := p.twigIndex.
			TwigFunctionDeprecation(name)
		if err != nil {
			return nil, fmt.Errorf(
				"query Twig function %q deprecation: %w",
				name,
				err,
			)
		}
		if !deprecated {
			continue
		}
		nameNode := twigCallableNameNode(call, name, false)
		result = append(result, twigCallableDeprecationDiagnostic(
			nameNode,
			document,
			name,
			"function",
			deprecatedTwigFunctionCode,
			message,
		))
	}
	for filter := range twigquery.IterateNodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigFilter,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		name := twigquery.FilterName(filter)
		deprecated, message, err := p.twigIndex.
			TwigFilterDeprecation(name)
		if err != nil {
			return nil, fmt.Errorf(
				"query Twig filter %q deprecation: %w",
				name,
				err,
			)
		}
		if !deprecated {
			continue
		}
		nameNode := twigCallableNameNode(filter, name, true)
		result = append(result, twigCallableDeprecationDiagnostic(
			nameNode,
			document,
			name,
			"filter",
			deprecatedTwigFilterCode,
			message,
		))
	}
	for _, expression := range twig.TestExpressions(
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		deprecated, message, err := p.twigIndex.
			TwigTestDeprecation(expression.Name)
		if err != nil {
			return nil, fmt.Errorf(
				"query Twig test %q deprecation: %w",
				expression.Name,
				err,
			)
		}
		if !deprecated {
			continue
		}
		result = append(result, twigCallableDeprecationDiagnostic(
			expression.Node,
			document,
			expression.Name,
			"test",
			deprecatedTwigTestCode,
			message,
		))
	}
	for _, usage := range twig.TwigTagUsages(document.Text) {
		if ctx.Err() != nil {
			return nil, nil
		}
		deprecated, message, err := p.twigIndex.
			TwigTagDeprecation(usage.Name)
		if err != nil {
			return nil, fmt.Errorf(
				"query Twig tag %q deprecation: %w",
				usage.Name,
				err,
			)
		}
		if !deprecated {
			continue
		}
		if message == "" {
			message = "Deprecated Twig tag"
		} else {
			message = "Deprecated Twig tag: " + message
		}
		result = append(result, lsp.Problem{
			Range:    usage.Range,
			Message:  message,
			Source:   "twig",
			Severity: protocol.DiagnosticSeverityHint,
			ID:       deprecatedTwigTagCode,
			Tags: []protocol.DiagnosticTag{
				protocol.DiagnosticTagDeprecated,
			},
		})
	}
	return result, nil
}

func twigCallableNameNode(
	container *twigsyntax.Node,
	name string,
	last bool,
) *twigsyntax.Node {
	var result *twigsyntax.Node
	for candidate := range twigquery.IterateNodes(
		container,
		twigsyntax.TwigLiteralName,
	) {
		if strings.TrimSpace(candidate.Text()) != name {
			continue
		}
		if container.Kind() == twigsyntax.TwigFunctionCall &&
			twigquery.FunctionCallAt(candidate) != container {
			continue
		}
		if container.Kind() == twigsyntax.TwigFilter &&
			twigquery.ClosestNodeOfKind(
				candidate,
				twigsyntax.TwigFilter,
			) != container {
			continue
		}
		result = candidate
		if !last {
			return result
		}
	}
	if result != nil {
		return result
	}
	return container
}

func twigCallableDeprecationDiagnostic(
	node *twigsyntax.Node,
	_ *lsp.TextDocument,
	name,
	kind string,
	code lsp.DiagnosticID,
	message string,
) lsp.Problem {
	if message == "" {
		message = "Deprecated Twig " + kind
	} else {
		message = "Deprecated Twig " + kind + ": " + message
	}
	return lsp.Problem{
		Range:    valueNodeTextRange(node, name),
		Message:  message,
		Source:   "twig",
		Severity: protocol.DiagnosticSeverityHint,
		ID:       code,
		Tags: []protocol.DiagnosticTag{
			protocol.DiagnosticTagDeprecated,
		},
	}
}
