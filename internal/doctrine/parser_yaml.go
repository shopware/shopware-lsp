package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func parseYAMLModels(path string, root *yamlsyntax.Node) []Model {
	mapping := yamlquery.RootValue(root)
	if !yamlquery.IsMapping(mapping) {
		return nil
	}
	var result []Model
	for _, pair := range yamlquery.Pairs(mapping) {
		classNode := yamlquery.PairKey(pair)
		className := normalizeClass(yamlquery.ScalarValue(classNode))
		config := yamlquery.PairValue(pair)
		if className == "" || !yamlquery.IsMapping(config) {
			continue
		}
		kind, mapped := yamlModelKind(
			yamlquery.ScalarValue(yamlquery.Property(config, "type")),
		)
		if !mapped && !yamlMappingHasMetadata(config) {
			continue
		}
		model := Model{
			Class: className,
			Repository: resolveMappingClass(
				yamlquery.ScalarValue(
					firstYAMLProperty(
						config,
						"repositoryClass",
						"repository-class",
					),
				),
				className,
			),
			Table: yamlquery.ScalarValue(yamlquery.Property(config, "table")),
			InheritanceType: yamlquery.ScalarValue(firstYAMLProperty(
				config,
				"inheritanceType",
				"inheritance-type",
			)),
			Kind:      kind,
			Source:    YAMLSource,
			File:      path,
			Range:     pair.RangeTrimmedTrivia(),
			NameRange: yamlScalarRange(classNode),
		}
		if repository := firstYAMLProperty(
			config,
			"repositoryClass",
			"repository-class",
		); repository != nil {
			model.RepositoryRange = yamlScalarRange(repository)
		}
		model.Fields = yamlModelFields(path, className, config)
		model.Callbacks = yamlLifecycleCallbacks(path, className, config)
		if column := firstYAMLProperty(
			config,
			"discriminatorColumn",
			"discriminator-column",
		); yamlquery.IsMapping(column) {
			model.DiscriminatorColumn = yamlquery.ScalarValue(
				yamlquery.Property(column, "name"),
			)
			model.DiscriminatorType = yamlquery.ScalarValue(
				yamlquery.Property(column, "type"),
			)
		}
		model.DiscriminatorMap = yamlDiscriminatorMap(
			path,
			className,
			config,
		)
		model.TableConstraints = yamlTableConstraints(path, config)
		result = append(result, normalizeModel(model))
	}
	return result
}

func yamlTableConstraints(
	path string,
	config *yamlsyntax.Node,
) []TableConstraint {
	var result []TableConstraint
	for _, group := range []struct {
		kind  TableConstraintKind
		names []string
	}{
		{
			kind:  IndexConstraint,
			names: []string{"indexes"},
		},
		{
			kind: UniqueConstraint,
			names: []string{
				"uniqueConstraints",
				"unique-constraints",
			},
		},
	} {
		mapping := firstYAMLProperty(config, group.names...)
		if !yamlquery.IsMapping(mapping) {
			continue
		}
		for _, pair := range yamlquery.Pairs(mapping) {
			nameNode := yamlquery.PairKey(pair)
			value := yamlquery.PairValue(pair)
			if !yamlquery.IsMapping(value) {
				continue
			}
			result = append(result, TableConstraint{
				Name:      yamlquery.ScalarValue(nameNode),
				Kind:      group.kind,
				File:      path,
				NameRange: yamlScalarRange(nameNode),
				Fields: yamlConstraintReferences(
					yamlquery.Property(value, "fields"),
				),
				Columns: yamlConstraintReferences(
					yamlquery.Property(value, "columns"),
				),
			})
		}
	}
	return result
}

func yamlConstraintReferences(
	node *yamlsyntax.Node,
) []TableConstraintReference {
	if node == nil {
		return nil
	}
	if yamlquery.IsSequence(node) {
		var result []TableConstraintReference
		for _, item := range yamlquery.Items(node) {
			value := yamlquery.ItemValue(item)
			result = append(result, TableConstraintReference{
				Name:  yamlquery.ScalarValue(value),
				Range: yamlScalarRange(value),
			})
		}
		return result
	}
	value := yamlquery.ScalarValue(node)
	rng := yamlScalarRange(node)
	if value == "" {
		return []TableConstraintReference{{Range: rng}}
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
		result = append(result, TableConstraintReference{
			Name: strings.TrimSpace(raw),
			Range: cst.TextRange{
				Start: rng.Start + uint32(start+leading),
				End:   rng.Start + uint32(end-trailing),
			},
		})
		if end == len(value) {
			break
		}
		start = end + 1
	}
	return result
}

func yamlDiscriminatorMap(
	path,
	className string,
	config *yamlsyntax.Node,
) []DiscriminatorMapping {
	mapping := firstYAMLProperty(
		config,
		"discriminatorMap",
		"discriminator-map",
	)
	if !yamlquery.IsMapping(mapping) {
		return nil
	}
	var result []DiscriminatorMapping
	for _, pair := range yamlquery.Pairs(mapping) {
		valueNode := yamlquery.PairKey(pair)
		classNode := yamlquery.PairValue(pair)
		value := yamlquery.ScalarValue(valueNode)
		class := resolveMappingClass(
			yamlquery.ScalarValue(classNode),
			className,
		)
		if value == "" && class == "" {
			continue
		}
		result = append(result, DiscriminatorMapping{
			Value:      value,
			Class:      class,
			File:       path,
			ValueRange: yamlScalarRange(valueNode),
			ClassRange: yamlScalarRange(classNode),
		})
	}
	return result
}

func yamlModelKind(value string) (ModelKind, bool) {
	switch strings.ToLower(strings.ReplaceAll(value, "-", "")) {
	case "entity":
		return EntityModel, true
	case "mappedsuperclass":
		return MappedSuperclassModel, true
	case "embeddable":
		return EmbeddableModel, true
	case "document":
		return DocumentModel, true
	default:
		return EntityModel, false
	}
}

func yamlMappingHasMetadata(config *yamlsyntax.Node) bool {
	for _, property := range []string{
		"id", "fields", "oneToOne", "oneToMany", "manyToOne",
		"manyToMany", "embedded", "repositoryClass",
		"indexes", "uniqueConstraints", "unique-constraints",
	} {
		if yamlquery.Property(config, property) != nil {
			return true
		}
	}
	return false
}

func yamlModelFields(
	path,
	className string,
	config *yamlsyntax.Node,
) []Field {
	var result []Field
	for _, groupName := range []string{"id", "fields"} {
		group := yamlquery.Property(config, groupName)
		if !yamlquery.IsMapping(group) {
			continue
		}
		for _, pair := range yamlquery.Pairs(group) {
			fieldName := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			fieldConfig := yamlquery.PairValue(pair)
			if fieldName == "" {
				continue
			}
			typeNode := yamlquery.Property(fieldConfig, "type")
			enumNode := firstYAMLProperty(
				fieldConfig,
				"enumType",
				"enum-type",
			)
			result = append(result, Field{
				Name:   fieldName,
				Column: yamlquery.ScalarValue(firstYAMLProperty(fieldConfig, "column", "fieldName")),
				Type:   yamlquery.ScalarValue(yamlquery.Property(fieldConfig, "type")),
				EnumType: resolveMappingClass(
					yamlquery.ScalarValue(firstYAMLProperty(fieldConfig, "enumType", "enum-type")),
					className,
				),
				Class:         className,
				File:          path,
				Range:         yamlScalarRange(yamlquery.PairKey(pair)),
				TypeRange:     yamlScalarRange(typeNode),
				EnumTypeRange: yamlScalarRange(enumNode),
			})
		}
	}
	for _, relation := range []string{
		"oneToOne", "oneToMany", "manyToOne", "manyToMany",
		"referenceOne", "referenceMany",
	} {
		group := yamlquery.Property(config, relation)
		if !yamlquery.IsMapping(group) {
			continue
		}
		for _, pair := range yamlquery.Pairs(group) {
			fieldName := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			fieldConfig := yamlquery.PairValue(pair)
			if fieldName == "" {
				continue
			}
			relationNode := firstYAMLProperty(
				fieldConfig,
				"targetEntity",
				"targetDocument",
			)
			result = append(result, Field{
				Name: fieldName,
				Relation: resolveMappingClass(
					yamlquery.ScalarValue(firstYAMLProperty(fieldConfig, "targetEntity", "targetDocument")),
					className,
				),
				RelationType:  canonicalYAMLRelation(relation),
				Class:         className,
				File:          path,
				Range:         yamlScalarRange(yamlquery.PairKey(pair)),
				RelationRange: yamlScalarRange(relationNode),
			})
		}
	}
	for _, groupName := range []string{"embedded", "embeddeds"} {
		group := yamlquery.Property(config, groupName)
		if !yamlquery.IsMapping(group) {
			continue
		}
		for _, pair := range yamlquery.Pairs(group) {
			fieldName := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			fieldConfig := yamlquery.PairValue(pair)
			embeddedClass := resolveMappingClass(
				yamlquery.ScalarValue(yamlquery.Property(fieldConfig, "class")),
				className,
			)
			if fieldName == "" || embeddedClass == "" {
				continue
			}
			prefix := fieldName + "_"
			if prefixNode := firstYAMLProperty(fieldConfig, "columnPrefix", "column-prefix"); prefixNode != nil {
				value := yamlquery.ScalarValue(prefixNode)
				if strings.EqualFold(value, "false") {
					prefix = ""
				} else {
					prefix = value
				}
			}
			embeddedNode := yamlquery.Property(fieldConfig, "class")
			result = append(result, Field{
				Name:          fieldName,
				RelationType:  "Embedded",
				EmbeddedClass: embeddedClass,
				ColumnPrefix:  prefix,
				Class:         className,
				File:          path,
				Range:         yamlScalarRange(yamlquery.PairKey(pair)),
				EmbeddedRange: yamlScalarRange(embeddedNode),
			})
		}
	}
	return result
}

func yamlLifecycleCallbacks(
	path,
	className string,
	config *yamlsyntax.Node,
) []LifecycleCallback {
	callbacks := firstYAMLProperty(
		config,
		"lifecycleCallbacks",
		"lifecycle-callbacks",
	)
	if !yamlquery.IsMapping(callbacks) {
		return nil
	}
	var result []LifecycleCallback
	for _, pair := range yamlquery.Pairs(callbacks) {
		event := yamlquery.ScalarValue(yamlquery.PairKey(pair))
		value := yamlquery.PairValue(pair)
		switch {
		case yamlquery.IsSequence(value):
			for _, item := range yamlquery.Items(value) {
				node := yamlquery.ItemValue(item)
				method := yamlquery.ScalarValue(node)
				if method != "" {
					result = append(result, LifecycleCallback{
						Event:  event,
						Method: method,
						Class:  className,
						File:   path,
						Range:  yamlScalarRange(node),
					})
				}
			}
		case value != nil:
			method := yamlquery.ScalarValue(value)
			if method != "" {
				result = append(result, LifecycleCallback{
					Event:  event,
					Method: method,
					Class:  className,
					File:   path,
					Range:  yamlScalarRange(value),
				})
			}
		}
	}
	return result
}

func firstYAMLProperty(
	mapping *yamlsyntax.Node,
	names ...string,
) *yamlsyntax.Node {
	if !yamlquery.IsMapping(mapping) {
		return nil
	}
	for _, name := range names {
		if value := yamlquery.Property(mapping, name); value != nil {
			return value
		}
	}
	return nil
}

func yamlScalarRange(node *yamlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 1 && (text[0] == '\'' || text[0] == '"') {
		rng.Start++
		if len(text) >= 2 && text[len(text)-1] == text[0] {
			rng.End--
		}
	}
	return rng
}

func canonicalYAMLRelation(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
