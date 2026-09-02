package twig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type ExtensionUsageKind uint8

const (
	ExtensionFunctionUsage ExtensionUsageKind = iota + 1
	ExtensionFilterUsage
	ExtensionTestUsage
)

// ExtensionUsage is one exact Twig function, filter, or test name occurrence.
type ExtensionUsage struct {
	Kind     ExtensionUsageKind
	Name     string
	FilePath string
	Range    cst.TextRange
}

type ExtensionUsageCatalog struct {
	Key    string
	Usages []ExtensionUsage
}

func ExtensionUsageKey(kind ExtensionUsageKind, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%d\x00%s", kind, name)
}

// GetExtensionUsagesForPHPSymbol resolves registered Twig callbacks at query
// time. This complements the fast semantic usage catalog when a template was
// indexed before its extension declaration.
func (idx *TwigIndexer) GetExtensionUsagesForPHPSymbol(
	phpIndex *php.PHPIndex,
	symbol semantic.Symbol,
) ([]ExtensionUsage, error) {
	if idx == nil || phpIndex == nil ||
		symbol.Kind != semantic.MethodSymbol {
		return nil, nil
	}
	resolver := PHPAccessResolver{PHP: phpIndex, Twig: idx}
	type callback struct {
		kind   ExtensionUsageKind
		name   string
		path   string
		method string
	}
	var callbacks []callback
	functions, err := idx.GetAllTwigFunctions()
	if err != nil {
		return nil, err
	}
	for _, function := range functions {
		callbacks = append(callbacks, callback{
			kind:   ExtensionFunctionUsage,
			name:   function.Name,
			path:   function.FilePath,
			method: function.Method,
		})
	}
	filters, err := idx.GetAllTwigFilters()
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		callbacks = append(callbacks, callback{
			kind:   ExtensionFilterUsage,
			name:   filter.Name,
			path:   filter.FilePath,
			method: filter.Method,
		})
	}
	tests, err := idx.GetAllTwigTests()
	if err != nil {
		return nil, err
	}
	for _, test := range tests {
		callbacks = append(callbacks, callback{
			kind:   ExtensionTestUsage,
			name:   test.Name,
			path:   test.FilePath,
			method: test.Method,
		})
	}

	seenSymbols := make(map[string]struct{})
	var result []ExtensionUsage
	for _, candidate := range callbacks {
		key := ExtensionUsageKey(candidate.kind, candidate.name)
		if key == "" || candidate.method == "" {
			continue
		}
		matched := false
		for _, method := range resolver.callbackMethodSymbols(
			candidate.path,
			candidate.method,
		) {
			if method.ID == symbol.ID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if _, duplicate := seenSymbols[key]; duplicate {
			continue
		}
		seenSymbols[key] = struct{}{}
		usages, usageErr := idx.GetExtensionUsages(
			candidate.kind,
			candidate.name,
		)
		if usageErr != nil {
			return nil, usageErr
		}
		result = append(result, usages...)
	}
	return uniqueExtensionUsages(result), nil
}

func ExtensionUsagesInDocument(
	filePath string,
	root *twigsyntax.Node,
) []ExtensionUsage {
	if root == nil {
		return nil
	}
	var result []ExtensionUsage
	for call := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		if twigFunctionCallIsFilter(call) ||
			IsTestFunctionCall(call) {
			continue
		}
		operand := functionNameOperand(call)
		if operand == nil {
			continue
		}
		hasAccessor := false
		for range twigquery.IterateNodes(
			operand,
			twigsyntax.TwigAccessor,
		) {
			hasAccessor = true
			break
		}
		if hasAccessor {
			continue
		}
		names := literalNames(operand)
		if len(names) != 1 {
			continue
		}
		result = append(result, ExtensionUsage{
			Kind:     ExtensionFunctionUsage,
			Name:     names[0].name,
			FilePath: filePath,
			Range:    names[0].rng,
		})
	}
	for filterNode := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigFilter,
	) {
		name := twigquery.FilterName(filterNode)
		if name == "" {
			continue
		}
		filter, _ := twigast.CastTwigFilter(filterNode)
		operand, found := filter.Filter()
		if !found {
			continue
		}
		var nameRange cst.TextRange
		for _, candidate := range literalNames(operand.Syntax()) {
			if strings.EqualFold(candidate.name, name) {
				nameRange = candidate.rng
				break
			}
		}
		if nameRange.Len() == 0 {
			continue
		}
		result = append(result, ExtensionUsage{
			Kind:     ExtensionFilterUsage,
			Name:     name,
			FilePath: filePath,
			Range:    nameRange,
		})
	}
	for apply := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigApplyStartingBlock,
	) {
		names := literalNames(apply)
		if len(names) == 0 {
			continue
		}
		result = append(result, ExtensionUsage{
			Kind:     ExtensionFilterUsage,
			Name:     names[0].name,
			FilePath: filePath,
			Range:    names[0].rng,
		})
	}
	for _, expression := range TestExpressions(root) {
		result = append(result, ExtensionUsage{
			Kind:     ExtensionTestUsage,
			Name:     expression.Name,
			FilePath: filePath,
			Range:    expression.Range,
		})
	}
	return uniqueExtensionUsages(result)
}

func ExtensionUsageAt(
	filePath string,
	root *twigsyntax.Node,
	offset uint32,
) (ExtensionUsage, bool) {
	for _, usage := range ExtensionUsagesInDocument(filePath, root) {
		if offset >= usage.Range.Start && offset <= usage.Range.End {
			return usage, true
		}
	}
	return ExtensionUsage{}, false
}

func twigFunctionCallIsFilter(call *twigsyntax.Node) bool {
	filterNode := twigquery.ClosestNodeOfKind(
		call,
		twigsyntax.TwigFilter,
	)
	if filterNode == nil {
		return false
	}
	filter, _ := twigast.CastTwigFilter(filterNode)
	operand, found := filter.Filter()
	return found && isDescendantNode(call, operand.Syntax())
}

func uniqueExtensionUsages(usages []ExtensionUsage) []ExtensionUsage {
	seen := make(map[string]struct{}, len(usages))
	result := make([]ExtensionUsage, 0, len(usages))
	for _, usage := range usages {
		semanticKey := ExtensionUsageKey(usage.Kind, usage.Name)
		if semanticKey == "" {
			continue
		}
		key := semanticKey + "\x00" + usage.FilePath + "\x00" +
			usage.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, usage)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}
