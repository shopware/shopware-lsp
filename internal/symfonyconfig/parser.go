package symfonyconfig

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const treeBuilderClass = "Symfony\\Component\\Config\\Definition\\Builder\\TreeBuilder"

type Root struct {
	Name  string
	Class string
	File  string
	Range cst.TextRange
}

func rootsInPHP(path string, root *phpsyntax.Node) []Root {
	if root == nil ||
		!strings.Contains(root.Text(), "getConfigTreeBuilder") ||
		!strings.Contains(root.Text(), "TreeBuilder") {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Root
	for _, class := range phpquery.Classes(root) {
		for _, method := range phpquery.Methods(class) {
			if !strings.EqualFold(
				phpquery.MethodName(method),
				"getConfigTreeBuilder",
			) {
				continue
			}
			name, rng := treeRootInMethod(method, resolver)
			if name == "" {
				continue
			}
			result = append(result, Root{
				Name: name,
				Class: strings.TrimPrefix(
					resolver.Resolve(phpquery.ClassName(class)),
					`\`,
				),
				File:  path,
				Range: rng,
			})
		}
	}
	return uniqueRoots(result)
}

func treeRootInMethod(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
) (string, cst.TextRange) {
	hasTreeBuilder := false
	for _, object := range phpquery.ObjectCreations(method) {
		if phpquery.FunctionLikeAt(object) != method ||
			!strings.EqualFold(
				strings.TrimPrefix(
					resolver.Resolve(phpquery.ObjectClassName(object)),
					`\`,
				),
				treeBuilderClass,
			) {
			continue
		}
		hasTreeBuilder = true
		node := phpquery.ArgumentExpression(object, 0)
		if value, found := staticConfigString(node); found &&
			value != "" {
			return value, phpquery.StringContentRange(node)
		}
	}
	if !hasTreeBuilder {
		return "", cst.TextRange{}
	}
	for _, call := range phpquery.Calls(method) {
		if phpquery.FunctionLikeAt(call) != method ||
			!strings.EqualFold(phpquery.CallMethodName(call), "root") {
			continue
		}
		node := phpquery.ArgumentExpression(call, 0)
		if value, found := staticConfigString(node); found &&
			value != "" {
			return value, phpquery.StringContentRange(node)
		}
	}
	return "", cst.TextRange{}
}

func staticConfigString(
	node *phpsyntax.Node,
) (string, bool) {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return "", false
	}
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 ||
		(text[0] != '\'' && text[0] != '"') ||
		text[len(text)-1] != text[0] {
		return "", false
	}
	if text[0] == '"' && strings.Contains(text[1:len(text)-1], "$") {
		return "", false
	}
	return phpquery.StringValue(node), true
}

func uniqueRoots(values []Root) []Root {
	seen := make(map[string]struct{}, len(values))
	result := make([]Root, 0, len(values))
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		key := strings.ToLower(value.Name) + ":" + value.File + ":" +
			value.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
