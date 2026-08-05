package symfony

import (
	"strings"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

// ParseYAMLRoutes parses Symfony YAML route definitions.
func ParseYAMLRoutes(filePath string, content []byte) ([]Route, error) {
	result := yamlparser.Parse(string(content))
	return ParseYAMLRoutesTree(filePath, result.Tree, yamlsyntax.NewLineIndex(result.Tree.Source))
}

func ParseYAMLRoutesTree(filePath string, tree *yamlsyntax.Tree, lineIndex *yamlsyntax.LineIndex) ([]Route, error) {
	if tree == nil || tree.Root == nil {
		return []Route{}, nil
	}
	root := yamlquery.RootValue(tree.Root)
	if !yamlquery.IsMapping(root) {
		return []Route{}, nil
	}

	routes := make([]Route, 0, len(yamlquery.Pairs(root)))
	for _, pair := range yamlquery.Pairs(root) {
		key := yamlquery.PairKey(pair)
		name := yamlquery.ScalarValue(key)
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}

		definition := yamlquery.PairValue(pair)
		if !yamlquery.IsMapping(definition) {
			continue
		}

		pathNode := yamlquery.Property(definition, "path")
		if yamlquery.IsNull(pathNode) {
			continue
		}
		path := yamlquery.ScalarValue(pathNode)
		if path == "" {
			continue
		}
		line, _ := lineIndex.Position(key.RangeTrimmedTrivia().Start)
		routes = append(routes, Route{
			Name:       name,
			Path:       path,
			Controller: yamlquery.ScalarValue(yamlquery.Property(definition, "controller")),
			Methods:    yamlRouteMethods(yamlquery.Property(definition, "methods")),
			FilePath:   filePath,
			Line:       int(line) + 1,
		})
	}

	return routes, nil
}

func yamlRouteMethods(node *yamlsyntax.Node) []string {
	if yamlquery.IsSequence(node) {
		var methods []string
		for _, item := range yamlquery.Items(node) {
			if method := yamlquery.ScalarValue(yamlquery.ItemValue(item)); method != "" {
				methods = append(methods, method)
			}
		}
		return methods
	}
	value := yamlquery.ScalarValue(node)
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == '|' || character == ',' || character == ' '
	})
}
