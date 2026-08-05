package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingTwigRenderBlockCode lsp.DiagnosticID = "twig.template.block.missing"

type TwigRenderBlockAnalyzer struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigRenderBlockAnalyzer(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigRenderBlockAnalyzer {
	return &TwigRenderBlockAnalyzer{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigRenderBlockAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.twigIndex == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		!strings.EqualFold(filepath.Ext(document.URI), ".php") {
		return nil, nil
	}
	references := twig.RenderBlockReferencesInPHP(document.SyntaxTree.Root)
	if len(references) == 0 {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	ctx = p.phpIndex.AddDocumentContext(
		ctx,
		path,
		document.Version,
		document.SyntaxTree.Root,
		document.SyntaxTree.Root,
	)
	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		if reference.Template == "" || reference.Block == "" ||
			!twig.ValidateRenderBlockReference(
				ctx,
				reference,
				p.phpIndex,
				document.Text,
			) {
			continue
		}
		blocks, err := p.twigIndex.GetTemplateBlocks(reference.Template)
		if err != nil {
			return nil, err
		}
		found := false
		names := make(map[string]string)
		for _, block := range blocks {
			names[strings.ToLower(block.Name)] = block.Name
			if strings.EqualFold(block.Name, reference.Block) {
				found = true
			}
		}
		if found || len(blocks) == 0 {
			continue
		}
		candidates := make([]string, 0, len(names))
		for _, name := range names {
			candidates = append(candidates, name)
		}
		result = append(result, lsp.Problem{
			Range: reference.BlockRange,
			Message: fmt.Sprintf(
				"Twig block '%s' not found in template '%s'",
				reference.Block,
				reference.Template,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "twig",
			ID:       missingTwigRenderBlockCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					reference.Block,
					candidates,
				),
			},
		})
	}
	return result, nil
}
