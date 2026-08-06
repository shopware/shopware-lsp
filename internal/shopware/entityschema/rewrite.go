package entityschema

import (
	"fmt"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	sourcerewrite "github.com/shopware/shopware-lsp/internal/rewrite"
)

// RewriteDefinition replaces only defineFields and adds missing imports. Other
// methods, attributes, constants and comments remain byte-for-byte intact.
func RewriteDefinition(source string, spec EntitySpec) (string, error) {
	generated, err := RenderDefinition(spec)
	if err != nil {
		return "", err
	}
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	_, generatedClass, err := parsedPHPClass(generated)
	if err != nil {
		return "", err
	}
	existingMethod := namedMethod(class, "defineFields")
	desiredMethod := namedMethod(generatedClass, "defineFields")
	if existingMethod == nil || desiredMethod == nil {
		return "", fmt.Errorf("cannot safely rewrite definition without defineFields")
	}
	editor := phprewrite.NewEditor(source, root)
	definitionClasses := definitionImports(CompleteSpec(spec))
	expectedReferences := newImportTable(definitionClasses)
	for _, className := range definitionClasses {
		if className == "" {
			continue
		}
		ref, refErr := editor.ClassReference(className)
		if refErr != nil {
			return "", refErr
		}
		expected := expectedReferences.Ref(className)
		if ref != expected && strings.Contains(desiredMethod.Text(), expected) {
			return "", fmt.Errorf("class import conflict for %s", className)
		}
	}
	if err := editor.ReplaceRange(existingMethod.RangeTrimmedTrivia(), strings.TrimSpace(desiredMethod.Text())); err != nil {
		return "", err
	}
	edits, err := editor.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

// RewriteEntity owns only properties and trivial accessors represented by the
// previous schema. A customized accessor is rejected instead of overwritten.
func RewriteEntity(source string, previous, next EntitySpec) (string, error) {
	desired, err := RenderEntity(next)
	if err != nil {
		return "", err
	}
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	desiredRoot, desiredClass, err := parsedPHPClass(desired)
	if err != nil {
		return "", err
	}
	_ = desiredRoot
	managedProperties, managedMethods := managedEntityMembers(previous)
	editor := phprewrite.NewEditor(source, root)
	entityClasses := entityImports(CompleteSpec(next))
	expectedReferences := newImportTable(entityClasses)
	for _, className := range entityClasses {
		if className == "" {
			continue
		}
		ref, refErr := editor.ClassReference(className)
		if refErr != nil {
			return "", refErr
		}
		expected := expectedReferences.Ref(className)
		if ref != expected && strings.Contains(desired, expected) {
			return "", fmt.Errorf("class import conflict for %s", className)
		}
	}
	for _, property := range phpquery.Properties(class) {
		variables := phpquery.PropertyVariables(property)
		remove := false
		for _, variable := range variables {
			if _, found := managedProperties[phpquery.VariableName(variable)]; found {
				remove = true
			}
		}
		if !remove {
			continue
		}
		if len(variables) != 1 {
			return "", fmt.Errorf("cannot safely rewrite combined managed property declaration")
		}
		if err := editor.RemoveClassMember(property); err != nil {
			return "", err
		}
	}
	for _, method := range phpquery.Methods(class) {
		name := phpquery.MethodName(method)
		if _, found := managedMethods[name]; !found {
			continue
		}
		if !trivialEntityAccessor(method.Text()) {
			return "", fmt.Errorf("cannot overwrite customized entity accessor %s", name)
		}
		if err := editor.RemoveClassMember(method); err != nil {
			return "", err
		}
	}
	var declarations []string
	for _, property := range phpquery.Properties(desiredClass) {
		declarations = append(declarations, strings.TrimSpace(property.Text()))
	}
	for _, method := range phpquery.Methods(desiredClass) {
		declarations = append(declarations, strings.TrimSpace(method.Text()))
	}
	if len(declarations) != 0 {
		if err := editor.InsertClassMember(class, strings.Join(declarations, "\n\n")); err != nil {
			return "", err
		}
	}
	edits, err := editor.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

func parsedPHPClass(source string) (*phpsyntax.Node, *phpsyntax.Node, error) {
	parsed := phpparser.Parse(source)
	if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) != 0 {
		return nil, nil, fmt.Errorf("PHP source contains syntax errors")
	}
	classes := phpquery.Classes(parsed.Tree.Root)
	if len(classes) != 1 {
		return nil, nil, fmt.Errorf("expected exactly one PHP class")
	}
	return parsed.Tree.Root, classes[0], nil
}

func namedMethod(class *phpsyntax.Node, name string) *phpsyntax.Node {
	for _, method := range phpquery.Methods(class) {
		if phpquery.MethodName(method) == name {
			return method
		}
	}
	return nil
}

func managedEntityMembers(spec EntitySpec) (map[string]struct{}, map[string]struct{}) {
	properties := make(map[string]struct{})
	methods := make(map[string]struct{})
	for _, field := range spec.Fields {
		if field.Kind == FieldID || field.Kind == FieldVersion || field.Kind == FieldReferenceVersion || field.Kind == FieldLocked {
			continue
		}
		names := []string{field.PropertyName}
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && !field.UsesExistingColumn {
			names = append(names, field.ForeignKeyPropertyName)
		}
		for _, name := range names {
			properties[name] = struct{}{}
			methods["get"+exported(name)] = struct{}{}
			methods["is"+exported(name)] = struct{}{}
			methods["set"+exported(name)] = struct{}{}
		}
	}
	return properties, methods
}

func trivialEntityAccessor(source string) bool {
	if strings.Contains(source, "//") || strings.Contains(source, "/*") || strings.Contains(source, "#[") {
		return false
	}
	compact := strings.Join(strings.Fields(source), " ")
	return strings.Contains(compact, "return $this->") ||
		(strings.Contains(compact, "$this->") && strings.Contains(compact, " = $"))
}
