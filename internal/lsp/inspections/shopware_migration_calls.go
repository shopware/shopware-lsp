package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const (
	contextMetadataStateFixID        lsp.FixID = "shopware-rector-context-metadata-state"
	fakerPropertyCallFixID           lsp.FixID = "shopware-rector-faker-property-call"
	productStreamEnrichCriteriaFixID lsp.FixID = "shopware-rector-product-stream-enrich-criteria"
)

type contextMetadataStateFix struct{}

func (contextMetadataStateFix) ID() lsp.FixID { return contextMetadataStateFixID }

func (contextMetadataStateFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.5: Migrate Context extension flag to state",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "context-metadata-state", err
}

func (contextMetadataStateFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "context-metadata-state" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("context state rewrite is no longer safe")
	}
	call, root, err := resolveMigrationPHPNode(fixContext, phpsyntax.PhpMemberCall)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !strings.EqualFold(phpquery.CallMethodName(call), "addExtension") ||
		len(phpquery.Arguments(call)) != 2 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("context addExtension() call changed")
	}
	name := callTargetNameForMigrationFix(call)
	if name == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("context addExtension() name is unavailable")
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	if err := editor.ReplaceRange(name.RangeTrimmedTrivia(), "addState"); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if err := editor.RemoveArgument(call, 1); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return finishPHPRewrite(fixContext, editor)
}

type fakerPropertyCallFix struct{}

func (fakerPropertyCallFix) ID() lsp.FixID { return fakerPropertyCallFixID }

func (fakerPropertyCallFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.5: Call Faker formatter as a method",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "faker-property-call", err
}

func (fakerPropertyCallFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "faker-property-call" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("faker property rewrite is no longer safe")
	}
	access, root, err := resolveMigrationPHPNode(fixContext, phpsyntax.PhpMemberAccess)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	if err := editor.Insert(access.RangeTrimmedTrivia().End, "()"); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return finishPHPRewrite(fixContext, editor)
}

type productStreamEnrichCriteriaFix struct{}

func (productStreamEnrichCriteriaFix) ID() lsp.FixID {
	return productStreamEnrichCriteriaFixID
}

func (productStreamEnrichCriteriaFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	title := "Shopware 6.8: Replace buildFilters() with enrichCriteria()"
	if payload.Kind == "manual" {
		title = "Shopware 6.8: Add buildFilters() migration TODO"
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "product-stream-enrich-criteria", err
}

func (productStreamEnrichCriteriaFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "product-stream-enrich-criteria" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("ProductStream rewrite is no longer safe")
	}
	call, _, err := resolveMigrationPHPNode(fixContext, phpsyntax.PhpMemberCall)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !strings.EqualFold(phpquery.CallMethodName(call), "buildFilters") || payload.Replacement == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("ProductStream migration target changed")
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	switch payload.Kind {
	case "assignment", "inline":
		if payload.Start >= payload.End || payload.End > uint32(len(fixContext.Document.Source)) {
			return rewrite.WorkspacePlan{}, fmt.Errorf("ProductStream migration target changed")
		}
		if err := builder.ReplaceRange(
			cst.TextRange{Start: payload.Start, End: payload.End},
			payload.Replacement,
		); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "manual":
		if payload.Start > uint32(len(fixContext.Document.Source)) {
			return rewrite.WorkspacePlan{}, fmt.Errorf("ProductStream migration target changed")
		}
		if err := builder.Insert(payload.Start, payload.Replacement); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	default:
		return rewrite.WorkspacePlan{}, fmt.Errorf("unknown ProductStream migration target %q", payload.Kind)
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	updated, err := rewrite.Apply(fixContext.Document.Source, edits)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if len(lsp.NewTextDocument(
		fixContext.Document.URI,
		updated,
		fixContext.Document.Version+1,
	).ParseErrors) != 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("ProductStream rewrite produced invalid PHP")
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

func resolveMigrationPHPNode(
	fixContext lsp.FixContext,
	kind phpsyntax.Kind,
) (*phpsyntax.Node, *phpsyntax.Node, error) {
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return nil, nil, err
	}
	node := ancestorNode(element, kind)
	if node == nil {
		return nil, nil, fmt.Errorf("shopware migration target is unavailable")
	}
	return node, fixContext.Document.SyntaxTree.Root, nil
}

func callTargetNameForMigrationFix(call *phpsyntax.Node) *phpsyntax.Node {
	if call == nil {
		return nil
	}
	var target *phpsyntax.Node
	for index := 0; index < call.ChildCount(); index++ {
		child, ok := call.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == phpsyntax.PhpArgumentList {
			return target
		}
		if child.Kind() == phpsyntax.PhpName {
			target = child
		}
	}
	return target
}
