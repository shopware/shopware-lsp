package security

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

const securityXMLNamespace = "symfony.com/schema/dic/security"

func parseXMLConfigOccurrences(
	path string,
	root *xmlsyntax.Node,
) []ConfigOccurrence {
	if root == nil || !strings.Contains(root.Text(), securityXMLNamespace) {
		return nil
	}

	var result []ConfigOccurrence
	for _, config := range xmlquery.Elements(root) {
		if xmlLocalName(xmlquery.ElementName(config)) != "config" {
			continue
		}
		for _, element := range xmlquery.ChildElements(config) {
			switch xmlLocalName(xmlquery.ElementName(element)) {
			case "provider":
				result = appendXMLProviderOccurrences(
					result,
					path,
					element,
				)
			case "firewall":
				result = appendXMLFirewallOccurrences(
					result,
					path,
					element,
				)
			}
		}
	}
	return uniqueConfigOccurrences(result)
}

func appendXMLProviderOccurrences(
	result []ConfigOccurrence,
	path string,
	provider *xmlsyntax.Node,
) []ConfigOccurrence {
	nameAttribute := xmlquery.Attribute(provider, "name")
	name := xmlquery.AttributeValue(nameAttribute)
	if name != "" && !strings.HasPrefix(name, "_") {
		result = append(result, ConfigOccurrence{
			Name:   name,
			Kind:   ConfigProvider,
			Role:   ConfigDeclaration,
			Origin: ConfigProviderDeclaration,
			File:   path,
			Range:  xmlConfigValueRange(nameAttribute),
		})
	}

	for _, child := range xmlquery.ChildElements(provider) {
		if xmlLocalName(xmlquery.ElementName(child)) != "chain" {
			continue
		}
		for _, reference := range xmlquery.ChildElements(child) {
			if xmlLocalName(xmlquery.ElementName(reference)) != "provider" {
				continue
			}
			value, rng := xmlConfigTextValue(reference)
			result = append(result, ConfigOccurrence{
				Name:   value,
				Kind:   ConfigProvider,
				Role:   ConfigReference,
				Origin: ConfigChainProvider,
				File:   path,
				Range:  rng,
			})
		}
	}
	return result
}

func appendXMLFirewallOccurrences(
	result []ConfigOccurrence,
	path string,
	firewall *xmlsyntax.Node,
) []ConfigOccurrence {
	nameAttribute := xmlquery.Attribute(firewall, "name")
	name := xmlquery.AttributeValue(nameAttribute)
	if name != "" && !strings.HasPrefix(name, "_") {
		result = append(result, ConfigOccurrence{
			Name:   name,
			Kind:   ConfigFirewall,
			Role:   ConfigDeclaration,
			Origin: ConfigFirewallDeclaration,
			File:   path,
			Range:  xmlConfigValueRange(nameAttribute),
		})
	}
	result = appendXMLProviderAttribute(
		result,
		path,
		firewall,
		ConfigFirewallProvider,
	)
	for _, child := range xmlquery.ChildElements(firewall) {
		origin := ConfigFirewallProvider
		if xmlLocalName(xmlquery.ElementName(child)) == "switch-user" {
			origin = ConfigSwitchUserProvider
		}
		result = appendXMLProviderAttribute(result, path, child, origin)
	}
	return result
}

func appendXMLProviderAttribute(
	result []ConfigOccurrence,
	path string,
	element *xmlsyntax.Node,
	origin ConfigOrigin,
) []ConfigOccurrence {
	attribute := xmlquery.Attribute(element, "provider")
	if attribute == nil {
		return result
	}
	return append(result, ConfigOccurrence{
		Name:   xmlquery.AttributeValue(attribute),
		Kind:   ConfigProvider,
		Role:   ConfigReference,
		Origin: origin,
		File:   path,
		Range:  xmlConfigValueRange(attribute),
	})
}

func xmlLocalName(name string) string {
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

func xmlConfigValueRange(node *xmlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	equals := strings.IndexByte(text, '=')
	if equals < 0 {
		return rng
	}
	value := strings.TrimSpace(text[equals+1:])
	offset := strings.Index(text, value)
	if offset < 0 {
		return rng
	}
	start := rng.Start + uint32(offset)
	end := start + uint32(len(value))
	if len(value) >= 1 && (value[0] == '"' || value[0] == '\'') {
		start++
		if len(value) >= 2 && value[len(value)-1] == value[0] {
			end--
		}
	}
	return cst.TextRange{Start: start, End: end}
}

func xmlConfigTextValue(element *xmlsyntax.Node) (string, cst.TextRange) {
	for _, text := range xmlquery.Nodes(element, xmlsyntax.XmlText) {
		if parent := xmlquery.ElementAt(text); parent != element {
			continue
		}
		raw := text.Text()
		value := strings.TrimSpace(raw)
		rng := text.RangeTrimmedTrivia()
		if value == "" {
			return "", rng
		}
		offset := strings.Index(raw, value)
		if offset < 0 {
			return value, rng
		}
		start := rng.Start + uint32(offset)
		return value, cst.TextRange{
			Start: start,
			End:   start + uint32(len(value)),
		}
	}
	return "", element.RangeTrimmedTrivia()
}
