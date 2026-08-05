package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

const twigComponentFactoryService = "ux.twig_component.component_factory"

// ContainerTwigComponent is the final runtime metadata emitted for a Symfony
// UX Twig Component by the compiled dependency-injection container.
type ContainerTwigComponent struct {
	Name               string
	Class              string
	Template           string
	TemplateFromMethod string
	Path               string
	NameRange          cst.TextRange
	ClassRange         cst.TextRange
	TemplateRange      cst.TextRange
}

func ParseXMLTwigComponents(
	path string,
	data []byte,
) []ContainerTwigComponent {
	tree := xmlparser.Parse(string(data)).Tree
	return ParseXMLTwigComponentsTree(path, tree)
}

func ParseXMLTwigComponentsTree(
	path string,
	tree *xmlsyntax.Tree,
) []ContainerTwigComponent {
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []ContainerTwigComponent
	byName := make(map[string]int)
	for _, service := range xmlquery.Elements(tree.Root, "service") {
		if xmlquery.AttributeValue(
			xmlquery.Attribute(service, "id"),
		) != twigComponentFactoryService {
			continue
		}
		arguments := xmlquery.ChildElements(service, "argument")
		if len(arguments) < 5 {
			continue
		}
		for _, component := range xmlquery.ChildElements(
			arguments[4],
			"argument",
		) {
			name := strings.TrimSpace(xmlquery.AttributeValue(
				xmlquery.Attribute(component, "key"),
			))
			nameRange := xmlAttributeContentRange(
				xmlquery.Attribute(component, "key"),
			)
			values := make(map[string]string)
			ranges := make(map[string]cst.TextRange)
			for _, value := range xmlquery.ChildElements(
				component,
				"argument",
			) {
				key := strings.TrimSpace(xmlquery.AttributeValue(
					xmlquery.Attribute(value, "key"),
				))
				if key == "" {
					continue
				}
				values[key], ranges[key] = xmlElementText(value)
			}
			if configured := strings.TrimSpace(values["key"]); configured != "" {
				name = configured
				nameRange = ranges["key"]
			}
			if name == "" {
				continue
			}
			entry := ContainerTwigComponent{
				Name:               name,
				Class:              normalizeContainerComponentClass(values["class"]),
				Template:           strings.TrimSpace(values["template"]),
				TemplateFromMethod: strings.TrimSpace(values["template_from_method"]),
				Path:               path,
				NameRange:          nameRange,
				ClassRange:         ranges["class"],
				TemplateRange:      ranges["template"],
			}
			if index, exists := byName[name]; exists {
				result[index] = entry
			} else {
				byName[name] = len(result)
				result = append(result, entry)
			}
		}
		if len(arguments) < 6 {
			continue
		}
		for _, mapping := range xmlquery.ChildElements(
			arguments[5],
			"argument",
		) {
			class := normalizeContainerComponentClass(
				xmlquery.AttributeValue(
					xmlquery.Attribute(mapping, "key"),
				),
			)
			name, nameRange := xmlElementText(mapping)
			if class == "" || name == "" {
				continue
			}
			if index, exists := byName[name]; exists {
				if result[index].Class == "" {
					result[index].Class = class
					result[index].ClassRange = xmlAttributeContentRange(
						xmlquery.Attribute(mapping, "key"),
					)
				}
				continue
			}
			byName[name] = len(result)
			result = append(result, ContainerTwigComponent{
				Name:      name,
				Class:     class,
				Path:      path,
				NameRange: nameRange,
				ClassRange: xmlAttributeContentRange(
					xmlquery.Attribute(mapping, "key"),
				),
			})
		}
	}
	return result
}

func normalizeContainerComponentClass(value string) string {
	return strings.Trim(strings.TrimSpace(value), `\`)
}
