package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpanalysis"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type ShopwareDecorationAnalyzer struct {
	php *php.PHPIndex
}

func NewShopwareDecorationAnalyzer(index *php.PHPIndex) *ShopwareDecorationAnalyzer {
	return &ShopwareDecorationAnalyzer{php: index}
}

func (a *ShopwareDecorationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if a == nil || a.php == nil || document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil || !strings.HasSuffix(strings.ToLower(document.URI), ".php") {
		return nil, nil
	}
	state, err := phpanalysis.ForDocument(a.php, document)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	analysis := state.Document
	var result []lsp.Problem
	for _, symbol := range analysis.Symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if symbol.Kind != semantic.MethodSymbol || symbol.Name != "__construct" {
			continue
		}
		for _, parameter := range symbol.Parameters {
			concreteName := strings.TrimPrefix(parameter.Type.Name(), `\`)
			if concreteName == "" {
				continue
			}
			concrete, found := a.php.FindClass(concreteName)
			if !found || concrete.Flags.Has(semantic.AbstractFlag) {
				continue
			}
			for _, parentName := range concrete.Extends() {
				parent, parentFound := a.php.FindClass(parentName)
				if !parentFound || !parent.Flags.Has(semantic.AbstractFlag) ||
					len(a.php.FindMethods(parent.FullyQualified, "getDecorated")) == 0 {
					continue
				}
				result = append(result, lsp.Problem{
					ID:       "shopware.decoration.abstraction",
					Range:    parameter.Range,
					Message:  "Depend on decoration abstraction " + strings.TrimPrefix(parent.FullyQualified, `\`) + " instead of concrete class " + concreteName,
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   "shopware-lsp",
					Payload: map[string]any{
						"abstractClass": strings.TrimPrefix(parent.FullyQualified, `\`),
						"concreteClass": concreteName,
					},
				})
				break
			}
		}
	}
	return result, nil
}
