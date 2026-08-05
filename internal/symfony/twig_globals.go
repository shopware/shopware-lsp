package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ContainerTwigGlobal is a global registered directly on the compiled Twig
// environment service.
type ContainerTwigGlobal struct {
	Name      string
	Value     string
	ServiceID string
	Path      string
	Range     cst.TextRange
}

func ParseXMLTwigGlobals(
	path string,
	data []byte,
) []ContainerTwigGlobal {
	tree := xmlparser.Parse(string(data)).Tree
	return ParseXMLTwigGlobalsTree(path, tree)
}

func ParseXMLTwigGlobalsTree(
	path string,
	tree *xmlsyntax.Tree,
) []ContainerTwigGlobal {
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []ContainerTwigGlobal
	for _, service := range xmlquery.Elements(tree.Root, "service") {
		if xmlquery.AttributeValue(xmlquery.Attribute(service, "id")) != "twig" {
			continue
		}
		for _, call := range xmlquery.ChildElements(service, "call") {
			if xmlquery.AttributeValue(
				xmlquery.Attribute(call, "method"),
			) != "addGlobal" {
				continue
			}
			arguments := xmlquery.ChildElements(call, "argument")
			if len(arguments) != 2 {
				continue
			}
			name, rng := xmlElementText(arguments[0])
			if name == "" {
				continue
			}
			value := ContainerTwigGlobal{
				Name:  name,
				Path:  path,
				Range: rng,
			}
			attributes := xmlquery.AttributeValues(arguments[1])
			if attributes["id"] != "" &&
				(attributes["type"] == "service" ||
					attributes["type"] == "") {
				value.ServiceID = attributes["id"]
			} else {
				value.Value, _ = xmlElementText(arguments[1])
			}
			result = append(result, value)
		}
	}
	return result
}

func xmlElementText(node *xmlsyntax.Node) (string, cst.TextRange) {
	for _, text := range xmlquery.Nodes(node, xmlsyntax.XmlText) {
		if xmlquery.ElementAt(text) != node {
			continue
		}
		raw := text.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		rng := text.Range()
		left := len(raw) - len(strings.TrimLeft(raw, " \t\r\n"))
		right := len(raw) - len(strings.TrimRight(raw, " \t\r\n"))
		rng.Start += uint32(left)
		rng.End -= uint32(right)
		return trimmed, rng
	}
	return "", cst.TextRange{}
}
