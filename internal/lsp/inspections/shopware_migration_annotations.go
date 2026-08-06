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

const routeAnnotationMigrationFixID lsp.FixID = "shopware-rector-route-annotation-default"

type routeAnnotationMigrationFix struct{}

func (routeAnnotationMigrationFix) ID() lsp.FixID {
	return routeAnnotationMigrationFixID
}

func (routeAnnotationMigrationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.5: Migrate annotation to route defaults",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "route-annotation-default", err
}

func (routeAnnotationMigrationFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "route-annotation-default" ||
		payload.Value == "" || payload.Replacement == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("route annotation migration is no longer safe")
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
	owner := migrationAnnotationOwner(element)
	if owner == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("route annotation owner changed")
	}
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	if payload.Kind == "route-scope" {
		if _, hasRoute := phprewrite.FindPHPDocAnnotation(owner, "Route"); hasRoute {
			replaced, replaceErr := editor.ReplacePHPDocAnnotation(owner, "Route", payload.Replacement)
			if replaceErr != nil {
				return rewrite.WorkspacePlan{}, replaceErr
			}
			if !replaced {
				return rewrite.WorkspacePlan{}, fmt.Errorf("route annotation changed")
			}
			removed, removeErr := editor.RemovePHPDocAnnotation(owner, payload.Value)
			if removeErr != nil {
				return rewrite.WorkspacePlan{}, removeErr
			}
			if !removed {
				return rewrite.WorkspacePlan{}, fmt.Errorf("route scope annotation changed")
			}
		} else {
			replaced, replaceErr := editor.ReplacePHPDocAnnotation(owner, payload.Value, payload.Replacement)
			if replaceErr != nil {
				return rewrite.WorkspacePlan{}, replaceErr
			}
			if !replaced {
				return rewrite.WorkspacePlan{}, fmt.Errorf("route scope annotation changed")
			}
		}
	} else {
		replaced, replaceErr := editor.ReplacePHPDocAnnotation(owner, "Route", payload.Replacement)
		if replaceErr != nil {
			return rewrite.WorkspacePlan{}, replaceErr
		}
		if !replaced {
			return rewrite.WorkspacePlan{}, fmt.Errorf("route annotation changed")
		}
		removed, removeErr := editor.RemovePHPDocAnnotation(owner, payload.Value)
		if removeErr != nil {
			return rewrite.WorkspacePlan{}, removeErr
		}
		if !removed {
			return rewrite.WorkspacePlan{}, fmt.Errorf("legacy route annotation changed")
		}
	}
	return finishPHPRewrite(fixContext, editor)
}

func migrationAnnotationOwner(element cst.Element) *phpsyntax.Node {
	for current := element; current != nil; current = current.Parent() {
		node, ok := current.(*phpsyntax.Node)
		if !ok {
			continue
		}
		switch node.Kind() {
		case phpsyntax.PhpMethodDeclaration, phpsyntax.PhpClassDeclaration:
			return node
		}
	}
	return nil
}
