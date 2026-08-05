package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const formClassConstantFixID lsp.FixID = "replace-form-alias-with-class-constant"

type formClassPayload struct {
	ClassName string `json:"className"`
}

func NewForm(index *form.Index, phpIndex *php.PHPIndex) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "symfony.form",
			Languages: []language.ID{language.PHP, language.Twig},
			Problems: []lsp.ProblemDefinition{
				{ID: "symfony.form.field.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.option.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.type.legacy_alias", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityHint},
				{ID: "symfony.form.type.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "symfony.form.view_var.missing", Source: "symfony", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewFormAnalyzer(index, phpIndex),
		fixes:    []lsp.QuickFix{suggestionFix{}, formClassConstantFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			bound := suggestionBoundFixes(payload)
			if string(code) != "symfony.form.type.legacy_alias" {
				return bound
			}
			className := mapString(payload, "className")
			if className == "" {
				return bound
			}
			return append(bound, lsp.BindFix(
				formClassConstantFixID,
				formClassPayload{ClassName: className},
			))
		},
	}
}

type formClassConstantFix struct{}

func (formClassConstantFix) ID() lsp.FixID { return formClassConstantFixID }

func (formClassConstantFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[formClassPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Symfony: Use class constant",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.ClassName != "", err
}

func (formClassConstantFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[formClassPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	literal := ancestorNode(element, phpsyntax.PhpString)
	if literal == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("form alias string is no longer available")
	}
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	qualifier, err := editor.ClassReference(payload.ClassName)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if err := editor.ReplaceRange(
		literal.RangeTrimmedTrivia(),
		qualifier+"::class",
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := editor.Finish()
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
