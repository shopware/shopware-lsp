package symfony

import (
	"html"
	"strings"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ParseXMLRoutes parses Symfony routing XML using the native lossless frontend.
func ParseXMLRoutes(filePath string, content []byte) ([]Route, error) {
	tree := xmlparser.Parse(string(content)).Tree
	return ParseXMLRoutesTree(
		filePath,
		tree,
		xmlsyntax.NewLineIndex(tree.Source),
	)
}

func ParseXMLRoutesTree(
	filePath string,
	tree *xmlsyntax.Tree,
	lineIndex *xmlsyntax.LineIndex,
) ([]Route, error) {
	if tree == nil || tree.Root == nil {
		return []Route{}, nil
	}
	roots := xmlquery.Elements(tree.Root, "routes")
	if len(roots) == 0 {
		return []Route{}, nil
	}

	var routes []Route
	for _, node := range xmlquery.ChildElements(roots[0], "route") {
		attributes := xmlquery.AttributeValues(node)
		name := attributes["id"]
		path := html.UnescapeString(attributes["path"])
		if name == "" || path == "" {
			continue
		}
		controller := attributes["controller"]
		if controller == "" {
			for _, defaultNode := range xmlquery.ChildElements(node, "default") {
				if xmlquery.AttributeValue(xmlquery.Attribute(defaultNode, "key")) == "_controller" {
					controller = strings.TrimSpace(xmlquery.TextContent(defaultNode))
					break
				}
			}
		}
		line, _ := lineIndex.Position(node.RangeTrimmedTrivia().Start)
		routes = append(routes, Route{
			Name:       name,
			Path:       path,
			Controller: controller,
			Methods: strings.FieldsFunc(attributes["methods"], func(character rune) bool {
				return character == '|' || character == ',' || character == ' '
			}),
			FilePath: filePath,
			Line:     int(line) + 1,
		})
	}
	return routes, nil
}
