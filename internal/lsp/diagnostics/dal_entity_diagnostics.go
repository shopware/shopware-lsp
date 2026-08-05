package diagnostics

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

// DALEntityAnalyzer validates only close misspellings of statically named DAL
// entities. The PHP definition catalog is intentionally not assumed complete:
// runtime custom entities are valid, so an unknown name without a strong
// indexed suggestion remains untouched.
type DALEntityAnalyzer struct {
	index *dal.Index
}

func NewDALEntityAnalyzer(index *dal.Index) *DALEntityAnalyzer {
	return &DALEntityAnalyzer{index: index}
}

func (p *DALEntityAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	definitions, err := p.index.Definitions()
	if err != nil || len(definitions) == 0 {
		return nil, err
	}
	known := make(map[string]struct{}, len(definitions))
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		if _, duplicate := known[definition.Name]; duplicate {
			continue
		}
		known[definition.Name] = struct{}{}
		names = append(names, definition.Name)
	}
	sort.Strings(names)
	var result []lsp.Problem
	for _, literal := range jsquery.Nodes(
		document.SyntaxTree.Root, jssyntax.JsString,
	) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reference, found := dal.JSEntityReferenceAt(literal)
		if !found || reference.Kind == dal.JSEntityReferenceDefinitionHas {
			continue
		}
		if _, exists := known[reference.Name]; exists {
			continue
		}
		suggestions := adminNearbySuggestions(reference.Name, names)
		if len(suggestions) == 0 {
			continue
		}
		result = append(result, lsp.Problem{
			ID:       "shopware.dal.entity-not-found",
			Range:    javaScriptStringContentRange(literal, document.Text),
			Element:  literal,
			Message:  fmt.Sprintf("Shopware DAL entity '%s' is not indexed", reference.Name),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
			Payload: map[string]any{
				"entityName":  reference.Name,
				"suggestions": suggestions,
			},
		})
	}
	return result, nil
}
