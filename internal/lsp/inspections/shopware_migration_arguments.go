package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const (
	removeArgumentMigrationFixID lsp.FixID = "shopware-rector-remove-argument"
	addDefaultArgumentFixID      lsp.FixID = "shopware-rector-add-default-argument"
	thumbnailGenerateFixID       lsp.FixID = "shopware-rector-thumbnail-generate"
)

type removeArgumentMigrationFix struct{}

func (removeArgumentMigrationFix) ID() lsp.FixID {
	return removeArgumentMigrationFixID
}

func (removeArgumentMigrationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware: Remove obsolete argument",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "argument-migration", err
}

func (removeArgumentMigrationFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "argument-migration" || payload.ArgumentIndex < 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("argument removal is no longer safe")
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
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	switch payload.Kind {
	case "call-argument":
		container := migrationCallOrCreation(element)
		if container == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("argument removal target changed")
		}
		if err := editor.RemoveArgument(container, payload.ArgumentIndex); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "constructor-parameter":
		constructor := ancestorNode(element, phpsyntax.PhpMethodDeclaration)
		if constructor == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("constructor parameter target changed")
		}
		if err := editor.RemoveParameter(constructor, payload.ArgumentIndex); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	default:
		return rewrite.WorkspacePlan{}, fmt.Errorf("unknown argument removal target %q", payload.Kind)
	}
	return finishPHPRewrite(fixContext, editor)
}

type addDefaultArgumentFix struct{}

func (addDefaultArgumentFix) ID() lsp.FixID { return addDefaultArgumentFixID }

func (addDefaultArgumentFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware: Add new default argument",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "argument-migration", err
}

func (addDefaultArgumentFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "argument-migration" ||
		payload.ArgumentIndex < 0 || payload.Value == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("default argument insertion is no longer safe")
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
	call := ancestorNode(element, phpsyntax.PhpMemberCall)
	if call == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("default argument target changed")
	}
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	if err := editor.InsertArgument(call, payload.ArgumentIndex, payload.Value); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return finishPHPRewrite(fixContext, editor)
}

type thumbnailGenerateFix struct{}

func (thumbnailGenerateFix) ID() lsp.FixID { return thumbnailGenerateFixID }

func (thumbnailGenerateFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.5: Generate thumbnails as a batch",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "thumbnail-generate", err
}

func (thumbnailGenerateFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "thumbnail-generate" ||
		payload.Start >= payload.End || payload.End > uint32(len(fixContext.Document.Source)) ||
		payload.Original == "" || payload.Replacement == "" ||
		fixContext.Document.Source[payload.Start:payload.End] != payload.Original {
		return rewrite.WorkspacePlan{}, fmt.Errorf("thumbnail generation target changed")
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
	if err := editor.ReplaceRange(
		cst.TextRange{Start: payload.Start, End: payload.End},
		payload.Replacement,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return finishPHPRewrite(fixContext, editor)
}

func migrationCallOrCreation(element cst.Element) *phpsyntax.Node {
	for current := element; current != nil; current = current.Parent() {
		node, ok := current.(*phpsyntax.Node)
		if !ok {
			continue
		}
		switch node.Kind() {
		case phpsyntax.PhpMemberCall, phpsyntax.PhpObjectCreation:
			return node
		}
	}
	return nil
}
