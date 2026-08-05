package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingTwigMacroCode lsp.DiagnosticID = "twig.macro.missing"

type TwigMacroAnalyzer struct {
	index *twig.TwigIndexer
}

func NewTwigMacroAnalyzer(
	index *twig.TwigIndexer,
) *TwigMacroAnalyzer {
	return &TwigMacroAnalyzer{index: index}
}

func (p *TwigMacroAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		strings.ToLower(filepath.Ext(document.URI)) != ".twig" {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	currentMacros := twig.MacrosInDocument(
		path,
		document.SyntaxTree.Root,
	)
	var result []lsp.Problem
	for _, reference := range twig.MacroReferencesInDocument(
		path,
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if reference.Role != twig.MacroUsageReference {
			continue
		}
		found, candidates, err := p.resolve(
			path,
			currentMacros,
			reference,
		)
		if err != nil {
			return nil, err
		}
		if found {
			continue
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Twig macro '%s' not found",
				reference.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "twig",
			ID:       missingTwigMacroCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					reference.Name,
					candidates,
				),
			},
		})
	}
	return result, nil
}

func (p *TwigMacroAnalyzer) resolve(
	path string,
	currentMacros []twig.Macro,
	reference twig.MacroReference,
) (bool, []string, error) {
	currentTemplates := twig.TemplateNames(path)
	names := make(map[string]string)
	for _, template := range reference.Templates {
		macros, err := p.index.GetMacros(template)
		if err != nil {
			return false, nil, err
		}
		for _, macro := range macros {
			if macro.FilePath == path &&
				containsTwigDiagnosticTemplate(
					currentTemplates,
					template,
				) {
				continue
			}
			names[strings.ToLower(macro.Name)] = macro.Name
		}
		if containsTwigDiagnosticTemplate(currentTemplates, template) {
			for _, macro := range currentMacros {
				names[strings.ToLower(macro.Name)] = macro.Name
			}
		}
	}
	_, found := names[strings.ToLower(reference.Name)]
	candidates := make([]string, 0, len(names))
	for _, name := range names {
		candidates = append(candidates, name)
	}
	sort.Slice(candidates, func(left, right int) bool {
		return strings.ToLower(candidates[left]) <
			strings.ToLower(candidates[right])
	})
	return found, candidates, nil
}

func containsTwigDiagnosticTemplate(
	values []string,
	candidate string,
) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
