package symfony

import (
	"sort"
	"strings"

	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ParseXMLDoctrineNamespaceAliasesTree extracts the legacy ORM and ODM
// namespace maps emitted into compiled Symfony containers. Doctrine accepts
// these maps in shortcuts such as AcmeShopBundle:Product.
func ParseXMLDoctrineNamespaceAliasesTree(
	root *xmlsyntax.Node,
) map[string][]string {
	result := make(map[string][]string)
	for _, call := range xmlquery.Elements(root, "call") {
		method := strings.ToLower(strings.TrimSpace(
			xmlquery.AttributeValue(xmlquery.Attribute(call, "method")),
		))
		if method != "setentitynamespaces" &&
			method != "setdocumentnamespaces" {
			continue
		}
		service := doctrineNamespaceService(call, method)
		if service == nil {
			continue
		}
		for _, argument := range xmlquery.Elements(call, "argument") {
			alias := strings.TrimSpace(
				xmlquery.AttributeValue(xmlquery.Attribute(argument, "key")),
			)
			namespace := strings.Trim(
				strings.TrimSpace(xmlquery.TextContent(argument)),
				`\`,
			)
			if alias == "" || namespace == "" {
				continue
			}
			result[alias] = appendUniqueDoctrineNamespace(
				result[alias],
				namespace,
			)
		}
	}
	for alias := range result {
		sort.Slice(result[alias], func(left, right int) bool {
			return strings.ToLower(result[alias][left]) <
				strings.ToLower(result[alias][right])
		})
	}
	return result
}

func doctrineNamespaceService(
	node *xmlsyntax.Node,
	method string,
) *xmlsyntax.Node {
	for current := xmlquery.ParentElement(node); current != nil; current = xmlquery.ParentElement(current) {
		if strings.EqualFold(xmlquery.ElementName(current), "service") &&
			doctrineNamespaceServiceMatches(
				xmlquery.AttributeValue(xmlquery.Attribute(current, "id")),
				method,
			) {
			return current
		}
	}
	return nil
}

func doctrineNamespaceServiceMatches(id, method string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	switch method {
	case "setentitynamespaces":
		return strings.HasPrefix(id, "doctrine.orm.")
	case "setdocumentnamespaces":
		return strings.HasPrefix(id, "doctrine_mongodb.odm.") ||
			strings.HasPrefix(id, "doctrine_couchdb.odm.")
	default:
		return false
	}
}

func appendUniqueDoctrineNamespace(values []string, candidate string) []string {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return values
		}
	}
	return append(values, candidate)
}
