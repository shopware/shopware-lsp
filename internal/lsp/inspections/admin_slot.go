package inspections

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const migrateAdminSlotFixID lsp.FixID = "migrate-admin-vue-slot"

type adminSlotReplacement struct {
	Replacement string `json:"replacement"`
}

type adminSlotMigrationFix struct{}

func (adminSlotMigrationFix) ID() lsp.FixID { return migrateAdminSlotFixID }

func (adminSlotMigrationFix) Present(
	_ context.Context,
	context lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminSlotReplacement](context)
	return lsp.FixPresentation{
		Title:      "Migrate to Vue v-slot syntax",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Replacement != "", err
}

func (adminSlotMigrationFix) Build(
	_ context.Context,
	context lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[adminSlotReplacement](context)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, err := context.Anchor.Resolve(
		context.Document.URI,
		context.Document.Version,
		context.Document.SyntaxLanguage,
		context.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	builder := rewrite.NewBuilder(context.Document.Source)
	if err := builder.ReplaceRange(
		protocolTextRange(context.Document.LineIndex, context.Diagnostic.Range),
		payload.Replacement,
	); err != nil {
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
