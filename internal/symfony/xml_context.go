package symfony

import (
	"slices"
	"strings"

	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

var xmlTaggedArgumentTypes = []string{"tagged_iterator", "tagged_locator", "tagged"}

func XMLServiceIsServiceID(node *xmlsyntax.Node) bool {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil {
		return false
	}
	name := xmlquery.AttributeName(attribute)
	element := xmlquery.ElementAt(attribute)
	return element != nil &&
		xmlquery.ElementName(element) == "service" &&
		(name == "id" || name == "class")
}

func XMLServiceIsServiceReference(node *xmlsyntax.Node) bool {
	_, ok := XMLServiceReferenceName(node)
	return ok
}

func XMLServiceReferenceName(node *xmlsyntax.Node) (string, bool) {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil {
		return "", false
	}
	element := xmlquery.ElementAt(attribute)
	if element == nil {
		return "", false
	}
	name := xmlquery.AttributeName(attribute)
	elementName := xmlquery.ElementName(element)
	switch {
	case elementName == "argument" && name == "id" &&
		xmlquery.AttributeValue(xmlquery.Attribute(element, "type")) == "service":
	case elementName == "alias" && name == "service":
	case elementName == "service" && name == "decorates":
	case elementName == "factory" && name == "service":
	default:
		return "", false
	}
	value := strings.TrimSpace(xmlquery.AttributeValue(attribute))
	if value == "" || value == "@" || value == "@?" || value == "@!" {
		return "", true
	}
	if normalized, ok := NormalizeServiceReference(value); ok {
		return normalized, true
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "@"))
	return value, value != "" && !strings.ContainsAny(value, "%${}")
}

func XMLServiceReferenceOptional(node *xmlsyntax.Node) bool {
	element := xmlquery.ElementAt(node)
	if element == nil {
		return false
	}
	switch strings.ToLower(
		xmlquery.AttributeValue(xmlquery.Attribute(element, "on-invalid")),
	) {
	case "ignore", "null", "ignore_uninitialized":
		return true
	default:
		return false
	}
}

func XMLServiceIsArgumentTag(node *xmlsyntax.Node) bool {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil || xmlquery.AttributeName(attribute) != "tag" {
		return false
	}
	element := xmlquery.ElementAt(attribute)
	if element == nil || xmlquery.ElementName(element) != "argument" {
		return false
	}
	argumentType := xmlquery.AttributeValue(xmlquery.Attribute(element, "type"))
	return slices.Contains(xmlTaggedArgumentTypes, argumentType)
}

func XMLServiceIsTagElement(node *xmlsyntax.Node) bool {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil || xmlquery.AttributeName(attribute) != "name" {
		return false
	}
	element := xmlquery.ElementAt(attribute)
	return element != nil && xmlquery.ElementName(element) == "tag"
}

func XMLServiceIsParameterReference(node *xmlsyntax.Node) bool {
	element := xmlquery.ElementAt(node)
	return element != nil &&
		xmlquery.ElementName(element) == "argument" &&
		strings.Contains(xmlquery.TextContent(element), "%")
}

func XMLCurrentServiceID(node *xmlsyntax.Node) string {
	for element := xmlquery.ElementAt(node); element != nil; element = xmlquery.ParentElement(element) {
		if xmlquery.ElementName(element) == "service" {
			return xmlquery.AttributeValue(xmlquery.Attribute(element, "id"))
		}
	}
	return ""
}

func XMLContextValue(node *xmlsyntax.Node) string {
	return xmlquery.NodeValue(node)
}

func XMLParameterReferenceName(node *xmlsyntax.Node) string {
	element := xmlquery.ElementAt(node)
	if element == nil {
		return ""
	}
	value := xmlquery.TextContent(element)
	start := strings.Index(value, "%")
	if start < 0 {
		return ""
	}
	end := strings.Index(value[start+1:], "%")
	if end < 0 {
		return ""
	}
	return value[start+1 : start+1+end]
}

func XMLClassReferenceName(node *xmlsyntax.Node) (string, bool) {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil || xmlquery.AttributeName(attribute) != "class" {
		return "", false
	}
	element := xmlquery.ElementAt(attribute)
	if element == nil || xmlquery.ElementName(element) != "service" {
		return "", false
	}
	value := strings.TrimSpace(xmlquery.AttributeValue(attribute))
	return value, value != "" && strings.Contains(value, "\\") &&
		!strings.ContainsAny(value, "%${}")
}
