package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const criteriaIDFixID lsp.FixID = "move-id-filter-to-criteria"

type criteriaIDFixPayload struct {
	Safe          bool   `json:"safe"`
	CriteriaStart uint32 `json:"criteriaStart"`
	CriteriaEnd   uint32 `json:"criteriaEnd"`
	FilterStart   uint32 `json:"filterStart"`
	FilterEnd     uint32 `json:"filterEnd"`
	Argument      string `json:"argument"`
}

func NewShopwareCriteria() lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "shopware.criteria",
			Languages: []language.ID{language.PHP},
			Problems: []lsp.ProblemDefinition{{
				ID:              "shopware.criteria.id-filter",
				Source:          "shopware-lsp",
				DefaultSeverity: protocol.DiagnosticSeverityWarning,
			}},
		},
		analyzer: diagnostics.NewShopwareCriteriaAnalyzer(),
		fixes:    []lsp.QuickFix{criteriaIDFix{}},
		bind: func(_ lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			safe, _ := payload["safe"].(bool)
			if !safe {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(criteriaIDFixID, payload)}
		},
	}
}

type criteriaIDFix struct{}

func (criteriaIDFix) ID() lsp.FixID { return criteriaIDFixID }

func (criteriaIDFix) Present(
	_ context.Context,
	context lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[criteriaIDFixPayload](context)
	return lsp.FixPresentation{
		Title:      "Move ID constraint to Criteria constructor",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Argument != "", err
}

func (criteriaIDFix) Build(
	_ context.Context,
	context lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[criteriaIDFixPayload](context)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Argument == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("criteria ID rewrite is no longer safe")
	}
	if _, err := context.Anchor.Resolve(
		context.Document.URI,
		context.Document.Version,
		context.Document.SyntaxLanguage,
		context.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	criteriaRange := cst.TextRange{Start: payload.CriteriaStart, End: payload.CriteriaEnd}
	filterRange := cst.TextRange{Start: payload.FilterStart, End: payload.FilterEnd}
	if criteriaRange.End > uint32(len(context.Document.Source)) ||
		filterRange.End > uint32(len(context.Document.Source)) ||
		criteriaRange.Start > criteriaRange.End || filterRange.Start > filterRange.End {
		return rewrite.WorkspacePlan{}, fmt.Errorf("criteria ID rewrite ranges changed")
	}
	builder := rewrite.NewBuilder(context.Document.Source)
	if err := builder.ReplaceRange(criteriaRange, "new Criteria("+payload.Argument+")"); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if err := builder.ReplaceRange(filterRange, ""); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := context.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(context.Document.URI, &version, context.Document.Source, edits),
	}}, nil
}
