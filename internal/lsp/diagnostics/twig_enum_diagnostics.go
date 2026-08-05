package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twig"
)

const (
	missingTwigEnumCode lsp.DiagnosticID = "twig.enum.missing"
	invalidTwigEnumCode lsp.DiagnosticID = "twig.enum.invalid"
)

type TwigEnumAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewTwigEnumAnalyzer(
	phpIndex *php.PHPIndex,
) *TwigEnumAnalyzer {
	return &TwigEnumAnalyzer{phpIndex: phpIndex}
}

func (p *TwigEnumAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		strings.ToLower(filepath.Ext(document.URI)) != ".twig" {
		return nil, nil
	}
	var enumNames []string
	var result []lsp.Problem
	for _, reference := range twig.EnumReferences(document.SyntaxTree.Root) {
		if ctx.Err() != nil {
			return nil, nil
		}
		symbol, found := p.phpIndex.FindClass(reference.Name)
		switch {
		case !found:
			if enumNames == nil {
				enumNames = p.enumNames()
			}
			result = append(result, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"PHP enum '%s' not found",
					reference.Name,
				),
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "twig",
				ID:       missingTwigEnumCode,
				Payload: map[string]any{
					"suggestions": escapeTwigEnumSuggestions(
						suggestion.Similar(
							reference.Name,
							enumNames,
						),
					),
				},
			})
		case symbol.Kind != semantic.EnumSymbol:
			result = append(result, lsp.Problem{
				Range: reference.Range,
				Message: fmt.Sprintf(
					"PHP class '%s' is not an enum",
					reference.Name,
				),
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "twig",
				ID:       invalidTwigEnumCode,
			})
		}
	}
	return result, nil
}

func (p *TwigEnumAnalyzer) enumNames() []string {
	var result []string
	for _, symbol := range p.phpIndex.ClassSymbolsView() {
		if symbol.Kind == semantic.EnumSymbol {
			result = append(
				result,
				strings.TrimPrefix(symbol.FullyQualified, `\`),
			)
		}
	}
	return result
}

func escapeTwigEnumSuggestions(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, twig.EscapeTwigClassName(value))
	}
	return result
}
