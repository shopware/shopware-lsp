package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

const deprecatedAdminI18nTCCode lsp.DiagnosticID = "admin.vue-i18n.tc-deprecated"

// AdminI18nDeprecationAnalyzer mirrors Shopware Administration's
// no-tc-translation ESLint rule for the native JavaScript, Twig, and mixed Vue
// CSTs. It deliberately targets calls only, so declarations and references to
// a property named $tc are left untouched.
type AdminI18nDeprecationAnalyzer struct{}

func NewAdminI18nDeprecationAnalyzer() *AdminI18nDeprecationAnalyzer {
	return &AdminI18nDeprecationAnalyzer{}
}

func (*AdminI18nDeprecationAnalyzer) Analyze(
	_ context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.Contains(document.URI, "Resources/app/administration") {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(document.URI))
	if ext != ".js" && ext != ".ts" && ext != ".twig" && ext != ".vue" {
		return nil, nil
	}

	var result []lsp.Problem
	if ext == ".js" || ext == ".ts" || ext == ".vue" {
		for call := range jsquery.IterateCalls(document.SyntaxTree.Root) {
			callee := jsquery.CallCallee(call)
			name := jsquery.MemberNameNode(callee)
			if name == nil || strings.TrimSpace(name.Text()) != "$tc" {
				continue
			}
			result = append(result, deprecatedAdminI18nTCProblem(name))
		}
	}
	if ext == ".twig" || ext == ".vue" {
		for _, call := range twigquery.Nodes(
			document.SyntaxTree.Root,
			twigsyntax.TwigFunctionCall,
		) {
			if twigquery.FunctionName(call) != "$tc" {
				continue
			}
			typed, ok := twigast.CastTwigFunctionCall(call)
			if !ok {
				continue
			}
			nameOperand, ok := typed.NameOperand()
			if !ok {
				continue
			}
			for _, name := range twigquery.Nodes(
				nameOperand.Syntax(),
				twigsyntax.TwigLiteralName,
			) {
				literal, ok := twigast.CastTwigLiteralName(name)
				if !ok || literal.GetName() == nil {
					continue
				}
				if literal.GetName().Text() == "$tc" {
					result = append(
						result,
						deprecatedAdminI18nTCProblem(literal.GetName()),
					)
					break
				}
			}
		}
	}
	return result, nil
}

func deprecatedAdminI18nTCProblem(name cst.Element) lsp.Problem {
	rng := name.Range()
	if node, ok := name.(*cst.Node); ok {
		rng = node.RangeTrimmedTrivia()
	}
	return lsp.Problem{
		ID:       deprecatedAdminI18nTCCode,
		Range:    rng,
		Element:  name,
		Message:  "Use $t() instead of deprecated $tc(); $t() handles pluralization natively",
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-lsp",
		Payload:  map[string]any{"replacement": "$t"},
	}
}
