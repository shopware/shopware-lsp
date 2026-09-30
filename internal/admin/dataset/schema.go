package dataset

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

var arraySelectorPattern = regexp.MustCompile(`^\[\d+\]$`)

// ResolveRoot maps a dataset id to its root entity. A literal publishData
// property path wins. Otherwise the id suffix after "__" is matched to an
// indexed entity name, converting camelCase to snake_case.
func ResolveRoot(
	definitions []dal.Definition,
	datasetID, propertyPath string,
) (string, bool) {
	schema := newEntitySchema(definitions)
	if propertyPath != "" {
		if entity, found := schema.walkPropertyPath(propertyPath); found {
			return entity, true
		}
	}
	suffix := datasetID
	if separator := strings.LastIndex(datasetID, "__"); separator >= 0 {
		suffix = datasetID[separator+2:]
	}
	return schema.matchEntity(suffix)
}

// RequiredEntities returns every entity reached by literal selectors, including
// the entity that owns each traversed field and each association target.
// resolved is false when a selector leaves the indexed schema.
func RequiredEntities(
	definitions []dal.Definition,
	root string,
	selectors []string,
) (entities []string, resolved bool) {
	schema := newEntitySchema(definitions)
	if root == "" || !schema.has(root) {
		return nil, false
	}
	resolved = true
	var found []string
	for _, selector := range selectors {
		parts, ok := selectorParts(selector)
		if !ok {
			resolved = false
			continue
		}
		reached, known := schema.selectorEntities(root, parts)
		if !known {
			resolved = false
		}
		found = append(found, reached...)
	}
	return uniqueSorted(found), resolved
}

type entitySchema struct {
	fields  map[string]map[string]dal.Field
	classes map[string]string
}

func newEntitySchema(definitions []dal.Definition) entitySchema {
	schema := entitySchema{
		fields:  map[string]map[string]dal.Field{},
		classes: map[string]string{},
	}
	shortCount := map[string]int{}
	shortEntity := map[string]string{}
	for _, definition := range definitions {
		if definition.Name == "" || definition.Kind == dal.DefinitionKindUnresolved {
			continue
		}
		fields := schema.fields[definition.Name]
		if fields == nil {
			fields = map[string]dal.Field{}
			schema.fields[definition.Name] = fields
		}
		for _, field := range definition.Fields {
			if field.Name == "" {
				continue
			}
			existing, exists := fields[field.Name]
			if !exists || (existing.TargetClass == "" && field.TargetClass != "") {
				fields[field.Name] = field
			}
		}
		if className := strings.Trim(definition.FullyQualifiedClass, `\`); className != "" {
			schema.classes[className] = definition.Name
		}
		if definition.Class != "" {
			shortCount[definition.Class]++
			shortEntity[definition.Class] = definition.Name
		}
	}
	for className, count := range shortCount {
		if count == 1 {
			if _, exists := schema.classes[className]; !exists {
				schema.classes[className] = shortEntity[className]
			}
		}
	}
	return schema
}

func (schema entitySchema) has(name string) bool {
	_, found := schema.fields[name]
	return found
}

func (schema entitySchema) field(entity, name string) (dal.Field, bool) {
	field, found := schema.fields[entity][name]
	return field, found
}

func (schema entitySchema) matchEntity(name string) (string, bool) {
	if schema.has(name) {
		return name, true
	}
	snake := camelToSnake(name)
	if snake != name && schema.has(snake) {
		return snake, true
	}
	return "", false
}

func (schema entitySchema) walkPropertyPath(path string) (string, bool) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || parts[0] == "" {
		return "", false
	}
	entity, found := schema.matchEntity(parts[0])
	if !found {
		return "", false
	}
	for _, part := range parts[1:] {
		if part == "" || isArraySelector(part) {
			return "", false
		}
		field, ok := schema.field(entity, part)
		if !ok || !field.Association {
			return "", false
		}
		target, ok := schema.associationTarget(entity, field)
		if !ok {
			return "", false
		}
		entity = target
	}
	return entity, true
}

func (schema entitySchema) selectorEntities(
	root string,
	parts []string,
) ([]string, bool) {
	current := root
	var found []string
	for index := 0; index < len(parts); {
		part := parts[index]
		if isArraySelector(part) {
			index++
			continue
		}
		field, ok := schema.field(current, part)
		if !ok {
			return found, false
		}
		final := index == len(parts)-1
		nextArray := index+1 < len(parts) && isArraySelector(parts[index+1])
		if !field.Association {
			if final || nextArray {
				found = append(found, current)
			}
			return found, true
		}
		target, ok := schema.associationTarget(current, field)
		if !ok {
			found = append(found, current)
			return found, false
		}
		found = append(found, current, target)
		if final {
			return found, true
		}
		current = target
		if nextArray {
			index += 2
			continue
		}
		index++
	}
	return found, true
}

func (schema entitySchema) associationTarget(
	entity string,
	field dal.Field,
) (string, bool) {
	target := strings.Trim(field.TargetClass, `\`)
	if name, found := schema.classes[target]; found && target != "" {
		return name, true
	}
	if separator := strings.LastIndex(target, `\`); separator >= 0 {
		target = target[separator+1:]
		if name, found := schema.classes[target]; found {
			return name, true
		}
	}
	switch field.Type {
	case "ParentAssociationField", "ChildrenAssociationField":
		if entity != "" {
			return entity, true
		}
	}
	return "", false
}

func selectorParts(selector string) ([]string, bool) {
	if strings.TrimSpace(selector) == "" || strings.Contains(selector, "${") {
		return nil, false
	}
	parts := strings.Split(selector, ".")
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
	}
	return parts, true
}

func isArraySelector(part string) bool {
	return part == "*" || arraySelectorPattern.MatchString(part)
}

func camelToSnake(name string) string {
	var builder strings.Builder
	builder.Grow(len(name) + 4)
	for index, value := range name {
		if unicode.IsUpper(value) {
			if index > 0 {
				builder.WriteByte('_')
			}
			builder.WriteRune(unicode.ToLower(value))
			continue
		}
		builder.WriteRune(value)
	}
	return builder.String()
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || (len(result) > 0 && value == result[len(result)-1]) {
			continue
		}
		result = append(result, value)
	}
	return result
}
