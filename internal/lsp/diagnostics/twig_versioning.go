package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	TwigVersioningOriginalMissingCode lsp.DiagnosticID = "twig.versioning.original_missing"
	TwigVersioningOutdatedCode        lsp.DiagnosticID = "twig.versioning.outdated"
	TwigVersioningCommentMissingCode  lsp.DiagnosticID = "twig.versioning.comment_missing"
	TwigBlockDeprecatedCode           lsp.DiagnosticID = "twig.block.deprecated"
	TwigBlockRedundantOverrideCode    lsp.DiagnosticID = "twig.block.redundant_override"
)

type TwigVersioningPayload struct {
	BlockName       string `json:"blockName"`
	HasComment      bool   `json:"hasComment,omitempty"`
	CoreDiff        bool   `json:"coreDiff,omitempty"`
	RecordedVersion string `json:"recordedVersion,omitempty"`
	UpstreamPath    string `json:"upstreamPath,omitempty"`
}

type TwigVersioningAnalyzer struct {
	versioning *twig.VersioningService
}

func NewTwigVersioningAnalyzer(
	versioning *twig.VersioningService,
) *TwigVersioningAnalyzer {
	return &TwigVersioningAnalyzer{versioning: versioning}
}

func (p *TwigVersioningAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || filepath.Ext(document.URI) != ".twig" ||
		p == nil || p.versioning == nil {
		return []lsp.Problem{}, nil
	}
	filePath, err := uriutil.Path(document.URI)
	if err != nil {
		filePath = document.URI
	}
	if twig.IsUpstreamTemplate(filePath) {
		return []lsp.Problem{}, nil
	}
	currentFile, err := twig.ParseTwigTree(
		filePath,
		document.SyntaxTree,
		document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	if currentFile.ExtendsFile == "" {
		return []lsp.Problem{}, nil
	}
	resolutions, err := p.versioning.ResolveBlocks(*currentFile)
	if err != nil {
		return nil, err
	}
	blockBodies := twig.BlockBodies(document.SyntaxTree.Root, document.SourceString())

	problems := make([]lsp.Problem, 0, len(currentFile.Blocks))
	for _, block := range currentFile.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolution := resolutions[block.Name]
		payload := TwigVersioningPayload{BlockName: block.Name}
		if len(resolution.Candidates) != 0 {
			payload.UpstreamPath = resolution.Candidates[0].AbsolutePath
		}

		for _, candidate := range resolution.Candidates {
			if candidate.Deprecation == "" {
				continue
			}
			problems = append(problems, lsp.Problem{
				Range:   block.NameRange,
				ID:      TwigBlockDeprecatedCode,
				Message: candidate.Deprecation,
				Tags:    []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
				Payload: payload,
				RelatedInformation: []protocol.DiagnosticRelatedInformation{
					blockRelatedInformation(candidate, "Deprecated upstream block"),
				},
			})
			break
		}

		body, hasBody := blockBodies[block.Range]
		if hasBody && twig.IsParentDelegation(body.Text) {
			continue
		}
		parent, redundant := twig.RedundantBlockOverride(block, resolution)
		if redundant && hasBody && strings.TrimSpace(body.Text) != "" {
			payload.UpstreamPath = parent.AbsolutePath
			problems = append(problems, lsp.Problem{
				Range: block.NameRange, ID: TwigBlockRedundantOverrideCode,
				Message: fmt.Sprintf(
					"The block %q duplicates its resolved parent; delegate to parent() instead",
					block.Name,
				),
				Payload: payload,
				RelatedInformation: []protocol.DiagnosticRelatedInformation{
					blockRelatedInformation(parent, "Identical parent block"),
				},
			})
			continue
		}

		if len(resolution.Candidates) == 0 {
			if block.HasVersioningComment && resolution.ParentResolved {
				locations, locationsErr := p.versioning.OtherLocations(block.Name, filePath)
				if locationsErr != nil {
					return nil, locationsErr
				}
				related := make([]protocol.DiagnosticRelatedInformation, 0, len(locations))
				for _, location := range locations {
					related = append(related, blockRelatedInformation(
						location,
						"A block with the same name still exists here",
					))
				}
				message := fmt.Sprintf(
					"The upstream block %q has been removed; check whether this override is still needed",
					block.Name,
				)
				if len(locations) != 0 {
					message = fmt.Sprintf(
						"The upstream block %q was removed from this template but still exists elsewhere",
						block.Name,
					)
				}
				problems = append(problems, lsp.Problem{
					Range: block.NameRange, ID: TwigVersioningOriginalMissingCode,
					Message: message, Payload: payload, RelatedInformation: related,
				})
			}
			continue
		}

		if !block.HasVersioningComment {
			problems = append(problems, lsp.Problem{
				Range: block.NameRange, ID: TwigVersioningCommentMissingCode,
				Message: fmt.Sprintf(
					"The block %q does not have a versioning comment", block.Name,
				),
				Payload: payload,
			})
			continue
		}
		payload.HasComment = true
		if block.VersionComment == nil {
			rng := block.NameRange
			if block.VersionCommentRange != nil {
				rng = *block.VersionCommentRange
			}
			problems = append(problems, lsp.Problem{
				Range: rng, ID: TwigVersioningCommentMissingCode,
				Message: "The Twig block versioning comment is malformed",
				Payload: payload,
			})
			continue
		}
		payload.RecordedVersion = block.VersionComment.Version
		matched := false
		for _, candidate := range resolution.Candidates {
			if candidate.Hash == block.VersionComment.Hash {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		selected := resolution.Candidates[0]
		payload.CoreDiff = twig.IsStorefrontTemplate(selected.AbsolutePath) &&
			block.VersionComment.Version != ""
		problems = append(problems, lsp.Problem{
			Range: block.VersionComment.Range, ID: TwigVersioningOutdatedCode,
			Message: fmt.Sprintf(
				"The upstream block has changed; review %s and update the versioning comment",
				selected.RelativePath,
			),
			Payload: payload,
			RelatedInformation: []protocol.DiagnosticRelatedInformation{
				blockRelatedInformation(selected, "Current upstream block"),
			},
		})
	}
	return problems, nil
}

func blockRelatedInformation(
	block twig.TwigBlockHash,
	message string,
) protocol.DiagnosticRelatedInformation {
	line := block.Line - 1
	if line < 0 {
		line = 0
	}
	return protocol.DiagnosticRelatedInformation{
		Location: protocol.Location{
			URI: uriutil.FileURI(block.AbsolutePath),
			Range: protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			},
		},
		Message: message,
	}
}
