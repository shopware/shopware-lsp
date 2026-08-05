package symfony

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// Service represents a Symfony service definition.
type Service struct {
	ID              string
	Class           string
	AliasTarget     string
	Decorates       string
	Parent          string
	Autowire        bool
	AutowireSet     bool
	Deprecated      bool
	Deprecation     string
	Tags            map[string]string
	InstanceofTags  map[string]string
	Path            string
	Line            int
	Range           cst.TextRange
	IDRange         cst.TextRange
	ClassRange      cst.TextRange
	DecoratesRange  cst.TextRange
	ParentRange     cst.TextRange
	DeprecatedRange cst.TextRange
}

// Parameter represents a Symfony container parameter.
type Parameter struct {
	Name  string
	Value string
	Path  string
	Line  int
}

// ParseXMLServices parses Symfony XML service definitions. Malformed editor
// input is tolerated and any structurally complete definitions are returned.
func ParseXMLServices(path string, data []byte) ([]Service, []Parameter, error) {
	tree := xmlparser.Parse(string(data)).Tree
	return ParseXMLServicesTree(path, tree, xmlsyntax.NewLineIndex(tree.Source))
}

func ParseXMLServicesTree(path string, tree *xmlsyntax.Tree, lineIndex *xmlsyntax.LineIndex) ([]Service, []Parameter, error) {
	services, parameters, _, err := parseXMLServiceConfigTree(
		path,
		tree,
		lineIndex,
	)
	return services, parameters, err
}

func parseXMLServiceConfigTree(
	path string,
	tree *xmlsyntax.Tree,
	lineIndex *xmlsyntax.LineIndex,
) ([]Service, []Parameter, []ServicePrototype, error) {
	if tree == nil || tree.Root == nil {
		return []Service{}, []Parameter{}, []ServicePrototype{}, nil
	}
	containers := xmlquery.Elements(tree.Root, "container")
	if len(containers) == 0 {
		return []Service{}, []Parameter{}, []ServicePrototype{}, nil
	}

	services := make([]Service, 0, 50)
	parameters := make([]Parameter, 0, 20)
	prototypes := make([]ServicePrototype, 0, 4)

	for _, child := range xmlquery.ChildElements(containers[0]) {
		switch xmlquery.ElementName(child) {
		case "service":
			if service := processXMLService(
				child,
				lineIndex,
				path,
				false,
				false,
			); service.ID != "" {
				services = append(services, service)
			}
		case "alias":
			if alias := processXMLAlias(child, lineIndex, path); alias.ID != "" {
				services = append(services, alias)
			}
		case "services":
			defaultAutowire, defaultAutowireSet := xmlServiceAutowire(
				xmlquery.ChildElement(child, "defaults"),
			)
			for _, serviceNode := range xmlquery.ChildElements(child, "service") {
				if service := processXMLService(
					serviceNode,
					lineIndex,
					path,
					defaultAutowire,
					defaultAutowireSet,
				); service.ID != "" {
					services = append(services, service)
				}
			}
			for _, aliasNode := range xmlquery.ChildElements(child, "alias") {
				if alias := processXMLAlias(aliasNode, lineIndex, path); alias.ID != "" {
					services = append(services, alias)
				}
			}
			for _, prototypeNode := range xmlquery.ChildElements(
				child,
				"prototype",
			) {
				if prototype := processXMLPrototype(
					prototypeNode,
					lineIndex,
					path,
					defaultAutowire,
					defaultAutowireSet,
				); prototype.Namespace != "" &&
					prototype.Resource != "" {
					prototypes = append(prototypes, prototype)
				}
			}
		case "parameters":
			for _, parameterNode := range xmlquery.ChildElements(child, "parameter") {
				if parameter := processXMLParameter(parameterNode, lineIndex, path); parameter.Name != "" {
					parameters = append(parameters, parameter)
				}
			}
		case "parameter":
			if parameter := processXMLParameter(child, lineIndex, path); parameter.Name != "" {
				parameters = append(parameters, parameter)
			}
		}
	}

	return services, parameters, prototypes, nil
}

func processXMLService(
	node *xmlsyntax.Node,
	lineIndex *xmlsyntax.LineIndex,
	path string,
	defaultAutowire bool,
	defaultAutowireSet bool,
) Service {
	attributes := xmlquery.AttributeValues(node)
	id := attributes["id"]
	if id == "" || strings.Contains(id, " ") {
		return Service{}
	}

	class := attributes["class"]
	classRange := xmlAttributeContentRange(
		xmlquery.Attribute(node, "class"),
	)
	if class == "" && attributes["alias"] == "" {
		class = id
		classRange = xmlAttributeContentRange(
			xmlquery.Attribute(node, "id"),
		)
	}
	deprecatedValue, deprecatedRange := xmlServiceDeprecation(
		node,
		attributes,
	)

	service := Service{
		ID:              id,
		Class:           class,
		AliasTarget:     attributes["alias"],
		Decorates:       attributes["decorates"],
		Parent:          attributes["parent"],
		Autowire:        defaultAutowire,
		AutowireSet:     defaultAutowireSet,
		Deprecated:      deprecatedConfigValue(deprecatedValue),
		Deprecation:     deprecationMessage(deprecatedValue),
		Tags:            make(map[string]string, 5),
		Path:            path,
		Line:            xmlLine(lineIndex, node),
		Range:           node.RangeTrimmedTrivia(),
		IDRange:         xmlAttributeContentRange(xmlquery.Attribute(node, "id")),
		ClassRange:      classRange,
		DecoratesRange:  xmlAttributeContentRange(xmlquery.Attribute(node, "decorates")),
		ParentRange:     xmlAttributeContentRange(xmlquery.Attribute(node, "parent")),
		DeprecatedRange: deprecatedRange,
	}
	if value, exists := attributes["autowire"]; exists {
		service.Autowire, _ = configuredServiceBool(value)
		service.AutowireSet = true
	}
	for _, tag := range xmlquery.ChildElements(node, "tag") {
		if name := xmlquery.AttributeValue(xmlquery.Attribute(tag, "name")); name != "" {
			service.Tags[name] = ""
		}
	}
	return service
}

func deprecatedConfigValue(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" &&
		!strings.EqualFold(value, "false") &&
		value != "0"
}

func deprecationMessage(value string) string {
	value = strings.TrimSpace(value)
	if !deprecatedConfigValue(value) ||
		strings.EqualFold(value, "true") ||
		strings.EqualFold(value, "null") ||
		value == "~" ||
		value == "1" {
		return ""
	}
	return value
}

func xmlServiceDeprecation(
	node *xmlsyntax.Node,
	attributes map[string]string,
) (string, cst.TextRange) {
	value := attributes["deprecated"]
	valueRange := xmlAttributeContentRange(
		xmlquery.Attribute(node, "deprecated"),
	)
	if deprecatedNode := xmlquery.ChildElement(
		node,
		"deprecated",
	); deprecatedNode != nil {
		if text, textRange := xmlElementText(deprecatedNode); text != "" {
			return text, textRange
		}
		return "true", deprecatedNode.RangeTrimmedTrivia()
	}
	return value, valueRange
}

func processXMLAlias(node *xmlsyntax.Node, lineIndex *xmlsyntax.LineIndex, path string) Service {
	attributes := xmlquery.AttributeValues(node)
	if attributes["id"] == "" || attributes["service"] == "" {
		return Service{}
	}
	deprecatedValue, deprecatedRange := xmlServiceDeprecation(
		node,
		attributes,
	)
	return Service{
		ID:              attributes["id"],
		AliasTarget:     attributes["service"],
		Deprecated:      deprecatedConfigValue(deprecatedValue),
		Deprecation:     deprecationMessage(deprecatedValue),
		Path:            path,
		Line:            xmlLine(lineIndex, node),
		Range:           node.RangeTrimmedTrivia(),
		IDRange:         xmlAttributeContentRange(xmlquery.Attribute(node, "id")),
		DeprecatedRange: deprecatedRange,
	}
}

func processXMLPrototype(
	node *xmlsyntax.Node,
	lineIndex *xmlsyntax.LineIndex,
	path string,
	defaultAutowire bool,
	defaultAutowireSet bool,
) ServicePrototype {
	namespaceAttribute := xmlquery.Attribute(node, "namespace")
	resourceAttribute := xmlquery.Attribute(node, "resource")
	excludeAttribute := xmlquery.Attribute(node, "exclude")
	namespace := strings.TrimPrefix(
		strings.TrimSpace(xmlquery.AttributeValue(namespaceAttribute)),
		"\\",
	)
	resource := serviceConfigPath(
		path,
		xmlquery.AttributeValue(resourceAttribute),
	)
	prototype := ServicePrototype{
		Namespace:      namespace,
		Resource:       resource,
		Autowire:       defaultAutowire,
		AutowireSet:    defaultAutowireSet,
		Tags:           make(map[string]string),
		Path:           path,
		Line:           xmlLine(lineIndex, node),
		Range:          node.RangeTrimmedTrivia(),
		NamespaceRange: xmlAttributeContentRange(namespaceAttribute),
		ResourceRange:  xmlAttributeContentRange(resourceAttribute),
	}
	if value := xmlquery.Attribute(node, "autowire"); value != nil {
		prototype.Autowire, _ = configuredServiceBool(
			xmlquery.AttributeValue(value),
		)
		prototype.AutowireSet = true
	}
	if exclude := strings.TrimSpace(
		xmlquery.AttributeValue(excludeAttribute),
	); exclude != "" {
		prototype.Excludes = []string{serviceConfigPath(path, exclude)}
	}
	for _, tag := range xmlquery.ChildElements(node, "tag") {
		if name := xmlquery.AttributeValue(
			xmlquery.Attribute(tag, "name"),
		); name != "" {
			prototype.Tags[name] = ""
		}
	}
	return prototype
}

func xmlServiceAutowire(node *xmlsyntax.Node) (bool, bool) {
	if node == nil {
		return false, false
	}
	attribute := xmlquery.Attribute(node, "autowire")
	if attribute == nil {
		return false, false
	}
	value, _ := configuredServiceBool(xmlquery.AttributeValue(attribute))
	return value, true
}

func processXMLParameter(node *xmlsyntax.Node, lineIndex *xmlsyntax.LineIndex, path string) Parameter {
	attributes := xmlquery.AttributeValues(node)
	name := attributes["key"]
	if name == "" {
		return Parameter{}
	}

	value := ""
	switch {
	case attributes["type"] == "service" && attributes["id"] != "":
		value = "@" + attributes["id"]
	case attributes["value"] != "":
		value = attributes["value"]
	default:
		value = strings.TrimSpace(xmlquery.TextContent(node))
	}

	return Parameter{
		Name:  name,
		Value: value,
		Path:  path,
		Line:  xmlLine(lineIndex, node),
	}
}

func xmlLine(lineIndex *xmlsyntax.LineIndex, node *xmlsyntax.Node) int {
	line, _ := lineIndex.Position(node.Range().Start)
	return int(line) + 1
}

func xmlAttributeContentRange(attribute *xmlsyntax.Node) cst.TextRange {
	if attribute == nil {
		return cst.TextRange{}
	}
	for child := range attribute.ChildNodes() {
		if child.Kind() != xmlsyntax.XmlAttributeValue {
			continue
		}
		rng := child.RangeTrimmedTrivia()
		text := strings.TrimSpace(child.Text())
		if len(text) >= 2 &&
			(text[0] == '\'' || text[0] == '"') &&
			text[len(text)-1] == text[0] &&
			rng.End > rng.Start+1 {
			rng.Start++
			rng.End--
		}
		return rng
	}
	return cst.TextRange{}
}

func serviceConfigPath(configPath, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), value))
}
