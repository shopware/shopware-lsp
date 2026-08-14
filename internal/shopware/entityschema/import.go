package entityschema

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

var (
	importedEntityNamePattern = regexp.MustCompile(`(?m)\bENTITY_NAME\s*=\s*['"]([^'"]+)['"]`)
	importedAcronymBoundary   = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	importedWordBoundary      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	importedNewClassPattern   = regexp.MustCompile(`(?i)(\bnew\s+)([\\A-Za-z_][\\A-Za-z0-9_]*)`)
	importedScopedRefPattern  = regexp.MustCompile(`([\\A-Za-z_][\\A-Za-z0-9_]*)::([A-Za-z_][A-Za-z0-9_]*)\b`)
)

type RelationTarget struct {
	DefinitionClass  string                `json:"definitionClass"`
	DefinitionKind   DefinitionKind        `json:"definitionKind,omitempty"`
	EntityClass      string                `json:"entityClass"`
	CollectionClass  string                `json:"collectionClass"`
	EntityName       string                `json:"entityName"`
	FileURI          string                `json:"fileUri,omitempty"`
	Fields           []RelationTargetField `json:"fields,omitempty"`
	VersionAware     bool                  `json:"versionAware,omitempty"`
	InheritanceAware bool                  `json:"inheritanceAware,omitempty"`
}

type RelationTargetField struct {
	PropertyName string `json:"propertyName"`
	StorageName  string `json:"storageName"`
	Primary      bool   `json:"primary,omitempty"`
}

// RelationLookup resolves either a fully-qualified definition class or a
// technical entity name. Entity-name resolution is required for Shopware 6.7+
// EntityExtension implementations, whose only mandatory target method is
// getEntityName().
type RelationLookup func(classOrEntityName string) (RelationTarget, bool)

type ImportedTranslation struct {
	Spec   TranslationSpec
	Fields []FieldSpec
}

func ImportDefinition(source string, lookup RelationLookup) (EntitySpec, error) {
	return importDefinitionClass(source, lookup, "", "")
}

// ImportClassBasedDefinition imports a class whose effective DAL base kind was
// resolved by the semantic index or the plugin-local ancestry scanner. This
// keeps custom abstract base classes editable without guessing from a filename
// or replacing the user's direct parent class.
func ImportClassBasedDefinition(source, definitionClass string, kind DefinitionKind, lookup RelationLookup) (EntitySpec, error) {
	switch kind {
	case DefinitionEntity, DefinitionMapping:
		return importDefinitionClass(source, lookup, definitionClass, kind)
	case DefinitionExtension:
		return importExtensionClass(source, lookup, definitionClass)
	case DefinitionBulkExtension:
		return importBulkExtensionClass(source, lookup, definitionClass)
	default:
		return EntitySpec{}, fmt.Errorf("unsupported class-based DAL kind %q", kind)
	}
}

func importDefinitionClass(source string, lookup RelationLookup, selectedClass string, selectedKind DefinitionKind) (EntitySpec, error) {
	tree := phpparser.Parse(source)
	if len(tree.Errors) != 0 || tree.Tree == nil || tree.Tree.Root == nil {
		return EntitySpec{}, fmt.Errorf("parse entity definition: PHP source contains syntax errors")
	}
	root := tree.Tree.Root
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	var definition *phpsyntax.Node
	definitionKind := DefinitionEntity
	for _, class := range phpquery.Classes(root) {
		if selectedClass != "" && strings.EqualFold(qualify(namespace, phpquery.ClassName(class)), strings.Trim(selectedClass, `\`)) {
			definition = class
			definitionKind = selectedKind
			break
		}
		for _, parent := range phpquery.ClassExtends(class) {
			short := ShortClass(parent)
			if short == "EntityDefinition" || short == "MappingEntityDefinition" {
				definition = class
				if short == "MappingEntityDefinition" {
					definitionKind = DefinitionMapping
				}
				break
			}
		}
		if definition != nil {
			break
		}
	}
	if definition == nil {
		return EntitySpec{}, fmt.Errorf("PHP file contains no concrete EntityDefinition")
	}
	className := phpquery.ClassName(definition)
	baseName := strings.TrimSuffix(className, "Definition")
	if baseName == className {
		baseName = strings.TrimSuffix(className, "Extension")
	}
	resolve := importClassResolver(root)
	spec := EntitySpec{
		Mode: "edit", DefinitionKind: definitionKind, Namespace: namespace, ClassName: baseName,
		DefinitionClass: qualify(namespace, className),
		CreateMigration: true,
	}
	if definitionKind == DefinitionEntity {
		spec.EntityClass = qualify(namespace, baseName+"Entity")
		spec.CollectionClass = qualify(namespace, baseName+"Collection")
	}
	if match := importedEntityNamePattern.FindStringSubmatch(definition.Text()); len(match) > 1 {
		spec.EntityName = match[1]
	}
	var defineFields *phpsyntax.Node
	var defineProtections *phpsyntax.Node
	for _, method := range phpquery.Methods(definition) {
		switch phpquery.MethodName(method) {
		case "getEntityName":
			if spec.EntityName == "" {
				if literal, ok := importedLiteralStringReturn(method); ok {
					spec.EntityName = literal
				}
			}
		case "getEntityClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				spec.EntityClass = class
			}
		case "getCollectionClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				spec.CollectionClass = class
			}
		case "isInheritanceAware":
			if value, literal := importedBooleanReturn(method); literal {
				spec.InheritanceAware = value
			}
		case "defineFields":
			defineFields = method
		case "defineProtections":
			defineProtections = method
		}
	}
	spec.DefinitionBehavior = importDefinitionBehavior(phpquery.Methods(definition), resolve, lookup, false)
	spec.DefinitionMetadata = importDefinitionMetadata(
		phpquery.Methods(definition), resolve,
		true,
		definitionKind == DefinitionEntity,
		definitionKind == DefinitionEntity,
	)
	if spec.EntityName == "" {
		return EntitySpec{}, fmt.Errorf("entity definition has no literal entity name")
	}
	if defineFields == nil {
		return EntitySpec{}, fmt.Errorf("entity definition has no defineFields method")
	}
	fields, translation, err := importFields(defineFields, resolve, lookup)
	if err != nil {
		return EntitySpec{}, err
	}
	spec.Fields = fields
	importEntityProtections(&spec, defineProtections)
	if translation != nil {
		translation.ParentDefinitionClass = spec.DefinitionClass
		spec.Translation = translation
	}
	return CompleteSpec(spec), nil
}

func importEntityProtections(spec *EntitySpec, method *phpsyntax.Node) {
	if method == nil {
		return
	}
	collections := phpquery.ObjectCreations(method, "EntityProtectionCollection")
	if len(collections) != 1 {
		spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
		return
	}
	arrays := phpquery.Arrays(collections[0])
	if len(arrays) != 1 {
		spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
		return
	}
	for _, item := range phpquery.ArrayItems(arrays[0]) {
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(item.Text()))
			continue
		}
		creations := phpquery.ObjectCreations(value)
		if len(creations) != 1 {
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(item.Text()))
			continue
		}
		creation := creations[0]
		scopes, recognized := importedProtectionScopes(creation)
		if !recognized {
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(item.Text()))
			continue
		}
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "ReadProtection":
			spec.ReadProtected = true
			spec.ReadProtectionScopes = appendUniqueStrings(spec.ReadProtectionScopes, scopes...)
		case "WriteProtection":
			spec.WriteProtected = true
			spec.WriteProtectionScopes = appendUniqueStrings(spec.WriteProtectionScopes, scopes...)
		default:
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(item.Text()))
		}
	}
}

func importedProtectionScopes(creation *phpsyntax.Node) ([]string, bool) {
	arguments := phpquery.Arguments(creation)
	scopes := make([]string, 0, len(arguments))
	for index, argument := range arguments {
		if phpquery.ArgumentName(argument) != "" {
			return nil, false
		}
		expression := phpquery.ArgumentExpression(creation, index)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			scopes = append(scopes, phpquery.StringValue(expression))
			continue
		}
		scope, recognized := writeScopeConstant(phpquery.ArgumentValueText(creation, index))
		if !recognized {
			return nil, false
		}
		scopes = append(scopes, scope)
	}
	return scopes, true
}

// ImportExtension imports literal $collection->add(...) calls from an
// EntityExtension. The calls are routed through the same field importer as
// EntityDefinition so extension editing never grows a second DAL field model.
func ImportExtension(source string, lookup RelationLookup) (EntitySpec, error) {
	return importExtensionClass(source, lookup, "")
}

func importExtensionClass(source string, lookup RelationLookup, selectedClass string) (EntitySpec, error) {
	tree := phpparser.Parse(source)
	if len(tree.Errors) != 0 || tree.Tree == nil || tree.Tree.Root == nil {
		return EntitySpec{}, fmt.Errorf("parse entity extension: PHP source contains syntax errors")
	}
	root := tree.Tree.Root
	resolve := importClassResolver(root)
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	var extension *phpsyntax.Node
	for _, class := range phpquery.Classes(root) {
		if selectedClass != "" && strings.EqualFold(qualify(namespace, phpquery.ClassName(class)), strings.Trim(selectedClass, `\`)) {
			extension = class
			break
		}
		for _, parent := range phpquery.ClassExtends(class) {
			if ShortClass(parent) == "EntityExtension" {
				extension = class
				break
			}
		}
		if extension != nil {
			break
		}
	}
	if extension == nil {
		return EntitySpec{}, fmt.Errorf("PHP file contains no concrete EntityExtension")
	}
	className := phpquery.ClassName(extension)
	baseName := strings.TrimSuffix(className, "Extension")
	if baseName == className {
		baseName = strings.TrimSuffix(className, "Definition")
	}
	spec := EntitySpec{
		Mode: "edit", DefinitionKind: DefinitionExtension,
		Namespace: namespace, ClassName: baseName,
		DefinitionClass: qualify(namespace, className),
		CreateMigration: true,
	}
	var extendFields *phpsyntax.Node
	var extendProtections *phpsyntax.Node
	var modifyFields *phpsyntax.Node
	for _, method := range phpquery.Methods(extension) {
		switch phpquery.MethodName(method) {
		case "extendFields":
			extendFields = method
		case "extendProtections":
			extendProtections = method
		case "modifyFields":
			modifyFields = method
		case "getDefinitionClass":
			if target := returnedImportedClass(method, resolve); target != "" {
				spec.ExtendedDefinitionClass = target
			}
		case "getEntityName":
			if target := returnedEntityNameDefinitionClass(method, resolve); target != "" {
				spec.ExtendedDefinitionClass = target
			}
			if spec.EntityName == "" {
				if literal, ok := importedLiteralStringReturn(method); ok {
					spec.EntityName = literal
				}
			}
		}
	}
	if spec.ExtendedDefinitionClass == "" && spec.EntityName != "" {
		if target, found := lookupRelationByEntityName(lookup, spec.EntityName); found {
			spec.ExtendedDefinitionClass = target.DefinitionClass
			spec.EntityName = target.EntityName
			spec.ExtendedFields = append([]RelationTargetField(nil), target.Fields...)
		}
	}
	if spec.ExtendedDefinitionClass == "" {
		return EntitySpec{}, fmt.Errorf("entity extension target %q is not present in the indexed DAL catalog", spec.EntityName)
	}
	if target, found := lookupRelation(lookup, spec.ExtendedDefinitionClass); found {
		spec.EntityName = target.EntityName
		spec.ExtendedFields = append([]RelationTargetField(nil), target.Fields...)
	}
	if spec.EntityName == "" {
		return EntitySpec{}, fmt.Errorf("entity extension target has no technical entity name")
	}
	var fields []FieldSpec
	var expressions []string
	var err error
	if extendFields != nil {
		fields, expressions, err = importExtensionFields(root, extendFields, lookup)
		if err != nil {
			return EntitySpec{}, err
		}
	}
	spec.Fields = fields
	importEntityExtensionProtections(&spec, extendProtections)
	importEntityExtensionFieldModifications(&spec, modifyFields, resolve)
	spec = CompleteSpec(spec)
	if len(ValidateSpec(spec)) != 0 {
		// Static extension calls can still contain constants, helper calls, or
		// other values the typed field model cannot prove. Preserve every call
		// losslessly instead of exposing a partly editable, invalid spec.
		spec.Fields = make([]FieldSpec, 0, len(expressions))
		for index, expression := range expressions {
			spec.Fields = append(spec.Fields, lockedField(index, expression))
		}
		spec = CompleteSpec(spec)
	}
	return spec, nil
}

// ImportBulkExtension imports a literal BulkEntityExtension::collect method.
// Each yield is represented as one independently validated extension target
// while reusing the normal DAL field importer.
func ImportBulkExtension(source string, lookup RelationLookup) (EntitySpec, error) {
	return importBulkExtensionClass(source, lookup, "")
}

func importBulkExtensionClass(source string, lookup RelationLookup, selectedClass string) (EntitySpec, error) {
	tree := phpparser.Parse(source)
	if len(tree.Errors) != 0 || tree.Tree == nil || tree.Tree.Root == nil {
		return EntitySpec{}, fmt.Errorf("parse bulk entity extension: PHP source contains syntax errors")
	}
	root := tree.Tree.Root
	resolve := importClassResolver(root)
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	var extension *phpsyntax.Node
	for _, class := range phpquery.Classes(root) {
		if selectedClass != "" && strings.EqualFold(qualify(namespace, phpquery.ClassName(class)), strings.Trim(selectedClass, `\`)) {
			extension = class
			break
		}
		for _, parent := range phpquery.ClassExtends(class) {
			if ShortClass(parent) == "BulkEntityExtension" {
				extension = class
				break
			}
		}
		if extension != nil {
			break
		}
	}
	if extension == nil {
		return EntitySpec{}, fmt.Errorf("PHP file contains no concrete BulkEntityExtension")
	}
	className := phpquery.ClassName(extension)
	baseName := strings.TrimSuffix(className, "BulkEntityExtension")
	if baseName == className {
		baseName = strings.TrimSuffix(className, "BulkExtension")
	}
	if baseName == className {
		baseName = strings.TrimSuffix(className, "Extension")
	}
	if baseName == className {
		baseName = strings.TrimSuffix(className, "Definition")
	}
	spec := EntitySpec{
		Mode: "edit", DefinitionKind: DefinitionBulkExtension,
		Namespace: namespace, ClassName: baseName,
		DefinitionClass: qualify(namespace, className),
		CreateMigration: true,
	}
	var collect *phpsyntax.Node
	for _, method := range phpquery.Methods(extension) {
		if phpquery.MethodName(method) == "collect" {
			collect = method
			break
		}
	}
	if collect == nil {
		return EntitySpec{}, fmt.Errorf("bulk entity extension has no collect method")
	}
	preserveCollect := func() EntitySpec {
		spec.BulkExtensions = nil
		spec.CollectMethodRaw = strings.TrimSpace(collect.Text())
		return CompleteSpec(spec)
	}
	yields := phpquery.Nodes(collect, phpsyntax.PhpYieldExpression)
	if len(yields) == 0 || len(phpquery.Nodes(collect, phpsyntax.PhpExpressionStatement)) != len(yields) {
		return preserveCollect(), nil
	}
	for index, yield := range yields {
		key, value := importedYieldParts(yield)
		if key == nil || value == nil || value.Kind() != phpsyntax.PhpArray {
			return preserveCollect(), nil
		}
		target := BulkExtensionTargetSpec{ID: fmt.Sprintf("bulk-target-%d", index)}
		if key.Kind() == phpsyntax.PhpString {
			target.EntityName = phpquery.StringValue(key)
			if relation, found := lookupRelationByEntityName(lookup, target.EntityName); found {
				target.ExtendedDefinitionClass = relation.DefinitionClass
				target.ExtendedFields = append([]RelationTargetField(nil), relation.Fields...)
			}
		} else if className := phpquery.ScopedAccessClass(key, "ENTITY_NAME"); className != "" {
			target.ExtendedDefinitionClass = resolve(className)
			if relation, found := lookupRelation(lookup, target.ExtendedDefinitionClass); found {
				target.EntityName = relation.EntityName
				target.ExtendedFields = append([]RelationTargetField(nil), relation.Fields...)
			}
		}
		if !entityNamePattern.MatchString(target.EntityName) {
			return preserveCollect(), nil
		}
		var expressions []string
		for _, item := range phpquery.ArrayItems(value) {
			expression := phpquery.ArrayItemValue(item)
			if expression == nil {
				return preserveCollect(), nil
			}
			expressions = append(expressions, strings.TrimSpace(expression.Text()))
		}
		fields, err := importExtensionFieldExpressions(root, expressions, lookup)
		if err != nil {
			return preserveCollect(), nil
		}
		target.Fields = fields
		targetSpec := bulkTargetEntitySpec(spec, target)
		if targetSpec.ExtendedDefinitionClass == "" {
			targetSpec.ExtendedDefinitionClass = spec.DefinitionClass
		}
		if len(ValidateSpec(targetSpec)) != 0 {
			target.Fields = make([]FieldSpec, 0, len(expressions))
			for expressionIndex, expression := range expressions {
				target.Fields = append(target.Fields, lockedField(expressionIndex, expression))
			}
		}
		spec.BulkExtensions = append(spec.BulkExtensions, target)
	}
	return CompleteSpec(spec), nil
}

func importedYieldParts(yield *phpsyntax.Node) (*phpsyntax.Node, *phpsyntax.Node) {
	var key, value *phpsyntax.Node
	afterArrow := false
	for index := 0; index < yield.ChildCount(); index++ {
		switch child := yield.Child(index).(type) {
		case *phpsyntax.Token:
			if child.Kind() == phpsyntax.TkArrow {
				afterArrow = true
			}
		case *phpsyntax.Node:
			if !afterArrow {
				key = child
			} else if value == nil {
				value = child
			}
		}
	}
	return key, value
}

func importEntityExtensionFieldModifications(spec *EntitySpec, method *phpsyntax.Node, resolve func(string) string) {
	if method == nil {
		return
	}
	lock := func() {
		spec.FieldModifications = nil
		spec.ModifyFieldsMethodRaw = strings.TrimSpace(method.Text())
	}
	if strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		lock()
		return
	}
	byProperty := make(map[string]int)
	var mutationCalls int
	for _, call := range phpquery.Calls(method) {
		operation := phpquery.CallMethodName(call)
		if operation == "get" {
			continue
		}
		if operation != "addFlags" && operation != "removeFlag" {
			lock()
			return
		}
		property := modifiedFieldProperty(call)
		if property == "" {
			lock()
			return
		}
		index, found := byProperty[property]
		if !found {
			index = len(spec.FieldModifications)
			byProperty[property] = index
			spec.FieldModifications = append(spec.FieldModifications, FieldModificationSpec{
				ID: "modify-" + property, PropertyName: property,
			})
		}
		modification := &spec.FieldModifications[index]
		switch operation {
		case "addFlags":
			for argumentIndex := range phpquery.Arguments(call) {
				expression := phpquery.ArgumentExpression(call, argumentIndex)
				creation := outerObjectCreation(expression)
				if creation == nil || strings.TrimSpace(creation.Text()) != strings.TrimSpace(expression.Text()) {
					lock()
					return
				}
				flag, recognized := importedFieldModificationFlag(creation, resolve)
				if !recognized {
					lock()
					return
				}
				modification.AddFlags = append(modification.AddFlags, flag)
			}
		case "removeFlag":
			className := importedClassArgument(call, 0)
			if className == "" {
				lock()
				return
			}
			kind, recognized := fieldFlagKindForClass(resolve(className))
			if !recognized {
				lock()
				return
			}
			modification.RemoveFlags = append(modification.RemoveFlags, kind)
		}
		mutationCalls++
	}
	if len(phpquery.Nodes(method, phpsyntax.PhpExpressionStatement)) != mutationCalls {
		lock()
	}
}

func modifiedFieldProperty(call *phpsyntax.Node) string {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return ""
	}
	candidates := phpquery.Calls(receiver)
	if phpquery.CallMethodName(receiver) == "get" {
		candidates = append([]*phpsyntax.Node{receiver}, candidates...)
	}
	for _, candidate := range candidates {
		if phpquery.CallMethodName(candidate) != "get" {
			continue
		}
		collection := phpquery.CallReceiver(candidate)
		if collection == nil || strings.TrimSpace(collection.Text()) != "$collection" {
			continue
		}
		expression := phpquery.ArgumentExpression(candidate, 0)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			return phpquery.StringValue(expression)
		}
	}
	return ""
}

func fieldFlagKindForClass(className string) (FieldFlagKind, bool) {
	short := ShortClass(className)
	for kind, candidate := range fieldFlagClasses {
		if ShortClass(candidate) == short {
			return kind, true
		}
	}
	return "", false
}

func importedFieldModificationFlag(creation *phpsyntax.Node, resolve func(string) string) (FieldFlagSpec, bool) {
	kind, recognized := fieldFlagKindForClass(resolve(phpquery.ObjectClassName(creation)))
	if !recognized {
		return FieldFlagSpec{}, false
	}
	flag := FieldFlagSpec{Kind: kind}
	switch kind {
	case FlagAPIAware:
		flag.APISources, recognized = importedAPIAwareSources(creation, resolve)
	case FlagSearchRanking:
		flag.SearchRanking, flag.SearchTokenize, recognized = importedSearchRanking(creation)
	case FlagRuntime:
		flag.RuntimeDependencies, flag.RuntimeDependenciesExpression = importedRuntimeDependencies(creation)
	case FlagInherited:
		flag.InheritedForeignKey = importedStringArgument(creation, 0)
		recognized = len(phpquery.Arguments(creation)) == 0 || flag.InheritedForeignKey != ""
	case FlagReverseInherited:
		flag.ReverseProperty = importedStringArgument(creation, 0)
		recognized = flag.ReverseProperty != "" && len(phpquery.Arguments(creation)) == 1
	case FlagWriteProtected:
		flag.WriteScopes, recognized = parseWriteProtectedFlag(strings.TrimSpace(creation.Text()))
	case FlagAllowHTML:
		value, valid := importedDefaultBoolArgument(creation, 0, true)
		flag.AllowHTMLSanitized, recognized = &value, valid
	case FlagCascadeDelete:
		if len(phpquery.Arguments(creation)) != 0 {
			value, valid := importedDefaultBoolArgument(creation, 0, true)
			flag.CloneRelevant, recognized = &value, valid
		}
	case FlagSetNullOnDelete:
		if len(phpquery.Arguments(creation)) != 0 {
			value, valid := importedDefaultBoolArgument(creation, 0, false)
			flag.EnforcedByConstraint, recognized = &value, valid
		}
	case FlagSince:
		flag.Since = importedStringArgument(creation, 0)
		recognized = flag.Since != "" && len(phpquery.Arguments(creation)) == 1
	case FlagDeprecated:
		flag.Deprecated, recognized = importedDeprecation(creation)
	case FlagRuleAreas:
		flag.RuleAreas, recognized = importedRuleAreas(creation)
	case FlagChoice:
		flag.Choice, recognized = importedChoice(creation)
	default:
		recognized = len(phpquery.Arguments(creation)) == 0
	}
	return flag, recognized
}

func importEntityExtensionProtections(spec *EntitySpec, method *phpsyntax.Node) {
	if method == nil {
		return
	}
	if strings.Contains(method.Text(), "//") || strings.Contains(method.Text(), "/*") || strings.Contains(method.Text(), "#[") {
		spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
		return
	}
	var expressions []*phpsyntax.Node
	for _, call := range phpquery.Calls(method) {
		receiver := phpquery.CallReceiver(call)
		if phpquery.CallMethodName(call) != "add" || receiver == nil || strings.TrimSpace(receiver.Text()) != "$protections" {
			spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
			return
		}
		expression := phpquery.ArgumentExpression(call, 0)
		if expression == nil {
			spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
			return
		}
		expressions = append(expressions, expression)
	}
	if len(phpquery.Nodes(method, phpsyntax.PhpExpressionStatement)) != len(expressions) {
		spec.ProtectionMethodRaw = strings.TrimSpace(method.Text())
		return
	}
	for _, expression := range expressions {
		creations := phpquery.ObjectCreations(expression)
		if len(creations) != 1 || strings.TrimSpace(creations[0].Text()) != strings.TrimSpace(expression.Text()) {
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(expression.Text()))
			continue
		}
		creation := creations[0]
		scopes, recognized := importedProtectionScopes(creation)
		if !recognized {
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(expression.Text()))
			continue
		}
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "ReadProtection":
			spec.ReadProtected = true
			spec.ReadProtectionScopes = appendUniqueStrings(spec.ReadProtectionScopes, scopes...)
		case "WriteProtection":
			spec.WriteProtected = true
			spec.WriteProtectionScopes = appendUniqueStrings(spec.WriteProtectionScopes, scopes...)
		default:
			spec.PreservedProtections = append(spec.PreservedProtections, strings.TrimSpace(expression.Text()))
		}
	}
}

func importExtensionFields(root, method *phpsyntax.Node, lookup RelationLookup) ([]FieldSpec, []string, error) {
	var expressions []string
	for _, call := range phpquery.Calls(method) {
		if phpquery.CallMethodName(call) != "add" {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || strings.TrimSpace(receiver.Text()) != "$collection" {
			continue
		}
		argument := phpquery.Argument(call, 0)
		if argument != nil {
			expressions = append(expressions, strings.TrimSpace(argument.Text()))
		}
	}
	if len(expressions) == 0 {
		if len(phpquery.ObjectCreations(method)) != 0 {
			return nil, nil, fmt.Errorf("entity extension fields are not literal $collection->add calls")
		}
		return nil, nil, nil
	}
	fields, err := importExtensionFieldExpressions(root, expressions, lookup)
	return fields, expressions, err
}

func importExtensionFieldExpressions(root *phpsyntax.Node, expressions []string, lookup RelationLookup) ([]FieldSpec, error) {
	if len(expressions) == 0 {
		return nil, nil
	}
	var synthetic strings.Builder
	synthetic.WriteString("<?php declare(strict_types=1);\nnamespace ")
	synthetic.WriteString(strings.Trim(phpquery.Namespace(root), `\`))
	synthetic.WriteString(";\n")
	for _, declaration := range phpquery.UseDeclarations(root) {
		synthetic.WriteString(strings.TrimSpace(declaration.Text()))
		synthetic.WriteByte('\n')
	}
	synthetic.WriteString("class ShopwareLspImportedDefinition extends \\Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition {\n")
	synthetic.WriteString("public const ENTITY_NAME = 'shopware_lsp_extension';\n")
	synthetic.WriteString("protected function defineFields(): \\Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection { return new FieldCollection([\n")
	synthetic.WriteString(strings.Join(expressions, ",\n"))
	synthetic.WriteString("\n]); }\n}\n")
	imported, err := ImportDefinition(synthetic.String(), lookup)
	if err != nil {
		return nil, fmt.Errorf("import entity extension fields: %w", err)
	}
	return imported.Fields, nil
}

func returnedEntityNameDefinitionClass(method *phpsyntax.Node, resolve func(string) string) string {
	returned := singleReturnOnly(method)
	if returned == nil {
		return ""
	}
	expression := returnedExpressionText(returned)
	for _, access := range phpquery.Nodes(returned, phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess) {
		if strings.TrimSpace(access.Text()) != expression {
			continue
		}
		if className := phpquery.ScopedAccessClass(access, "ENTITY_NAME"); className != "" {
			return resolve(className)
		}
	}
	return ""
}

func importedBooleanReturn(method *phpsyntax.Node) (bool, bool) {
	returned := singleReturnOnly(method)
	if returned == nil {
		return false, false
	}
	booleans := phpquery.Nodes(returned, phpsyntax.PhpBoolean)
	if len(booleans) != 1 || strings.TrimSpace(booleans[0].Text()) != returnedExpressionText(returned) {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(booleans[0].Text())) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// ImportTranslationDefinition imports the literal, designer-owned portion of
// an EntityTranslationDefinition. AttachTranslation pairs it with the parent
// definition's TranslatedField facades.
func ImportTranslationDefinition(source string, lookup RelationLookup) (ImportedTranslation, error) {
	return importTranslationClass(source, lookup, "")
}

func importTranslationClass(source string, lookup RelationLookup, selectedClass string) (ImportedTranslation, error) {
	tree := phpparser.Parse(source)
	if len(tree.Errors) != 0 || tree.Tree == nil || tree.Tree.Root == nil {
		return ImportedTranslation{}, fmt.Errorf("parse translation definition: PHP source contains syntax errors")
	}
	root := tree.Tree.Root
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	var definition *phpsyntax.Node
	for _, class := range phpquery.Classes(root) {
		if selectedClass != "" && strings.EqualFold(qualify(namespace, phpquery.ClassName(class)), strings.Trim(selectedClass, `\`)) {
			definition = class
			break
		}
		for _, parent := range phpquery.ClassExtends(class) {
			if ShortClass(parent) == "EntityTranslationDefinition" {
				definition = class
				break
			}
		}
		if definition != nil {
			break
		}
	}
	if definition == nil {
		return ImportedTranslation{}, fmt.Errorf("PHP file contains no EntityTranslationDefinition")
	}
	className := phpquery.ClassName(definition)
	baseName := strings.TrimSuffix(className, "Definition")
	resolve := importClassResolver(root)
	result := ImportedTranslation{Spec: TranslationSpec{
		Enabled: true, DefinitionClass: qualify(namespace, className),
		EntityClass: qualify(namespace, baseName+"Entity"), CollectionClass: qualify(namespace, baseName+"Collection"),
	}}
	if match := importedEntityNamePattern.FindStringSubmatch(definition.Text()); len(match) > 1 {
		result.Spec.EntityName = match[1]
	}
	var defineFields *phpsyntax.Node
	for _, method := range phpquery.Methods(definition) {
		switch phpquery.MethodName(method) {
		case "getEntityName":
			if result.Spec.EntityName == "" {
				if literal, ok := importedLiteralStringReturn(method); ok {
					result.Spec.EntityName = literal
				}
			}
		case "getEntityClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				result.Spec.EntityClass = class
			}
		case "getCollectionClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				result.Spec.CollectionClass = class
			}
		case "getParentDefinitionClass":
			result.Spec.ParentDefinitionClass = returnedImportedClass(method, resolve)
		case "defineFields":
			defineFields = method
		}
	}
	result.Spec.DefinitionMetadata = importDefinitionMetadata(
		phpquery.Methods(definition), resolve, true, false, false,
	)
	result.Spec.DefinitionBehavior = importDefinitionBehavior(phpquery.Methods(definition), resolve, lookup, true)
	if result.Spec.EntityName == "" && result.Spec.ParentDefinitionClass != "" {
		if target, found := lookupRelation(lookup, result.Spec.ParentDefinitionClass); found {
			result.Spec.EntityName = target.EntityName + "_translation"
		} else if parentName := inferEntityName(result.Spec.ParentDefinitionClass); parentName != "" {
			result.Spec.EntityName = parentName + "_translation"
		}
	}
	if result.Spec.EntityName == "" || result.Spec.ParentDefinitionClass == "" || defineFields == nil {
		return ImportedTranslation{}, fmt.Errorf("translation definition has incomplete literal identity or fields")
	}
	fields, _, err := importFields(defineFields, resolve, lookup)
	if err != nil {
		return ImportedTranslation{}, err
	}
	result.Fields = fields
	return result, nil
}

// AttachTranslation replaces parent TranslatedField placeholders with the
// concrete storage fields imported from the companion definition.
func AttachTranslation(parent EntitySpec, imported ImportedTranslation) EntitySpec {
	childByProperty := make(map[string]int)
	for index, field := range imported.Fields {
		if field.Kind != FieldLocked && field.PropertyName != "" {
			childByProperty[field.PropertyName] = index
		}
	}
	used := make(map[int]struct{})
	combined := make([]FieldSpec, 0, len(parent.Fields)+len(imported.Fields))
	for _, parentField := range parent.Fields {
		childIndex, found := childByProperty[parentField.PropertyName]
		if !parentField.Translated || !found {
			combined = append(combined, parentField)
			continue
		}
		field := imported.Fields[childIndex]
		field.ID = parentField.ID
		field.Translated = true
		field.TranslationUseForSort = parentField.TranslationUseForSort
		field.TranslationAPIAware = parentField.TranslationAPIAware
		field.TranslationAPIAwareSources = append([]string(nil), parentField.TranslationAPIAwareSources...)
		field.TranslationSearchRank = parentField.TranslationSearchRank
		field.TranslationSearchTokenize = parentField.TranslationSearchTokenize
		field.TranslationBehavior = parentField.TranslationBehavior
		field.TranslationMetadata = parentField.TranslationMetadata
		field.TranslationWriteProtected = parentField.TranslationWriteProtected
		field.TranslationWriteScopes = append([]string(nil), parentField.TranslationWriteScopes...)
		field.TranslationInherited = parentField.TranslationInherited
		field.TranslationInheritedFK = parentField.TranslationInheritedFK
		field.TranslationFlags = append([]string(nil), parentField.TranslationFlags...)
		field.TranslationBeforeFlags = append([]string(nil), parentField.TranslationBeforeFlags...)
		field.TranslationAfterFlags = append([]string(nil), parentField.TranslationAfterFlags...)
		combined = append(combined, field)
		used[childIndex] = struct{}{}
	}
	for index, field := range imported.Fields {
		if _, found := used[index]; found {
			continue
		}
		field.ID = "translation-" + field.ID
		field.Translated = true
		field.TranslationDefinitionOnly = true
		combined = append(combined, field)
	}
	parent.Fields = combined
	translation := imported.Spec
	if parent.Translation != nil {
		translation.ParentStorageName = parent.Translation.ParentStorageName
		translation.ParentPropertyName = parent.Translation.ParentPropertyName
		translation.AssociationProperty = parent.Translation.AssociationProperty
		translation.AssociationLocalField = parent.Translation.AssociationLocalField
		translation.AssociationRequired = parent.Translation.AssociationRequired
		translation.AssociationAPIAware = parent.Translation.AssociationAPIAware
		translation.AssociationAPIAwareSources = append([]string(nil), parent.Translation.AssociationAPIAwareSources...)
		translation.AssociationBehavior = parent.Translation.AssociationBehavior
		translation.AssociationMetadata = parent.Translation.AssociationMetadata
		translation.AssociationWriteProtected = parent.Translation.AssociationWriteProtected
		translation.AssociationWriteScopes = append([]string(nil), parent.Translation.AssociationWriteScopes...)
		translation.AssociationInherited = parent.Translation.AssociationInherited
		translation.AssociationInheritedFK = parent.Translation.AssociationInheritedFK
		translation.ReverseInheritedProperty = parent.Translation.ReverseInheritedProperty
		translation.AssociationFlags = append([]string(nil), parent.Translation.AssociationFlags...)
		translation.AssociationBeforeFlags = append([]string(nil), parent.Translation.AssociationBeforeFlags...)
		translation.AssociationAfterFlags = append([]string(nil), parent.Translation.AssociationAfterFlags...)
	}
	parent.Translation = &translation
	return CompleteSpec(parent)
}

type importedForeignKey struct {
	field FieldSpec
	raw   string
}

func importFields(method *phpsyntax.Node, resolve func(string) string, lookup RelationLookup) ([]FieldSpec, *TranslationSpec, error) {
	collections := phpquery.ObjectCreations(method, "FieldCollection")
	if len(collections) == 0 {
		return nil, nil, fmt.Errorf("defineFields does not return a literal FieldCollection")
	}
	array := importedFieldCollectionArray(method, collections[0])
	if array == nil {
		return nil, nil, fmt.Errorf("defineFields FieldCollection has no literal array")
	}
	var fields []FieldSpec
	var translation *TranslationSpec
	foreignKeys := make(map[string]importedForeignKey)
	localFields := importedLocalFields(method, resolve, lookup)
	hierarchyIndex := -1
	ensureHierarchy := func(id string) int {
		if hierarchyIndex >= 0 {
			return hierarchyIndex
		}
		hierarchyIndex = len(fields)
		fields = append(fields, FieldSpec{
			ID: id, Kind: FieldHierarchy, PropertyName: "children",
			HierarchyParentProperty: "parent", ForeignKeyPropertyName: "parentId",
			StorageName: "parent_id", ReferenceField: "id", ReferenceStorageName: "id",
			DeleteBehavior: DeleteCascade, Editable: true,
		})
		return hierarchyIndex
	}
	for itemIndex, item := range phpquery.ArrayItems(array) {
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			continue
		}
		creations := phpquery.ObjectCreations(value)
		if len(creations) == 0 {
			if variable := directVariableExpression(value); variable != "" {
				if local, found := localFields[variable]; found {
					local.ID = fmt.Sprintf("field-%d", itemIndex+1)
					fields = append(fields, local)
					continue
				}
			}
			fields = append(fields, lockedField(itemIndex, item.Text()))
			continue
		}
		creation := creations[0]
		kindName := ShortClass(phpquery.ObjectClassName(creation))
		flags := importedFlags(value, creation, resolve)
		modifiers := importedModifiers(value, creation)
		raw := strings.TrimSpace(item.Text())
		id := fmt.Sprintf("field-%d", itemIndex+1)
		if conditional, found := importConditionalAssociationValue(id, raw, value, flags, modifiers, resolve, lookup); found {
			fields = append(fields, conditional)
			continue
		}
		if specialized, found := importSpecializedField(id, raw, creation, flags, modifiers, resolve); found {
			fields = append(fields, specialized)
			continue
		}
		if simple, importedTranslation, found := importSimpleDefinitionField(
			itemIndex, id, raw, kindName, creation, flags, modifiers, resolve, lookup,
		); found {
			if importedTranslation != nil {
				translation = importedTranslation
			}
			if simple.Kind != "" {
				fields = append(fields, simple)
			}
			continue
		}
		switch kindName {
		case "ParentFkField":
			index := ensureHierarchy(id)
			field := &fields[index]
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 0))
			field.Required = false
			field.Primary = false
			field.APIAware = flags.apiAware
			field.APIAwareSources = append([]string(nil), flags.apiAwareSources...)
			field.SearchRanking = flags.ranking
			field.SearchRankingTokenize = flags.rankingTokenize
			field.Behavior = flags.behavior
			field.Metadata = flags.metadata
			field.Inherited = flags.inherited
			field.InheritedForeignKey = flags.inheritedForeignKey
			field.PreservedFlags = flags.preserved
			field.Raw = raw
			*field = withFieldModifiers(*field, modifiers)
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(field, target)
			}
		case "ParentAssociationField":
			index := ensureHierarchy(id)
			field := &fields[index]
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 0))
			field.ReferenceField = defaultString(importedStringArgument(creation, 1), "id")
			field.ReferenceStorageName = field.ReferenceField
			field.AssociationFlags = flags.preserved
			field.AssociationAPIAware = flags.apiAware
			field.AssociationAPIAwareSources = append([]string(nil), flags.apiAwareSources...)
			field.AssociationSearchRank = flags.ranking
			field.AssociationSearchTokenize = flags.rankingTokenize
			field.AssociationBehavior = flags.behavior
			field.AssociationMetadata = flags.metadata
			field.AssociationInherited = flags.inherited
			field.AssociationInheritedFK = flags.inheritedForeignKey
			field.ReverseInheritedProperty = flags.reverseInheritedProperty
			*field = withAssociationModifiers(*field, modifiers)
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(field, target)
			}
		case "ChildrenAssociationField":
			index := ensureHierarchy(id)
			field := &fields[index]
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 0))
			field.PropertyName = defaultString(importedStringArgument(creation, 1), "children")
			field.HierarchyChildrenFlags = flags.preserved
			field.HierarchyChildrenAPIAware = flags.apiAware
			field.HierarchyChildrenAPISources = append([]string(nil), flags.apiAwareSources...)
			field.HierarchyChildrenRank = flags.ranking
			field.HierarchyChildrenTokenize = flags.rankingTokenize
			field.HierarchyChildrenBehavior = flags.behavior
			field.HierarchyChildrenMetadata = flags.metadata
			field.HierarchyChildrenInherited = flags.inherited
			field.HierarchyChildrenInheritedFK = flags.inheritedForeignKey
			field.HierarchyChildrenReverse = flags.reverseInheritedProperty
			field.HierarchyChildrenBefore = modifiers.beforeFlags
			field.HierarchyChildrenAfter = modifiers.afterFlags
			field.DeleteBehavior = DeleteCascade
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(field, target)
			}
		case "FkField":
			storage := importedStringArgument(creation, 0)
			foreignKeys[storage] = importedForeignKey{field: withFieldModifiers(FieldSpec{
				ID: id, Kind: FieldManyToOne, StorageName: storage,
				ForeignKeyPropertyName: importedStringArgument(creation, 1),
				TargetDefinitionClass:  resolve(importedClassArgument(creation, 2)),
				ReferenceField:         defaultString(importedStringArgument(creation, 3), "id"),
				Required:               flags.required, Primary: flags.primary, APIAware: flags.apiAware,
				APIAwareSources: append([]string(nil), flags.apiAwareSources...),
				SearchRanking:   flags.ranking, SearchRankingTokenize: flags.rankingTokenize, Behavior: flags.behavior, Metadata: flags.metadata,
				Inherited: flags.inherited, InheritedForeignKey: flags.inheritedForeignKey,
				PreservedFlags: flags.preserved, Editable: true,
			}, modifiers), raw: raw}
		case "ManyToOneAssociationField":
			storage := importedStringArgument(creation, 1)
			foreignKey, found := foreignKeys[storage]
			if !found {
				field := FieldSpec{
					ID: id, Kind: FieldManyToOne,
					PropertyName:               importedStringArgument(creation, 0),
					StorageName:                storage,
					TargetDefinitionClass:      resolve(importedClassArgument(creation, 2)),
					ReferenceField:             defaultString(importedStringArgument(creation, 3), "id"),
					ReferenceStorageName:       defaultString(importedStringArgument(creation, 3), "id"),
					DeleteBehavior:             importedDeleteBehavior(creations),
					AssociationFlags:           flags.preserved,
					AssociationAPIAware:        flags.apiAware,
					AssociationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
					AssociationSearchRank:      flags.ranking,
					AssociationSearchTokenize:  flags.rankingTokenize,
					AssociationBehavior:        flags.behavior,
					AssociationMetadata:        flags.metadata,
					AssociationAutoload:        importedAssociationAutoload(creation, false),
					AssociationInherited:       flags.inherited,
					AssociationInheritedFK:     flags.inheritedForeignKey,
					ReverseInheritedProperty:   flags.reverseInheritedProperty,
					UsesExistingColumn:         true,
					Editable:                   true,
					Raw:                        raw,
				}
				applyImportedDeleteOptions(&field, creations)
				field = withAssociationModifiers(field, modifiers)
				if target, targetFound := lookupRelation(lookup, field.TargetDefinitionClass); targetFound {
					enrichRelation(&field, target)
				}
				fields = append(fields, field)
				continue
			}
			field := foreignKey.field
			field.PropertyName = importedStringArgument(creation, 0)
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 2))
			field.ReferenceField = defaultString(importedStringArgument(creation, 3), "id")
			field.ReferenceStorageName = field.ReferenceField
			field.AssociationAutoload = importedAssociationAutoload(creation, false)
			field.DeleteBehavior = importedDeleteBehavior(creations)
			applyImportedDeleteOptions(&field, creations)
			associationFlags := importedFlags(value, creation, resolve)
			field.AssociationFlags = associationFlags.preserved
			field.AssociationAPIAware = associationFlags.apiAware
			field.AssociationAPIAwareSources = append([]string(nil), associationFlags.apiAwareSources...)
			field.AssociationSearchRank = associationFlags.ranking
			field.AssociationSearchTokenize = associationFlags.rankingTokenize
			field.AssociationBehavior = associationFlags.behavior
			field.AssociationMetadata = associationFlags.metadata
			field.AssociationInherited = associationFlags.inherited
			field.AssociationInheritedFK = associationFlags.inheritedForeignKey
			field.ReverseInheritedProperty = associationFlags.reverseInheritedProperty
			field = withAssociationModifiers(field, modifiers)
			field.Raw = foreignKey.raw + ",\n" + raw
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			fields = append(fields, field)
			delete(foreignKeys, storage)
		case "OneToOneAssociationField":
			storage := importedStringArgument(creation, 1)
			foreignKey, found := foreignKeys[storage]
			if !found {
				field := FieldSpec{
					ID:                         id,
					Kind:                       FieldOneToOne,
					PropertyName:               importedStringArgument(creation, 0),
					StorageName:                storage,
					ReferenceField:             defaultString(importedStringArgument(creation, 2), "id"),
					ReferenceStorageName:       defaultString(importedStringArgument(creation, 2), "id"),
					TargetDefinitionClass:      resolve(importedClassArgument(creation, 3)),
					DeleteBehavior:             importedDeleteBehavior(creations),
					AssociationFlags:           flags.preserved,
					AssociationAPIAware:        flags.apiAware,
					AssociationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
					AssociationSearchRank:      flags.ranking,
					AssociationSearchTokenize:  flags.rankingTokenize,
					AssociationBehavior:        flags.behavior,
					AssociationMetadata:        flags.metadata,
					AssociationAutoload:        importedAssociationAutoload(creation, true),
					AssociationInherited:       flags.inherited,
					AssociationInheritedFK:     flags.inheritedForeignKey,
					ReverseInheritedProperty:   flags.reverseInheritedProperty,
					UsesExistingColumn:         true,
					Editable:                   true,
					Raw:                        raw,
				}
				field = withAssociationModifiers(field, modifiers)
				applyImportedDeleteOptions(&field, creations)
				if target, targetFound := lookupRelation(lookup, field.TargetDefinitionClass); targetFound {
					enrichRelation(&field, target)
				}
				fields = append(fields, field)
				continue
			}
			field := foreignKey.field
			field.Kind = FieldOneToOne
			field.PropertyName = importedStringArgument(creation, 0)
			field.ReferenceField = defaultString(importedStringArgument(creation, 2), "id")
			field.ReferenceStorageName = field.ReferenceField
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 3))
			field.AssociationAutoload = importedAssociationAutoload(creation, true)
			field.DeleteBehavior = importedDeleteBehavior(creations)
			applyImportedDeleteOptions(&field, creations)
			field.AssociationFlags = flags.preserved
			field.AssociationAPIAware = flags.apiAware
			field.AssociationAPIAwareSources = append([]string(nil), flags.apiAwareSources...)
			field.AssociationSearchRank = flags.ranking
			field.AssociationSearchTokenize = flags.rankingTokenize
			field.AssociationBehavior = flags.behavior
			field.AssociationMetadata = flags.metadata
			field.AssociationInherited = flags.inherited
			field.AssociationInheritedFK = flags.inheritedForeignKey
			field.ReverseInheritedProperty = flags.reverseInheritedProperty
			field = withAssociationModifiers(field, modifiers)
			field.Raw = foreignKey.raw + ",\n" + raw
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			fields = append(fields, field)
			delete(foreignKeys, storage)
		case "OneToManyAssociationField":
			field := FieldSpec{
				ID: id, Kind: FieldOneToMany,
				PropertyName:               importedStringArgument(creation, 0),
				TargetDefinitionClass:      resolve(importedClassArgument(creation, 1)),
				ReferenceStorageName:       importedStringArgument(creation, 2),
				SourceColumn:               defaultString(importedStringArgument(creation, 3), "id"),
				DeleteBehavior:             importedDeleteBehavior(creations),
				AssociationFlags:           flags.preserved,
				AssociationAPIAware:        flags.apiAware,
				AssociationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
				AssociationSearchRank:      flags.ranking,
				AssociationSearchTokenize:  flags.rankingTokenize,
				AssociationBehavior:        flags.behavior,
				AssociationMetadata:        flags.metadata,
				AssociationInherited:       flags.inherited,
				AssociationInheritedFK:     flags.inheritedForeignKey,
				ReverseInheritedProperty:   flags.reverseInheritedProperty,
				Editable:                   true, Raw: raw,
			}
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			applyImportedDeleteOptions(&field, creations)
			field = withAssociationModifiers(field, modifiers)
			fields = append(fields, field)
		case "ManyToManyAssociationField":
			field := FieldSpec{
				ID:                         id,
				Kind:                       FieldManyToMany,
				PropertyName:               importedStringArgument(creation, 0),
				TargetDefinitionClass:      resolve(importedClassArgument(creation, 1)),
				MappingDefinitionClass:     resolve(importedClassArgument(creation, 2)),
				MappingLocalColumn:         importedStringArgument(creation, 3),
				MappingReferenceColumn:     importedStringArgument(creation, 4),
				SourceColumn:               defaultString(importedStringArgument(creation, 5), "id"),
				ReferenceField:             defaultString(importedStringArgument(creation, 6), "id"),
				DeleteBehavior:             importedDeleteBehavior(creations),
				AssociationFlags:           flags.preserved,
				AssociationAPIAware:        flags.apiAware,
				AssociationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
				AssociationSearchRank:      flags.ranking,
				AssociationSearchTokenize:  flags.rankingTokenize,
				AssociationBehavior:        flags.behavior,
				AssociationMetadata:        flags.metadata,
				AssociationInherited:       flags.inherited,
				AssociationInheritedFK:     flags.inheritedForeignKey,
				ReverseInheritedProperty:   flags.reverseInheritedProperty,
				Editable:                   true,
				Raw:                        raw,
			}
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			applyImportedDeleteOptions(&field, creations)
			field = withAssociationModifiers(field, modifiers)
			fields = append(fields, field)
		default:
			fields = append(fields, lockedField(itemIndex, raw))
		}
	}
	fields = collapseImportedHierarchy(fields, hierarchyIndex)
	fields = appendUnassociatedForeignKeys(fields, foreignKeys)
	promoteImportedWriteProtection(fields, translation)
	return fields, translation, nil
}

func importedEnumCase(creation *phpsyntax.Node, resolve func(string) string) (string, string) {
	expression := phpquery.ArgumentExpression(creation, 2)
	if expression == nil {
		return "", ""
	}
	text := strings.TrimSpace(expression.Text())
	separator := strings.LastIndex(text, "::")
	if separator <= 0 {
		return "", ""
	}
	className := strings.TrimSpace(text[:separator])
	caseName := strings.TrimSpace(text[separator+2:])
	if className == "" || !propertyPattern.MatchString(caseName) {
		return "", ""
	}
	return resolve(className), caseName
}

// importedFieldCollectionArray supports both new FieldCollection([...]) and
// the common, equivalent local form `$fields = [...]; return new
// FieldCollection($fields);`. It deliberately refuses mutation between the
// literal assignment and constructor so a dynamic collection is never
// misrepresented as a complete static schema.
func importedFieldCollectionArray(method, collection *phpsyntax.Node) *phpsyntax.Node {
	if arrays := phpquery.Arrays(collection); len(arrays) != 0 {
		return arrays[0]
	}
	variable := directVariableExpression(phpquery.ArgumentExpression(collection, 0))
	if variable == "" {
		return nil
	}
	var result *phpsyntax.Node
	for _, statement := range phpquery.ExpressionStatements(method) {
		if statement.Range().Start >= collection.Range().Start {
			break
		}
		if phpquery.AssignedVariable(statement) != variable {
			continue
		}
		value := phpquery.AssignmentValue(statement)
		if value == nil || value.Kind() != phpsyntax.PhpArray {
			result = nil
			continue
		}
		result = value
	}
	if result == nil {
		return nil
	}
	// Appends, element assignments, and mutating method calls would mean the
	// literal is only a partial view of the collection.
	between := method.Text()[result.Range().End-method.Range().Start : collection.Range().Start-method.Range().Start]
	if strings.Contains(between, variable+"[") || strings.Contains(between, variable+"->") {
		return nil
	}
	return result
}

func importConditionalAssociationValue(
	id, raw string,
	value *phpsyntax.Node,
	flags importedFlagSet,
	modifiers importedModifierSet,
	resolve func(string) string,
	lookup RelationLookup,
) (FieldSpec, bool) {
	ternaries := phpquery.Nodes(value, phpsyntax.PhpTernaryExpression)
	if len(ternaries) != 1 {
		return FieldSpec{}, false
	}
	creations := phpquery.ObjectCreations(ternaries[0])
	if len(creations) != 2 {
		return FieldSpec{}, false
	}
	first, firstOK := importedStandaloneAssociation(creations[0], resolve)
	second, secondOK := importedStandaloneAssociation(creations[1], resolve)
	if !firstOK || !secondOK || !sameImportedAssociationIdentity(first, second) {
		return FieldSpec{}, false
	}
	parts := directNodeChildren(ternaries[0])
	if len(parts) < 3 {
		return FieldSpec{}, false
	}
	first.ID = id
	first.Raw = raw
	first.ConditionalAssociation = &ConditionalAssociation{
		ConditionExpression: normalizeImportedPHPExpression(parts[0].Text(), resolve),
		AlternativeKind:     second.Kind, AlternativeAutoload: second.AssociationAutoload,
	}
	first.AssociationAPIAware = flags.apiAware
	first.AssociationAPIAwareSources = append([]string(nil), flags.apiAwareSources...)
	first.AssociationSearchRank = flags.ranking
	first.AssociationSearchTokenize = flags.rankingTokenize
	first.AssociationBehavior = flags.behavior
	first.AssociationMetadata = flags.metadata
	first.AssociationInherited = flags.inherited
	first.AssociationInheritedFK = flags.inheritedForeignKey
	first.ReverseInheritedProperty = flags.reverseInheritedProperty
	first.AssociationFlags = append([]string(nil), flags.preserved...)
	first = withAssociationModifiers(first, modifiers)
	if target, found := lookupRelation(lookup, first.TargetDefinitionClass); found {
		enrichRelation(&first, target)
	}
	return first, true
}

func directVariableExpression(node *phpsyntax.Node) string {
	if node == nil || node.Kind() != phpsyntax.PhpVariable {
		return ""
	}
	if name := phpquery.VariableName(node); name != "" {
		return "$" + name
	}
	return ""
}

// importedLocalFields resolves the narrow, deterministic pattern used by
// version-gated Shopware association declarations: a local variable assigned
// a ternary whose two branches are association constructors, followed by
// addFlags()/description modifiers. It intentionally refuses branches that do
// not describe the same logical association.
func importedLocalFields(method *phpsyntax.Node, resolve func(string) string, lookup RelationLookup) map[string]FieldSpec {
	result := make(map[string]FieldSpec)
	statements := phpquery.ExpressionStatements(method)
	for index, statement := range statements {
		variable := phpquery.AssignedVariable(statement)
		value := phpquery.AssignmentValue(statement)
		if variable == "" || value == nil || value.Kind() != phpsyntax.PhpTernaryExpression {
			continue
		}
		creations := phpquery.ObjectCreations(value)
		if len(creations) != 2 {
			continue
		}
		first, firstOK := importedStandaloneAssociation(creations[0], resolve)
		second, secondOK := importedStandaloneAssociation(creations[1], resolve)
		if !firstOK || !secondOK || !sameImportedAssociationIdentity(first, second) {
			continue
		}
		// Prefer the first branch: it represents the enabled target-version
		// shape in Shopware Feature::isActive() declarations.
		field := first
		parts := directNodeChildren(value)
		if len(parts) < 3 {
			continue
		}
		field.ConditionalAssociation = &ConditionalAssociation{
			ConditionExpression: normalizeImportedPHPExpression(parts[0].Text(), resolve),
			AlternativeKind:     second.Kind, AlternativeAutoload: second.AssociationAutoload,
		}
		for _, following := range statements[index+1:] {
			if phpquery.AssignedVariable(following) != "" {
				break
			}
			text := strings.TrimSpace(following.Text())
			if !strings.HasPrefix(text, variable+"->") {
				continue
			}
			flagSet := importedVariableFlags(following, resolve)
			field.AssociationAPIAware = flagSet.apiAware
			field.AssociationAPIAwareSources = append([]string(nil), flagSet.apiAwareSources...)
			field.AssociationSearchRank = flagSet.ranking
			field.AssociationSearchTokenize = flagSet.rankingTokenize
			field.AssociationBehavior = flagSet.behavior
			field.AssociationMetadata = flagSet.metadata
			field.AssociationInherited = flagSet.inherited
			field.AssociationInheritedFK = flagSet.inheritedForeignKey
			field.ReverseInheritedProperty = flagSet.reverseInheritedProperty
			field.AssociationFlags = append([]string(nil), flagSet.preserved...)
			field.AssociationBeforeFlags = importedVariableModifiers(following, true)
			field.AssociationAfterFlags = importedVariableModifiers(following, false)
			break
		}
		if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
			enrichRelation(&field, target)
		}
		result[variable] = field
	}
	return result
}

func directNodeChildren(node *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	if node == nil {
		return result
	}
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
}

func importedStandaloneAssociation(creation *phpsyntax.Node, resolve func(string) string) (FieldSpec, bool) {
	field := FieldSpec{Editable: true, UsesExistingColumn: true, Raw: strings.TrimSpace(creation.Text())}
	switch ShortClass(phpquery.ObjectClassName(creation)) {
	case "OneToOneAssociationField":
		field.Kind = FieldOneToOne
		field.PropertyName = importedStringArgument(creation, 0)
		field.StorageName = importedStringArgument(creation, 1)
		field.ReferenceField = defaultString(importedStringArgument(creation, 2), "id")
		field.ReferenceStorageName = field.ReferenceField
		field.TargetDefinitionClass = resolve(importedClassArgument(creation, 3))
		field.AssociationAutoload = importedAssociationAutoload(creation, true)
	case "ManyToOneAssociationField":
		field.Kind = FieldManyToOne
		field.PropertyName = importedStringArgument(creation, 0)
		field.StorageName = importedStringArgument(creation, 1)
		field.TargetDefinitionClass = resolve(importedClassArgument(creation, 2))
		field.ReferenceField = defaultString(importedStringArgument(creation, 3), "id")
		field.ReferenceStorageName = field.ReferenceField
		field.AssociationAutoload = importedAssociationAutoload(creation, false)
	default:
		return FieldSpec{}, false
	}
	return field, field.PropertyName != "" && field.StorageName != "" && field.TargetDefinitionClass != ""
}

func sameImportedAssociationIdentity(left, right FieldSpec) bool {
	return left.PropertyName == right.PropertyName && left.StorageName == right.StorageName &&
		left.ReferenceField == right.ReferenceField && left.TargetDefinitionClass == right.TargetDefinitionClass
}

func importedVariableFlags(statement *phpsyntax.Node, resolve func(string) string) importedFlagSet {
	var result importedFlagSet
	for _, call := range phpquery.Calls(statement) {
		if call.Kind() != phpsyntax.PhpMemberCall || phpquery.CallMethodName(call) != "addFlags" {
			continue
		}
		for argument := range phpquery.Arguments(call) {
			source := strings.TrimSpace(phpquery.ArgumentValueText(call, argument))
			creation := outerObjectCreation(phpquery.ArgumentExpression(call, argument))
			if creation != nil {
				importFlagCreation(&result, creation, source, resolve)
			} else if source != "" {
				result.preserved = append(result.preserved, source)
			}
		}
	}
	return result
}

func importedVariableModifiers(statement *phpsyntax.Node, before bool) []string {
	calls := phpquery.Calls(statement)
	seenFlags := false
	var result []string
	for _, call := range calls {
		method := phpquery.CallMethodName(call)
		if method == "addFlags" {
			seenFlags = true
			continue
		}
		if seenFlags == before {
			continue
		}
		if suffix := importedCallSuffix(call, method); suffix != "" {
			result = append(result, suffix)
		}
	}
	return result
}

func promoteImportedWriteProtection(fields []FieldSpec, translation *TranslationSpec) {
	for index := range fields {
		field := &fields[index]
		promoteWriteProtectedFlags(&field.WriteProtected, &field.WriteProtectedScopes, &field.PreservedFlags)
		promoteWriteProtectedFlags(&field.AssociationWriteProtected, &field.AssociationWriteScopes, &field.AssociationFlags)
		promoteWriteProtectedFlags(&field.TranslationWriteProtected, &field.TranslationWriteScopes, &field.TranslationFlags)
		promoteWriteProtectedFlags(&field.HierarchyChildrenProtected, &field.HierarchyChildrenWriteScopes, &field.HierarchyChildrenFlags)
		promoteWriteProtectedFlags(&field.HierarchyVersionProtected, &field.HierarchyVersionWriteScopes, &field.HierarchyVersionFlags)
	}
	if translation != nil {
		promoteWriteProtectedFlags(
			&translation.AssociationWriteProtected,
			&translation.AssociationWriteScopes,
			&translation.AssociationFlags,
		)
	}
}

func promoteWriteProtectedFlags(enabled *bool, scopes *[]string, values *[]string) {
	remaining := make([]string, 0, len(*values))
	for _, value := range *values {
		parsedScopes, recognized := parseWriteProtectedFlag(value)
		if !recognized {
			remaining = append(remaining, value)
			continue
		}
		*enabled = true
		*scopes = appendUniqueStrings(*scopes, parsedScopes...)
	}
	*values = remaining
}

func parseWriteProtectedFlag(source string) ([]string, bool) {
	parsed := phpparser.Parse("<?php " + source + ";")
	if len(parsed.Errors) != 0 || parsed.Tree == nil || parsed.Tree.Root == nil {
		return nil, false
	}
	creations := phpquery.ObjectCreations(parsed.Tree.Root)
	if len(creations) != 1 || ShortClass(phpquery.ObjectClassName(creations[0])) != "WriteProtected" {
		return nil, false
	}
	creation := creations[0]
	arguments := phpquery.Arguments(creation)
	scopes := make([]string, 0, len(arguments))
	for index, argument := range arguments {
		expression := phpquery.ArgumentExpression(creation, index)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			scopes = append(scopes, phpquery.StringValue(expression))
			continue
		}
		scope, recognized := writeScopeConstant(phpquery.ArgumentValueText(creation, index))
		if !recognized || phpquery.ArgumentName(argument) != "" {
			return nil, false
		}
		scopes = append(scopes, scope)
	}
	return scopes, true
}

func writeScopeConstant(expression string) (string, bool) {
	normalized := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(expression), " ", ""), `\`)
	switch normalized {
	case "Context::SYSTEM_SCOPE", `Shopware\Core\Framework\Context::SYSTEM_SCOPE`:
		return "system", true
	case "Context::USER_SCOPE", `Shopware\Core\Framework\Context::USER_SCOPE`:
		return "user", true
	case "Context::CRUD_API_SCOPE", `Shopware\Core\Framework\Context::CRUD_API_SCOPE`:
		return "crud", true
	case "Context::SYSTEM_SCOPE_DAL_WRITE_EVENT", `Shopware\Core\Framework\Context::SYSTEM_SCOPE_DAL_WRITE_EVENT`:
		return "system-scope-dal-write-event", true
	default:
		return "", false
	}
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, found := seen[value]; found {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}

func importReferenceVersionField(
	id, raw string,
	creation *phpsyntax.Node,
	flags importedFlagSet,
	modifiers importedModifierSet,
	resolve func(string) string,
	lookup RelationLookup,
) FieldSpec {
	storage := importedStringArgument(creation, 1)
	targetClass := resolve(importedClassArgument(creation, 0))
	field := FieldSpec{
		ID: id, Kind: FieldReferenceVersion,
		StorageName:           storage,
		PropertyName:          camelizeStorageName(storage),
		TargetDefinitionClass: targetClass,
		Required:              flags.required,
		Primary:               flags.primary,
		APIAware:              flags.apiAware,
		APIAwareSources:       append([]string(nil), flags.apiAwareSources...),
		SearchRanking:         flags.ranking,
		SearchRankingTokenize: flags.rankingTokenize,
		Behavior:              flags.behavior,
		Metadata:              flags.metadata,
		Inherited:             flags.inherited,
		InheritedForeignKey:   flags.inheritedForeignKey,
		PreservedFlags:        flags.preserved,
		Editable:              true,
		Raw:                   raw,
	}
	if target, found := lookupRelation(lookup, targetClass); found {
		enrichRelation(&field, target)
		if field.StorageName == "" {
			field.StorageName = target.EntityName + "_version_id"
			field.PropertyName = camelizeStorageName(field.StorageName)
		}
	}
	return withFieldModifiers(field, modifiers)
}

func collapseImportedHierarchy(fields []FieldSpec, hierarchyIndex int) []FieldSpec {
	if hierarchyIndex < 0 {
		return fields
	}
	hierarchy := fields[hierarchyIndex]
	referenceVersionIndex := -1
	for index, field := range fields {
		if index == hierarchyIndex || field.Kind != FieldReferenceVersion ||
			field.StorageName != "parent_version_id" ||
			field.TargetDefinitionClass != hierarchy.TargetDefinitionClass {
			continue
		}
		referenceVersionIndex = index
		hierarchy.HierarchyVersionAware = true
		hierarchy.HierarchyVersionAPIAware = field.APIAware
		hierarchy.HierarchyVersionAPISources = append([]string(nil), field.APIAwareSources...)
		hierarchy.HierarchyVersionBehavior = field.Behavior
		hierarchy.HierarchyVersionMetadata = field.Metadata
		hierarchy.HierarchyVersionInherited = field.Inherited
		hierarchy.HierarchyVersionInheritedFK = field.InheritedForeignKey
		hierarchy.HierarchyVersionFlags = append([]string(nil), field.PreservedFlags...)
		hierarchy.HierarchyVersionBefore = append([]string(nil), field.ModifiersBeforeFlags...)
		hierarchy.HierarchyVersionAfter = append([]string(nil), field.ModifiersAfterFlags...)
		break
	}
	collapsed := make([]FieldSpec, 0, len(fields))
	for index, field := range fields {
		if index == referenceVersionIndex {
			continue
		}
		if index == hierarchyIndex {
			field = hierarchy
		}
		collapsed = append(collapsed, field)
	}
	return collapsed
}

func appendUnassociatedForeignKeys(fields []FieldSpec, foreignKeys map[string]importedForeignKey) []FieldSpec {
	foreignKeyNames := make([]string, 0, len(foreignKeys))
	for storage := range foreignKeys {
		foreignKeyNames = append(foreignKeyNames, storage)
	}
	sort.Strings(foreignKeyNames)
	for _, storage := range foreignKeyNames {
		foreignKey := foreignKeys[storage]
		field := foreignKey.field
		field.Kind = FieldForeignKey
		field.PropertyName = field.ForeignKeyPropertyName
		field.ForeignKeyPropertyName = ""
		field.Raw = foreignKey.raw
		fields = append(fields, field)
	}
	return fields
}

func importedScalarKind(name string) FieldKind {
	switch name {
	case "StringField":
		return FieldString
	case "LongTextField":
		return FieldLongText
	case "IntField":
		return FieldInt
	case "FloatField":
		return FieldFloat
	case "BoolField":
		return FieldBool
	case "DateField":
		return FieldDate
	case "DateTimeField":
		return FieldDateTime
	case "JsonField":
		return FieldJSON
	case "ListField":
		return FieldList
	case "ObjectField":
		return FieldObject
	case "BlobField":
		return FieldBlob
	default:
		return FieldLocked
	}
}

type importedFlagSet struct {
	required                 bool
	primary                  bool
	apiAware                 bool
	apiAwareSources          []string
	ranking                  float64
	rankingTokenize          *bool
	behavior                 *FieldBehavior
	metadata                 *FieldMetadata
	inherited                bool
	inheritedForeignKey      string
	reverseInheritedProperty string
	preserved                []string
}

type importedModifierSet struct {
	beforeFlags []string
	afterFlags  []string
}

func importedModifiers(value, creation *phpsyntax.Node) importedModifierSet {
	calls := phpquery.Calls(value)
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].Range().End != calls[j].Range().End {
			return calls[i].Range().End < calls[j].Range().End
		}
		return calls[i].Range().Start > calls[j].Range().Start
	})
	var result importedModifierSet
	seenFlags := false
	for _, call := range calls {
		if call.Kind() != phpsyntax.PhpMemberCall || call.Range().Start > creation.Range().Start || call.Range().End < creation.Range().End {
			continue
		}
		method := phpquery.CallMethodName(call)
		if method == "addFlags" {
			seenFlags = true
			continue
		}
		suffix := importedCallSuffix(call, method)
		if suffix == "" {
			continue
		}
		if seenFlags {
			result.afterFlags = append(result.afterFlags, suffix)
		} else {
			result.beforeFlags = append(result.beforeFlags, suffix)
		}
	}
	return result
}

func importedCallSuffix(call *phpsyntax.Node, method string) string {
	if method == "" {
		return ""
	}
	text := call.Text()
	index := strings.LastIndex(text, "->"+method)
	if index < 0 {
		return ""
	}
	return dedentInlinePHPExpression(text[index:])
}

func withFieldModifiers(field FieldSpec, modifiers importedModifierSet) FieldSpec {
	field.ModifiersBeforeFlags = append([]string(nil), modifiers.beforeFlags...)
	field.ModifiersAfterFlags = append([]string(nil), modifiers.afterFlags...)
	return field
}

func withAssociationModifiers(field FieldSpec, modifiers importedModifierSet) FieldSpec {
	field.AssociationBeforeFlags = append([]string(nil), modifiers.beforeFlags...)
	field.AssociationAfterFlags = append([]string(nil), modifiers.afterFlags...)
	return field
}

func importedFlags(value, fieldCreation *phpsyntax.Node, resolve func(string) string) importedFlagSet {
	var result importedFlagSet
	for _, call := range phpquery.Calls(value) {
		if call.Kind() != phpsyntax.PhpMemberCall || phpquery.CallMethodName(call) != "addFlags" ||
			call.Range().Start > fieldCreation.Range().Start || call.Range().End < fieldCreation.Range().End {
			continue
		}
		for index := range phpquery.Arguments(call) {
			source := strings.TrimSpace(phpquery.ArgumentValueText(call, index))
			expression := phpquery.ArgumentExpression(call, index)
			creation := outerObjectCreation(expression)
			if creation == nil {
				if source != "" {
					result.preserved = append(result.preserved, source)
				}
				continue
			}
			importFlagCreation(&result, creation, source, resolve)
		}
	}
	return result
}

func outerObjectCreation(expression *phpsyntax.Node) *phpsyntax.Node {
	creations := phpquery.ObjectCreations(expression)
	if len(creations) == 0 {
		return nil
	}
	outer := creations[0]
	for _, creation := range creations[1:] {
		if creation.Range().End-creation.Range().Start > outer.Range().End-outer.Range().Start {
			outer = creation
		}
	}
	return outer
}

func importFlagCreation(result *importedFlagSet, creation *phpsyntax.Node, source string, resolve func(string) string) {
	preserve := func() {
		if source == "" {
			source = strings.TrimSpace(creation.Text())
		}
		result.preserved = append(result.preserved, source)
	}
	switch ShortClass(phpquery.ObjectClassName(creation)) {
	case "Required":
		result.required = true
	case "PrimaryKey":
		result.primary = true
	case "ApiAware":
		sources, recognized := importedAPIAwareSources(creation, resolve)
		if !recognized {
			preserve()
			return
		}
		result.apiAware = true
		result.apiAwareSources = sources
	case "SearchRanking":
		ranking, tokenize, recognized := importedSearchRanking(creation)
		if !recognized {
			preserve()
			return
		}
		result.ranking = ranking
		result.rankingTokenize = tokenize
	case "Runtime":
		behavior := ensureImportedBehavior(result)
		behavior.Runtime = true
		behavior.RuntimeDependencies, behavior.RuntimeDependenciesExpression = importedRuntimeDependencies(creation)
	case "Computed":
		ensureImportedBehavior(result).Computed = true
	case "NoConstraint":
		ensureImportedBehavior(result).NoConstraint = true
	case "AllowHtml":
		value, recognized := importedDefaultBoolArgument(creation, 0, true)
		if !recognized {
			preserve()
			return
		}
		ensureImportedMetadata(result).AllowHTML = &value
	case "AllowEmptyString":
		ensureImportedMetadata(result).AllowEmptyString = true
	case "AsArray":
		ensureImportedMetadata(result).AsArray = true
	case "Immutable":
		ensureImportedMetadata(result).Immutable = true
	case "Since":
		if value := importedStringArgument(creation, 0); value != "" {
			ensureImportedMetadata(result).Since = value
		} else {
			preserve()
		}
	case "Deprecated":
		if deprecated, recognized := importedDeprecation(creation); recognized {
			ensureImportedMetadata(result).Deprecated = deprecated
		} else {
			preserve()
		}
	case "IgnoreInOpenapiSchema":
		ensureImportedMetadata(result).IgnoreInOpenAPISchema = true
	case "IgnoreInUnusedMediaSearch":
		ensureImportedMetadata(result).IgnoreInUnusedMediaSearch = true
	case "ApiCriteriaAware":
		ensureImportedMetadata(result).APICriteriaAware = true
	case "RuleAreas":
		if areas, recognized := importedRuleAreas(creation); recognized {
			ensureImportedMetadata(result).RuleAreas = areas
		} else {
			preserve()
		}
	case "Choice":
		if choice, recognized := importedChoice(creation); recognized {
			ensureImportedMetadata(result).Choice = choice
		} else {
			preserve()
		}
	case "DoNotUseContext":
		ensureImportedMetadata(result).DoNotUseContext = true
	case "Extension":
		ensureImportedMetadata(result).Extension = true
	case "Inherited":
		result.inherited = true
		result.inheritedForeignKey = importedStringArgument(creation, 0)
	case "ReverseInherited":
		result.reverseInheritedProperty = importedStringArgument(creation, 0)
	case "CascadeDelete", "SetNullOnDelete", "RestrictDelete":
		// Represented structurally by the field kind or relation behavior.
	default:
		preserve()
	}
}

func ensureImportedBehavior(flags *importedFlagSet) *FieldBehavior {
	if flags.behavior == nil {
		flags.behavior = &FieldBehavior{}
	}
	return flags.behavior
}

func ensureImportedMetadata(flags *importedFlagSet) *FieldMetadata {
	if flags.metadata == nil {
		flags.metadata = &FieldMetadata{}
	}
	return flags.metadata
}

func importedDefaultBoolArgument(creation *phpsyntax.Node, index int, fallback bool) (bool, bool) {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return fallback, true
	}
	value := strings.ToLower(strings.TrimSpace(phpquery.ArgumentValueText(creation, index)))
	if value != "true" && value != "false" {
		return false, false
	}
	return value == "true", true
}

func importedAPIAwareSources(creation *phpsyntax.Node, resolve func(string) string) ([]string, bool) {
	arguments := phpquery.Arguments(creation)
	if len(arguments) == 0 {
		return nil, true
	}
	sources := make([]string, 0, len(arguments))
	for index := range arguments {
		class := importedClassArgument(creation, index)
		if class == "" {
			class = importedStringArgument(creation, index)
		}
		class = strings.Trim(resolve(class), `\ `)
		if class == "" {
			return nil, false
		}
		sources = appendUniqueStrings(sources, class)
	}
	return sources, true
}

// normalizeImportedPHPExpression makes class references self-contained so a
// generated definition does not depend on aliases from the source file's old
// use list. Other syntax remains byte-for-byte unchanged.
func normalizeImportedPHPExpression(source string, resolve func(string) string) string {
	source = dedentInlinePHPExpression(source)
	qualify := func(class string) string {
		if strings.HasPrefix(strings.TrimSpace(class), `\`) {
			return `\` + strings.Trim(class, `\ `)
		}
		switch strings.ToLower(strings.TrimSpace(class)) {
		case "", "self", "static", "parent", "class":
			return class
		}
		resolved := strings.Trim(resolve(class), `\ `)
		if resolved == "" {
			return class
		}
		return `\` + resolved
	}
	source = importedNewClassPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := importedNewClassPattern.FindStringSubmatch(match)
		return parts[1] + qualify(parts[2])
	})
	return importedScopedRefPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := importedScopedRefPattern.FindStringSubmatch(match)
		return qualify(parts[1]) + "::" + parts[2]
	})
}

func dedentInlinePHPExpression(source string) string {
	lines := strings.Split(strings.TrimSpace(source), "\n")
	if len(lines) < 2 {
		return strings.TrimSpace(source)
	}
	minimum := -1
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minimum < 0 || indent < minimum {
			minimum = indent
		}
	}
	if minimum > 0 {
		for index := 1; index < len(lines); index++ {
			if len(lines[index]) >= minimum {
				lines[index] = lines[index][minimum:]
			}
		}
	}
	return strings.Join(lines, "\n")
}

func importedDeprecation(creation *phpsyntax.Node) (*Deprecation, bool) {
	arguments := phpquery.Arguments(creation)
	if len(arguments) < 2 || len(arguments) > 3 {
		return nil, false
	}
	deprecated := &Deprecation{
		DeprecatedSince: importedStringArgument(creation, 0),
		WillBeRemovedIn: importedStringArgument(creation, 1),
	}
	if len(arguments) == 3 {
		deprecated.ReplacedBy = importedStringArgument(creation, 2)
	}
	return deprecated, deprecated.DeprecatedSince != "" && deprecated.WillBeRemovedIn != ""
}

func importedRuleAreas(creation *phpsyntax.Node) ([]string, bool) {
	arguments := phpquery.Arguments(creation)
	areas := make([]string, 0, len(arguments))
	for index := range arguments {
		expression := phpquery.ArgumentExpression(creation, index)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			areas = append(areas, phpquery.StringValue(expression))
			continue
		}
		normalized := strings.ReplaceAll(strings.TrimSpace(phpquery.ArgumentValueText(creation, index)), " ", "")
		separator := strings.LastIndex(normalized, "::")
		if separator < 0 {
			return nil, false
		}
		constant := normalized[separator+2:]
		switch constant {
		case "PRODUCT_AREA":
			areas = append(areas, "product")
		case "PAYMENT_AREA":
			areas = append(areas, "payment")
		case "SHIPPING_AREA":
			areas = append(areas, "shipping")
		case "PROMOTION_AREA":
			areas = append(areas, "promotion")
		case "FLOW_AREA":
			areas = append(areas, "flow")
		case "FLOW_CONDITION_AREA":
			areas = append(areas, "flow-condition")
		case "CATEGORY_AREA":
			areas = append(areas, "category")
		case "LANDING_PAGE_AREA":
			areas = append(areas, "landing-page")
		default:
			return nil, false
		}
	}
	return areas, true
}

func importedChoice(creation *phpsyntax.Node) (*ChoiceSpec, bool) {
	arguments := phpquery.Arguments(creation)
	if len(arguments) == 0 || len(arguments) > 2 {
		return nil, false
	}
	expression := phpquery.ArgumentExpression(creation, 0)
	if expression == nil {
		return nil, false
	}
	arrays := phpquery.Arrays(expression)
	if len(arrays) != 1 || arrays[0].Range() != expression.Range() {
		return nil, false
	}
	choice := &ChoiceSpec{}
	for _, item := range phpquery.ArrayItems(arrays[0]) {
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			return nil, false
		}
		choice.Values = append(choice.Values, strings.TrimSpace(value.Text()))
	}
	if len(arguments) == 2 {
		strict, recognized := importedDefaultBoolArgument(creation, 1, false)
		if !recognized {
			return nil, false
		}
		choice.Strict = &strict
	}
	return choice, true
}

func importedSearchRanking(creation *phpsyntax.Node) (float64, *bool, bool) {
	arguments := phpquery.Arguments(creation)
	if len(arguments) == 0 || len(arguments) > 2 {
		return 0, nil, false
	}
	rankingExpression := strings.TrimSpace(phpquery.ArgumentValueText(creation, 0))
	ranking, err := strconv.ParseFloat(rankingExpression, 64)
	if err != nil {
		normalized := strings.ReplaceAll(strings.TrimPrefix(rankingExpression, `\`), " ", "")
		switch {
		case strings.HasSuffix(normalized, "SearchRanking::ASSOCIATION_SEARCH_RANKING"):
			ranking = 0.25
		case strings.HasSuffix(normalized, "SearchRanking::LOW_SEARCH_RANKING"):
			ranking = 80
		case strings.HasSuffix(normalized, "SearchRanking::MIDDLE_SEARCH_RANKING"):
			ranking = 250
		case strings.HasSuffix(normalized, "SearchRanking::HIGH_SEARCH_RANKING"):
			ranking = 500
		default:
			return 0, nil, false
		}
	}
	if len(arguments) == 1 {
		return ranking, nil, true
	}
	value := strings.ToLower(strings.TrimSpace(phpquery.ArgumentValueText(creation, 1)))
	if value != "true" && value != "false" {
		return 0, nil, false
	}
	tokenize := value == "true"
	return ranking, &tokenize, true
}

func importedRuntimeDependencies(creation *phpsyntax.Node) ([]string, string) {
	arguments := phpquery.Arguments(creation)
	if len(arguments) == 0 {
		return nil, ""
	}
	expression := phpquery.ArgumentExpression(creation, 0)
	if expression == nil {
		return nil, strings.TrimSpace(phpquery.ArgumentValueText(creation, 0))
	}
	arrays := phpquery.Arrays(expression)
	if len(arrays) != 1 || arrays[0].Range() != expression.Range() {
		return nil, strings.TrimSpace(expression.Text())
	}
	items := phpquery.ArrayItems(arrays[0])
	dependencies := make([]string, 0, len(items))
	for _, item := range items {
		value := phpquery.ArrayItemValue(item)
		if value == nil || value.Kind() != phpsyntax.PhpString {
			return nil, strings.TrimSpace(expression.Text())
		}
		dependencies = append(dependencies, phpquery.StringValue(value))
	}
	return dependencies, ""
}

func camelizeStorageName(value string) string {
	parts := strings.Split(value, "_")
	if len(parts) == 0 {
		return value
	}
	for index := 1; index < len(parts); index++ {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "")
}

func importedDeleteBehavior(creations []*phpsyntax.Node) DeleteBehavior {
	for _, creation := range creations[1:] {
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "CascadeDelete":
			return DeleteCascade
		case "SetNullOnDelete":
			return DeleteSetNull
		case "RestrictDelete":
			return DeleteRestrict
		}
	}
	return ""
}

func applyImportedDeleteOptions(field *FieldSpec, creations []*phpsyntax.Node) {
	for _, creation := range creations[1:] {
		arguments := phpquery.Arguments(creation)
		if len(arguments) == 0 {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(phpquery.ArgumentValueText(creation, 0)))
		if value != "true" && value != "false" {
			continue
		}
		option := value == "true"
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "CascadeDelete":
			field.DeleteCloneRelevant = &option
		case "SetNullOnDelete":
			field.DeleteEnforcedByConstraint = &option
		}
	}
}

func importedStringArgument(creation *phpsyntax.Node, index int) string {
	return phpquery.StringValue(phpquery.StringArgument(creation, index))
}

func importedClassArgument(creation *phpsyntax.Node, index int) string {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return ""
	}
	return phpquery.ClassConstantName(arguments[index])
}

func importedIntArgument(creation *phpsyntax.Node, index, fallback int) int {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(arguments[index].Text()))
	if err != nil {
		return fallback
	}
	return value
}

func importedBoolArgument(creation *phpsyntax.Node, index int) bool {
	arguments := phpquery.Arguments(creation)
	return index >= 0 && index < len(arguments) && strings.EqualFold(strings.TrimSpace(arguments[index].Text()), "true")
}

func importedAssociationAutoload(creation *phpsyntax.Node, fallback bool) bool {
	arguments := phpquery.Arguments(creation)
	for index, argument := range arguments {
		if strings.EqualFold(phpquery.ArgumentName(argument), "autoload") {
			return strings.EqualFold(phpquery.ArgumentValueText(creation, index), "true")
		}
	}
	if len(arguments) <= 4 {
		return fallback
	}
	return strings.EqualFold(phpquery.ArgumentValueText(creation, 4), "true")
}

func importedOptionalIntArgument(creation *phpsyntax.Node, index int) *int {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) || strings.EqualFold(strings.TrimSpace(arguments[index].Text()), "null") {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(arguments[index].Text()))
	if err != nil {
		return nil
	}
	return &value
}

func returnedImportedClass(method *phpsyntax.Node, resolve func(string) string) string {
	returned := singleReturnOnly(method)
	if returned == nil {
		return ""
	}
	expression := returnedExpressionText(returned)
	for _, access := range phpquery.Nodes(returned, phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess) {
		if strings.TrimSpace(access.Text()) != expression {
			continue
		}
		if className := phpquery.ClassConstantName(access); className != "" {
			return resolve(className)
		}
	}
	return ""
}

func singleReturnOnly(method *phpsyntax.Node) *phpsyntax.Node {
	if method == nil {
		return nil
	}
	returns := phpquery.Nodes(method, phpsyntax.PhpReturnStatement)
	if len(returns) != 1 {
		return nil
	}
	text := method.Text()
	open := strings.IndexByte(text, '{')
	close := strings.LastIndexByte(text, '}')
	if open < 0 || close <= open || strings.TrimSpace(text[open+1:close]) != strings.TrimSpace(returns[0].Text()) {
		return nil
	}
	return returns[0]
}

func returnedExpressionText(returned *phpsyntax.Node) string {
	if returned == nil {
		return ""
	}
	statement := strings.TrimSpace(returned.Text())
	if len(statement) < len("return") || !strings.EqualFold(statement[:len("return")], "return") {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement[len("return"):]), ";"))
}

func importClassResolver(root *phpsyntax.Node) func(string) string {
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	currentClass := ""
	if classes := phpquery.Classes(root); len(classes) != 0 {
		currentClass = qualify(namespace, phpquery.ClassName(classes[0]))
	}
	aliases := make(map[string]string)
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind == phpresolver.ClassImport {
				aliases[strings.ToLower(imported.Alias)] = strings.Trim(imported.Target, `\`)
			}
		}
	}
	return func(name string) string {
		name = strings.Trim(strings.TrimSpace(name), `\`)
		if name == "" {
			return ""
		}
		if strings.EqualFold(name, "self") || strings.EqualFold(name, "static") {
			return currentClass
		}
		parts := strings.SplitN(name, `\`, 2)
		if target, found := aliases[strings.ToLower(parts[0])]; found {
			if len(parts) > 1 {
				return target + `\` + parts[1]
			}
			return target
		}
		if namespace != "" {
			return namespace + `\` + name
		}
		return name
	}
}

func lookupRelation(lookup RelationLookup, class string) (RelationTarget, bool) {
	if lookup != nil {
		if target, found := lookup(class); found {
			return target, true
		}
	}
	class = strings.Trim(class, `\`)
	short := ShortClass(class)
	if !strings.HasSuffix(short, "Definition") {
		return RelationTarget{}, false
	}
	base := strings.TrimSuffix(short, "Definition")
	namespace := strings.TrimSuffix(class, short)
	entityName := importedAcronymBoundary.ReplaceAllString(base, `${1}_${2}`)
	entityName = importedWordBoundary.ReplaceAllString(entityName, `${1}_${2}`)
	return RelationTarget{
		DefinitionClass: class,
		EntityClass:     namespace + base + "Entity",
		CollectionClass: namespace + base + "Collection",
		EntityName:      strings.ToLower(entityName),
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}, true
}

func lookupRelationByEntityName(lookup RelationLookup, entityName string) (RelationTarget, bool) {
	if lookup == nil || entityName == "" {
		return RelationTarget{}, false
	}
	target, found := lookup(entityName)
	if !found || target.DefinitionClass == "" || target.EntityName != entityName {
		return RelationTarget{}, false
	}
	return target, true
}

func enrichRelation(field *FieldSpec, target RelationTarget) {
	field.TargetEntityClass = target.EntityClass
	field.TargetCollectionClass = target.CollectionClass
	field.TargetEntityName = target.EntityName
	if field.Kind == FieldOneToMany {
		return
	}
	if field.ReferenceField == "" {
		field.ReferenceField = "id"
	}
	if field.ReferenceStorageName == "" {
		field.ReferenceStorageName = "id"
	}
	for _, candidate := range target.Fields {
		if candidate.PropertyName == field.ReferenceField || candidate.Primary {
			field.ReferenceStorageName = candidate.StorageName
			if candidate.PropertyName == field.ReferenceField {
				break
			}
		}
	}
}

func lockedField(index int, raw string) FieldSpec {
	return FieldSpec{ID: fmt.Sprintf("locked-%d", index+1), Kind: FieldLocked, Editable: false, Raw: strings.TrimSpace(raw)}
}
