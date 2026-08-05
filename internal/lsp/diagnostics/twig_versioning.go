package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
)

const (
	twigVersioningOriginalMissingCode lsp.DiagnosticID = "twig.versioning.original_missing"
	twigVersioningOutdatedCode        lsp.DiagnosticID = "twig.versioning.outdated"
	twigVersioningCommentMissingCode  lsp.DiagnosticID = "twig.versioning.comment_missing"
)

type TwigVersioningAnalyzer struct {
	twigIndexer *twig.TwigIndexer
}

func NewTwigVersioningAnalyzer(twigIndexer *twig.TwigIndexer) *TwigVersioningAnalyzer {
	return &TwigVersioningAnalyzer{twigIndexer: twigIndexer}
}

func (p *TwigVersioningAnalyzer) Analyze(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	uri := document.URI
	if filepath.Ext(uri) != ".twig" {
		return []lsp.Problem{}, nil
	}

	if p.twigIndexer == nil {
		return []lsp.Problem{}, nil
	}

	if twig.IsStorefrontTemplate(uri) {
		return []lsp.Problem{}, nil
	}

	currentFile, err := twig.ParseTwigTree(uri, document.SyntaxTree, document.LineIndex)
	if err != nil {
		return nil, err
	}

	var diagnostics []lsp.Problem

	for _, block := range currentFile.Blocks {
		if ctx.Err() != nil {
			return nil, nil
		}
		allBlockHashes, lookupErr := p.twigIndexer.GetTwigBlockHashes(block.Name)
		if lookupErr == nil {
			original := twig.FindOriginalStorefrontHashForExtends(allBlockHashes, currentFile.ExtendsFile)
			if original != nil && original.Deprecation != "" {
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    block.NameRange,
					Severity: protocol.DiagnosticSeverityWarning,
					ID:       "twig.block.deprecated",
					Source:   "shopware-lsp",
					Message:  original.Deprecation,
				})
			}
		}
		if block.VersionComment != nil {
			allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(block.Name)
			if err != nil {
				continue
			}

			originalHash := twig.FindOriginalStorefrontHashForExtends(allBlockHashes, currentFile.ExtendsFile)
			if originalHash == nil {
				lineIdx := block.Line - 1
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    diagnosticLineRange(document.LineIndex, lineIdx),
					Severity: protocol.DiagnosticSeverityWarning,
					ID:       twigVersioningOriginalMissingCode,
					Source:   "shopware-lsp",
					Message:  fmt.Sprintf("Original block not found in Storefront for block '%s'", block.Name),
				})
				continue
			}

			if originalHash.Hash != block.VersionComment.Hash {
				lineIdx := block.VersionComment.Line - 1
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    diagnosticLineRange(document.LineIndex, lineIdx),
					Severity: protocol.DiagnosticSeverityWarning,
					ID:       twigVersioningOutdatedCode,
					Source:   "shopware-lsp",
					Message:  fmt.Sprintf("The upstream block has been changed, please update the block (expected: %s, got: %s, source: %s)", truncateHash(originalHash.Hash, 12), truncateHash(block.VersionComment.Hash, 12), originalHash.RelativePath),
				})
			}
		} else {
			allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(block.Name)
			if err != nil {
				continue
			}

			originalHash := twig.FindOriginalStorefrontHashForExtends(allBlockHashes, currentFile.ExtendsFile)
			if originalHash != nil {
				lineIdx := block.Line - 1
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    diagnosticLineRange(document.LineIndex, lineIdx),
					Severity: protocol.DiagnosticSeverityWarning,
					ID:       twigVersioningCommentMissingCode,
					Source:   "shopware-lsp",
					Message:  fmt.Sprintf("The block '%s' does not have a versioning comment", block.Name),
				})
			}
		}
	}

	return diagnostics, nil
}

func diagnosticLineRange(index *cst.LineIndex, line int) cst.TextRange {
	if index == nil || line < 0 {
		return cst.TextRange{}
	}
	lineNumber := uint32(line)
	return cst.TextRange{
		Start: index.Offset(lineNumber, 0),
		End:   index.LineEnd(lineNumber),
	}
}

func truncateHash(hash string, length int) string {
	if len(hash) <= length {
		return hash
	}
	return hash[:length]
}
