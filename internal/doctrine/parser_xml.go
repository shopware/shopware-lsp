package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

func parseXMLModels(path string, root *xmlsyntax.Node) []Model {
	if root == nil {
		return nil
	}
	var result []Model
	for _, element := range xmlquery.Elements(
		root,
		"entity",
		"mapped-superclass",
		"embeddable",
		"document",
		"embedded-document",
		"embedded",
	) {
		if !isDoctrineXMLModelElement(element) {
			continue
		}
		if strings.EqualFold(xmlquery.ElementName(element), "embedded") {
			parent := xmlquery.ParentElement(element)
			if parent == nil || !strings.EqualFold(
				xmlquery.ElementName(parent),
				"doctrine-mapping",
			) {
				continue
			}
		}
		classAttribute := xmlquery.Attribute(element, "name")
		className := normalizeClass(xmlquery.AttributeValue(classAttribute))
		if className == "" {
			continue
		}
		model := Model{
			Class: className,
			Repository: resolveMappingClass(
				xmlquery.AttributeValue(
					xmlquery.Attribute(element, "repository-class"),
				),
				className,
			),
			Table: xmlquery.AttributeValue(xmlquery.Attribute(element, "table")),
			InheritanceType: firstNonEmpty(
				xmlquery.AttributeValue(
					xmlquery.Attribute(element, "inheritance-type"),
				),
				xmlquery.AttributeValue(
					xmlquery.Attribute(element, "inheritanceType"),
				),
			),
			Kind:      xmlModelKind(xmlquery.ElementName(element)),
			Source:    XMLSource,
			File:      path,
			Range:     element.RangeTrimmedTrivia(),
			NameRange: xmlValueRange(classAttribute),
		}
		if repository := xmlquery.Attribute(element, "repository-class"); repository != nil {
			model.RepositoryRange = xmlValueRange(repository)
		}
		model.Fields = xmlModelFields(path, model.Class, element)
		model.Callbacks = xmlLifecycleCallbacks(path, model.Class, element)
		if column := xmlquery.ChildElement(
			element,
			"discriminator-column",
		); column != nil {
			model.DiscriminatorColumn = xmlquery.AttributeValue(
				xmlquery.Attribute(column, "name"),
			)
			model.DiscriminatorType = xmlquery.AttributeValue(
				xmlquery.Attribute(column, "type"),
			)
		}
		model.DiscriminatorMap = xmlDiscriminatorMap(
			path,
			model.Class,
			element,
		)
		model.TableConstraints = xmlTableConstraints(
			path,
			element,
		)
		result = append(result, normalizeModel(model))
	}
	return result
}

func xmlTableConstraints(
	path string,
	model *xmlsyntax.Node,
) []TableConstraint {
	var result []TableConstraint
	for _, group := range xmlquery.ChildElements(model) {
		groupName := strings.ToLower(xmlLocalName(group))
		kind := IndexConstraint
		entryName := "index"
		switch groupName {
		case "indexes":
		case "unique-constraints":
			kind = UniqueConstraint
			entryName = "unique-constraint"
		default:
			continue
		}
		for _, entry := range xmlquery.ChildElements(
			group,
			entryName,
		) {
			nameAttribute := xmlquery.Attribute(entry, "name")
			constraint := TableConstraint{
				Name:      xmlquery.AttributeValue(nameAttribute),
				Kind:      kind,
				File:      path,
				NameRange: xmlValueRange(nameAttribute),
				Fields: xmlCommaSeparatedReferences(
					xmlquery.Attribute(entry, "fields"),
				),
				Columns: xmlCommaSeparatedReferences(
					xmlquery.Attribute(entry, "columns"),
				),
			}
			result = append(result, constraint)
		}
	}
	return result
}

func xmlCommaSeparatedReferences(
	attribute *xmlsyntax.Node,
) []TableConstraintReference {
	if attribute == nil {
		return nil
	}
	value := xmlquery.AttributeValue(attribute)
	valueRange := xmlValueRange(attribute)
	if value == "" {
		return []TableConstraintReference{{
			Range: cst.TextRange{
				Start: valueRange.Start,
				End:   valueRange.Start,
			},
		}}
	}
	var result []TableConstraintReference
	start := 0
	for start <= len(value) {
		end := strings.IndexByte(value[start:], ',')
		if end < 0 {
			end = len(value)
		} else {
			end += start
		}
		raw := value[start:end]
		leading := len(raw) - len(strings.TrimLeft(raw, " \t\r\n"))
		trailing := len(raw) - len(strings.TrimRight(raw, " \t\r\n"))
		nameStart := start + leading
		nameEnd := end - trailing
		result = append(result, TableConstraintReference{
			Name: strings.TrimSpace(raw),
			Range: cst.TextRange{
				Start: valueRange.Start + uint32(nameStart),
				End:   valueRange.Start + uint32(nameEnd),
			},
		})
		if end == len(value) {
			break
		}
		start = end + 1
	}
	return result
}

func isDoctrineXMLModelElement(element *xmlsyntax.Node) bool {
	if element == nil {
		return false
	}
	parent := xmlquery.ParentElement(element)
	return parent != nil &&
		isDoctrineMappingRootName(xmlquery.ElementName(parent))
}

func isDoctrineMappingRootName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		name = name[separator+1:]
	}
	parts := strings.Split(name, "-")
	if len(parts) < 2 || parts[0] != "doctrine" ||
		parts[len(parts)-1] != "mapping" {
		return false
	}
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			return false
		}
	}
	return true
}

func xmlDiscriminatorMap(
	path,
	className string,
	model *xmlsyntax.Node,
) []DiscriminatorMapping {
	mapping := xmlquery.ChildElement(model, "discriminator-map")
	if mapping == nil {
		return nil
	}
	var result []DiscriminatorMapping
	for _, entry := range xmlquery.ChildElements(
		mapping,
		"discriminator-mapping",
	) {
		valueAttribute := xmlquery.Attribute(entry, "value")
		classAttribute := xmlquery.Attribute(entry, "class")
		value := xmlquery.AttributeValue(valueAttribute)
		class := resolveMappingClass(
			xmlquery.AttributeValue(classAttribute),
			className,
		)
		if value == "" && class == "" {
			continue
		}
		result = append(result, DiscriminatorMapping{
			Value:      value,
			Class:      class,
			File:       path,
			ValueRange: xmlValueRange(valueAttribute),
			ClassRange: xmlValueRange(classAttribute),
		})
	}
	return result
}

func xmlModelKind(name string) ModelKind {
	switch strings.ToLower(name) {
	case "mapped-superclass":
		return MappedSuperclassModel
	case "embeddable":
		return EmbeddableModel
	case "embedded-document", "embedded":
		return EmbeddableModel
	case "document":
		return DocumentModel
	default:
		return EntityModel
	}
}

func xmlModelFields(
	path,
	className string,
	model *xmlsyntax.Node,
) []Field {
	var result []Field
	for _, element := range xmlquery.ChildElements(model) {
		name := strings.ToLower(xmlquery.ElementName(element))
		attributes := xmlquery.AttributeValues(element)
		switch name {
		case "id", "field":
			fieldName := attributes["name"]
			if fieldName == "" {
				fieldName = attributes["field"]
			}
			if fieldName == "" {
				continue
			}
			typeAttribute := xmlquery.Attribute(element, "type")
			enumAttribute := firstXMLAttribute(
				element,
				"enum-type",
				"enumType",
			)
			result = append(result, Field{
				Name:   fieldName,
				Column: firstNonEmpty(attributes["column"], attributes["fieldName"]),
				Type:   attributes["type"],
				EnumType: resolveMappingClass(
					firstNonEmpty(attributes["enum-type"], attributes["enumType"]),
					className,
				),
				Class: className,
				File:  path,
				Range: xmlValueRange(firstXMLAttribute(
					element,
					"name",
					"field",
				)),
				TypeRange:     xmlValueRange(typeAttribute),
				EnumTypeRange: xmlValueRange(enumAttribute),
			})
		case "one-to-one", "one-to-many", "many-to-one", "many-to-many",
			"reference-one", "reference-many":
			fieldName := firstNonEmpty(attributes["field"], attributes["name"])
			if fieldName == "" {
				continue
			}
			relationAttribute := firstXMLAttribute(
				element,
				"target-entity",
				"target-document",
			)
			result = append(result, Field{
				Name:         fieldName,
				Relation:     resolveMappingClass(firstNonEmpty(attributes["target-entity"], attributes["target-document"]), className),
				RelationType: canonicalXMLRelation(name),
				Class:        className,
				File:         path,
				Range: xmlValueRange(firstXMLAttribute(
					element,
					"field",
					"name",
				)),
				RelationRange: xmlValueRange(relationAttribute),
			})
		case "embedded", "embed-one", "embed-many":
			fieldName := firstNonEmpty(attributes["name"], attributes["field"])
			embeddedClass := resolveMappingClass(
				firstNonEmpty(attributes["class"], attributes["target-document"]),
				className,
			)
			if fieldName == "" || embeddedClass == "" {
				continue
			}
			prefix := fieldName + "_"
			if strings.EqualFold(attributes["use-column-prefix"], "false") {
				prefix = ""
			} else if configured, exists := attributes["column-prefix"]; exists {
				prefix = configured
			}
			embeddedAttribute := firstXMLAttribute(
				element,
				"class",
				"target-document",
			)
			result = append(result, Field{
				Name:          fieldName,
				RelationType:  canonicalXMLRelation(name),
				EmbeddedClass: embeddedClass,
				ColumnPrefix:  prefix,
				Class:         className,
				File:          path,
				Range: xmlValueRange(firstXMLAttribute(
					element,
					"name",
					"field",
				)),
				EmbeddedRange: xmlValueRange(embeddedAttribute),
			})
		}
	}
	return result
}

func xmlLifecycleCallbacks(
	path,
	className string,
	model *xmlsyntax.Node,
) []LifecycleCallback {
	var result []LifecycleCallback
	callbacks := xmlquery.ChildElement(model, "lifecycle-callbacks")
	if callbacks == nil {
		return nil
	}
	for _, callback := range xmlquery.ChildElements(
		callbacks,
		"lifecycle-callback",
	) {
		attributes := xmlquery.AttributeValues(callback)
		method := attributes["method"]
		event := attributes["type"]
		if method == "" || event == "" {
			continue
		}
		result = append(result, LifecycleCallback{
			Event:  event,
			Method: method,
			Class:  className,
			File:   path,
			Range:  xmlValueRange(xmlquery.Attribute(callback, "method")),
		})
	}
	return result
}

func xmlValueRange(node *xmlsyntax.Node) cst.TextRange {
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
	valueOffset := strings.Index(text, value)
	if valueOffset < 0 {
		return rng
	}
	start := rng.Start + uint32(valueOffset)
	end := start + uint32(len(value))
	if len(value) >= 1 && (value[0] == '"' || value[0] == '\'') {
		start++
		if len(value) >= 2 && value[len(value)-1] == value[0] {
			end--
		}
	}
	return cst.TextRange{Start: start, End: end}
}

func firstXMLAttribute(
	element *xmlsyntax.Node,
	names ...string,
) *xmlsyntax.Node {
	for _, name := range names {
		if attribute := xmlquery.Attribute(element, name); attribute != nil {
			return attribute
		}
	}
	return nil
}

func canonicalXMLRelation(value string) string {
	var result strings.Builder
	upper := true
	for _, character := range value {
		if character == '-' {
			upper = true
			continue
		}
		if upper {
			result.WriteString(strings.ToUpper(string(character)))
			upper = false
		} else {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func resolveMappingClass(value, owner string) string {
	value = normalizeClass(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, `\`) {
		return value
	}
	owner = normalizeClass(owner)
	if position := strings.LastIndex(owner, `\`); position >= 0 {
		return owner[:position+1] + value
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
