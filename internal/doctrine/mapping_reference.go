package doctrine

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

type MappingReferenceRole uint8

const (
	MappingModelClass MappingReferenceRole = iota
	MappingRepositoryClass
	MappingTargetClass
	MappingEmbeddedClass
	MappingEnumClass
	MappingType
	MappingProperty
	MappingLifecycleMethod
	MappingDiscriminatorClass
	MappingDiscriminatorValue
	MappingConstraintField
	MappingConstraintColumn
)

type MappingReference struct {
	Role  MappingReferenceRole
	Name  string
	Owner string
	Field string
	File  string
	Range cst.TextRange
}

// MappingReferencesInDocument returns navigable values declared by Doctrine
// metadata in PHP attributes, XML, or YAML. Values are resolved relative to
// their owning model while their ranges continue to point at source spelling.
func MappingReferencesInDocument(
	path string,
	root *cst.Node,
	source string,
) []MappingReference {
	models := ModelsInDocument(path, root, source)
	var result []MappingReference
	for _, model := range models {
		if model.Source == XMLSource || model.Source == YAMLSource {
			result = appendMappingReference(result, MappingReference{
				Role:  MappingModelClass,
				Name:  model.Class,
				Owner: model.Class,
				Range: model.NameRange,
			})
		}
		if hasMappingRange(model.RepositoryRange) || model.Repository != "" {
			result = appendMappingReference(result, MappingReference{
				Role:  MappingRepositoryClass,
				Name:  model.Repository,
				Owner: model.Class,
				Range: model.RepositoryRange,
			})
		}
		for _, field := range model.Fields {
			if model.Source == XMLSource || model.Source == YAMLSource {
				result = appendMappingReference(result, MappingReference{
					Role:  MappingProperty,
					Name:  field.Name,
					Owner: model.Class,
					Field: field.Name,
					Range: field.Range,
				})
			}
			if hasMappingRange(field.TypeRange) || field.Type != "" {
				result = appendMappingReference(result, MappingReference{
					Role:  MappingType,
					Name:  field.Type,
					Owner: model.Class,
					Field: field.Name,
					Range: field.TypeRange,
				})
			}
			if hasMappingRange(field.RelationRange) || field.Relation != "" {
				result = appendMappingReference(result, MappingReference{
					Role:  MappingTargetClass,
					Name:  field.Relation,
					Owner: model.Class,
					Field: field.Name,
					Range: field.RelationRange,
				})
			}
			if hasMappingRange(field.EmbeddedRange) || field.EmbeddedClass != "" {
				result = appendMappingReference(result, MappingReference{
					Role:  MappingEmbeddedClass,
					Name:  field.EmbeddedClass,
					Owner: model.Class,
					Field: field.Name,
					Range: field.EmbeddedRange,
				})
			}
			if hasMappingRange(field.EnumTypeRange) || field.EnumType != "" {
				result = appendMappingReference(result, MappingReference{
					Role:  MappingEnumClass,
					Name:  field.EnumType,
					Owner: model.Class,
					Field: field.Name,
					Range: field.EnumTypeRange,
				})
			}
		}
		for _, callback := range model.Callbacks {
			result = appendMappingReference(result, MappingReference{
				Role:  MappingLifecycleMethod,
				Name:  callback.Method,
				Owner: model.Class,
				Range: callback.Range,
			})
		}
		for _, mapping := range model.DiscriminatorMap {
			result = appendMappingReference(result, MappingReference{
				Role:  MappingDiscriminatorValue,
				Name:  mapping.Value,
				Owner: model.Class,
				Range: mapping.ValueRange,
			})
			result = appendMappingReference(result, MappingReference{
				Role:  MappingDiscriminatorClass,
				Name:  mapping.Class,
				Owner: model.Class,
				Field: mapping.Value,
				Range: mapping.ClassRange,
			})
		}
		for _, constraint := range model.TableConstraints {
			for _, field := range constraint.Fields {
				result = appendMappingReference(
					result,
					MappingReference{
						Role:  MappingConstraintField,
						Name:  field.Name,
						Owner: model.Class,
						Field: field.Name,
						Range: field.Range,
					},
				)
			}
			for _, column := range constraint.Columns {
				result = appendMappingReference(
					result,
					MappingReference{
						Role:  MappingConstraintColumn,
						Name:  column.Name,
						Owner: model.Class,
						Field: modelConstraintColumnField(
							model,
							column.Name,
						),
						Range: column.Range,
					},
				)
			}
		}
	}
	for position := range result {
		result[position].File = path
	}
	sort.SliceStable(result, func(left, right int) bool {
		leftWidth := result[left].Range.Len()
		rightWidth := result[right].Range.Len()
		if leftWidth != rightWidth {
			return leftWidth < rightWidth
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func modelConstraintColumnField(
	model Model,
	column string,
) string {
	for _, field := range model.Fields {
		name := field.Column
		if name == "" {
			name = field.Name
		}
		if strings.EqualFold(name, column) {
			return field.Name
		}
	}
	return ""
}

func MappingReferenceAt(
	path string,
	root *cst.Node,
	source string,
	offset uint32,
) (MappingReference, bool) {
	for _, reference := range MappingReferencesInDocument(
		path,
		root,
		source,
	) {
		if mappingRangeContainsCursor(reference.Range, offset) {
			return reference, true
		}
	}
	if reference, found := emptyYAMLMappingReferenceAt(
		path,
		root,
		source,
		offset,
	); found {
		return reference, true
	}
	if reference, found := xmlMappingReferenceAt(
		path,
		root,
		offset,
	); found {
		return reference, true
	}
	return MappingReference{}, false
}

func xmlMappingReferenceAt(
	path string,
	root *xmlsyntax.Node,
	offset uint32,
) (MappingReference, bool) {
	if strings.ToLower(filepath.Ext(path)) != ".xml" || root == nil {
		return MappingReference{}, false
	}
	attribute := xmlquery.AttributeAt(root.NodeAtOffset(offset))
	if attribute == nil {
		return MappingReference{}, false
	}
	rng := xmlValueRange(attribute)
	if !mappingRangeContainsCursor(rng, offset) {
		return MappingReference{}, false
	}
	element := xmlquery.ElementAt(attribute)
	if element == nil || doctrineXMLMappingRoot(element) == nil {
		return MappingReference{}, false
	}
	elementName := strings.ToLower(xmlquery.ElementName(element))
	attributeName := strings.ToLower(xmlquery.AttributeName(attribute))
	owner := xmlMappingOwner(element)
	role := MappingReferenceRole(255)
	switch attributeName {
	case "repository-class", "repositoryclass":
		if elementName == "entity" || elementName == "document" {
			role = MappingRepositoryClass
		}
	case "target-entity", "target-document":
		role = MappingTargetClass
	case "enum-type", "enumtype":
		role = MappingEnumClass
	case "class":
		switch elementName {
		case "embedded", "embed-one", "embed-many":
			role = MappingEmbeddedClass
		case "discriminator-mapping":
			role = MappingDiscriminatorClass
		}
	case "type":
		if elementName == "field" || elementName == "id" {
			role = MappingType
		}
	case "method":
		if elementName == "lifecycle-callback" {
			role = MappingLifecycleMethod
		}
	case "field":
		switch elementName {
		case "one-to-one", "one-to-many", "many-to-one", "many-to-many",
			"reference-one", "reference-many", "embed-one", "embed-many":
			role = MappingProperty
		}
	case "name":
		switch elementName {
		case "entity", "document", "mapped-superclass", "embeddable",
			"embedded-document":
			role = MappingModelClass
		case "embedded":
			if parent := xmlquery.ParentElement(element); parent != nil &&
				strings.EqualFold(
					xmlquery.ElementName(parent),
					"doctrine-mapping",
				) {
				role = MappingModelClass
			} else {
				role = MappingProperty
			}
		case "field", "id":
			role = MappingProperty
		}
	case "value":
		if elementName == "discriminator-mapping" {
			role = MappingDiscriminatorValue
		}
	}
	if role == MappingReferenceRole(255) {
		return MappingReference{}, false
	}
	name := strings.TrimSpace(xmlquery.AttributeValue(attribute))
	switch role {
	case MappingRepositoryClass,
		MappingTargetClass,
		MappingEmbeddedClass,
		MappingEnumClass,
		MappingDiscriminatorClass:
		name = resolveMappingClass(name, owner)
	case MappingModelClass:
		name = normalizeClass(name)
	}
	return MappingReference{
		Role:  role,
		Name:  name,
		Owner: owner,
		File:  path,
		Range: rng,
	}, true
}

func doctrineXMLMappingRoot(element *xmlsyntax.Node) *xmlsyntax.Node {
	for current := element; current != nil; current = xmlquery.ParentElement(current) {
		if isDoctrineMappingRootName(xmlquery.ElementName(current)) {
			return current
		}
	}
	return nil
}

func xmlMappingOwner(element *xmlsyntax.Node) string {
	for current := element; current != nil; current = xmlquery.ParentElement(current) {
		switch strings.ToLower(xmlquery.ElementName(current)) {
		case "entity", "document", "mapped-superclass", "embeddable",
			"embedded-document":
			return normalizeClass(xmlquery.AttributeValue(
				xmlquery.Attribute(current, "name"),
			))
		case "embedded":
			parent := xmlquery.ParentElement(current)
			if parent != nil && strings.EqualFold(
				xmlquery.ElementName(parent),
				"doctrine-mapping",
			) {
				return normalizeClass(xmlquery.AttributeValue(
					xmlquery.Attribute(current, "name"),
				))
			}
		}
	}
	return ""
}

func emptyYAMLMappingReferenceAt(
	path string,
	root *cst.Node,
	source string,
	offset uint32,
) (MappingReference, bool) {
	extension := strings.ToLower(filepath.Ext(path))
	if (extension != ".yaml" && extension != ".yml") ||
		root == nil || int(offset) > len(source) {
		return MappingReference{}, false
	}
	lineStart := strings.LastIndex(source[:offset], "\n") + 1
	line := source[lineStart:offset]
	trimmed := strings.TrimSpace(line)
	separator := strings.LastIndex(trimmed, ":")
	if separator < 0 {
		return MappingReference{}, false
	}
	key := strings.TrimSpace(trimmed[:separator])
	value := strings.TrimSpace(trimmed[separator+1:])
	if strings.Trim(value, `"'`) != "" {
		return MappingReference{}, false
	}
	role := MappingReferenceRole(255)
	switch strings.ToLower(strings.ReplaceAll(key, "-", "")) {
	case "repositoryclass":
		role = MappingRepositoryClass
	case "targetentity", "targetdocument":
		role = MappingTargetClass
	case "enumtype":
		role = MappingEnumClass
	case "class":
		role = MappingEmbeddedClass
	case "type":
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent >= 4 {
			role = MappingType
		}
	}
	if role == MappingReferenceRole(255) {
		pairPath := yamlquery.PairPath(root.NodeAtOffset(offset))
		if len(pairPath) >= 2 &&
			strings.EqualFold(
				strings.ReplaceAll(pairPath[len(pairPath)-2], "-", ""),
				"discriminatorMap",
			) {
			role = MappingDiscriminatorClass
		}
	}
	if role == MappingReferenceRole(255) {
		return MappingReference{}, false
	}
	owner := ""
	for _, model := range ModelsInDocument(path, root, source) {
		if mappingRangeContainsCursor(model.Range, offset) {
			owner = model.Class
			break
		}
	}
	return MappingReference{
		Role:  role,
		Owner: owner,
		File:  path,
		Range: cst.TextRange{Start: offset, End: offset},
	}, true
}

func appendMappingReference(
	target []MappingReference,
	reference MappingReference,
) []MappingReference {
	if reference.Range.Start == 0 && reference.Range.End == 0 &&
		reference.Name == "" {
		return target
	}
	return append(target, reference)
}

func mappingRangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	if rng.Start == 0 && rng.End == 0 {
		return false
	}
	return offset >= rng.Start && offset <= rng.End
}

func hasMappingRange(rng cst.TextRange) bool {
	return rng.Start != 0 || rng.End != 0
}

func IsMappingDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".xml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}
