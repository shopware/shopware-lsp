package entityschema

import (
	"fmt"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func importDefinitionBehavior(
	methods []*phpsyntax.Node,
	resolve func(string) string,
	lookup RelationLookup,
	translation bool,
) *DefinitionBehaviorSpec {
	behavior := &DefinitionBehaviorSpec{}
	for _, method := range methods {
		switch phpquery.MethodName(method) {
		case "getParentDefinitionClass":
			if translation {
				continue
			}
			if class := returnedImportedClass(method, resolve); class != "" {
				behavior.ParentDefinitionClass = class
			} else {
				behavior.ParentDefinitionMethodRaw = strings.TrimSpace(method.Text())
			}
		case "isVersionAware":
			if value, literal := importedBooleanReturn(method); literal {
				behavior.VersionAware = boolPointer(value)
			} else {
				behavior.VersionAwareMethodRaw = strings.TrimSpace(method.Text())
			}
		case "isInheritanceAware":
			if translation {
				continue
			}
			if _, literal := importedBooleanReturn(method); !literal {
				behavior.InheritanceAwareMethodRaw = strings.TrimSpace(method.Text())
			}
		case "defaultFields":
			behavior.OverrideDefaultFields = true
			if fields, literal := importedDefaultFields(method, resolve, lookup); literal {
				behavior.DefaultFields = fields
			} else {
				behavior.DefaultFieldsMethodRaw = strings.TrimSpace(method.Text())
			}
		case "getBaseFields":
			behavior.OverrideBaseFields = true
			if fields, literal := importedDefinitionFields(method, resolve, lookup, "base-field"); literal {
				behavior.BaseFields = fields
			} else {
				behavior.BaseFieldsMethodRaw = strings.TrimSpace(method.Text())
			}
		case "getRestrictDeleteMetaFields":
			if translation {
				continue
			}
			if properties, literal := importedRestrictDeleteProperties(method); literal {
				behavior.RestrictDeleteMetaProperties = properties
			} else {
				behavior.RestrictDeleteMetaMethodRaw = strings.TrimSpace(method.Text())
			}
		}
	}
	if definitionBehaviorEmpty(behavior) {
		return nil
	}
	return behavior
}

func boolPointer(value bool) *bool {
	return &value
}

func importedDefaultFields(method *phpsyntax.Node, resolve func(string) string, lookup RelationLookup) ([]FieldSpec, bool) {
	return importedDefinitionFields(method, resolve, lookup, "default-field")
}

func importedDefinitionFields(
	method *phpsyntax.Node,
	resolve func(string) string,
	lookup RelationLookup,
	idPrefix string,
) ([]FieldSpec, bool) {
	returned := singleReturnOnly(method)
	if returned == nil {
		return nil, false
	}
	array := phpquery.DirectChild(returned, phpsyntax.PhpArray)
	if array == nil || strings.TrimSpace(array.Text()) != returnedExpressionText(returned) {
		return nil, false
	}
	synthetic := phpparser.Parse("<?php class DesignerDefaultFields { protected function defineFields(): FieldCollection { return new FieldCollection(" + array.Text() + "); } }")
	if synthetic.Tree == nil || synthetic.Tree.Root == nil || len(synthetic.Errors) != 0 {
		return nil, false
	}
	classes := phpquery.Classes(synthetic.Tree.Root)
	if len(classes) != 1 {
		return nil, false
	}
	var defineFields *phpsyntax.Node
	for _, candidate := range phpquery.Methods(classes[0]) {
		if phpquery.MethodName(candidate) == "defineFields" {
			defineFields = candidate
			break
		}
	}
	if defineFields == nil {
		return nil, false
	}
	fields, translation, err := importFields(defineFields, resolve, lookup)
	if err != nil || translation != nil {
		return nil, false
	}
	for index := range fields {
		if fields[index].Kind == FieldLocked {
			return nil, false
		}
		fields[index].ID = fmt.Sprintf("%s-%d", idPrefix, index+1)
	}
	return fields, true
}

func importedRestrictDeleteProperties(method *phpsyntax.Node) ([]string, bool) {
	returned := singleReturnOnly(method)
	if returned == nil {
		return nil, false
	}
	arrays := phpquery.Nodes(returned, phpsyntax.PhpArray)
	if len(arrays) != 1 {
		return nil, false
	}
	for _, array := range arrays {
		items := phpquery.ArrayItems(array)
		if len(items) == 0 {
			continue
		}
		properties := make([]string, 0, len(items))
		valid := true
		for _, item := range items {
			if phpquery.ArrayItemKey(item) != nil {
				valid = false
				break
			}
			value := phpquery.ArrayItemValue(item)
			if value == nil || value.Kind() != phpsyntax.PhpString {
				valid = false
				break
			}
			property := phpquery.StringValue(value)
			if property == "" {
				valid = false
				break
			}
			properties = append(properties, property)
		}
		if valid {
			return properties, true
		}
	}
	return nil, false
}

func definitionBehaviorEmpty(behavior *DefinitionBehaviorSpec) bool {
	return behavior == nil || behavior.ParentDefinitionClass == "" && behavior.VersionAware == nil &&
		!behavior.OverrideDefaultFields && len(behavior.DefaultFields) == 0 &&
		!behavior.OverrideBaseFields && len(behavior.BaseFields) == 0 && len(behavior.RestrictDeleteMetaProperties) == 0 &&
		behavior.ParentDefinitionMethodRaw == "" && behavior.VersionAwareMethodRaw == "" &&
		behavior.InheritanceAwareMethodRaw == "" && behavior.DefaultFieldsMethodRaw == "" && behavior.BaseFieldsMethodRaw == "" &&
		behavior.RestrictDeleteMetaMethodRaw == ""
}

func validateDefinitionBehavior(v *specValidator, behavior *DefinitionBehaviorSpec, owner DefinitionKind, translation bool) {
	if behavior == nil {
		return
	}
	fieldID := ""
	if translation {
		fieldID = "translation"
	}
	if owner == DefinitionExtension || owner == DefinitionBulkExtension {
		v.add("entity.definitionBehavior.owner.unsupported", "EntityExtension classes cannot declare EntityDefinition behavior", fieldID)
		return
	}
	if translation && (behavior.ParentDefinitionClass != "" || behavior.ParentDefinitionMethodRaw != "" ||
		behavior.InheritanceAwareMethodRaw != "" || len(behavior.RestrictDeleteMetaProperties) != 0 || behavior.RestrictDeleteMetaMethodRaw != "") {
		v.add("entity.definitionBehavior.translation.unsupported", "Translation behavior supports explicit version-awareness, default fields, and base fields only", fieldID)
	}
	if behavior.ParentDefinitionClass != "" && !namespacePattern.MatchString(behavior.ParentDefinitionClass) {
		v.add("entity.definitionBehavior.parent.invalid", "Aggregate parent must be a valid fully-qualified EntityDefinition class", fieldID)
	}
	validateDefinitionBehaviorRawConflict(v, behavior.ParentDefinitionClass != "", behavior.ParentDefinitionMethodRaw, "getParentDefinitionClass", fieldID)
	validateDefinitionBehaviorRawConflict(v, behavior.VersionAware != nil, behavior.VersionAwareMethodRaw, "isVersionAware", fieldID)
	validateDefinitionBehaviorRawConflict(v, v.spec.InheritanceAware, behavior.InheritanceAwareMethodRaw, "isInheritanceAware", fieldID)
	validateDefinitionBehaviorRawConflict(v, len(behavior.DefaultFields) != 0, behavior.DefaultFieldsMethodRaw, "defaultFields", fieldID)
	validateDefinitionBehaviorRawConflict(v, len(behavior.BaseFields) != 0, behavior.BaseFieldsMethodRaw, "getBaseFields", fieldID)
	validateDefinitionBehaviorRawConflict(v, len(behavior.RestrictDeleteMetaProperties) != 0, behavior.RestrictDeleteMetaMethodRaw, "getRestrictDeleteMetaFields", fieldID)
	if behavior.DefaultFieldsMethodRaw != "" && !behavior.OverrideDefaultFields {
		v.add("entity.definitionBehavior.defaultFields.override.missing", "A preserved defaultFields method requires overrideDefaultFields", fieldID)
	}
	if len(behavior.DefaultFields) != 0 && !behavior.OverrideDefaultFields {
		v.add("entity.definitionBehavior.defaultFields.override.missing", "Structured default fields require overrideDefaultFields", fieldID)
	}
	if behavior.BaseFieldsMethodRaw != "" && !behavior.OverrideBaseFields {
		v.add("entity.definitionBehavior.baseFields.override.missing", "A preserved getBaseFields method requires overrideBaseFields", fieldID)
	}
	if len(behavior.BaseFields) != 0 && !behavior.OverrideBaseFields {
		v.add("entity.definitionBehavior.baseFields.override.missing", "Structured base fields require overrideBaseFields", fieldID)
	}
	if owner == DefinitionMapping && (behavior.OverrideDefaultFields || len(behavior.DefaultFields) != 0 || behavior.DefaultFieldsMethodRaw != "") {
		v.add("entity.definitionBehavior.defaultFields.mapping.unsupported", "MappingEntityDefinition already disables framework default fields", fieldID)
	}
	for _, property := range behavior.RestrictDeleteMetaProperties {
		if !propertyPattern.MatchString(property) {
			v.add("entity.definitionBehavior.restrictDelete.property.invalid", fmt.Sprintf("Restrict-delete metadata property %q is invalid", property), fieldID)
		}
	}
	seen := make(map[string]struct{}, len(behavior.RestrictDeleteMetaProperties))
	for _, property := range behavior.RestrictDeleteMetaProperties {
		if _, duplicate := seen[property]; duplicate {
			v.add("entity.definitionBehavior.restrictDelete.property.duplicate", fmt.Sprintf("Restrict-delete metadata property %q is duplicated", property), fieldID)
		}
		seen[property] = struct{}{}
	}
	if v.spec.Mode != "edit" && definitionBehaviorHasRaw(behavior) {
		v.add("entity.definitionBehavior.raw.creation.unsupported", "Custom definition behavior can only be preserved from an existing PHP class", fieldID)
	}
}

func validateDefinitionBehaviorRawConflict(v *specValidator, typed bool, raw, method, fieldID string) {
	if typed && strings.TrimSpace(raw) != "" {
		v.add("entity.definitionBehavior.raw.conflict", fmt.Sprintf("Structured %s behavior cannot be combined with a preserved custom method", method), fieldID)
	}
}

func definitionBehaviorHasRaw(behavior *DefinitionBehaviorSpec) bool {
	return behavior != nil && (strings.TrimSpace(behavior.ParentDefinitionMethodRaw) != "" ||
		strings.TrimSpace(behavior.VersionAwareMethodRaw) != "" || strings.TrimSpace(behavior.InheritanceAwareMethodRaw) != "" ||
		strings.TrimSpace(behavior.DefaultFieldsMethodRaw) != "" || strings.TrimSpace(behavior.BaseFieldsMethodRaw) != "" ||
		strings.TrimSpace(behavior.RestrictDeleteMetaMethodRaw) != "")
}

func renderDefinitionBehavior(behavior *DefinitionBehaviorSpec, imports *importTable, translation bool) (string, error) {
	if behavior == nil {
		return "", nil
	}
	var methods []string
	if !translation {
		methods = appendDefinitionBehaviorMethod(methods, behavior.ParentDefinitionMethodRaw, renderParentDefinitionMethod(behavior.ParentDefinitionClass, imports))
	}
	methods = appendDefinitionBehaviorMethod(methods, behavior.VersionAwareMethodRaw, renderBooleanDefinitionMethod("isVersionAware", behavior.VersionAware))
	if !translation {
		methods = appendDefinitionBehaviorMethod(methods, behavior.InheritanceAwareMethodRaw, "")
	}
	defaultFields, err := renderDefaultFieldsMethod(behavior, imports)
	if err != nil {
		return "", err
	}
	methods = appendDefinitionBehaviorMethod(methods, behavior.DefaultFieldsMethodRaw, defaultFields)
	baseFields, err := renderBaseFieldsMethod(behavior, imports)
	if err != nil {
		return "", err
	}
	methods = appendDefinitionBehaviorMethod(methods, behavior.BaseFieldsMethodRaw, baseFields)
	if !translation {
		methods = appendDefinitionBehaviorMethod(methods, behavior.RestrictDeleteMetaMethodRaw, renderRestrictDeleteMetaMethod(behavior.RestrictDeleteMetaProperties))
	}
	return strings.Join(methods, "\n\n"), nil
}

func appendDefinitionBehaviorMethod(methods []string, raw, generated string) []string {
	if strings.TrimSpace(raw) != "" {
		return append(methods, dedentPreservedMember(raw))
	}
	if generated != "" {
		return append(methods, generated)
	}
	return methods
}

func renderParentDefinitionMethod(class string, imports *importTable) string {
	if class == "" {
		return ""
	}
	return "protected function getParentDefinitionClass(): ?string\n{\n    return " + imports.Ref(class) + "::class;\n}"
}

func renderBooleanDefinitionMethod(name string, value *bool) string {
	if value == nil {
		return ""
	}
	return "public function " + name + "(): bool\n{\n    return " + fmt.Sprintf("%t", *value) + ";\n}"
}

func renderDefaultFieldsMethod(behavior *DefinitionBehaviorSpec, imports *importTable) (string, error) {
	if behavior == nil || !behavior.OverrideDefaultFields || strings.TrimSpace(behavior.DefaultFieldsMethodRaw) != "" {
		return "", nil
	}
	return renderDefinitionFieldsMethod("defaultFields", behavior.DefaultFields, imports)
}

func renderBaseFieldsMethod(behavior *DefinitionBehaviorSpec, imports *importTable) (string, error) {
	if behavior == nil || !behavior.OverrideBaseFields || strings.TrimSpace(behavior.BaseFieldsMethodRaw) != "" {
		return "", nil
	}
	return renderDefinitionFieldsMethod("getBaseFields", behavior.BaseFields, imports)
}

func renderDefinitionFieldsMethod(name string, fields []FieldSpec, imports *importTable) (string, error) {
	var builder strings.Builder
	builder.WriteString("protected function ")
	builder.WriteString(name)
	builder.WriteString("(): array\n{\n    return [\n")
	for _, field := range fields {
		rendered, err := renderDefinitionField(field, imports)
		if err != nil {
			return "", err
		}
		for _, expression := range rendered {
			for _, line := range strings.Split(strings.TrimSpace(expression), "\n") {
				builder.WriteString("        ")
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
		}
	}
	builder.WriteString("    ];\n}")
	return builder.String(), nil
}

func renderRestrictDeleteMetaMethod(properties []string) string {
	if len(properties) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(properties))
	for _, property := range properties {
		quoted = append(quoted, quotePHP(property))
	}
	return "public function getRestrictDeleteMetaFields(): FieldCollection\n{\n" +
		"    return $this->getFields()->filter(\n" +
		"        static fn (Field $field): bool => \\in_array($field->getPropertyName(), [" + strings.Join(quoted, ", ") + "], true)\n" +
		"    );\n}"
}
