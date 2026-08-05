package diagnostics

import (
	"context"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

var (
	criteriaReceiverPattern   = regexp.MustCompile(`^\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*->\s*addFilter\s*\(`)
	criteriaAssignmentPattern = regexp.MustCompile(`^\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*=\s*new\s+\\?(?:[A-Za-z_][A-Za-z0-9_]*\\)*Criteria\s*\(\s*\)\s*;?\s*$`)
)

type ShopwareCriteriaAnalyzer struct{}

func NewShopwareCriteriaAnalyzer() *ShopwareCriteriaAnalyzer {
	return &ShopwareCriteriaAnalyzer{}
}

func (*ShopwareCriteriaAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".php") {
		return nil, nil
	}
	var result []lsp.Problem
	for _, creation := range phpquery.ObjectCreations(document.SyntaxTree.Root) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		class := shortPHPName(phpquery.ObjectClassName(creation))
		if class != "EqualsFilter" && class != "EqualsAnyFilter" {
			continue
		}
		id := phpquery.StringArgument(creation, 0)
		if id == nil || phpquery.StringValue(id) != "id" {
			continue
		}
		payload := criteriaFixPayload(document, creation)
		result = append(result, lsp.Problem{
			ID:       "shopware.criteria.id-filter",
			Range:    phpquery.StringContentRange(id),
			Message:  "Pass entity IDs to the Criteria constructor instead of filtering the id field",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
			Payload:  payload,
		})
	}
	return result, nil
}

func criteriaFixPayload(document *lsp.TextDocument, filter *phpsyntax.Node) map[string]any {
	payload := map[string]any{"safe": false}
	function := phpquery.FunctionLikeAt(filter)
	statement := ancestorPHPNode(filter, phpsyntax.PhpExpressionStatement)
	if function == nil || statement == nil {
		return payload
	}
	receiverMatch := criteriaReceiverPattern.FindStringSubmatch(statement.Text())
	if len(receiverMatch) < 2 {
		return payload
	}
	value := strings.TrimSpace(phpquery.ArgumentValueText(filter, 1))
	if value == "" {
		return payload
	}
	var matchingCriteria *phpsyntax.Node
	for _, candidate := range phpquery.ObjectCreations(function) {
		if shortPHPName(phpquery.ObjectClassName(candidate)) != "Criteria" ||
			len(phpquery.Arguments(candidate)) != 0 {
			continue
		}
		assignment := ancestorPHPNode(candidate, phpsyntax.PhpExpressionStatement)
		if assignment == nil {
			continue
		}
		match := criteriaAssignmentPattern.FindStringSubmatch(assignment.Text())
		if len(match) < 2 || match[1] != receiverMatch[1] {
			continue
		}
		if matchingCriteria != nil {
			return payload
		}
		matchingCriteria = candidate
	}
	if matchingCriteria == nil {
		return payload
	}
	criteriaRange := matchingCriteria.RangeTrimmedTrivia()
	filterRange := statement.RangeTrimmedTrivia()
	if criteriaRange.End > uint32(len(document.Source)) ||
		filterRange.End > uint32(len(document.Source)) {
		return payload
	}
	return map[string]any{
		"safe":          true,
		"criteriaStart": criteriaRange.Start,
		"criteriaEnd":   criteriaRange.End,
		"filterStart":   filterRange.Start,
		"filterEnd":     filterRange.End,
		"argument":      value,
	}
}

func ancestorPHPNode(node *phpsyntax.Node, kind cst.Kind) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func shortPHPName(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), `\`)
	if separator := strings.LastIndex(name, `\`); separator >= 0 {
		return name[separator+1:]
	}
	return name
}
