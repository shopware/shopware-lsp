package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const apiRenameFixID lsp.FixID = "shopware-rector-api-rename"

type apiRenameFix struct{}

func (apiRenameFix) ID() lsp.FixID { return apiRenameFixID }

func (apiRenameFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	title := "Shopware: Rename deprecated API"
	switch payload.Kind {
	case "class":
		title = "Shopware: Rename class"
	case "method", "static-method":
		title = "Shopware: Rename method"
	case "constant":
		title = "Shopware: Move class constant"
	case "property":
		title = "Shopware: Replace property access with method call"
	case "exception-factory":
		title = "Shopware: Replace exception construction with factory"
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "api-rename", err
}

func (apiRenameFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "api-rename" ||
		(len(payload.Edits) == 0 && (payload.Start >= payload.End ||
			payload.End > uint32(len(fixContext.Document.Source)) ||
			payload.Replacement == "")) {
		return rewrite.WorkspacePlan{}, fmt.Errorf("shopware API rename is no longer safe")
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	edits := payload.Edits
	if len(edits) == 0 {
		edits = []diagnostics.ShopwareMigrationEdit{{
			Start:       payload.Start,
			End:         payload.End,
			Original:    payload.Original,
			Replacement: payload.Replacement,
		}}
	}
	for _, edit := range edits {
		if edit.Start >= edit.End || edit.End > uint32(len(fixContext.Document.Source)) ||
			edit.Original == "" || edit.Replacement == "" ||
			fixContext.Document.Source[edit.Start:edit.End] != edit.Original {
			return rewrite.WorkspacePlan{}, fmt.Errorf("shopware API rename target changed")
		}
		if err := editor.ReplaceRange(
			cst.TextRange{Start: edit.Start, End: edit.End},
			edit.Replacement,
		); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	return finishPHPRewrite(fixContext, editor)
}
