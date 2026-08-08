package diagnostics

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin/twigmigration"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/shopware"
)

type AdminTwigMigrationPayload struct {
	Rule      string `json:"rule"`
	SourceTag string `json:"sourceTag"`
	TargetTag string `json:"targetTag"`
	Safe      bool   `json:"safe"`
}

type AdminTwigMigrationAnalyzer struct {
	version shopware.ResolvedVersion
}

func NewAdminTwigMigrationAnalyzer(version shopware.ResolvedVersion) *AdminTwigMigrationAnalyzer {
	return &AdminTwigMigrationAnalyzer{version: version}
}

func (p *AdminTwigMigrationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || !p.version.AtLeast(6, 7, 0) || document == nil ||
		document.SyntaxLanguage != language.Twig || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.Contains(document.URI, "Resources/app/administration") ||
		strings.ToLower(filepath.Ext(document.URI)) != ".twig" {
		return nil, nil
	}

	var problems []lsp.Problem
	for _, node := range twigquery.Nodes(document.SyntaxTree.Root, twigsyntax.HtmlTag) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tag, ok := twigast.CastHtmlTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		rule, found := twigmigration.RuleForTag(tag.Name().Text())
		if !found {
			continue
		}
		_, compileErr := twigmigration.Compile(document.Source, node, rule)
		if compileErr != nil && !errors.Is(compileErr, twigmigration.ErrUnsafe) {
			return nil, compileErr
		}
		problems = append(problems, lsp.Problem{
			ID:       lsp.DiagnosticID("admin.twig.migration." + rule.ID),
			Range:    tag.Name().Range(),
			Element:  node,
			Message:  rule.Message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
			Tags:     []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			Payload: AdminTwigMigrationPayload{
				Rule: rule.ID, SourceTag: rule.SourceTag,
				TargetTag: rule.TargetTag, Safe: compileErr == nil,
			},
		})
	}
	return problems, nil
}
