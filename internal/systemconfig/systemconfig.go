package systemconfig

import (
	"strings"

	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// SystemConfigField represents a field in the system config XML.
type SystemConfigField struct {
	Name      string
	Label     string
	Type      string
	Component string
	FilePath  string
	Line      uint32
}

// IsSystemConfigXML checks if the XML file is a system config XML file.
func IsSystemConfigXML(content []byte) bool {
	return strings.Contains(string(content), "SystemConfig/Schema/config.xsd")
}

// GetSystemConfigFieldName extracts the name from a system config field node.
func GetSystemConfigFieldName(node *xmlsyntax.Node, _ []byte) string {
	element := fieldElement(node)
	if element == nil {
		return ""
	}
	name := xmlquery.ChildElement(element, "n", "name")
	if name == nil {
		return ""
	}
	return strings.TrimSpace(xmlquery.TextContent(name))
}

// GetSystemConfigFieldLabel extracts the untranslated/default label.
func GetSystemConfigFieldLabel(node *xmlsyntax.Node, _ []byte) string {
	element := fieldElement(node)
	if element == nil {
		return ""
	}
	for _, label := range xmlquery.ChildElements(element, "label") {
		if xmlquery.Attribute(label, "lang") != nil {
			continue
		}
		return strings.TrimSpace(xmlquery.TextContent(label))
	}
	return ""
}

// GetSystemConfigFieldType extracts the type from an input-field node.
func GetSystemConfigFieldType(node *xmlsyntax.Node, _ []byte) string {
	element := fieldElement(node)
	if element == nil || xmlquery.ElementName(element) != "input-field" {
		return ""
	}
	return xmlquery.AttributeValue(xmlquery.Attribute(element, "type"))
}

// GetSystemConfigComponent extracts the component name from a component node.
func GetSystemConfigComponent(node *xmlsyntax.Node, _ []byte) string {
	element := fieldElement(node)
	if element == nil || xmlquery.ElementName(element) != "component" {
		return ""
	}
	return xmlquery.AttributeValue(xmlquery.Attribute(element, "name"))
}

// ParseSystemConfigField parses a system config field node.
func ParseSystemConfigField(node *xmlsyntax.Node, content []byte, filePath string) SystemConfigField {
	return parseSystemConfigField(node, content, filePath, xmlsyntax.NewLineIndex(string(content)))
}

func parseSystemConfigField(node *xmlsyntax.Node, content []byte, filePath string, lineIndex *xmlsyntax.LineIndex) SystemConfigField {
	field := SystemConfigField{FilePath: filePath}
	element := fieldElement(node)
	if element == nil {
		return field
	}

	line, _ := lineIndex.Position(element.Range().Start)
	field.Line = line + 1
	field.Name = GetSystemConfigFieldName(element, content)
	field.Label = GetSystemConfigFieldLabel(element, content)

	switch xmlquery.ElementName(element) {
	case "input-field":
		field.Type = GetSystemConfigFieldType(element, content)
	case "component":
		field.Component = GetSystemConfigComponent(element, content)
	}

	return field
}

// FindAllSystemConfigFields finds all input-field and component elements.
func FindAllSystemConfigFields(root *xmlsyntax.Node, content []byte, filePath string) []SystemConfigField {
	return findAllSystemConfigFields(root, content, filePath, xmlsyntax.NewLineIndex(string(content)))
}

func findAllSystemConfigFields(root *xmlsyntax.Node, content []byte, filePath string, lineIndex *xmlsyntax.LineIndex) []SystemConfigField {
	nodes := xmlquery.Elements(root, "input-field", "component")
	fields := make([]SystemConfigField, 0, len(nodes))
	for _, node := range nodes {
		field := parseSystemConfigField(node, content, filePath, lineIndex)
		if field.Name != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

func fieldElement(node *xmlsyntax.Node) *xmlsyntax.Node {
	if node == nil {
		return nil
	}
	if element := xmlquery.ElementAt(node); element != nil {
		return element
	}
	if node.Kind() == xmlsyntax.XmlDocument {
		elements := xmlquery.Elements(node)
		if len(elements) > 0 {
			return elements[0]
		}
	}
	return nil
}
