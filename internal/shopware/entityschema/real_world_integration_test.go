//go:build integration

package entityschema

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/require"
)

func TestShopwareCoreClassBasedSourceCoverage(t *testing.T) {
	root := realWorldShopwareRoot(t)
	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)

	covered := make(map[string]int)
	var failures []string
	err = filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "Test" || entry.Name() == "Tests" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".php" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		tree := php.Parse(string(content))
		if tree.Tree == nil || tree.Tree.Root == nil {
			return nil
		}
		for _, class := range phpquery.Classes(tree.Tree.Root) {
			if phpquery.IsAbstract(class) || isRuntimeDALDefinitionTemplate(phpquery.ClassName(class)) {
				continue
			}
			kind := directClassBasedDALKind(class)
			if kind == "" {
				continue
			}
			require.True(t, classBasedSourceCandidate(class),
				"production source index must capture %s in %s",
				phpquery.ClassName(class), path)
			covered[kind]++
			var importErr error
			switch kind {
			case "extension":
				_, importErr = ImportExtension(string(content), lookup)
			case "bulk-extension":
				_, importErr = ImportBulkExtension(string(content), lookup)
			case "translation":
				_, importErr = ImportTranslationDefinition(string(content), lookup)
			default:
				_, importErr = ImportDefinition(string(content), lookup)
			}
			if importErr != nil {
				relative, _ := filepath.Rel(root, path)
				failures = append(failures, fmt.Sprintf("%s (%s): %v", filepath.ToSlash(relative), phpquery.ClassName(class), importErr))
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, failures, "every concrete static class-based DAL definition must import; runtime-generated definition templates are intentionally excluded")
	require.GreaterOrEqual(t, covered["entity"], 140)
	require.GreaterOrEqual(t, covered["mapping"], 30)
	require.GreaterOrEqual(t, covered["translation"], 30)
	require.GreaterOrEqual(t, covered["extension"], 5)
	require.GreaterOrEqual(t, covered["bulk-extension"], 1)
	t.Logf("covered concrete direct class-based DAL sources: %v", covered)
}

// Shopware's DAL test definitions deliberately exercise shapes which do not
// necessarily occur in the production catalog. Importing the entire corpus
// keeps the designer honest about valid class-based extension and field forms;
// semantic validation is tested separately because several fixtures are
// intentionally invalid runtime inputs.
func TestShopwareCoreDALTestFixtureImportCoverage(t *testing.T) {
	root := realWorldShopwareRoot(t)
	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	productionLookup := relationLookupFromDefinitions(definitions)
	fixtureRoot := filepath.Join(root, "src", "Core", "Framework", "Test", "DataAbstractionLayer", "Field", "TestDefinition")

	type fixture struct {
		path, source, class, kind string
	}
	var fixtures []fixture
	covered := make(map[string]int)
	err = filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".php" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed := php.Parse(string(content))
		if parsed.Tree == nil || parsed.Tree.Root == nil {
			return nil
		}
		namespace := strings.Trim(phpquery.Namespace(parsed.Tree.Root), `\`)
		for _, class := range phpquery.Classes(parsed.Tree.Root) {
			className := qualify(namespace, phpquery.ClassName(class))
			if phpquery.IsAbstract(class) {
				continue
			}
			kind := directClassBasedDALKind(class)
			if kind == "" {
				continue
			}
			covered[kind]++
			fixtures = append(fixtures, fixture{path: path, source: string(content), class: className, kind: kind})
		}
		return nil
	})
	require.NoError(t, err)
	localDefinitions := make([]ScannedDefinition, 0, len(fixtures))
	for _, candidate := range fixtures {
		if candidate.kind != classBasedEntity && candidate.kind != classBasedMapping {
			continue
		}
		spec, importErr := importDefinitionClass(candidate.source, productionLookup, candidate.class, DefinitionKind(candidate.kind))
		require.NoError(t, importErr, candidate.path)
		localDefinitions = append(localDefinitions, ScannedDefinition{Path: candidate.path, Spec: spec})
	}
	lookup := relationLookupFromDefinitions(append(definitions, localDefinitions...))

	var failures []string
	for _, candidate := range fixtures {
		var importErr error
		switch candidate.kind {
		case classBasedTranslation:
			_, importErr = importTranslationClass(candidate.source, lookup, candidate.class)
		default:
			_, importErr = importClassBasedSpec(candidate.source, candidate.class, candidate.kind, lookup)
		}
		if importErr != nil {
			relative, _ := filepath.Rel(root, candidate.path)
			failures = append(failures, fmt.Sprintf("%s (%s): %v", filepath.ToSlash(relative), candidate.class, importErr))
			continue
		}
	}
	require.Empty(t, failures, "every concrete class-based Shopware DAL test fixture must import")
	require.Greater(t, covered[classBasedEntity], 20)
	require.Greater(t, covered[classBasedExtension], 5)
	t.Logf("covered class-based Shopware DAL test fixtures: %v", covered)
}

func TestShopwareCoreBulkEntityExtensionRoundTrip(t *testing.T) {
	root := realWorldShopwareRoot(t)
	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	bulkCount := 0
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionBulkExtension {
			continue
		}
		bulkCount++
		t.Run(definition.Spec.DefinitionClass, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors, source)
			roundTripped, importErr := ImportBulkExtension(source, lookup)
			require.NoError(t, importErr)
			require.Equal(t, DefinitionBulkExtension, roundTripped.DefinitionKind)
			require.Len(t, roundTripped.BulkExtensions, len(definition.Spec.BulkExtensions))
			before, schemaErr := SchemaEntitiesFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaEntitiesFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			for index := range before {
				before[index].BackfillFree()
				after[index].BackfillFree()
				before[index] = before[index].NormalizeForTest()
				after[index] = after[index].NormalizeForTest()
			}
			require.True(t, reflect.DeepEqual(before, after), "bulk schema changed after render/import round trip")
		})
	}
	require.GreaterOrEqual(t, bulkCount, 1)
}

func realWorldShopwareRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}
	return root
}

func isRuntimeDALDefinitionTemplate(name string) bool {
	switch name {
	case "AttributeEntityDefinition", "AttributeMappingDefinition", "AttributeTranslationDefinition",
		"DynamicEntityDefinition", "DynamicMappingEntityDefinition", "DynamicTranslationEntityDefinition",
		"FilteredBulkEntityExtension":
		return true
	default:
		return false
	}
}

func directClassBasedDALKind(class *phpsyntax.Node) string {
	for _, parent := range phpquery.ClassExtends(class) {
		switch ShortClass(parent) {
		case "EntityDefinition":
			return "entity"
		case "MappingEntityDefinition":
			return "mapping"
		case "EntityTranslationDefinition":
			return "translation"
		case "EntityExtension":
			return "extension"
		case "BulkEntityExtension":
			return "bulk-extension"
		}
	}
	return ""
}

// TestRealWorldPluginEntityRoundTrip exercises the designer importer against a
// real plugin checkout without modifying it. Override the default fixture with
// SHOPWARE_LSP_ENTITY_PLUGIN_ROOT.
func TestRealWorldPluginEntityRoundTrip(t *testing.T) {
	pluginRoot := os.Getenv("SHOPWARE_LSP_ENTITY_PLUGIN_ROOT")
	if pluginRoot == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		pluginRoot = filepath.Join(home, "Developer", "sw-trunk", "custom", "plugins", "FroshMySQLSearch")
	}
	if info, err := os.Stat(pluginRoot); err != nil || !info.IsDir() {
		t.Skipf("real-world entity plugin is unavailable: %s", pluginRoot)
	}

	schema, definitions, err := ScanPluginSchema(pluginRoot)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(definitions), 4)
	ownedEntities := make(map[string]struct{})
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionExtension && definition.Spec.DefinitionKind != DefinitionBulkExtension {
			ownedEntities[definition.Spec.EntityName] = struct{}{}
		}
	}
	require.GreaterOrEqual(t, len(schema.Entities), len(ownedEntities))
	lookup := relationLookupFromDefinitions(definitions)
	for _, definition := range definitions {
		t.Run(definition.Spec.EntityName, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors)
			var roundTripped EntitySpec
			var importErr error
			if definition.Spec.DefinitionKind == DefinitionExtension {
				roundTripped, importErr = ImportExtension(source, lookup)
			} else if definition.Spec.DefinitionKind == DefinitionBulkExtension {
				roundTripped, importErr = ImportBulkExtension(source, lookup)
			} else {
				roundTripped, importErr = ImportDefinition(source, lookup)
			}
			require.NoError(t, importErr)
			before, schemaErr := SchemaEntitiesFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaEntitiesFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			for index := range before {
				before[index].BackfillFree()
				after[index].BackfillFree()
				before[index] = before[index].NormalizeForTest()
				after[index] = after[index].NormalizeForTest()
			}
			require.True(t, reflect.DeepEqual(before, after), "schema changed after render/import round trip")
		})
	}
}

func TestShopwareCoreEntityExtensionRoundTrip(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	extensionCount := 0
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionExtension {
			continue
		}
		extensionCount++
		t.Run(definition.Spec.DefinitionClass, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors, source)
			roundTripped, importErr := ImportExtension(source, lookup)
			require.NoError(t, importErr)
			require.Equal(t, DefinitionExtension, roundTripped.DefinitionKind)
			before, schemaErr := SchemaFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			before.BackfillFree()
			after.BackfillFree()
			require.True(t, reflect.DeepEqual(before.NormalizeForTest(), after.NormalizeForTest()), "schema changed after extension render/import round trip")
		})
	}
	require.GreaterOrEqual(t, extensionCount, 5)
}

func TestShopwareCoreHierarchyImport(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	definitions := []string{
		"src/Core/Checkout/Order/Aggregate/OrderLineItem/OrderLineItemDefinition.php",
		"src/Core/Content/Category/CategoryDefinition.php",
		"src/Core/Content/Flow/Aggregate/FlowSequence/FlowSequenceDefinition.php",
		"src/Core/Content/Media/Aggregate/MediaFolder/MediaFolderDefinition.php",
		"src/Core/Content/Product/ProductDefinition.php",
		"src/Core/Content/ProductStream/Aggregate/ProductStreamFilter/ProductStreamFilterDefinition.php",
		"src/Core/Content/Rule/Aggregate/RuleCondition/RuleConditionDefinition.php",
		"src/Core/System/Language/LanguageDefinition.php",
	}
	for _, relativePath := range definitions {
		relativePath := relativePath
		t.Run(filepath.Base(relativePath), func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, relativePath))
			require.NoError(t, err)
			spec, err := ImportDefinition(string(source), nil)
			require.NoError(t, err)

			var hierarchy *FieldSpec
			versionAware := false
			for index := range spec.Fields {
				field := &spec.Fields[index]
				if field.Kind == FieldVersion {
					versionAware = true
				}
				if field.Kind == FieldHierarchy {
					require.Nil(t, hierarchy, "multiple logical hierarchy rows imported")
					hierarchy = field
				}
			}
			require.NotNil(t, hierarchy)
			require.Equal(t, spec.DefinitionClass, hierarchy.TargetDefinitionClass)
			require.Equal(t, "parent_id", hierarchy.StorageName)
			require.Equal(t, "parentId", hierarchy.ForeignKeyPropertyName)
			require.Equal(t, "parent", hierarchy.HierarchyParentProperty)
			require.Equal(t, DeleteCascade, hierarchy.DeleteBehavior)
			require.Equal(t, versionAware, hierarchy.HierarchyVersionAware)
		})
	}
}

func TestShopwareCoreClassBasedFieldCoverage(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	require.Greater(t, len(definitions), 100)
	supported := map[string]struct{}{
		"IdField": {}, "StringField": {}, "LongTextField": {}, "IntField": {}, "FloatField": {}, "BoolField": {},
		"DateField": {}, "DateTimeField": {}, "JsonField": {}, "ListField": {}, "ObjectField": {}, "BlobField": {},
		"AutoIncrementField": {}, "CreatedAtField": {}, "UpdatedAtField": {}, "VersionField": {}, "ReferenceVersionField": {},
		"FkField": {}, "ManyToOneAssociationField": {}, "OneToOneAssociationField": {}, "OneToManyAssociationField": {},
		"ManyToManyAssociationField": {}, "ParentFkField": {}, "ParentAssociationField": {}, "ChildrenAssociationField": {},
		"TranslatedField": {}, "TranslationsAssociationField": {},
	}
	for name := range specializedFieldDescriptors {
		supported[name] = struct{}{}
	}
	lockedByClass := make(map[string]int)
	preservedFlags := make(map[string]int)
	totalFields := 0
	lockedFields := 0
	for _, definition := range definitions {
		for _, field := range definition.Spec.Fields {
			totalFields++
			flagGroups := [][]string{field.PreservedFlags, field.AssociationFlags, field.TranslationFlags, field.HierarchyChildrenFlags, field.HierarchyVersionFlags}
			for _, group := range flagGroups {
				for _, source := range group {
					parsedFlag := php.Parse("<?php " + strings.TrimSpace(source) + ";")
					if parsedFlag.Tree == nil || parsedFlag.Tree.Root == nil {
						continue
					}
					creations := phpquery.ObjectCreations(parsedFlag.Tree.Root)
					if len(creations) != 0 {
						outer := creations[0]
						for _, creation := range creations[1:] {
							if creation.Range().End-creation.Range().Start > outer.Range().End-outer.Range().Start {
								outer = creation
							}
						}
						name := ShortClass(phpquery.ObjectClassName(outer))
						preservedFlags[name]++
						t.Logf("opaque flag in %s: %s", definition.Spec.DefinitionClass, source)
					}
				}
			}
			if field.Kind != FieldLocked {
				continue
			}
			lockedFields++
			parsed := php.Parse("<?php " + strings.TrimSuffix(strings.TrimSpace(field.Raw), ",") + ";")
			if parsed.Tree == nil || parsed.Tree.Root == nil {
				continue
			}
			creations := phpquery.ObjectCreations(parsed.Tree.Root)
			if len(creations) == 0 {
				t.Logf("custom locked expression in %s: %s", definition.Spec.DefinitionClass, field.Raw)
				continue
			}
			name := ShortClass(phpquery.ObjectClassName(creations[0]))
			lockedByClass[name]++
			if _, expectedEditable := supported[name]; expectedEditable {
				t.Errorf("known class-based field %s stayed locked in %s: %s", name, definition.Spec.DefinitionClass, field.Raw)
			}
		}
	}
	require.Greater(t, totalFields, 1000)
	t.Logf("imported %d class-based fields; %d custom expressions remain losslessly locked: %v; opaque flags: %v", totalFields, lockedFields, lockedByClass, preservedFlags)
	require.Zero(t, lockedFields, "Shopware's production class-based definitions must remain fully typed; custom plugin expressions may still be losslessly locked")
	require.Empty(t, preservedFlags, "Shopware's production class-based flags must remain fully typed")
}

func TestShopwareCoreFieldAndFlagAPIsAreExplicitlyClassified(t *testing.T) {
	root := realWorldShopwareRoot(t)
	fieldDirectory := filepath.Join(root, "src/Core/Framework/DataAbstractionLayer/Field")
	entries, err := os.ReadDir(fieldDirectory)
	require.NoError(t, err)

	standard := stringSet(
		"AutoIncrementField", "BlobField", "BoolField", "ChildrenAssociationField", "CreatedAtField", "DateField", "DateTimeField",
		"EnumField", "FkField", "FloatField", "IdField", "IntField", "JsonField", "ListField", "LongTextField",
		"ManyToManyAssociationField", "ManyToOneAssociationField", "ObjectField", "OneToManyAssociationField",
		"OneToOneAssociationField", "ParentAssociationField", "ParentFkField", "ReferenceVersionField", "StringField",
		"TranslatedField", "TranslationsAssociationField", "UpdatedAtField", "VersionField",
	)
	frameworkBase := stringSet("AssociationField", "Field", "StorageAware")
	excluded := stringSet("SerializedField")
	var unknownFields []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".php" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".php")
		if _, found := standard[name]; found {
			continue
		}
		if _, found := specializedFieldDescriptors[name]; found {
			continue
		}
		if _, found := frameworkBase[name]; found {
			continue
		}
		if _, found := excluded[name]; found {
			continue
		}
		unknownFields = append(unknownFields, name)
	}
	sort.Strings(unknownFields)
	require.Empty(t, unknownFields, "classify and support every new class-based DAL field before accepting an upstream Shopware field addition")
	serialized, err := os.ReadFile(filepath.Join(fieldDirectory, "SerializedField.php"))
	require.NoError(t, err)
	require.Contains(t, string(serialized), "only via the #[Serialized] attribute", "SerializedField is excluded only while Shopware documents it as attribute-only")

	typedFlags := stringSet(
		"AllowEmptyString", "AllowHtml", "ApiAware", "ApiCriteriaAware", "AsArray", "CascadeDelete", "Choice", "Computed",
		"Deprecated", "DoNotUseContext", "Extension", "IgnoreInOpenapiSchema", "IgnoreInUnusedMediaSearch", "Immutable",
		"Inherited", "NoConstraint", "PrimaryKey", "Required", "RestrictDelete", "ReverseInherited", "RuleAreas", "Runtime",
		"SearchRanking", "SetNullOnDelete", "Since", "WriteProtected",
	)
	flagEntries, err := os.ReadDir(filepath.Join(fieldDirectory, "Flag"))
	require.NoError(t, err)
	var unknownFlags []string
	for _, entry := range flagEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".php" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".php")
		if name == "Flag" {
			continue
		}
		if _, found := typedFlags[name]; !found {
			unknownFlags = append(unknownFlags, name)
		}
	}
	sort.Strings(unknownFlags)
	require.Empty(t, unknownFlags, "classify and support every new concrete DAL flag before accepting an upstream Shopware flag addition")
}

func TestShopwareEntityDefinitionHooksAreExplicitlyClassified(t *testing.T) {
	root := realWorldShopwareRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "src/Core/Framework/DataAbstractionLayer/EntityDefinition.php"))
	require.NoError(t, err)
	parsed := php.Parse(string(source))
	require.NotNil(t, parsed.Tree)
	require.NotNil(t, parsed.Tree.Root)
	require.Empty(t, parsed.Errors)
	classes := phpquery.Classes(parsed.Tree.Root)
	require.Len(t, classes, 1)

	// Schema and typed hooks are modeled by EntitySpec. Derived hooks are
	// deliberately computed by Shopware. Opaque hooks remain untouched by the
	// edit rewriter, so the designer never replaces arbitrary application code.
	known := map[string]string{
		"__construct": "lifecycle", "compile": "lifecycle",
		"getEntityName": "identity", "getEntityClass": "identity", "getCollectionClass": "identity",
		"getDefaults": "typed", "getChildDefaults": "typed", "isInheritanceAware": "typed", "isVersionAware": "typed",
		"since": "typed", "getHydratorClass": "typed", "getRestrictDeleteMetaFields": "typed",
		"getParentDefinitionClass": "typed", "defaultFields": "schema", "defineFields": "schema",
		"defineProtections": "typed", "getBaseFields": "schema",
		"getParentDefinition": "derived", "isChildrenAware": "derived", "isParentAware": "derived", "isLockAware": "derived",
		"isSeoAware": "derived", "getTranslatedFields": "derived", "getExtensionFields": "derived",
		"decode": "opaque",
	}
	var unknown []string
	seen := make(map[string]struct{})
	for _, method := range phpquery.Methods(classes[0]) {
		visibility := phpquery.DeclarationVisibility(method)
		if visibility != "public" && visibility != "protected" || directPHPModifier(method, "final") {
			continue
		}
		name := phpquery.MethodName(method)
		seen[name] = struct{}{}
		if _, classified := known[name]; !classified {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	require.Empty(t, unknown, "new overridable EntityDefinition hooks need an explicit typed, derived, opaque, or lifecycle policy")
	for name := range known {
		_, found := seen[name]
		require.True(t, found, "classified EntityDefinition hook %s no longer exists upstream; update the compatibility policy", name)
	}
}

func TestShopwareClassBasedDALRootAPIsAreExplicitlyClassified(t *testing.T) {
	root := realWorldShopwareRoot(t)
	tests := []struct {
		file    string
		methods map[string]string
	}{
		{
			file: "MappingEntityDefinition.php",
			methods: map[string]string{
				"getCollectionClass": "identity", "getEntityClass": "identity", "getBaseFields": "schema", "defaultFields": "schema",
			},
		},
		{
			file: "EntityTranslationDefinition.php",
			methods: map[string]string{
				"getParentDefinition": "derived", "isVersionAware": "typed", "hasRequiredField": "derived",
				"getParentDefinitionClass": "identity", "getBaseFields": "schema",
			},
		},
		{
			file: "EntityExtension.php",
			methods: map[string]string{
				"extendFields": "schema", "modifyFields": "typed-or-opaque", "extendProtections": "typed-or-opaque", "getEntityName": "identity",
			},
		},
		{
			file:    "BulkEntityExtension.php",
			methods: map[string]string{"collect": "schema-or-opaque"},
		},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, "src/Core/Framework/DataAbstractionLayer", test.file))
			require.NoError(t, err)
			parsed := php.Parse(string(source))
			require.NotNil(t, parsed.Tree)
			require.NotNil(t, parsed.Tree.Root)
			require.Empty(t, parsed.Errors)
			classes := phpquery.Classes(parsed.Tree.Root)
			require.Len(t, classes, 1)
			seen := make(map[string]struct{})
			var unknown []string
			for _, method := range phpquery.Methods(classes[0]) {
				visibility := phpquery.DeclarationVisibility(method)
				if (visibility != "public" && visibility != "protected") || directPHPModifier(method, "final") {
					continue
				}
				name := phpquery.MethodName(method)
				seen[name] = struct{}{}
				if _, classified := test.methods[name]; !classified {
					unknown = append(unknown, name)
				}
			}
			sort.Strings(unknown)
			require.Empty(t, unknown, "new overridable %s hooks need an explicit designer policy", test.file)
			for name := range test.methods {
				_, found := seen[name]
				require.True(t, found, "classified %s hook %s no longer exists upstream", test.file, name)
			}
		})
	}
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func directPHPModifier(node *phpsyntax.Node, modifier string) bool {
	cursor := node.ChildTokenCursor()
	for cursor.Next() {
		text := cursor.Token().Text()
		if strings.EqualFold(text, modifier) {
			return true
		}
		if strings.EqualFold(text, "function") {
			return false
		}
	}
	return false
}

func TestShopwareCoreAdvancedFieldArgumentsRoundTrip(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	tests := []struct {
		path, property string
		assert         func(*testing.T, FieldSpec)
	}{
		{
			path: "src/Core/Content/Cms/Aggregate/CmsBlock/CmsBlockDefinition.php", property: "visibility",
			assert: func(t *testing.T, field FieldSpec) {
				require.Contains(t, field.JSONPropertyMappingExpression, "new \\Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\BoolField")
				require.Empty(t, field.JSONDefaultExpression)
			},
		},
		{
			path: "src/Core/Content/Flow/Aggregate/FlowSequence/FlowSequenceDefinition.php", property: "config",
			assert: func(t *testing.T, field FieldSpec) {
				require.Equal(t, "[]", field.JSONPropertyMappingExpression)
				require.Equal(t, "[]", field.JSONDefaultExpression)
			},
		},
		{
			path: "src/Core/System/SalesChannel/Aggregate/SalesChannelFile/SalesChannelFileDefinition.php", property: "templateOverrides",
			assert: func(t *testing.T, field FieldSpec) {
				require.Equal(t, []string{"Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource"}, field.APIAwareSources)
				require.Equal(t, "[]", field.JSONPropertyMappingExpression)
				require.Equal(t, "[]", field.JSONDefaultExpression)
			},
		},
	}

	for _, test := range tests {
		t.Run(filepath.Base(test.path)+"/"+test.property, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, test.path))
			require.NoError(t, err)
			spec, err := ImportDefinition(string(source), nil)
			require.NoError(t, err)
			before := fieldByProperty(t, spec.Fields, test.property)
			test.assert(t, before)

			rendered, err := RenderDefinition(spec)
			require.NoError(t, err)
			require.Empty(t, php.Parse(rendered).Errors, rendered)
			roundTripped, err := ImportDefinition(rendered, nil)
			require.NoError(t, err)
			after := fieldByProperty(t, roundTripped.Fields, test.property)
			require.Equal(t, before.JSONPropertyMappingExpression, after.JSONPropertyMappingExpression)
			require.Equal(t, before.JSONDefaultExpression, after.JSONDefaultExpression)
			require.Equal(t, before.APIAwareSources, after.APIAwareSources)
		})
	}
}

func TestShopwareCoreConditionalAssociationRoundTrip(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	source, err := os.ReadFile(filepath.Join(root, "src/Core/Checkout/Customer/CustomerDefinition.php"))
	require.NoError(t, err)
	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	spec, err := ImportDefinition(string(source), lookup)
	require.NoError(t, err)
	field := fieldByProperty(t, spec.Fields, "defaultBillingAddress")
	if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
		enrichRelation(&field, target)
	}
	require.NotEmpty(t, field.TargetEntityClass)
	require.Equal(t, FieldOneToOne, field.Kind)
	require.NotNil(t, field.ConditionalAssociation)
	require.Equal(t, FieldManyToOne, field.ConditionalAssociation.AlternativeKind)
	require.Equal(t, `\Shopware\Core\Framework\Feature::isActive('v6.8.0.0')`, field.ConditionalAssociation.ConditionExpression)
	require.True(t, field.AssociationAPIAware)
	require.Equal(t, 0.25, field.AssociationSearchRank)

	renderSpec := exampleSpec()
	renderSpec.Indexes = nil
	renderSpec.Fields = []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}}
	for _, candidate := range spec.Fields {
		if candidate.Kind == FieldForeignKey && candidate.StorageName == field.StorageName {
			if target, found := lookupRelation(lookup, candidate.TargetDefinitionClass); found {
				enrichRelation(&candidate, target)
			}
			renderSpec.Fields = append(renderSpec.Fields, candidate)
		}
	}
	renderSpec.Fields = append(renderSpec.Fields, field)
	rendered, err := RenderDefinition(renderSpec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, `\Shopware\Core\Framework\Feature::isActive('v6.8.0.0') ? new OneToOneAssociationField`)
	require.Contains(t, rendered, `: new ManyToOneAssociationField`)
	roundTripped, err := ImportDefinition(rendered, lookup)
	require.NoError(t, err)
	after := fieldByProperty(t, roundTripped.Fields, "defaultBillingAddress")
	require.Equal(t, field.ConditionalAssociation, after.ConditionalAssociation)
	require.Equal(t, field.AssociationSearchRank, after.AssociationSearchRank)
}

func TestShopwareCoreInheritanceImport(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	productSource, err := os.ReadFile(filepath.Join(root, "src/Core/Content/Product/ProductDefinition.php"))
	require.NoError(t, err)
	product, err := ImportDefinition(string(productSource), nil)
	require.NoError(t, err)
	require.True(t, product.InheritanceAware)
	require.Equal(t, FieldHierarchy, fieldByProperty(t, product.Fields, "children").Kind)
	require.True(t, fieldByProperty(t, product.Fields, "manufacturerNumber").Inherited)
	manufacturer := fieldByProperty(t, product.Fields, "manufacturer")
	require.True(t, manufacturer.Inherited)
	require.True(t, manufacturer.AssociationInherited)
	require.True(t, fieldByProperty(t, product.Fields, "prices").AssociationInherited)
	require.True(t, fieldByProperty(t, product.Fields, "name").TranslationInherited)

	unitSource, err := os.ReadFile(filepath.Join(root, "src/Core/System/Unit/UnitDefinition.php"))
	require.NoError(t, err)
	unit, err := ImportDefinition(string(unitSource), nil)
	require.NoError(t, err)
	require.False(t, unit.InheritanceAware)
	require.Equal(t, "unit", fieldByProperty(t, unit.Fields, "products").ReverseInheritedProperty)
}

func TestShopwareCoreAssociationAutoloadImport(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	namedSource, err := os.ReadFile(filepath.Join(root, "src/Core/Framework/Test/DataAbstractionLayer/Field/TestDefinition/NamedDefinition.php"))
	require.NoError(t, err)
	named, err := ImportDefinition(string(namedSource), nil)
	require.NoError(t, err)
	require.True(t, fieldByProperty(t, named.Fields, "optionalGroup").AssociationAutoload)

	integrationSource, err := os.ReadFile(filepath.Join(root, "src/Core/System/Integration/IntegrationDefinition.php"))
	require.NoError(t, err)
	integration, err := ImportDefinition(string(integrationSource), nil)
	require.NoError(t, err)
	require.False(t, fieldByProperty(t, integration.Fields, "app").AssociationAutoload)
}

func TestShopwareCoreWriteProtectionImport(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	source, err := os.ReadFile(filepath.Join(root, "src/Core/Framework/Test/DataAbstractionLayer/Field/TestDefinition/WriteProtectedDefinition.php"))
	require.NoError(t, err)
	spec, err := ImportDefinition(string(source), nil)
	require.NoError(t, err)
	require.True(t, fieldByProperty(t, spec.Fields, "protected").WriteProtected)
	require.Equal(t, []string{"system"}, fieldByProperty(t, spec.Fields, "systemProtected").WriteProtectedScopes)
	require.True(t, fieldByProperty(t, spec.Fields, "relation").AssociationWriteProtected)
	require.Equal(t, []string{"system"}, fieldByProperty(t, spec.Fields, "systemRelation").AssociationWriteScopes)
	require.True(t, fieldByProperty(t, spec.Fields, "relations").AssociationWriteProtected)
}

func TestRealWorldPluginEntityExtensionRoundTrip(t *testing.T) {
	pluginRoot := os.Getenv("SHOPWARE_LSP_ENTITY_EXTENSION_ROOT")
	if pluginRoot == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		pluginRoot = filepath.Join(home, "Downloads", "shop")
	}
	if info, err := os.Stat(pluginRoot); err != nil || !info.IsDir() {
		t.Skipf("real-world entity-extension project is unavailable: %s", pluginRoot)
	}

	_, definitions, err := ScanPluginSchema(pluginRoot)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	extensionCount := 0
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionExtension {
			continue
		}
		extensionCount++
		t.Run(definition.Spec.DefinitionClass, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors, source)
			roundTripped, importErr := ImportExtension(source, lookup)
			require.NoError(t, importErr)
			before, schemaErr := SchemaFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			before.BackfillFree()
			after.BackfillFree()
			require.True(t, reflect.DeepEqual(before.NormalizeForTest(), after.NormalizeForTest()), "schema changed after real plugin extension round trip")
		})
	}
	require.GreaterOrEqual(t, extensionCount, 20)
}

func TestShopwareCoreMappingDefinitionRoundTrip(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	mappingCount := 0
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionMapping || strings.Contains(filepath.ToSlash(definition.Path), "/Framework/Test/") {
			continue
		}
		mappingCount++
		t.Run(definition.Spec.EntityName, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors, source)
			roundTripped, importErr := ImportDefinition(source, lookup)
			require.NoError(t, importErr)
			require.Equal(t, DefinitionMapping, roundTripped.DefinitionKind)
			before, schemaErr := SchemaFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			before.BackfillFree()
			after.BackfillFree()
			require.True(t, reflect.DeepEqual(before.NormalizeForTest(), after.NormalizeForTest()), "schema changed after mapping render/import round trip")
			require.Equal(t, definitionBehaviorProjection(definition.Spec.DefinitionBehavior), definitionBehaviorProjection(roundTripped.DefinitionBehavior), "mapping behavior changed after render/import round trip")
			require.Equal(t, definition.Spec.DefinitionMetadata, roundTripped.DefinitionMetadata, "mapping metadata changed after render/import round trip")
		})
	}
	require.GreaterOrEqual(t, mappingCount, 30)
}

func TestShopwareCoreEntityDefinitionRoundTrip(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("Shopware checkout is unavailable: %s", root)
	}

	_, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	lookup := relationLookupFromDefinitions(definitions)
	count := 0
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind != DefinitionEntity || strings.Contains(filepath.ToSlash(definition.Path), "/Framework/Test/") {
			continue
		}
		count++
		t.Run(definition.Spec.EntityName, func(t *testing.T) {
			issues := ValidateSpec(definition.Spec)
			for _, issue := range issues {
				for _, field := range definition.Spec.Fields {
					if field.ID == issue.FieldID {
						t.Logf("%s: kind=%s property=%q storage=%q target=%q targetEntity=%q targetCollection=%q reference=%q mapping=%q runtime=%t raw=%s", issue.Code, field.Kind, field.PropertyName, field.StorageName, field.TargetDefinitionClass, field.TargetEntityClass, field.TargetCollectionClass, field.ReferenceStorageName, field.MappingDefinitionClass, field.Behavior != nil && field.Behavior.Runtime || field.AssociationBehavior != nil && field.AssociationBehavior.Runtime, field.Raw)
						break
					}
				}
			}
			require.Empty(t, issues)
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors, source)
			roundTripped, importErr := ImportDefinition(source, lookup)
			require.NoError(t, importErr)
			if definition.Spec.Translation != nil && definition.Spec.Translation.Enabled {
				translationSource, translationRenderErr := RenderTranslationDefinition(definition.Spec)
				require.NoError(t, translationRenderErr)
				require.Empty(t, php.Parse(translationSource).Errors, translationSource)
				translation, translationImportErr := ImportTranslationDefinition(translationSource, lookup)
				require.NoError(t, translationImportErr)
				roundTripped = AttachTranslation(roundTripped, translation)
			}
			before, schemaErr := SchemaFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			before.BackfillFree()
			after.BackfillFree()
			require.True(t, reflect.DeepEqual(before.NormalizeForTest(), after.NormalizeForTest()), "database schema changed after entity render/import round trip")
			require.Equal(t, semanticFieldProjection(definition.Spec.Fields), semanticFieldProjection(roundTripped.Fields), "typed field semantics changed after entity render/import round trip")
			require.Equal(t, definition.Spec.InheritanceAware, roundTripped.InheritanceAware, "inheritance awareness changed after render/import round trip")
			require.Equal(t, definitionBehaviorProjection(definition.Spec.DefinitionBehavior), definitionBehaviorProjection(roundTripped.DefinitionBehavior), "definition behavior changed after render/import round trip")
			require.Equal(t, definition.Spec.DefinitionMetadata, roundTripped.DefinitionMetadata, "definition metadata changed after render/import round trip")
			if definition.Spec.Translation != nil {
				require.NotNil(t, roundTripped.Translation)
				require.Equal(t, definitionBehaviorProjection(definition.Spec.Translation.DefinitionBehavior), definitionBehaviorProjection(roundTripped.Translation.DefinitionBehavior), "translation behavior changed after render/import round trip")
				require.Equal(t, definition.Spec.Translation.DefinitionMetadata, roundTripped.Translation.DefinitionMetadata, "translation metadata changed after render/import round trip")
			}
		})
	}
	require.GreaterOrEqual(t, count, 140)
}

type projectedDefinitionBehavior struct {
	ParentDefinitionClass        string
	VersionAware                 *bool
	OverrideDefaultFields        bool
	DefaultFields                []FieldSpec
	OverrideBaseFields           bool
	BaseFields                   []FieldSpec
	RestrictDeleteMetaProperties []string
	ParentDefinitionMethodRaw    string
	VersionAwareMethodRaw        string
	InheritanceAwareMethodRaw    string
	DefaultFieldsMethodRaw       string
	BaseFieldsMethodRaw          string
	RestrictDeleteMetaMethodRaw  string
}

func definitionBehaviorProjection(behavior *DefinitionBehaviorSpec) *projectedDefinitionBehavior {
	if behavior == nil {
		return nil
	}
	return &projectedDefinitionBehavior{
		ParentDefinitionClass:        behavior.ParentDefinitionClass,
		VersionAware:                 behavior.VersionAware,
		OverrideDefaultFields:        behavior.OverrideDefaultFields,
		DefaultFields:                semanticFieldProjection(behavior.DefaultFields),
		OverrideBaseFields:           behavior.OverrideBaseFields,
		BaseFields:                   semanticFieldProjection(behavior.BaseFields),
		RestrictDeleteMetaProperties: behavior.RestrictDeleteMetaProperties,
		ParentDefinitionMethodRaw:    strings.TrimSpace(behavior.ParentDefinitionMethodRaw),
		VersionAwareMethodRaw:        strings.TrimSpace(behavior.VersionAwareMethodRaw),
		InheritanceAwareMethodRaw:    strings.TrimSpace(behavior.InheritanceAwareMethodRaw),
		DefaultFieldsMethodRaw:       strings.TrimSpace(behavior.DefaultFieldsMethodRaw),
		BaseFieldsMethodRaw:          strings.TrimSpace(behavior.BaseFieldsMethodRaw),
		RestrictDeleteMetaMethodRaw:  strings.TrimSpace(behavior.RestrictDeleteMetaMethodRaw),
	}
}

func semanticFieldProjection(fields []FieldSpec) []FieldSpec {
	result := make([]FieldSpec, len(fields))
	for index, field := range fields {
		field.ID = ""
		field.Raw = ""
		field.Editable = false
		field.MigrationDefault = ""
		result[index] = field
	}
	return result
}

func relationLookupFromDefinitions(definitions []ScannedDefinition) RelationLookup {
	targets := make(map[string]RelationTarget, len(definitions))
	for _, definition := range definitions {
		if definition.Spec.DefinitionKind == DefinitionExtension || definition.Spec.DefinitionKind == DefinitionBulkExtension {
			continue
		}
		target := RelationTarget{DefinitionClass: definition.Spec.DefinitionClass, EntityClass: definition.Spec.EntityClass, CollectionClass: definition.Spec.CollectionClass, EntityName: definition.Spec.EntityName, InheritanceAware: definition.Spec.InheritanceAware}
		for _, field := range definition.Spec.Fields {
			if field.Kind == FieldVersion {
				target.VersionAware = true
			}
			if field.StorageName != "" && field.Kind != FieldLocked && !isNonStoredForTest(field) {
				target.Fields = append(target.Fields, RelationTargetField{PropertyName: field.PropertyName, StorageName: field.StorageName, Primary: field.Primary || field.Kind == FieldID || field.Kind == FieldVersion})
			}
		}
		targets[target.DefinitionClass] = target
		if target.EntityName != "" {
			targets[target.EntityName] = target
		}
	}
	return func(class string) (RelationTarget, bool) { target, found := targets[class]; return target, found }
}

func isNonStoredForTest(field FieldSpec) bool {
	return field.Kind == FieldOneToMany || field.Kind == FieldManyToMany || field.UsesExistingColumn
}

func (e Entity) NormalizeForTest() Entity {
	schema := EmptySchema()
	schema.Entities[e.Name] = e
	return schema.Normalize().Entities[e.Name]
}
