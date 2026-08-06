package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

type adminI18nTCReplacement struct {
	Replacement string `json:"replacement"`
}

type adminI18nTCFix struct{}

func (adminI18nTCFix) ID() lsp.FixID { return migrateAdminI18nTCFixID }

func (adminI18nTCFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminI18nTCReplacement](fixContext)
	return lsp.FixPresentation{
		Title:      "Replace deprecated $tc() with $t()",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Replacement == "$t", err
}

func (adminI18nTCFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminI18nTCReplacement](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if payload.Replacement != "$t" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("invalid Vue I18n replacement")
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
	if element.Text() != "$tc" {
		return rewrite.WorkspacePlan{}, rewrite.ErrStaleHandle
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.Replace(element, payload.Replacement); err != nil {
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
