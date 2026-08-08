package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/admin/twigmigration"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

type adminTwigComponentMigrationFix struct{}

func (adminTwigComponentMigrationFix) ID() lsp.FixID {
	return migrateAdminTwigComponentFixID
}

func (adminTwigComponentMigrationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.AdminTwigMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      fmt.Sprintf("Migrate %s to %s", payload.SourceTag, payload.TargetTag),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule != "" && payload.SourceTag != "" && payload.TargetTag != "", err
}

func (adminTwigComponentMigrationFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.AdminTwigMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe {
		return rewrite.WorkspacePlan{}, twigmigration.ErrUnsafe
	}
	rule, found := twigmigration.RuleByID(payload.Rule)
	if !found || rule.SourceTag != payload.SourceTag || rule.TargetTag != payload.TargetTag {
		return rewrite.WorkspacePlan{}, fmt.Errorf("administration Twig migration rule changed")
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
	node, ok := element.(*twigsyntax.Node)
	if !ok {
		return rewrite.WorkspacePlan{}, fmt.Errorf("administration Twig migration target is unavailable")
	}
	for node != nil && node.Kind() != twigsyntax.HtmlTag {
		node = node.Parent()
	}
	if node == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("administration Twig component tag is unavailable")
	}
	edits, err := twigmigration.Compile(fixContext.Document.Source, node, rule)
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
