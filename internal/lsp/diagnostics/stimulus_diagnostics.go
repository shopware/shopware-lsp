package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingStimulusControllerCode lsp.DiagnosticID = "symfony.stimulus.controller.missing"

type StimulusAnalyzer struct {
	index *stimulus.Index
}

func NewStimulusAnalyzer(
	index *stimulus.Index,
) *StimulusAnalyzer {
	return &StimulusAnalyzer{index: index}
}

func (p *StimulusAnalyzer) Analyze(
	_ context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension != ".twig" && extension != ".html" {
		return nil, nil
	}
	controllers, err := p.index.Controllers()
	if err != nil || len(controllers) == 0 {
		return nil, err
	}
	htmlNames := make([]string, 0, len(controllers))
	twigNames := make([]string, 0, len(controllers))
	known := make(map[string]struct{}, len(controllers))
	for _, controller := range controllers {
		htmlNames = append(htmlNames, controller.Name)
		twigNames = append(twigNames, controller.TwigName())
		known[strings.ToLower(controller.Name)] = struct{}{}
	}
	path, _ := uriutil.Path(document.URI)
	var result []lsp.Problem
	for _, reference := range stimulus.References(
		path,
		document.SyntaxTree.Root,
	) {
		if reference.Name == "" {
			continue
		}
		if _, exists := known[strings.ToLower(
			stimulus.NormalizeName(reference.Name),
		)]; exists {
			continue
		}
		names := htmlNames
		if reference.Twig {
			names = twigNames
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Stimulus controller '%s' not found",
				reference.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       missingStimulusControllerCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					reference.Name,
					names,
				),
			},
		})
	}
	return result, nil
}
