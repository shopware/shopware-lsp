package entityschema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	sourcerewrite "github.com/shopware/shopware-lsp/internal/rewrite"
)

// RewriteDefinition replaces the managed defineFields, inheritance and entity
// protection methods and adds missing imports. Other methods, attributes,
// constants and comments remain byte-for-byte intact.
func RewriteDefinition(source string, spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	generated, err := RenderDefinition(spec)
	if err != nil {
		return "", err
	}
	return rewriteDefinitionSource(source, generated, definitionImports(spec), spec.DefinitionKind == DefinitionEntity)
}

// RewriteDefinitionFrom rewrites the managed definition members and removes
// imports that belonged to the previous schema but are no longer used by the
// next schema. Imports referenced by custom source are always preserved.
func RewriteDefinitionFrom(source string, previous, next EntitySpec) (string, error) {
	previous = CompleteSpec(previous)
	next = CompleteSpec(next)
	var result string
	var err error
	if previous.DefinitionKind != next.DefinitionKind {
		result, err = rewriteDefinitionKindTransition(source, previous, next)
	} else {
		result, err = RewriteDefinition(source, next)
		if err == nil && (next.DefinitionKind == DefinitionEntity || next.DefinitionKind == DefinitionMapping) {
			result, err = rewriteDefinitionIdentityChanges(result, previous, next)
		}
	}
	if err != nil {
		return "", err
	}
	return removeObsoleteManagedImports(result, definitionImports(previous), definitionImports(next))
}

func rewriteDefinitionKindTransition(source string, previous, next EntitySpec) (string, error) {
	generated, err := RenderDefinition(next)
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

	editor := phprewrite.NewEditor(source, root)
	expectedReferences := newImportTable(definitionImports(next))
	for _, className := range expectedReferences.classes {
		if className == "" {
			continue
		}
		ref, refErr := editor.ClassReference(className)
		if refErr != nil {
			return "", refErr
		}
		expected := expectedReferences.Ref(className)
		if ref != expected && strings.Contains(generated, expected) {
			return "", fmt.Errorf("class import conflict for %s", className)
		}
	}
	parentClass := definitionParentClass(next.DefinitionKind)
	parentReference, err := editor.ClassReference(parentClass)
	if err != nil {
		return "", err
	}
	if err := editor.SetExtends(class, parentReference); err != nil {
		return "", err
	}

	desiredMethodList := phpquery.Methods(generatedClass)
	desiredMethods := make(map[string]*phpsyntax.Node, len(desiredMethodList))
	for _, method := range desiredMethodList {
		desiredMethods[phpquery.MethodName(method)] = method
	}
	previousManaged := managedDefinitionMethods(previous)
	removedMethods := make(map[string]struct{})
	for _, method := range phpquery.Methods(class) {
		name := phpquery.MethodName(method)
		if _, managed := previousManaged[name]; !managed {
			continue
		}
		_, needed := desiredMethods[name]
		if !safeDefinitionTransitionMethod(method, name) {
			if needed {
				return "", fmt.Errorf("cannot overwrite customized definition method %s", name)
			}
			continue
		}
		if err := editor.RemoveClassMember(method); err != nil {
			return "", err
		}
		removedMethods[name] = struct{}{}
	}
	for _, method := range desiredMethodList {
		name := phpquery.MethodName(method)
		if existing := namedMethod(class, name); existing != nil {
			if _, removed := removedMethods[name]; !removed {
				return "", fmt.Errorf("cannot add managed definition method %s because a custom method already exists", name)
			}
		}
		if err := editor.InsertClassMember(class, strings.Trim(method.Text(), "\r\n")); err != nil {
			return "", err
		}
	}

	previousConstants := directClassConstants(class)
	desiredConstants := directClassConstants(generatedClass)
	removedEntityName := false
	for _, constant := range previousConstants {
		if !isEntityNameConstant(constant) {
			continue
		}
		if !safeEntityNameConstant(constant) {
			if len(desiredConstants) != 0 {
				return "", fmt.Errorf("cannot overwrite customized ENTITY_NAME constant")
			}
			continue
		}
		if err := editor.RemoveClassMember(constant); err != nil {
			return "", err
		}
		removedEntityName = true
	}
	for _, constant := range desiredConstants {
		if !isEntityNameConstant(constant) {
			continue
		}
		if !removedEntityName && hasEntityNameConstant(previousConstants) {
			return "", fmt.Errorf("cannot add managed ENTITY_NAME constant because a custom constant already exists")
		}
		if err := editor.InsertClassMember(class, strings.Trim(constant.Text(), "\r\n")); err != nil {
			return "", err
		}
	}

	edits, err := editor.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

func definitionParentClass(kind DefinitionKind) string {
	switch kind {
	case DefinitionBulkExtension:
		return `Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension`
	case DefinitionExtension:
		return `Shopware\Core\Framework\DataAbstractionLayer\EntityExtension`
	case DefinitionMapping:
		return `Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition`
	default:
		return `Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`
	}
}

func managedDefinitionMethods(spec EntitySpec) map[string]struct{} {
	if spec.DefinitionKind == DefinitionBulkExtension {
		return map[string]struct{}{"collect": {}}
	}
	result := map[string]struct{}{"getEntityName": {}}
	if spec.DefinitionKind == DefinitionExtension {
		result["extendFields"] = struct{}{}
		result["getDefinitionClass"] = struct{}{}
		if strings.TrimSpace(spec.ProtectionMethodRaw) == "" {
			result["extendProtections"] = struct{}{}
		}
		if strings.TrimSpace(spec.ModifyFieldsMethodRaw) == "" {
			result["modifyFields"] = struct{}{}
		}
		return result
	}
	result["defineFields"] = struct{}{}
	result["getEntityClass"] = struct{}{}
	result["getCollectionClass"] = struct{}{}
	for _, name := range managedDefinitionMetadataMethodNames(spec.DefinitionMetadata) {
		result[name] = struct{}{}
	}
	for _, name := range managedDefinitionBehaviorMethodNames(spec.DefinitionBehavior) {
		result[name] = struct{}{}
	}
	if spec.DefinitionKind == DefinitionEntity {
		result["isInheritanceAware"] = struct{}{}
		if strings.TrimSpace(spec.ProtectionMethodRaw) == "" {
			result["defineProtections"] = struct{}{}
		}
	}
	return result
}

func managedDefinitionBehaviorMethodNames(behavior *DefinitionBehaviorSpec) []string {
	if behavior == nil {
		return nil
	}
	var names []string
	if behavior.ParentDefinitionClass != "" || behavior.ParentDefinitionMethodRaw != "" {
		names = append(names, "getParentDefinitionClass")
	}
	if behavior.VersionAware != nil || behavior.VersionAwareMethodRaw != "" {
		names = append(names, "isVersionAware")
	}
	if behavior.InheritanceAwareMethodRaw != "" {
		names = append(names, "isInheritanceAware")
	}
	if behavior.OverrideDefaultFields || behavior.DefaultFieldsMethodRaw != "" {
		names = append(names, "defaultFields")
	}
	if behavior.OverrideBaseFields || behavior.BaseFieldsMethodRaw != "" {
		names = append(names, "getBaseFields")
	}
	if len(behavior.RestrictDeleteMetaProperties) != 0 || behavior.RestrictDeleteMetaMethodRaw != "" {
		names = append(names, "getRestrictDeleteMetaFields")
	}
	return names
}

func managedDefinitionMetadataMethodNames(metadata *DefinitionMetadataSpec) []string {
	if metadata == nil {
		return nil
	}
	var names []string
	if metadata.Since != "" || metadata.SinceMethodRaw != "" {
		names = append(names, "since")
	}
	if len(metadata.Defaults) != 0 || metadata.DefaultsMethodRaw != "" {
		names = append(names, "getDefaults")
	}
	if len(metadata.ChildDefaults) != 0 || metadata.ChildDefaultsMethodRaw != "" {
		names = append(names, "getChildDefaults")
	}
	if metadata.HydratorClass != "" || metadata.HydratorMethodRaw != "" {
		names = append(names, "getHydratorClass")
	}
	return names
}

func safeDefinitionTransitionMethod(method *phpsyntax.Node, name string) bool {
	switch name {
	case "defineFields", "extendFields", "collect":
		return true
	case "defineProtections":
		return safeProtectionMethod(method)
	case "extendProtections":
		return safeExtensionProtectionMethod(method)
	case "modifyFields":
		return safeFieldModificationMethod(method)
	case "isInheritanceAware", "isVersionAware":
		_, literal := importedBooleanReturn(method)
		return literal && safeBooleanMethod(method)
	case "defaultFields", "getBaseFields", "getRestrictDeleteMetaFields", "getParentDefinitionClass":
		return safeDefinitionBehaviorMethod(method, name)
	case "since", "getDefaults", "getChildDefaults", "getHydratorClass":
		return safeDefinitionMetadataMethod(method, name)
	default:
		return safeLiteralReturnMethod(method)
	}
}

func directClassConstants(class *phpsyntax.Node) []*phpsyntax.Node {
	body := phpquery.ClassBody(class)
	if body == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for index := 0; index < body.ChildCount(); index++ {
		child, ok := body.Child(index).(*phpsyntax.Node)
		if ok && child.Kind() == phpsyntax.PhpClassConstDeclaration {
			result = append(result, child)
		}
	}
	return result
}

func isEntityNameConstant(constant *phpsyntax.Node) bool {
	return constant != nil && strings.Contains(constant.Text(), "ENTITY_NAME")
}

func hasEntityNameConstant(constants []*phpsyntax.Node) bool {
	for _, constant := range constants {
		if isEntityNameConstant(constant) {
			return true
		}
	}
	return false
}

func safeEntityNameConstant(constant *phpsyntax.Node) bool {
	if !isEntityNameConstant(constant) || strings.Contains(constant.Text(), "//") ||
		strings.Contains(constant.Text(), "/*") || strings.Contains(constant.Text(), "#[") {
		return false
	}
	text := strings.TrimSpace(constant.Text())
	return strings.Count(text, "=") == 1 && !strings.Contains(text, ",") && strings.HasSuffix(text, ";")
}

func rewriteDefinitionIdentityChanges(source string, previous, next EntitySpec) (string, error) {
	generated, err := RenderDefinition(next)
	if err != nil {
		return "", err
	}
	return rewriteDefinitionEntityName(source, generated, previous.EntityName, next.EntityName)
}

func rewriteDefinitionEntityName(source, generated, previous, next string) (string, error) {
	if previous == next {
		return source, nil
	}
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	_, generatedClass, err := parsedPHPClass(generated)
	if err != nil {
		return "", err
	}
	editor := phprewrite.NewEditor(source, root)
	existing := directEntityNameConstant(class)
	desired := directEntityNameConstant(generatedClass)
	if desired == nil {
		return "", errors.New("generated definition has no ENTITY_NAME constant")
	}
	if existing == nil {
		if err := editor.InsertClassMember(class, strings.TrimSpace(desired.Text())); err != nil {
			return "", err
		}
	} else {
		if !safeEntityNameConstant(existing) {
			return "", errors.New("cannot overwrite customized ENTITY_NAME constant")
		}
		if err := editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text())); err != nil {
			return "", err
		}
	}
	existingMethod := namedMethod(class, "getEntityName")
	desiredMethod := namedMethod(generatedClass, "getEntityName")
	if desiredMethod == nil {
		return "", errors.New("generated definition has no getEntityName method")
	}
	if existingMethod == nil {
		if err := editor.InsertClassMember(class, strings.TrimSpace(desiredMethod.Text())); err != nil {
			return "", err
		}
	} else if strings.TrimSpace(existingMethod.Text()) != strings.TrimSpace(desiredMethod.Text()) {
		if !safeLiteralReturnMethod(existingMethod) {
			return "", errors.New("cannot overwrite customized definition identity method getEntityName")
		}
		if err := editor.ReplaceRange(existingMethod.RangeTrimmedTrivia(), strings.TrimSpace(desiredMethod.Text())); err != nil {
			return "", err
		}
	}
	edits, err := editor.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

func directEntityNameConstant(class *phpsyntax.Node) *phpsyntax.Node {
	for _, constant := range directClassConstants(class) {
		if isEntityNameConstant(constant) {
			return constant
		}
	}
	return nil
}

func RewriteTranslationDefinition(source string, spec EntitySpec) (string, error) {
	generated, err := RenderTranslationDefinition(spec)
	if err != nil {
		return "", err
	}
	return rewriteDefinitionSource(source, generated, translationDefinitionImports(CompleteSpec(spec)), false)
}

// RewriteTranslationDefinitionFrom additionally updates the literal entity
// name identity when a translation table is renamed. Arbitrary identity
// implementations remain untouched and reject the requested change.
func RewriteTranslationDefinitionFrom(source string, previous, next EntitySpec) (string, error) {
	result, err := RewriteTranslationDefinition(source, next)
	if err != nil {
		return "", err
	}
	if previous.Translation == nil || next.Translation == nil {
		return "", errors.New("translation identity metadata is missing")
	}
	generated, err := RenderTranslationDefinition(next)
	if err != nil {
		return "", err
	}
	return rewriteDefinitionEntityName(result, generated, previous.Translation.EntityName, next.Translation.EntityName)
}

func rewriteDefinitionSource(source, generated string, definitionClasses []string, manageInheritance bool) (string, error) {
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	_, generatedClass, err := parsedPHPClass(generated)
	if err != nil {
		return "", err
	}
	methodName := "defineFields"
	if namedMethod(generatedClass, "extendFields") != nil {
		methodName = "extendFields"
	} else if namedMethod(generatedClass, "collect") != nil {
		methodName = "collect"
	}
	existingMethod := namedMethod(class, methodName)
	desiredMethod := namedMethod(generatedClass, methodName)
	if desiredMethod == nil || existingMethod == nil && methodName != "extendFields" {
		return "", fmt.Errorf("cannot safely rewrite definition without %s", methodName)
	}
	editor := phprewrite.NewEditor(source, root)
	expectedReferences := newImportTable(definitionClasses)
	for _, className := range expectedReferences.classes {
		if className == "" {
			continue
		}
		ref, refErr := editor.ClassReference(className)
		if refErr != nil {
			return "", refErr
		}
		expected := expectedReferences.Ref(className)
		if ref != expected && strings.Contains(generated, expected) {
			return "", fmt.Errorf("class import conflict for %s", className)
		}
	}
	if existingMethod == nil {
		if err := editor.InsertClassMember(class, strings.TrimSpace(desiredMethod.Text())); err != nil {
			return "", err
		}
	} else if err := editor.ReplaceRange(existingMethod.RangeTrimmedTrivia(), strings.TrimSpace(desiredMethod.Text())); err != nil {
		return "", err
	}
	if manageInheritance {
		if err := rewriteInheritanceAwareMethod(editor, class, generatedClass); err != nil {
			return "", err
		}
		if err := rewriteProtectionMethod(editor, class, generatedClass); err != nil {
			return "", err
		}
	} else if methodName == "extendFields" {
		if err := rewriteExtensionProtectionMethod(editor, class, generatedClass); err != nil {
			return "", err
		}
		if err := rewriteFieldModificationMethod(editor, class, generatedClass); err != nil {
			return "", err
		}
		if err := rewriteExtensionTargetMethods(editor, class, generatedClass); err != nil {
			return "", err
		}
	}
	if methodName == "defineFields" {
		if err := rewriteDefinitionMetadataMethods(editor, class, generatedClass); err != nil {
			return "", err
		}
		if err := rewriteDefinitionBehaviorMethods(editor, class, generatedClass); err != nil {
			return "", err
		}
	}
	edits, err := editor.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

func rewriteDefinitionBehaviorMethods(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	for _, name := range []string{"getParentDefinitionClass", "isVersionAware", "defaultFields", "getBaseFields", "getRestrictDeleteMetaFields"} {
		existing := namedMethod(class, name)
		desired := namedMethod(generatedClass, name)
		if existing == nil && desired == nil {
			continue
		}
		if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
			continue
		}
		if existing == nil {
			if err := editor.InsertClassMember(class, strings.TrimSpace(desired.Text())); err != nil {
				return err
			}
			continue
		}
		if !safeDefinitionBehaviorMethod(existing, name) {
			return fmt.Errorf("cannot overwrite customized definition behavior method %s", name)
		}
		if desired == nil {
			if err := editor.RemoveClassMember(existing); err != nil {
				return err
			}
			continue
		}
		if err := editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text())); err != nil {
			return err
		}
	}
	return nil
}

func safeDefinitionBehaviorMethod(method *phpsyntax.Node, name string) bool {
	if method == nil || strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		return false
	}
	switch name {
	case "isVersionAware":
		_, literal := importedBooleanReturn(method)
		return literal
	case "defaultFields", "getBaseFields":
		_, literal := importedDefinitionFields(method, func(value string) string { return value }, nil, "rewrite-field")
		return literal
	case "getRestrictDeleteMetaFields":
		_, literal := importedRestrictDeleteProperties(method)
		return literal
	default:
		return safeLiteralReturnMethod(method)
	}
}

func rewriteDefinitionMetadataMethods(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	for _, name := range []string{"since", "getDefaults", "getChildDefaults", "getHydratorClass"} {
		existing := namedMethod(class, name)
		desired := namedMethod(generatedClass, name)
		if existing == nil && desired == nil {
			continue
		}
		if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
			continue
		}
		if existing == nil {
			if err := editor.InsertClassMember(class, strings.TrimSpace(desired.Text())); err != nil {
				return err
			}
			continue
		}
		if !safeDefinitionMetadataMethod(existing, name) {
			return fmt.Errorf("cannot overwrite customized definition metadata method %s", name)
		}
		if desired == nil {
			if err := editor.RemoveClassMember(existing); err != nil {
				return err
			}
			continue
		}
		if err := editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text())); err != nil {
			return err
		}
	}
	return nil
}

func safeDefinitionMetadataMethod(method *phpsyntax.Node, name string) bool {
	if method == nil || strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		return false
	}
	if name == "getDefaults" || name == "getChildDefaults" {
		_, literal := importedDefinitionDefaults(method)
		return literal
	}
	return safeLiteralReturnMethod(method)
}

func rewriteFieldModificationMethod(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	existing := namedMethod(class, "modifyFields")
	desired := namedMethod(generatedClass, "modifyFields")
	if existing == nil && desired == nil {
		return nil
	}
	if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
		return nil
	}
	if existing == nil {
		return editor.InsertClassMember(class, strings.TrimSpace(desired.Text()))
	}
	if desired != nil && !safeFieldModificationMethod(desired) {
		return nil
	}
	if !safeFieldModificationMethod(existing) {
		return fmt.Errorf("cannot overwrite customized modifyFields method")
	}
	if desired == nil {
		return editor.RemoveClassMember(existing)
	}
	return editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text()))
}

func safeFieldModificationMethod(method *phpsyntax.Node) bool {
	if method == nil {
		return false
	}
	var spec EntitySpec
	importEntityExtensionFieldModifications(&spec, method, func(class string) string { return class })
	return strings.TrimSpace(spec.ModifyFieldsMethodRaw) == ""
}

func rewriteExtensionProtectionMethod(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	existing := namedMethod(class, "extendProtections")
	desired := namedMethod(generatedClass, "extendProtections")
	if existing == nil && desired == nil {
		return nil
	}
	if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
		return nil
	}
	if existing == nil {
		return editor.InsertClassMember(class, strings.TrimSpace(desired.Text()))
	}
	// A non-literal method is an immutable source fragment carried by
	// ProtectionMethodRaw. It is intentionally not regenerated or normalized.
	if desired != nil && !safeExtensionProtectionMethod(desired) {
		return nil
	}
	if !safeExtensionProtectionMethod(existing) {
		return fmt.Errorf("cannot overwrite customized extendProtections method")
	}
	if desired == nil {
		return editor.RemoveClassMember(existing)
	}
	return editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text()))
}

func safeExtensionProtectionMethod(method *phpsyntax.Node) bool {
	if method == nil {
		return false
	}
	var spec EntitySpec
	importEntityExtensionProtections(&spec, method)
	return strings.TrimSpace(spec.ProtectionMethodRaw) == ""
}

func rewriteExtensionTargetMethods(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	for _, name := range []string{"getEntityName", "getDefinitionClass"} {
		existing := namedMethod(class, name)
		desired := namedMethod(generatedClass, name)
		if existing == nil && desired == nil {
			continue
		}
		if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
			continue
		}
		if existing == nil {
			if err := editor.InsertClassMember(class, strings.TrimSpace(desired.Text())); err != nil {
				return err
			}
			continue
		}
		if !safeLiteralReturnMethod(existing) {
			return fmt.Errorf("cannot overwrite customized entity extension method %s", name)
		}
		if desired == nil {
			if err := editor.RemoveClassMember(existing); err != nil {
				return err
			}
			continue
		}
		if err := editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text())); err != nil {
			return err
		}
	}
	return nil
}

func safeLiteralReturnMethod(method *phpsyntax.Node) bool {
	if method == nil || strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		return false
	}
	returned := singleReturnOnly(method)
	if returned == nil {
		return false
	}
	expression := returnedExpressionText(returned)
	literal := phpquery.DirectChild(returned, phpsyntax.PhpString)
	if literal != nil && strings.TrimSpace(literal.Text()) == expression {
		return true
	}
	for _, access := range phpquery.Nodes(returned, phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess) {
		if strings.TrimSpace(access.Text()) == expression {
			return true
		}
	}
	return false
}

func removeObsoleteManagedImports(source string, previous, next []string) (string, error) {
	retained := make(map[string]struct{}, len(next))
	for _, className := range next {
		retained[strings.ToLower(strings.Trim(className, `\ `))] = struct{}{}
	}
	obsolete := make(map[string]struct{}, len(previous))
	for _, className := range previous {
		key := strings.ToLower(strings.Trim(className, `\ `))
		if key == "" {
			continue
		}
		if _, found := retained[key]; !found {
			obsolete[key] = struct{}{}
		}
	}
	if len(obsolete) == 0 {
		return source, nil
	}

	parsed := phpparser.Parse(source)
	if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) != 0 {
		return "", fmt.Errorf("rewritten PHP source contains syntax errors")
	}
	builder := sourcerewrite.NewBuilder(source)
	for _, declaration := range phpquery.UseDeclarations(parsed.Tree.Root) {
		imports := phpresolver.ParseUseDeclaration(declaration.Text())
		if len(imports) != 1 || imports[0].Kind != phpresolver.ClassImport {
			continue
		}
		imported := imports[0]
		if _, found := obsolete[strings.ToLower(strings.Trim(imported.Target, `\ `))]; !found {
			continue
		}
		if identifierUsedOutsideRange(source, imported.Alias, declaration.Range()) {
			continue
		}
		start, end := wholeLineRange(source, int(declaration.Range().Start), int(declaration.Range().End))
		if err := builder.ReplaceRange(cst.TextRange{Start: uint32(start), End: uint32(end)}, ""); err != nil {
			return "", err
		}
	}
	edits, err := builder.Finish()
	if err != nil {
		return "", err
	}
	return sourcerewrite.Apply(source, edits)
}

func identifierUsedOutsideRange(source, identifier string, excluded cst.TextRange) bool {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return false
	}
	return containsPHPIdentifier(source[:excluded.Start], identifier) ||
		containsPHPIdentifier(source[excluded.End:], identifier)
}

func containsPHPIdentifier(source, identifier string) bool {
	for offset := 0; offset <= len(source)-len(identifier); {
		relative := strings.Index(source[offset:], identifier)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(identifier)
		beforeIdentifier := start == 0 || !isPHPIdentifierByte(source[start-1])
		afterIdentifier := end == len(source) || !isPHPIdentifierByte(source[end])
		if beforeIdentifier && afterIdentifier {
			return true
		}
		offset = start + 1
	}
	return false
}

func isPHPIdentifierByte(value byte) bool {
	return value == '_' || value >= 0x80 || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func rewriteProtectionMethod(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	existing := namedMethod(class, "defineProtections")
	desired := namedMethod(generatedClass, "defineProtections")
	if existing == nil && desired == nil {
		return nil
	}
	if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
		return nil
	}
	if existing == nil {
		return editor.InsertClassMember(class, strings.TrimSpace(desired.Text()))
	}
	if !safeProtectionMethod(existing) {
		return fmt.Errorf("cannot overwrite customized defineProtections method")
	}
	if desired == nil {
		return editor.RemoveClassMember(existing)
	}
	return editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text()))
}

func safeProtectionMethod(method *phpsyntax.Node) bool {
	if method == nil || strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		return false
	}
	var spec EntitySpec
	importEntityProtections(&spec, method)
	return strings.TrimSpace(spec.ProtectionMethodRaw) == ""
}

func rewriteInheritanceAwareMethod(editor *phprewrite.Editor, class, generatedClass *phpsyntax.Node) error {
	existing := namedMethod(class, "isInheritanceAware")
	desired := namedMethod(generatedClass, "isInheritanceAware")
	if existing != nil && desired != nil && strings.TrimSpace(existing.Text()) == strings.TrimSpace(desired.Text()) {
		return nil
	}
	if desired == nil {
		value, literal := importedBooleanReturn(existing)
		if !literal || !value {
			return nil
		}
		if !safeBooleanMethod(existing) {
			return fmt.Errorf("cannot remove customized isInheritanceAware method")
		}
		return editor.RemoveClassMember(existing)
	}
	if existing == nil {
		return editor.InsertClassMember(class, strings.TrimSpace(desired.Text()))
	}
	value, literal := importedBooleanReturn(existing)
	if !literal {
		return fmt.Errorf("cannot overwrite customized isInheritanceAware method")
	}
	if value {
		return nil
	}
	if !safeBooleanMethod(existing) {
		return fmt.Errorf("cannot overwrite customized isInheritanceAware method")
	}
	return editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(desired.Text()))
}

func safeBooleanMethod(method *phpsyntax.Node) bool {
	return method != nil && !strings.Contains(method.Text(), "//") &&
		!strings.Contains(method.Text(), "/*") && !strings.Contains(method.Text(), "#[")
}

// RewriteCollection updates the generated API alias after a technical entity
// rename while preserving every unrelated collection member.
func RewriteCollection(source string, previous, next EntitySpec) (string, error) {
	desired, err := RenderCollection(next)
	if err != nil {
		return "", err
	}
	return rewriteCollectionAlias(source, desired, previous.EntityName, next.EntityName)
}

// RewriteTranslationCollection updates the translation collection API alias
// after a translation-table rename.
func RewriteTranslationCollection(source string, previous, next EntitySpec) (string, error) {
	desired, err := RenderTranslationCollection(next)
	if err != nil {
		return "", err
	}
	if previous.Translation == nil || next.Translation == nil {
		return "", errors.New("translation collection identity metadata is missing")
	}
	return rewriteCollectionAlias(source, desired, previous.Translation.EntityName, next.Translation.EntityName)
}

func rewriteCollectionAlias(source, desired, previousName, nextName string) (string, error) {
	if previousName == nextName {
		return source, nil
	}
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	_, desiredClass, err := parsedPHPClass(desired)
	if err != nil {
		return "", err
	}
	existing := namedMethod(class, "getApiAlias")
	replacement := namedMethod(desiredClass, "getApiAlias")
	if replacement == nil {
		return "", errors.New("generated collection has no getApiAlias method")
	}
	editor := phprewrite.NewEditor(source, root)
	if existing == nil {
		if err := editor.InsertClassMember(class, strings.TrimSpace(replacement.Text())); err != nil {
			return "", err
		}
	} else {
		if !safeLiteralReturnMethod(existing) {
			return "", errors.New("cannot overwrite customized collection getApiAlias method")
		}
		if err := editor.ReplaceRange(existing.RangeTrimmedTrivia(), strings.TrimSpace(replacement.Text())); err != nil {
			return "", err
		}
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
	managedProperties, managedMethods := managedEntityMembers(previous)
	result, err := rewriteEntitySource(
		source, desired, managedProperties, managedMethods,
		entityImports(CompleteSpec(next)),
		entityImplementationTraits(previous, false),
		entityImplementationTraits(next, false),
	)
	if err != nil {
		return "", err
	}
	return removeObsoleteManagedImports(result, entityImports(CompleteSpec(previous)), entityImports(CompleteSpec(next)))
}

func RewriteTranslationEntity(source string, previous, next EntitySpec) (string, error) {
	desired, err := RenderTranslationEntity(next)
	if err != nil {
		return "", err
	}
	managedProperties, managedMethods := managedTranslationEntityMembers(previous)
	previousClasses := translationEntityImports(CompleteSpec(previous))
	nextClasses := translationEntityImports(CompleteSpec(next))
	result, err := rewriteEntitySource(
		source, desired, managedProperties, managedMethods, nextClasses,
		entityImplementationTraits(previous, true),
		entityImplementationTraits(next, true),
	)
	if err != nil {
		return "", err
	}
	return removeObsoleteManagedImports(result, previousClasses, nextClasses)
}

func rewriteEntitySource(
	source, desired string,
	managedProperties, managedMethods map[string]struct{},
	entityClasses, previousTraits, nextTraits []string,
) (string, error) {
	root, class, err := parsedPHPClass(source)
	if err != nil {
		return "", err
	}
	desiredRoot, desiredClass, err := parsedPHPClass(desired)
	if err != nil {
		return "", err
	}
	_ = desiredRoot
	editor := phprewrite.NewEditor(source, root)
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
	if err := rewriteManagedEntityTraits(editor, root, class, previousTraits, nextTraits); err != nil {
		return "", err
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

func rewriteManagedEntityTraits(
	editor *phprewrite.Editor,
	root, class *phpsyntax.Node,
	previousTraits, nextTraits []string,
) error {
	previous := classNameSet(previousTraits)
	next := classNameSet(nextTraits)
	existing := make(map[string]struct{})
	resolve := importClassResolver(root)
	body := phpquery.ClassBody(class)
	if body == nil {
		return fmt.Errorf("entity class body is unavailable")
	}
	for index := 0; index < body.ChildCount(); index++ {
		declaration, ok := body.Child(index).(*phpsyntax.Node)
		if !ok || declaration.Kind() != phpsyntax.PhpTraitUseDeclaration {
			continue
		}
		text := declaration.Text()
		if strings.Contains(text, "{") || strings.Contains(text, ",") ||
			strings.Contains(text, "//") || strings.Contains(text, "/*") {
			continue
		}
		names := phpquery.Nodes(declaration, phpsyntax.PhpName)
		if len(names) != 1 {
			continue
		}
		className := strings.Trim(resolve(phpquery.NameValue(names[0])), `\ `)
		key := strings.ToLower(className)
		existing[key] = struct{}{}
		if _, wasManaged := previous[key]; !wasManaged {
			continue
		}
		if _, retained := next[key]; retained {
			continue
		}
		if err := editor.RemoveClassMember(declaration); err != nil {
			return err
		}
		delete(existing, key)
	}
	for _, trait := range nextTraits {
		key := strings.ToLower(strings.Trim(trait, `\ `))
		if _, found := existing[key]; found {
			continue
		}
		reference, err := editor.ClassReference(trait)
		if err != nil {
			return err
		}
		if err := editor.InsertClassMember(class, "use "+reference+";"); err != nil {
			return err
		}
		existing[key] = struct{}{}
	}
	return nil
}

func classNameSet(classes []string) map[string]struct{} {
	result := make(map[string]struct{}, len(classes))
	for _, className := range classes {
		if className = strings.ToLower(strings.Trim(className, `\ `)); className != "" {
			result[className] = struct{}{}
		}
	}
	return result
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
	for _, field := range schemaDefinitionFields(CompleteSpec(spec)) {
		if field.Kind == FieldID || field.Kind == FieldVersion || field.Kind == FieldReferenceVersion ||
			field.Kind == FieldCreatedAt || field.Kind == FieldUpdatedAt || field.Kind == FieldLocked {
			continue
		}
		if field.Implementation != nil && !field.Implementation.ManageEntity {
			continue
		}
		if field.Implementation != nil && field.Implementation.EntityTrait != "" {
			continue
		}
		names := []string{field.PropertyName}
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && !field.UsesExistingColumn {
			names = append(names, field.ForeignKeyPropertyName)
		}
		if field.Kind == FieldHierarchy {
			names = append(names, field.ForeignKeyPropertyName, field.HierarchyParentProperty)
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

func managedTranslationEntityMembers(spec EntitySpec) (map[string]struct{}, map[string]struct{}) {
	spec = CompleteSpec(spec)
	properties := make(map[string]struct{})
	methods := make(map[string]struct{})
	if spec.Translation == nil || !spec.Translation.Enabled {
		return properties, methods
	}
	var names []string
	behavior := spec.Translation.DefinitionBehavior
	if behavior == nil || !behavior.OverrideBaseFields {
		names = append(names,
			camelizeStorageName(spec.Translation.ParentStorageName),
			spec.Translation.ParentPropertyName,
		)
		if hasVersionField(spec) {
			names = append(names, camelizeStorageName(strings.TrimSuffix(spec.Translation.ParentStorageName, "_id")+"_version_id"))
		}
	}
	appendFieldNames := func(field FieldSpec) {
		if field.Kind == FieldID || field.Kind == FieldVersion || field.Kind == FieldReferenceVersion ||
			field.Kind == FieldCreatedAt || field.Kind == FieldUpdatedAt || field.Kind == FieldLocked ||
			field.Implementation != nil && (!field.Implementation.ManageEntity || field.Implementation.EntityTrait != "") {
			return
		}
		names = append(names, field.PropertyName)
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && !field.UsesExistingColumn {
			names = append(names, field.ForeignKeyPropertyName)
		}
		if field.Kind == FieldHierarchy {
			names = append(names, field.ForeignKeyPropertyName, field.HierarchyParentProperty)
		}
	}
	if behavior != nil {
		if behavior.OverrideDefaultFields {
			for _, field := range behavior.DefaultFields {
				appendFieldNames(field)
			}
		}
		if behavior.OverrideBaseFields {
			for _, field := range behavior.BaseFields {
				appendFieldNames(field)
			}
		}
	}
	for _, field := range spec.Fields {
		if field.Translated && field.Kind != FieldLocked {
			if field.Implementation != nil && (!field.Implementation.ManageEntity || field.Implementation.EntityTrait != "") {
				continue
			}
			names = append(names, field.PropertyName)
		}
	}
	for _, name := range names {
		properties[name] = struct{}{}
		methods["get"+exported(name)] = struct{}{}
		methods["is"+exported(name)] = struct{}{}
		methods["set"+exported(name)] = struct{}{}
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
