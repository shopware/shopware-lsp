package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const (
	yamlEscapeFixID lsp.FixID = "escape-invalid-backslash"
	yamlQuoteFixID  lsp.FixID = "quote-scalar"
)

type yamlReplacement struct {
	Replacement string `json:"replacement"`
}

func NewYAMLCompatibility(model *project.Model) lsp.Inspection {
	escapeFix := yamlCompatibilityFix{id: yamlEscapeFixID}
	quoteFix := yamlCompatibilityFix{id: yamlQuoteFixID}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.yaml.compatibility",
			Languages: []language.ID{language.YAML},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.yaml.quoted_escape", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.yaml.unquoted_indicator", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.yaml.unquoted_colon", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
			},
		},
		analyzer: diagnostics.NewYAMLCompatibilityAnalyzer(model),
		fixes:    []lsp.QuickFix{escapeFix, quoteFix},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			replacement, _ := payload["replacement"].(string)
			if replacement == "" {
				return nil
			}
			fix := yamlQuoteFixID
			if string(code) == "symfony.yaml.quoted_escape" {
				fix = yamlEscapeFixID
			}
			return []lsp.BoundFix{lsp.BindFix(fix, yamlReplacement{Replacement: replacement})}
		},
	}
}

type yamlCompatibilityFix struct {
	id lsp.FixID
}

func (f yamlCompatibilityFix) ID() lsp.FixID { return f.id }

func (f yamlCompatibilityFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[yamlReplacement](fixContext)
	if err != nil || payload.Replacement == "" {
		return lsp.FixPresentation{}, false, err
	}
	title := "Symfony: Quote scalar"
	if f.id == yamlEscapeFixID {
		title = "Symfony: Escape invalid backslash"
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (f yamlCompatibilityFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[yamlReplacement](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	fragment := yamlparser.Parse("value: " + payload.Replacement + "\n")
	if len(fragment.Errors) != 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("replacement is not a valid YAML scalar")
	}
	rng := protocolTextRange(fixContext.Document.LineIndex, fixContext.Diagnostic.Range)
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(rng, payload.Replacement); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}
