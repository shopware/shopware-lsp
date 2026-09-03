package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	twigVersionCommentFixID   lsp.FixID = "twig-version-comment"
	twigBlockDiffFixID        lsp.FixID = "twig-block-diff"
	twigParentDelegationFixID lsp.FixID = "twig-parent-delegation"
)

func NewTwigVersioning(versioning *twig.VersioningService) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "shopware.twig_versioning",
			Languages: []language.ID{language.Twig},
			Problems: []lsp.ProblemDefinition{
				{
					ID: diagnostics.TwigVersioningCommentMissingCode, Source: "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityWarning, DisabledByDefault: true,
				},
				{
					ID: diagnostics.TwigBlockDeprecatedCode, Source: "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: diagnostics.TwigBlockRedundantOverrideCode, Source: "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityHint,
				},
				{
					ID: diagnostics.TwigVersioningOriginalMissingCode, Source: "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: diagnostics.TwigVersioningOutdatedCode, Source: "shopware-lsp",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
			},
		},
		analyzer: diagnostics.NewTwigVersioningAnalyzer(versioning),
		fixes: []lsp.QuickFix{
			twigVersionCommentFix{versioning: versioning},
			twigBlockDiffFix{},
			twigParentDelegationFix{versioning: versioning},
		},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			switch code {
			case diagnostics.TwigVersioningCommentMissingCode:
				return []lsp.BoundFix{lsp.BindFix(twigVersionCommentFixID, payload)}
			case diagnostics.TwigVersioningOutdatedCode:
				fixes := []lsp.BoundFix{lsp.BindFix(twigVersionCommentFixID, payload)}
				if coreDiff, _ := payload["coreDiff"].(bool); coreDiff {
					fixes = append(fixes, lsp.BindFix(twigBlockDiffFixID, payload))
				}
				return fixes
			case diagnostics.TwigBlockRedundantOverrideCode:
				return []lsp.BoundFix{lsp.BindFix(twigParentDelegationFixID, payload)}
			default:
				return nil
			}
		},
	}
}

type twigVersionCommentFix struct {
	versioning *twig.VersioningService
}

func (twigVersionCommentFix) ID() lsp.FixID { return twigVersionCommentFixID }

func (twigVersionCommentFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil || payload.BlockName == "" {
		return lsp.FixPresentation{}, false, err
	}
	update := fmt.Sprint(fixContext.Diagnostic.Code) ==
		string(diagnostics.TwigVersioningOutdatedCode)
	title := "Shopware: Add Twig block version comment"
	if update {
		title = "Shopware: Update Twig block version comment"
	}
	return lsp.FixPresentation{
		Title: title, Kind: protocol.CodeActionQuickFix,
		Preferred: !update, Resolution: lsp.FixLazy,
	}, true, nil
}

func (fix twigVersionCommentFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	if fix.versioning == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("twig versioning is unavailable")
	}
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	path, err := uriutil.Path(fixContext.Document.URI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	rng, replacement, err := fix.versioning.VersionCommentEdit(
		path,
		fixContext.Document.Source,
		payload.BlockName,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(rng, replacement); err != nil {
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

type twigParentDelegationFix struct {
	versioning *twig.VersioningService
}

func (twigParentDelegationFix) ID() lsp.FixID { return twigParentDelegationFixID }

func (twigParentDelegationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil || payload.BlockName == "" {
		return lsp.FixPresentation{}, false, err
	}
	return lsp.FixPresentation{
		Title:      "Shopware: Delegate Twig block to parent",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, true, nil
}

func (fix twigParentDelegationFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	if fix.versioning == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("twig versioning is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	path, err := uriutil.Path(fixContext.Document.URI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	_, block, resolution, err := fix.versioning.ResolveDocument(
		path,
		fixContext.Document.Source,
		payload.BlockName,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, redundant := twig.RedundantBlockOverride(block, resolution); !redundant {
		return rewrite.WorkspacePlan{}, fmt.Errorf(
			"twig block %q no longer duplicates its resolved parent",
			payload.BlockName,
		)
	}
	body, relativeBodyRange, ok := twig.ParseBlockBody(block.Text)
	if !ok || strings.TrimSpace(body) == "" || twig.IsParentDelegation(body) {
		return rewrite.WorkspacePlan{}, fmt.Errorf(
			"twig block %q no longer has replaceable duplicate content",
			payload.BlockName,
		)
	}
	bodyRange := cst.TextRange{
		Start: block.Range.Start + relativeBodyRange.Start,
		End:   block.Range.Start + relativeBodyRange.End,
	}
	if bodyRange.Start > bodyRange.End || bodyRange.End > uint32(len(fixContext.Document.Source)) {
		return rewrite.WorkspacePlan{}, fmt.Errorf(
			"twig block %q body is outside the document",
			payload.BlockName,
		)
	}
	trimmed := strings.TrimSpace(body)
	contentStart := strings.Index(body, trimmed)
	contentEnd := contentStart + len(trimmed)
	replacement := body[:contentStart] + "{{ parent() }}" + body[contentEnd:]

	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(bodyRange, replacement); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if block.VersionCommentRange != nil {
		commentRange := twigCommentRemovalRange(
			fixContext.Document.Source,
			*block.VersionCommentRange,
		)
		if err := builder.ReplaceRange(commentRange, ""); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
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

func twigCommentRemovalRange(source string, rng cst.TextRange) cst.TextRange {
	start, end := int(rng.Start), int(rng.End)
	if start < 0 || end < start || end > len(source) {
		return rng
	}
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	lineEnd := len(source)
	if offset := strings.Index(source[end:], "\n"); offset >= 0 {
		lineEnd = end + offset + 1
	}
	withoutNewlineEnd := lineEnd
	if withoutNewlineEnd > end && source[withoutNewlineEnd-1] == '\n' {
		withoutNewlineEnd--
	}
	if strings.TrimSpace(source[lineStart:start]) == "" &&
		strings.TrimSpace(source[end:withoutNewlineEnd]) == "" {
		return cst.TextRange{Start: uint32(lineStart), End: uint32(lineEnd)}
	}
	return rng
}

type twigBlockDiffFix struct{}

func (twigBlockDiffFix) ID() lsp.FixID { return twigBlockDiffFixID }

func (twigBlockDiffFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil || !payload.CoreDiff || payload.BlockName == "" {
		return lsp.FixPresentation{}, false, err
	}
	return lsp.FixPresentation{
		Title:      "Shopware: Show Twig block difference",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, true, nil
}

func (twigBlockDiffFix) BuildCommand(
	_ context.Context,
	fixContext lsp.FixContext,
) (*protocol.CommandAction, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.TwigVersioningPayload](fixContext)
	if err != nil {
		return nil, err
	}
	return &protocol.CommandAction{
		Title:   "Show Twig Block Difference",
		Command: "shopware.twig.showBlockDiff",
		Arguments: []any{
			fixContext.Document.URI,
			payload.BlockName,
		},
	}, nil
}
