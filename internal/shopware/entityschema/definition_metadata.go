package entityschema

import (
	"fmt"
	"regexp"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

var definitionSincePattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:\.\d+)?$`)

func importDefinitionMetadata(
	methods []*phpsyntax.Node,
	resolve func(string) string,
	allowDefaults, allowChildDefaults, allowHydrator bool,
) *DefinitionMetadataSpec {
	metadata := &DefinitionMetadataSpec{}
	for _, method := range methods {
		switch phpquery.MethodName(method) {
		case "since":
			if value, ok := importedLiteralStringReturn(method); ok {
				metadata.Since = value
			} else {
				metadata.SinceMethodRaw = strings.TrimSpace(method.Text())
			}
		case "getDefaults":
			if !allowDefaults {
				continue
			}
			if values, ok := importedDefinitionDefaults(method); ok {
				metadata.Defaults = values
			} else {
				metadata.DefaultsMethodRaw = strings.TrimSpace(method.Text())
			}
		case "getChildDefaults":
			if !allowChildDefaults {
				continue
			}
			if values, ok := importedDefinitionDefaults(method); ok {
				metadata.ChildDefaults = values
			} else {
				metadata.ChildDefaultsMethodRaw = strings.TrimSpace(method.Text())
			}
		case "getHydratorClass":
			if !allowHydrator {
				continue
			}
			if class := returnedImportedClass(method, resolve); class != "" {
				metadata.HydratorClass = class
			} else {
				metadata.HydratorMethodRaw = strings.TrimSpace(method.Text())
			}
		}
	}
	if definitionMetadataEmpty(metadata) {
		return nil
	}
	return metadata
}

func importedLiteralStringReturn(method *phpsyntax.Node) (string, bool) {
	returned := singleReturnOnly(method)
	if returned == nil {
		return "", false
	}
	stringsInReturn := phpquery.Nodes(returned, phpsyntax.PhpString)
	if len(stringsInReturn) != 1 || strings.TrimSpace(stringsInReturn[0].Text()) != returnedExpressionText(returned) {
		return "", false
	}
	value := phpquery.StringValue(stringsInReturn[0])
	return value, value != ""
}

func importedDefinitionDefaults(method *phpsyntax.Node) ([]DefinitionDefaultSpec, bool) {
	returned := singleReturnOnly(method)
	if returned == nil {
		return nil, false
	}
	array := phpquery.DirectChild(returned, phpsyntax.PhpArray)
	if array == nil || strings.TrimSpace(array.Text()) != returnedExpressionText(returned) {
		return nil, false
	}
	items := phpquery.ArrayItems(array)
	result := make([]DefinitionDefaultSpec, 0, len(items))
	for _, item := range items {
		key := phpquery.ArrayItemKey(item)
		value := phpquery.ArrayItemValue(item)
		if key == nil || key.Kind() != phpsyntax.PhpString || value == nil {
			return nil, false
		}
		property := phpquery.StringValue(key)
		expression := strings.TrimSpace(value.Text())
		if property == "" || expression == "" {
			return nil, false
		}
		result = append(result, DefinitionDefaultSpec{PropertyName: property, ValueExpression: expression})
	}
	return result, true
}

func definitionMetadataEmpty(metadata *DefinitionMetadataSpec) bool {
	return metadata == nil || metadata.Since == "" && len(metadata.Defaults) == 0 &&
		len(metadata.ChildDefaults) == 0 && metadata.HydratorClass == "" &&
		metadata.SinceMethodRaw == "" && metadata.DefaultsMethodRaw == "" &&
		metadata.ChildDefaultsMethodRaw == "" && metadata.HydratorMethodRaw == ""
}

func validateDefinitionMetadata(v *specValidator, metadata *DefinitionMetadataSpec, owner DefinitionKind, translation bool) {
	if metadata == nil {
		return
	}
	fieldID := ""
	if translation {
		fieldID = "translation"
	}
	if owner == DefinitionExtension || owner == DefinitionBulkExtension {
		v.add("entity.definitionMetadata.owner.unsupported", "EntityExtension classes cannot declare EntityDefinition metadata", fieldID)
		return
	}
	if metadata.Since != "" && !definitionSincePattern.MatchString(metadata.Since) {
		v.add("entity.definitionMetadata.since.invalid", "Definition availability must use a numeric Shopware version such as 6.7.1.0", fieldID)
	}
	validateDefinitionRawConflict(v, metadata.Since != "", metadata.SinceMethodRaw, "since", fieldID)
	validateDefinitionRawConflict(v, len(metadata.Defaults) != 0, metadata.DefaultsMethodRaw, "getDefaults", fieldID)
	validateDefinitionRawConflict(v, len(metadata.ChildDefaults) != 0, metadata.ChildDefaultsMethodRaw, "getChildDefaults", fieldID)
	validateDefinitionRawConflict(v, metadata.HydratorClass != "", metadata.HydratorMethodRaw, "getHydratorClass", fieldID)
	if translation && (len(metadata.ChildDefaults) != 0 || metadata.ChildDefaultsMethodRaw != "" ||
		metadata.HydratorClass != "" || metadata.HydratorMethodRaw != "") {
		v.add("entity.definitionMetadata.translation.unsupported", "Translation definitions support since and defaults, but not child defaults or custom hydrators", fieldID)
	}
	if owner != DefinitionEntity && (len(metadata.ChildDefaults) != 0 || metadata.ChildDefaultsMethodRaw != "" ||
		metadata.HydratorClass != "" || metadata.HydratorMethodRaw != "") {
		v.add("entity.definitionMetadata.kind.unsupported", "Only normal entity definitions support child defaults and custom hydrators", fieldID)
	}
	validateDefinitionDefaults(v, metadata.Defaults, "defaults", fieldID)
	validateDefinitionDefaults(v, metadata.ChildDefaults, "child defaults", fieldID)
	if metadata.HydratorClass != "" && !namespacePattern.MatchString(metadata.HydratorClass) {
		v.add("entity.definitionMetadata.hydrator.invalid", "Custom hydrator must be a valid fully-qualified PHP class", fieldID)
	}
	if v.spec.Mode != "edit" && definitionMetadataHasRaw(metadata) {
		v.add("entity.definitionMetadata.raw.creation.unsupported", "Custom definition metadata can only be preserved from an existing PHP class", fieldID)
	}
}

func definitionMetadataHasRaw(metadata *DefinitionMetadataSpec) bool {
	return metadata != nil && (strings.TrimSpace(metadata.SinceMethodRaw) != "" ||
		strings.TrimSpace(metadata.DefaultsMethodRaw) != "" || strings.TrimSpace(metadata.ChildDefaultsMethodRaw) != "" ||
		strings.TrimSpace(metadata.HydratorMethodRaw) != "")
}

func validateDefinitionRawConflict(v *specValidator, typed bool, raw, method, fieldID string) {
	if typed && strings.TrimSpace(raw) != "" {
		v.add("entity.definitionMetadata.raw.conflict", fmt.Sprintf("Structured %s metadata cannot be combined with a preserved custom method", method), fieldID)
	}
}

func validateDefinitionDefaults(v *specValidator, values []DefinitionDefaultSpec, label, fieldID string) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !propertyPattern.MatchString(value.PropertyName) {
			v.add("entity.definitionMetadata.default.property.invalid", fmt.Sprintf("%s property %q is not a valid DAL property name", label, value.PropertyName), fieldID)
		}
		if _, duplicate := seen[value.PropertyName]; duplicate {
			v.add("entity.definitionMetadata.default.property.duplicate", fmt.Sprintf("%s property %q is duplicated", label, value.PropertyName), fieldID)
		}
		seen[value.PropertyName] = struct{}{}
		if !validPHPExpression(value.ValueExpression) {
			v.add("entity.definitionMetadata.default.expression.invalid", fmt.Sprintf("%s value for %q is not a valid PHP expression", label, value.PropertyName), fieldID)
		}
	}
}

func validPHPExpression(expression string) bool {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return false
	}
	parsed := phpparser.Parse("<?php $value = " + expression + ";")
	return parsed.Tree != nil && parsed.Tree.Root != nil && len(parsed.Errors) == 0
}

func renderDefinitionMetadata(metadata *DefinitionMetadataSpec, imports *importTable, translation bool) string {
	if metadata == nil {
		return ""
	}
	var methods []string
	methods = appendDefinitionMetadataMethod(methods, metadata.SinceMethodRaw, renderSinceMethod(metadata.Since))
	methods = appendDefinitionMetadataMethod(methods, metadata.DefaultsMethodRaw, renderDefaultsMethod("getDefaults", metadata.Defaults))
	if !translation {
		methods = appendDefinitionMetadataMethod(methods, metadata.ChildDefaultsMethodRaw, renderDefaultsMethod("getChildDefaults", metadata.ChildDefaults))
		methods = appendDefinitionMetadataMethod(methods, metadata.HydratorMethodRaw, renderHydratorMethod(metadata.HydratorClass, imports))
	}
	return strings.Join(methods, "\n\n")
}

func appendDefinitionMetadataMethod(methods []string, raw, generated string) []string {
	if strings.TrimSpace(raw) != "" {
		return append(methods, dedentPreservedMember(raw))
	}
	if generated != "" {
		return append(methods, generated)
	}
	return methods
}

func dedentPreservedMember(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) < 2 {
		return strings.Join(lines, "\n")
	}
	indent := -1
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		width := 0
		for width < len(line) && (line[width] == ' ' || line[width] == '\t') {
			width++
		}
		if indent < 0 || width < indent {
			indent = width
		}
	}
	if indent <= 0 {
		return strings.Join(lines, "\n")
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "" {
			lines[index] = ""
			continue
		}
		lines[index] = lines[index][indent:]
	}
	return strings.Join(lines, "\n")
}

func renderSinceMethod(since string) string {
	if since == "" {
		return ""
	}
	return "public function since(): ?string\n{\n    return " + quotePHP(since) + ";\n}"
}

func renderDefaultsMethod(name string, values []DefinitionDefaultSpec) string {
	if len(values) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("public function ")
	builder.WriteString(name)
	builder.WriteString("(): array\n{\n    return [\n")
	for _, value := range values {
		builder.WriteString("        ")
		builder.WriteString(quotePHP(value.PropertyName))
		builder.WriteString(" => ")
		builder.WriteString(strings.TrimSpace(value.ValueExpression))
		builder.WriteString(",\n")
	}
	builder.WriteString("    ];\n}")
	return builder.String()
}

func renderHydratorMethod(class string, imports *importTable) string {
	if class == "" {
		return ""
	}
	return "public function getHydratorClass(): string\n{\n    return " + imports.Ref(class) + "::class;\n}"
}
