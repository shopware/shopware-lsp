package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/integration"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestMCPServerAdvertisesAnalysisAndWriteTools(t *testing.T) {
	root := t.TempDir()
	session := connectMCPTestClient(t, root)
	initializeResult := session.InitializeResult()
	require.NotNil(t, initializeResult)
	require.Contains(t, initializeResult.Instructions, "always run shopware_entity_schema_bootstrap")
	require.Contains(t, initializeResult.Instructions, `spec.definitionKind="mapping"`)
	require.Contains(t, initializeResult.Instructions, `spec.definitionKind="extension"`)
	require.Contains(t, initializeResult.Instructions, `spec.definitionKind="bulk-extension"`)
	require.Contains(t, initializeResult.Instructions, "one bulkExtensions entry per indexed target")
	require.Contains(t, initializeResult.Instructions, "bootstrap.definitionKinds")
	require.Contains(t, initializeResult.Instructions, "collectMethodRaw")
	require.Contains(t, initializeResult.Instructions, "spec.definitionBehavior")
	require.Contains(t, initializeResult.Instructions, "spec.definitionMetadata")
	require.Contains(t, initializeResult.Instructions, "*MethodRaw")
	require.Contains(t, initializeResult.Instructions, `kind="hierarchy"`)
	require.Contains(t, initializeResult.Instructions, `spec.inheritanceAware=true`)
	require.Contains(t, initializeResult.Instructions, "reverseInheritedProperty")
	require.Contains(t, initializeResult.Instructions, "shopware_entity_schema_preview and shopware_entity_schema_apply")
	require.Contains(t, initializeResult.Instructions, "always use shopware_scaffold")
	leadingInstructions := initializeResult.Instructions[:min(512, len(initializeResult.Instructions))]
	require.Contains(t, leadingInstructions, "never handwrite entity PHP")
	require.Contains(t, leadingInstructions, `kind="plugin"`)

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	byName := make(map[string]*mcp.Tool, len(tools.Tools))
	registeredNames := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
		registeredNames = append(registeredNames, tool.Name)
	}
	catalogNames := make([]string, 0, len(projectconfig.MCPToolCatalog))
	for _, tool := range projectconfig.MCPToolCatalog {
		catalogNames = append(catalogNames, tool.ID)
	}
	require.ElementsMatch(t, catalogNames, registeredNames)
	for _, name := range []string{
		"shopware_diagnostics",
		"shopware_code_actions",
		"shopware_apply_code_action",
		"shopware_hover",
		"shopware_definition",
		"shopware_references",
		"shopware_workspace_symbols",
		"shopware_scaffold_catalog",
		"shopware_scaffold",
		"shopware_entity_schema_bootstrap",
		"shopware_entity_schema_field_types",
		"shopware_entity_schema_search",
		"shopware_entity_schema_load",
		"shopware_entity_schema_preview",
		"shopware_entity_schema_apply",
		"shopware_entity_schema_reconcile",
	} {
		require.Contains(t, byName, name)
		require.NotNil(t, byName[name].OutputSchema, "%s has no output schema", name)
	}
	require.Contains(t, outputSchemaProperties(t, byName["shopware_diagnostics"]), "diagnostics")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_code_actions"]), "actions")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_apply_code_action"]), "diff")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_hover"]), "hover")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_definition"]), "locations")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_references"]), "locations")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_workspace_symbols"]), "symbols")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_scaffold_catalog"]), "scaffolds")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_scaffold"]), "diff")
	require.Contains(t, byName["shopware_scaffold"].Title, "plugin")
	require.Contains(t, byName["shopware_scaffold"].Description, "Always use")
	require.Contains(t, byName["shopware_scaffold"].Description, "kind=plugin")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "Always call this first")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "MappingEntityDefinition")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "EntityExtension")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "BulkEntityExtension")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "kind=hierarchy")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Description, "inheritanceAware=true")
	require.Contains(t, byName["shopware_entity_schema_bootstrap"].Title, "creating or editing")
	require.Contains(t, byName["shopware_entity_schema_field_types"].Description, "content.json")
	require.Contains(t, byName["shopware_entity_schema_search"].Description, "Use after entity-schema bootstrap")
	require.Contains(t, byName["shopware_entity_schema_load"].Description, "edit an existing DAL entity")
	require.Contains(t, byName["shopware_entity_schema_preview"].Description, "Required after bootstrap/load")
	require.Contains(t, byName["shopware_entity_schema_apply"].Description, "never reproduce the preview")
	require.Contains(t, byName["shopware_entity_schema_apply"].Description, "without refreshing")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_bootstrap"]), "spec")
	require.NotContains(t, outputSchemaProperties(t, byName["shopware_entity_schema_bootstrap"]), "existing")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_field_types"]), "fieldTypes")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_preview"]), "revision")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_apply"]), "diff")
	entitySchemaInput, err := json.Marshal(byName["shopware_entity_schema_preview"].InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(entitySchemaInput), `"inheritanceAware"`)
	require.Contains(t, string(entitySchemaInput), `"associationInherited"`)
	require.Contains(t, string(entitySchemaInput), `"associationAutoload"`)
	require.Contains(t, string(entitySchemaInput), `"writeProtected"`)
	require.Contains(t, string(entitySchemaInput), `"writeProtectedScopes"`)
	require.Contains(t, string(entitySchemaInput), `"associationWriteProtected"`)
	require.Contains(t, string(entitySchemaInput), `"translationWriteProtected"`)
	require.Contains(t, string(entitySchemaInput), `"translationInherited"`)
	require.Contains(t, string(entitySchemaInput), `"reverseInheritedProperty"`)
	require.Contains(t, string(entitySchemaInput), `"jsonPropertyMappingExpression"`)
	require.Contains(t, string(entitySchemaInput), `"apiAwareSources"`)
	require.Contains(t, string(entitySchemaInput), `"conditionalAssociation"`)
	require.Contains(t, string(entitySchemaInput), `"minimumAdditionalArguments"`)
	require.Contains(t, string(entitySchemaInput), `"bulkExtensions"`)
	require.Contains(t, string(entitySchemaInput), `"definitionBehavior"`)
	require.Contains(t, string(entitySchemaInput), `"definitionMetadata"`)
	require.Contains(t, string(entitySchemaInput), `"parentDefinitionClass"`)
	require.Contains(t, string(entitySchemaInput), `"overrideDefaultFields"`)
	require.Contains(t, string(entitySchemaInput), `"overrideBaseFields"`)
	require.Contains(t, string(entitySchemaInput), `"hydratorClass"`)
	loadSchemaInput, err := json.Marshal(byName["shopware_entity_schema_load"].InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(loadSchemaInput), `"definitionKind"`)
	require.True(t, byName["shopware_diagnostics"].Annotations.ReadOnlyHint)
	require.False(t, byName["shopware_apply_code_action"].Annotations.ReadOnlyHint)
	require.NotNil(t, byName["shopware_apply_code_action"].Annotations.DestructiveHint)
	require.True(t, *byName["shopware_apply_code_action"].Annotations.DestructiveHint)
}

func TestMCPConfigurationDisablesExactTools(t *testing.T) {
	root := t.TempDir()
	configuration := projectconfig.Default()
	configuration.MCP.Tools["shopware_hover"] = false
	configuration.MCP.Tools["shopware_scaffold"] = false
	session := connectMCPTestClientWithConfiguration(t, root, configuration)

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.NotContains(t, names, "shopware_hover")
	require.NotContains(t, names, "shopware_scaffold")
	require.Contains(t, names, "shopware_diagnostics")
	require.Contains(t, names, "shopware_scaffold_catalog")
}

func TestMCPConfigurationUsesProjectAndEditorLayers(t *testing.T) {
	root := t.TempDir()
	path := projectconfig.Path(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
        "version": 1,
        "mcp": {"tools": {"shopware_scaffold": false}}
    }`), 0o644))
	editor := projectconfig.Partial{MCP: &projectconfig.MCPConfig{Tools: map[string]bool{
		"shopware_scaffold": true,
		"shopware_hover":    false,
	}}}
	effective, err := mcpConfiguration(root, &editor)
	require.NoError(t, err)
	require.True(t, effective.MCPToolEnabled("shopware_scaffold"))
	require.False(t, effective.MCPToolEnabled("shopware_hover"))
	session := connectMCPTestClientWithConfiguration(t, root, effective)
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "shopware_scaffold")
	require.NotContains(t, names, "shopware_hover")
}

func TestMCPScaffoldCatalogAndShopwarePreviewWrite(t *testing.T) {
	root := t.TempDir()
	session := connectMCPTestClient(t, root)
	ctx := context.Background()

	catalogResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_scaffold_catalog", Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, catalogResult.IsError, toolResultText(catalogResult))
	var catalog scaffoldCatalogOutput
	decodeMCPStructuredContent(t, catalogResult, &catalog)
	require.True(t, catalog.EntitySchema)
	require.Contains(t, scaffoldKindNames(catalog.Scaffolds), "shopware:app-cms")
	require.Contains(t, scaffoldKindNames(catalog.Scaffolds), "symfony:controller")

	arguments := map[string]any{
		"family": "shopware", "kind": "app-cms",
		"directory": ".", "name": "cms",
	}
	preview, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_scaffold", Arguments: arguments,
	})
	require.NoError(t, err)
	require.False(t, preview.IsError, toolResultText(preview))
	var previewOutput scaffoldOutput
	decodeMCPStructuredContent(t, preview, &previewOutput)
	require.False(t, previewOutput.Applied)
	require.Contains(t, previewOutput.Diff, "Resources/cms.xml")
	require.NoFileExists(t, filepath.Join(root, "Resources", "cms.xml"))

	arguments["write"] = true
	applied, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_scaffold", Arguments: arguments,
	})
	require.NoError(t, err)
	require.False(t, applied.IsError, toolResultText(applied))
	var appliedOutput scaffoldOutput
	decodeMCPStructuredContent(t, applied, &appliedOutput)
	require.True(t, appliedOutput.Applied)
	require.Equal(t, "Resources/cms.xml", appliedOutput.PrimaryFile)
	require.FileExists(t, filepath.Join(root, "Resources", "cms.xml"))
}

func TestMCPSymfonyScaffoldWritesThroughProductionCommand(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "Controller"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
        "name": "acme/example",
        "autoload": {"psr-4": {"App\\": "src/"}}
    }`), 0o644))
	session := connectMCPTestClient(t, root)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "shopware_scaffold",
		Arguments: map[string]any{
			"family": "symfony", "kind": "controller",
			"directory": "src/Controller", "name": "Product", "write": true,
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError, toolResultText(result))
	var output scaffoldOutput
	decodeMCPStructuredContent(t, result, &output)
	require.True(t, output.Applied)
	require.Equal(t, "src/Controller/ProductController.php", output.PrimaryFile)
	content, err := os.ReadFile(filepath.Join(root, output.PrimaryFile))
	require.NoError(t, err)
	require.Contains(t, string(content), "namespace App\\Controller;")
}

func TestMCPEntitySchemaBootstrapPreviewAndApply(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	directory := filepath.Join(plugin, "src", "Content", "Example")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugin, "composer.json"), []byte(`{
        "name": "acme/example",
        "type": "shopware-platform-plugin",
        "require": {"shopware/core": "~6.7"},
        "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
        "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
    }`), 0o644))
	session := connectMCPTestClient(t, root)
	ctx := context.Background()

	bootstrapResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_bootstrap",
		Arguments: map[string]any{"directory": "custom/plugins/Example/src/Content/Example"},
	})
	require.NoError(t, err)
	require.False(t, bootstrapResult.IsError, toolResultText(bootstrapResult))
	var bootstrap entitySchemaBootstrapOutput
	decodeMCPStructuredContent(t, bootstrapResult, &bootstrap)
	require.Equal(t, "custom/plugins/Example", bootstrap.Plugin.RootURI)
	require.Equal(t, "custom/plugins/Example/src/Content/Example", bootstrap.Spec.DirectoryURI)
	require.NotEmpty(t, bootstrap.FieldTypes)
	enumAvailable := false
	for _, fieldType := range bootstrap.FieldTypes {
		if fieldType.Kind == string(entityschema.FieldEnum) {
			enumAvailable = true
		}
	}
	require.True(t, enumAvailable)
	require.Contains(t, bootstrap.NextAction, "Do not inspect content.json")
	bootstrapJSON, err := json.Marshal(bootstrap)
	require.NoError(t, err)
	t.Logf("compact entity bootstrap payload: %d bytes", len(bootstrapJSON))
	require.Less(t, len(bootstrapJSON), 12_000)
	require.NotContains(t, string(bootstrapJSON), `"existing"`)
	require.NotContains(t, string(bootstrapJSON), `"template"`)

	fieldListResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_field_types",
		Arguments: map[string]any{
			"directory":      "custom/plugins/Example/src/Content/Example",
			"definitionKind": "entity",
			"query":          "custom fields",
		},
	})
	require.NoError(t, err)
	require.False(t, fieldListResult.IsError, toolResultText(fieldListResult))
	var fieldList entitySchemaFieldTypesOutput
	decodeMCPStructuredContent(t, fieldListResult, &fieldList)
	require.Len(t, fieldList.FieldTypes, 1)
	require.Equal(t, "specialized:CustomFields", fieldList.FieldTypes[0].ID)
	require.Empty(t, fieldList.FieldTypes[0].Template)
	require.Contains(t, fieldList.NextAction, "exact id")

	fieldDetailResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_field_types",
		Arguments: map[string]any{
			"directory": "custom/plugins/Example/src/Content/Example",
			"id":        "specialized:CustomFields",
		},
	})
	require.NoError(t, err)
	require.False(t, fieldDetailResult.IsError, toolResultText(fieldDetailResult))
	var fieldDetail entitySchemaFieldTypesOutput
	decodeMCPStructuredContent(t, fieldDetailResult, &fieldDetail)
	require.Len(t, fieldDetail.FieldTypes, 1)
	template := fieldDetail.FieldTypes[0].Template
	require.Equal(t, "replace-me", template["id"])
	implementation, ok := template["implementation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, `Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields`, implementation["class"])
	require.Contains(t, fieldDetail.NextAction, "Copy template")

	scalarDetailResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_field_types",
		Arguments: map[string]any{
			"directory": "custom/plugins/Example/src/Content/Example",
			"id":        "string",
		},
	})
	require.NoError(t, err)
	require.False(t, scalarDetailResult.IsError, toolResultText(scalarDetailResult))
	var scalarDetail entitySchemaFieldTypesOutput
	decodeMCPStructuredContent(t, scalarDetailResult, &scalarDetail)
	require.Len(t, scalarDetail.FieldTypes, 1)
	require.Equal(t, "string", scalarDetail.FieldTypes[0].Template["kind"])
	require.Equal(t, "replace-me", scalarDetail.FieldTypes[0].Template["id"])
	require.NotContains(t, scalarDetail.FieldTypes[0].Template, "translated")
	require.Contains(t, scalarDetail.FieldTypes[0].Usage, "translated=true")

	unknownFieldResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_field_types",
		Arguments: map[string]any{
			"directory": "custom/plugins/Example/src/Content/Example",
			"id":        "not-a-field",
		},
	})
	require.NoError(t, err)
	require.True(t, unknownFieldResult.IsError)
	require.Contains(t, toolResultText(unknownFieldResult), "field type \"not-a-field\" is unavailable")
	bootstrap.Spec.Fields = []entityschema.FieldSpec{
		{ID: "id", Kind: entityschema.FieldID, Editable: true},
		{ID: "version", Kind: entityschema.FieldVersion, Editable: true},
		{ID: "hierarchy", Kind: entityschema.FieldHierarchy, PropertyName: "children", Editable: true},
		{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Inherited: true, Editable: true},
	}
	bootstrap.Spec.InheritanceAware = true
	bootstrap.Spec.DefinitionBehavior = &entityschema.DefinitionBehaviorSpec{
		ParentDefinitionClass:        `Shopware\Core\Content\Product\ProductDefinition`,
		VersionAware:                 mcpBoolPointer(true),
		RestrictDeleteMetaProperties: []string{"id"},
	}
	bootstrap.Spec.DefinitionMetadata = &entityschema.DefinitionMetadataSpec{
		Since:         "6.7.1.0",
		Defaults:      []entityschema.DefinitionDefaultSpec{{PropertyName: "name", ValueExpression: "'default'"}},
		ChildDefaults: []entityschema.DefinitionDefaultSpec{{PropertyName: "name", ValueExpression: "'child'"}},
	}

	previewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": bootstrap.Spec},
	})
	require.NoError(t, err)
	require.False(t, previewResult.IsError, toolResultText(previewResult))
	var preview entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, previewResult, &preview)
	require.NotEmpty(t, preview.Revision)
	require.Empty(t, preview.Issues)
	require.NotEmpty(t, preview.Files)
	require.NotZero(t, preview.MigrationTimestamp)
	require.Contains(t, preview.Revision, ":")
	require.Equal(t, "ready", preview.Status)
	require.True(t, preview.ReadyToApply)
	require.Contains(t, preview.NextAction, "Do not preview again")
	previewJSON, err := json.Marshal(preview)
	require.NoError(t, err)
	require.Less(t, len(previewJSON), 8_000)
	require.NotContains(t, string(previewJSON), `"after"`)

	applyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_apply",
		Arguments: map[string]any{
			"spec": bootstrap.Spec, "revision": preview.Revision,
		},
	})
	require.NoError(t, err)
	require.False(t, applyResult.IsError, toolResultText(applyResult))
	var applied entitySchemaWriteOutput
	decodeMCPStructuredContent(t, applyResult, &applied)
	require.True(t, applied.Applied)
	require.NotEmpty(t, applied.SnapshotID)
	require.Contains(t, applied.Files, "custom/plugins/Example/src/Content/Example/ExampleDefinition.php")
	require.FileExists(t, filepath.Join(directory, "ExampleDefinition.php"))
	definitionSource, err := os.ReadFile(filepath.Join(directory, "ExampleDefinition.php"))
	require.NoError(t, err)
	require.Contains(t, string(definitionSource), "new ParentFkField(ExampleDefinition::class)")
	require.Contains(t, string(definitionSource), "new ReferenceVersionField(ExampleDefinition::class, 'parent_version_id')")
	require.Contains(t, string(definitionSource), "new ParentAssociationField(ExampleDefinition::class, 'id')")
	require.Contains(t, string(definitionSource), "new ChildrenAssociationField(ExampleDefinition::class)")
	require.Contains(t, string(definitionSource), "public function isInheritanceAware(): bool")
	require.Contains(t, string(definitionSource), "protected function getParentDefinitionClass(): ?string")
	require.Contains(t, string(definitionSource), "public function isVersionAware(): bool")
	require.Contains(t, string(definitionSource), "public function since(): ?string")
	require.Contains(t, string(definitionSource), "public function getDefaults(): array")
	require.Contains(t, string(definitionSource), "public function getChildDefaults(): array")
	require.Contains(t, string(definitionSource), "public function getRestrictDeleteMetaFields(): FieldCollection")
	require.Contains(t, string(definitionSource), "new StringField('name', 'name'))->addFlags(new Inherited())")

	loadResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_load",
		Arguments: map[string]any{
			"path": "custom/plugins/Example/src/Content/Example/ExampleDefinition.php",
		},
	})
	require.NoError(t, err)
	require.False(t, loadResult.IsError, toolResultText(loadResult))
	var loaded entityschema.EntitySpec
	decodeMCPStructuredContent(t, loadResult, &loaded)
	require.Equal(t, "edit", loaded.Mode)
	require.Equal(t, bootstrap.Spec.DefinitionBehavior.ParentDefinitionClass, loaded.DefinitionBehavior.ParentDefinitionClass)
	require.Equal(t, bootstrap.Spec.DefinitionMetadata.Since, loaded.DefinitionMetadata.Since)
	require.Equal(t, bootstrap.Spec.DefinitionMetadata.Defaults, loaded.DefinitionMetadata.Defaults)
	require.Equal(t, bootstrap.Spec.DefinitionMetadata.ChildDefaults, loaded.DefinitionMetadata.ChildDefaults)

	roundTripResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": loaded},
	})
	require.NoError(t, err)
	require.False(t, roundTripResult.IsError, toolResultText(roundTripResult))
	var roundTrip entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, roundTripResult, &roundTrip)
	require.False(t, roundTrip.Drift, roundTrip.DriftMessage)
	require.Empty(t, roundTrip.Issues)

	loaded.DefinitionKind = entityschema.DefinitionMapping
	loaded.Translation = nil
	loaded.InheritanceAware = false
	loaded.Fields = []entityschema.FieldSpec{{
		ID: "id", Kind: entityschema.FieldBinaryID, PropertyName: "id", StorageName: "id",
		Required: true, Primary: true, Editable: true,
	}}
	transitionPreviewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": loaded},
	})
	require.NoError(t, err)
	require.False(t, transitionPreviewResult.IsError, toolResultText(transitionPreviewResult))
	var transitionPreview entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, transitionPreviewResult, &transitionPreview)
	require.Empty(t, transitionPreview.Issues)
	require.Equal(t, "requires-destructive-confirmation", transitionPreview.Status)
	require.True(t, transitionPreview.Destructive)

	transitionApplyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_apply",
		Arguments: map[string]any{
			"spec": loaded, "revision": transitionPreview.Revision, "allowDestructive": true,
		},
	})
	require.NoError(t, err)
	require.False(t, transitionApplyResult.IsError, toolResultText(transitionApplyResult))
	require.NoFileExists(t, filepath.Join(directory, "ExampleEntity.php"))
	require.NoFileExists(t, filepath.Join(directory, "ExampleCollection.php"))
	definitionSource, err = os.ReadFile(filepath.Join(directory, "ExampleDefinition.php"))
	require.NoError(t, err)
	require.Contains(t, string(definitionSource), "extends MappingEntityDefinition")
}

func TestCompactEntitySchemaDiffKeepsTableRenameQuestions(t *testing.T) {
	summary := compactEntitySchemaDiff(entityschema.SchemaDiff{
		EntityRenameQuestions: []entityschema.EntityRenameQuestion{{
			Added: "acme_new", Candidates: []entityschema.RenameCandidate{{From: "acme_old", Score: 100}},
		}},
	})
	require.Equal(t, []entityschema.EntityRenameQuestion{{
		Added: "acme_new", Candidates: []entityschema.RenameCandidate{{From: "acme_old", Score: 100}},
	}}, summary.EntityRenameQuestions)
}

func TestMCPEntitySchemaCreatesMappingDefinition(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	directory := filepath.Join(plugin, "src", "Content", "ProductTag")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugin, "composer.json"), []byte(`{
        "name": "acme/example",
        "type": "shopware-platform-plugin",
        "require": {"shopware/core": "~6.7"},
        "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
        "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
    }`), 0o644))
	session := connectMCPTestClient(t, root)
	ctx := context.Background()

	bootstrapResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_bootstrap",
		Arguments: map[string]any{"directory": "custom/plugins/Example/src/Content/ProductTag"},
	})
	require.NoError(t, err)
	require.False(t, bootstrapResult.IsError, toolResultText(bootstrapResult))
	var bootstrap entitySchemaBootstrapOutput
	decodeMCPStructuredContent(t, bootstrapResult, &bootstrap)
	bootstrap.Spec.DefinitionKind = entityschema.DefinitionMapping
	bootstrap.Spec.Fields = []entityschema.FieldSpec{
		{ID: "product", Kind: entityschema.FieldForeignKey, PropertyName: "productId", StorageName: "product_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityName: "product", Required: true, Primary: true, Editable: true},
		{ID: "tag", Kind: entityschema.FieldForeignKey, PropertyName: "tagId", StorageName: "tag_id", TargetDefinitionClass: `Shopware\Core\System\Tag\TagDefinition`, TargetEntityName: "tag", Required: true, Primary: true, Editable: true},
	}

	previewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": bootstrap.Spec},
	})
	require.NoError(t, err)
	require.False(t, previewResult.IsError, toolResultText(previewResult))
	var preview entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, previewResult, &preview)
	require.Equal(t, "ready", preview.Status)
	require.Empty(t, preview.Issues)
	require.Len(t, preview.Files, 5)

	applyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_apply",
		Arguments: map[string]any{
			"spec": bootstrap.Spec, "revision": preview.Revision,
		},
	})
	require.NoError(t, err)
	require.False(t, applyResult.IsError, toolResultText(applyResult))
	content, err := os.ReadFile(filepath.Join(directory, "ProductTagDefinition.php"))
	require.NoError(t, err)
	require.Contains(t, string(content), "extends MappingEntityDefinition")
	require.NotContains(t, string(content), "getEntityClass")
	require.NoFileExists(t, filepath.Join(directory, "ProductTagEntity.php"))
	require.NoFileExists(t, filepath.Join(directory, "ProductTagCollection.php"))
}

func TestMCPEntitySchemaCreatesEntityExtension(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	definitionDirectory := filepath.Join(plugin, "src", "Content", "Product")
	extensionDirectory := filepath.Join(plugin, "src", "Extension", "Product")
	require.NoError(t, os.MkdirAll(definitionDirectory, 0o755))
	require.NoError(t, os.MkdirAll(extensionDirectory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugin, "composer.json"), []byte(`{
        "name": "acme/example",
        "type": "shopware-platform-plugin",
        "require": {"shopware/core": "~6.7"},
        "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
        "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
    }`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(definitionDirectory, "ProductDefinition.php"), []byte(`<?php declare(strict_types=1);
namespace Acme\Example\Content\Product;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ProductDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_product';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`), 0o644))
	session := connectMCPTestClient(t, root)
	ctx := context.Background()

	bootstrapResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_bootstrap",
		Arguments: map[string]any{"directory": "custom/plugins/Example/src/Extension/Product"},
	})
	require.NoError(t, err)
	require.False(t, bootstrapResult.IsError, toolResultText(bootstrapResult))
	var bootstrap entitySchemaBootstrapOutput
	decodeMCPStructuredContent(t, bootstrapResult, &bootstrap)
	bootstrap.Spec.DefinitionKind = entityschema.DefinitionExtension
	bootstrap.Spec.ClassName = "ProductCustom"
	bootstrap.Spec.DefinitionClass = ""
	bootstrap.Spec.EntityName = "acme_product"
	bootstrap.Spec.ExtendedDefinitionClass = `Acme\Example\Content\Product\ProductDefinition`
	bootstrap.Spec.Fields = []entityschema.FieldSpec{{
		ID: "parent", Kind: entityschema.FieldManyToOne, PropertyName: "parent", ForeignKeyPropertyName: "parentId",
		StorageName: "acme_parent_id", TargetDefinitionClass: `Acme\Example\Content\Product\ProductDefinition`,
		TargetEntityClass: `Acme\Example\Content\Product\ProductEntity`, TargetCollectionClass: `Acme\Example\Content\Product\ProductCollection`,
		TargetEntityName: "acme_product", ReferenceField: "id", ReferenceStorageName: "id", DeleteBehavior: entityschema.DeleteSetNull, Editable: true,
	}}
	bootstrap.Spec.Indexes = []entityschema.IndexSpec{{
		Name: "uniq.acme_product.acme_parent_id_id", Kind: entityschema.IndexUnique,
		Columns: []string{"acme_parent_id", "id"},
	}}

	previewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": bootstrap.Spec},
	})
	require.NoError(t, err)
	require.False(t, previewResult.IsError, toolResultText(previewResult))
	var preview entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, previewResult, &preview)
	require.Equal(t, "ready", preview.Status)
	require.Empty(t, preview.Issues)

	applyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_apply",
		Arguments: map[string]any{
			"spec": bootstrap.Spec, "revision": preview.Revision,
		},
	})
	require.NoError(t, err)
	require.False(t, applyResult.IsError, toolResultText(applyResult))
	source, err := os.ReadFile(filepath.Join(extensionDirectory, "ProductCustomExtension.php"))
	require.NoError(t, err)
	require.Contains(t, string(source), "extends EntityExtension")
	require.Contains(t, string(source), "return ProductDefinition::ENTITY_NAME")
	migrations, err := filepath.Glob(filepath.Join(plugin, "src", "Migration", "*.php"))
	require.NoError(t, err)
	require.Len(t, migrations, 1)
	migration, err := os.ReadFile(migrations[0])
	require.NoError(t, err)
	require.Contains(t, string(migration), "ADD UNIQUE INDEX `uniq.acme_product.acme_parent_id_id` (`acme_parent_id`, `id`)")
	require.NoFileExists(t, filepath.Join(extensionDirectory, "ProductCustomEntity.php"))
	require.NoFileExists(t, filepath.Join(extensionDirectory, "ProductCustomCollection.php"))
}

func TestMCPEntitySchemaCreatesBulkEntityExtension(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "BulkExample")
	extensionDirectory := filepath.Join(plugin, "src", "Extension", "Catalog")
	require.NoError(t, os.MkdirAll(extensionDirectory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugin, "composer.json"), []byte(`{
        "name": "acme/bulk-example",
        "type": "shopware-platform-plugin",
        "require": {"shopware/core": "~6.7"},
        "autoload": {"psr-4": {"Acme\\BulkExample\\": "src/"}},
        "extra": {"shopware-plugin-class": "Acme\\BulkExample\\BulkExample"}
    }`), 0o644))
	writeDefinition := func(directory, namespace, className, entityName string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(directory, 0o755))
		source := `<?php declare(strict_types=1);
namespace ` + namespace + `;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ` + className + ` extends EntityDefinition {
    public const ENTITY_NAME = '` + entityName + `';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
		require.NoError(t, os.WriteFile(filepath.Join(directory, className+".php"), []byte(source), 0o644))
	}
	writeDefinition(filepath.Join(plugin, "src", "Content", "Product"), `Acme\BulkExample\Content\Product`, "ProductDefinition", "acme_product")
	writeDefinition(filepath.Join(plugin, "src", "Content", "Category"), `Acme\BulkExample\Content\Category`, "CategoryDefinition", "acme_category")

	session := connectMCPTestClient(t, root)
	ctx := context.Background()
	bootstrapResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_bootstrap",
		Arguments: map[string]any{
			"directory": "custom/plugins/BulkExample/src/Extension/Catalog",
		},
	})
	require.NoError(t, err)
	require.False(t, bootstrapResult.IsError, toolResultText(bootstrapResult))
	var bootstrap entitySchemaBootstrapOutput
	decodeMCPStructuredContent(t, bootstrapResult, &bootstrap)
	productDefinition := `Acme\BulkExample\Content\Product\ProductDefinition`
	categoryDefinition := `Acme\BulkExample\Content\Category\CategoryDefinition`
	toOne := func(id, property, storage, targetClass, targetEntity string) entityschema.FieldSpec {
		base := strings.TrimSuffix(targetClass, "Definition")
		return entityschema.FieldSpec{
			ID: id, Kind: entityschema.FieldManyToOne, PropertyName: property,
			ForeignKeyPropertyName: property + "Id", StorageName: storage,
			TargetDefinitionClass: targetClass, TargetEntityClass: base + "Entity",
			TargetCollectionClass: base + "Collection", TargetEntityName: targetEntity,
			ReferenceField: "id", ReferenceStorageName: "id", DeleteBehavior: entityschema.DeleteSetNull, Editable: true,
		}
	}
	bootstrap.Spec.DefinitionKind = entityschema.DefinitionBulkExtension
	bootstrap.Spec.ClassName = "Catalog"
	bootstrap.Spec.DefinitionClass = ""
	bootstrap.Spec.EntityName = ""
	bootstrap.Spec.Fields = nil
	bootstrap.Spec.Indexes = nil
	bootstrap.Spec.BulkExtensions = []entityschema.BulkExtensionTargetSpec{
		{
			ID: "product", EntityName: "acme_product", ExtendedDefinitionClass: productDefinition,
			Fields: []entityschema.FieldSpec{toOne("category", "category", "acme_category_id", categoryDefinition, "acme_category")},
		},
		{
			ID: "category", EntityName: "acme_category", ExtendedDefinitionClass: categoryDefinition,
			Fields: []entityschema.FieldSpec{toOne("product", "product", "acme_product_id", productDefinition, "acme_product")},
		},
	}

	previewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_preview", Arguments: map[string]any{"spec": bootstrap.Spec},
	})
	require.NoError(t, err)
	require.False(t, previewResult.IsError, toolResultText(previewResult))
	var preview entitySchemaPreviewOutput
	decodeMCPStructuredContent(t, previewResult, &preview)
	require.Equal(t, "ready", preview.Status)
	require.Empty(t, preview.Issues)
	require.ElementsMatch(t, []string{"acme_category.acme_product_id", "acme_product.acme_category_id"}, preview.Diff.AddedColumns)

	applyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_apply",
		Arguments: map[string]any{"spec": bootstrap.Spec, "revision": preview.Revision},
	})
	require.NoError(t, err)
	require.False(t, applyResult.IsError, toolResultText(applyResult))
	definitionPath := filepath.Join(extensionDirectory, "CatalogBulkEntityExtension.php")
	source, err := os.ReadFile(definitionPath)
	require.NoError(t, err)
	require.Contains(t, string(source), "extends BulkEntityExtension")
	require.Contains(t, string(source), "yield ProductDefinition::ENTITY_NAME")
	require.Contains(t, string(source), "yield CategoryDefinition::ENTITY_NAME")
	service, err := os.ReadFile(filepath.Join(plugin, "src", "Resources", "config", "services.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(service), "shopware.bulk.entity.extension")
	require.NoFileExists(t, filepath.Join(extensionDirectory, "CatalogEntity.php"))
	require.NoFileExists(t, filepath.Join(extensionDirectory, "CatalogCollection.php"))

	loadResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_entity_schema_load", Arguments: map[string]any{"path": "custom/plugins/BulkExample/src/Extension/Catalog/CatalogBulkEntityExtension.php"},
	})
	require.NoError(t, err)
	require.False(t, loadResult.IsError, toolResultText(loadResult))
	var loaded entityschema.EntitySpec
	decodeMCPStructuredContent(t, loadResult, &loaded)
	require.Equal(t, entityschema.DefinitionBulkExtension, loaded.DefinitionKind)
	require.Len(t, loaded.BulkExtensions, 2)
}

func TestMCPDiagnosticsAndCodeActionUseProductionLSP(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(
		root,
		"custom/plugins/Test/src/Resources/app/administration/src/component.js",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(
		path, []byte("this.$tc('translation.key');\n"), 0o644,
	))
	session := connectMCPTestClient(t, root)
	ctx := context.Background()

	diagnostics, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_diagnostics",
		Arguments: map[string]any{
			"path": "custom/plugins/Test/src/Resources/app/administration/src/component.js",
		},
	})
	require.NoError(t, err)
	require.False(t, diagnostics.IsError, toolResultText(diagnostics))
	var diagnosticResult diagnosticsOutput
	decodeMCPStructuredContent(t, diagnostics, &diagnosticResult)
	require.GreaterOrEqual(t, diagnosticResult.Total, 1)
	var found bool
	for _, diagnostic := range diagnosticResult.Diagnostics {
		if diagnostic.Code == "admin.vue-i18n.tc-deprecated" {
			found = true
			require.Equal(t, 1, diagnostic.Range.Start.Line)
			require.Equal(t, 6, diagnostic.Range.Start.Column)
		}
	}
	require.True(t, found, "expected admin.vue-i18n.tc-deprecated in %#v", diagnosticResult)

	actions, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_code_actions",
		Arguments: map[string]any{
			"path":   "custom/plugins/Test/src/Resources/app/administration/src/component.js",
			"line":   1,
			"column": 6,
			"kind":   "quickfix",
		},
	})
	require.NoError(t, err)
	require.False(t, actions.IsError, toolResultText(actions))
	var actionResult codeActionsOutput
	decodeMCPStructuredContent(t, actions, &actionResult)
	require.Contains(t, actionTitles(actionResult.Actions), "Replace deprecated $tc() with $t()")

	applied, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_apply_code_action",
		Arguments: map[string]any{
			"path":   "custom/plugins/Test/src/Resources/app/administration/src/component.js",
			"line":   1,
			"column": 6,
			"kind":   "quickfix",
			"title":  "Replace deprecated $tc() with $t()",
		},
	})
	require.NoError(t, err)
	require.False(t, applied.IsError, toolResultText(applied))
	var applyResult applyCodeActionOutput
	decodeMCPStructuredContent(t, applied, &applyResult)
	require.True(t, applyResult.Applied)
	require.Contains(t, applyResult.Diff, "+this.$t('translation.key');")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "this.$t('translation.key');\n", string(content))
}

func TestMCPTwigVersioningQuickFixUsesProductionIndex(t *testing.T) {
	root := t.TempDir()
	upstreamPath := filepath.Join(
		root,
		"src/Storefront/Resources/views/storefront/page/example.html.twig",
	)
	overridePath := filepath.Join(
		root,
		"custom/plugins/Test/src/Resources/views/storefront/page/example.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(upstreamPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(overridePath), 0o755))
	require.NoError(t, os.WriteFile(
		upstreamPath,
		[]byte("{% block content %}upstream{% endblock %}\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		overridePath,
		[]byte(`{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{# shopware-block: deadbeef@6.6.0.0 #}
{% block content %}local{% endblock %}
`),
		0o644,
	))
	session := connectMCPTestClient(t, root)
	ctx := context.Background()
	const relativePath = "custom/plugins/Test/src/Resources/views/storefront/page/example.html.twig"

	diagnostics, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_diagnostics",
		Arguments: map[string]any{
			"path": relativePath,
		},
	})
	require.NoError(t, err)
	require.False(t, diagnostics.IsError, toolResultText(diagnostics))
	var diagnosticResult diagnosticsOutput
	decodeMCPStructuredContent(t, diagnostics, &diagnosticResult)
	var found bool
	for _, diagnostic := range diagnosticResult.Diagnostics {
		if diagnostic.Code == "twig.versioning.outdated" {
			found = true
		}
		require.NotEqual(t, "twig.versioning.comment_missing", diagnostic.Code)
	}
	require.True(t, found, "expected outdated Twig versioning diagnostic in %#v", diagnosticResult)

	actions, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_code_actions",
		Arguments: map[string]any{
			"path": relativePath, "line": 3, "column": 10,
			"kind": "quickfix",
		},
	})
	require.NoError(t, err)
	require.False(t, actions.IsError, toolResultText(actions))
	var actionResult codeActionsOutput
	decodeMCPStructuredContent(t, actions, &actionResult)
	require.Contains(
		t, actionTitles(actionResult.Actions),
		"Shopware: Update Twig block version comment",
	)

	applied, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "shopware_apply_code_action",
		Arguments: map[string]any{
			"path": relativePath, "line": 3, "column": 10,
			"kind":  "quickfix",
			"title": "Shopware: Update Twig block version comment",
		},
	})
	require.NoError(t, err)
	require.False(t, applied.IsError, toolResultText(applied))
	var applyResult applyCodeActionOutput
	decodeMCPStructuredContent(t, applied, &applyResult)
	require.True(t, applyResult.Applied)
	require.Contains(t, applyResult.Diff, "-\u007b# shopware-block: deadbeef@6.6.0.0 #\u007d")
	content, err := os.ReadFile(overridePath)
	require.NoError(t, err)
	require.NotContains(t, string(content), "deadbeef")
	require.Contains(t, string(content), "{# shopware-block: ")
}

func TestMCPRejectsPathsOutsideWorkspaceBeforeIndexing(t *testing.T) {
	root := t.TempDir()
	session := connectMCPTestClient(t, root)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "shopware_diagnostics",
		Arguments: map[string]any{
			"path": filepath.Join(filepath.Dir(root), "outside.php"),
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, toolResultText(result), "outside workspace root")
}

func TestMCPRejectsUnsafeWorkspaceEdits(t *testing.T) {
	root := t.TempDir()
	runtime := &mcpRuntime{root: root}
	outside := uriutil.FileURI(filepath.Join(filepath.Dir(root), "outside.php"))
	err := runtime.validateWorkspaceEdit(&protocol.WorkspaceEdit{
		Changes: map[string][]protocol.TextEdit{outside: nil},
	})
	require.ErrorContains(t, err, "outside workspace root")

	err = runtime.validateWorkspaceEdit(&protocol.WorkspaceEdit{
		DocumentChanges: []protocol.DocumentChange{{Kind: "delete", URI: outside}},
	})
	require.ErrorContains(t, err, "outside workspace root")

	err = runtime.validateWorkspaceEdit(&protocol.WorkspaceEdit{
		DocumentChanges: []protocol.DocumentChange{{Kind: "rename", URI: outside}},
	})
	require.ErrorContains(t, err, "unsupported workspace resource operation")
}

func TestMCPEditorConfigurationReadsDiagnosticOverrides(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_EDITOR_CONFIGURATION", `{
        "mcp": {"tools": {"shopware_scaffold": false}},
        "diagnostics": {
            "overrides": [{"files":["custom/plugins/Test/**"],"enabled":false}]
        }
    }`)
	configuration, err := mcpEditorConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration)
	require.Len(t, configuration.Diagnostics.Overrides, 1)
	require.Equal(t, "custom/plugins/Test/**", configuration.Diagnostics.Overrides[0].Files[0])
	require.False(t, configuration.MCP.Tools["shopware_scaffold"])
}

func connectMCPTestClient(t *testing.T, root string) *mcp.ClientSession {
	return connectMCPTestClientWithConfiguration(t, root, projectconfig.Effective{})
}

func connectMCPTestClientWithConfiguration(
	t *testing.T,
	root string,
	configuration projectconfig.Effective,
) *mcp.ClientSession {
	t.Helper()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	runner := New(Options{Version: "test"})
	runner.root = root
	runner.allowUnsupportedProject = true
	runner.errOut = &bytes.Buffer{}
	runtime := &mcpRuntime{runner: runner, root: root, configuration: configuration}
	server := newMCPServer(runtime)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(
		&mcp.Implementation{Name: "shopware-lsp-test", Version: "test"}, nil,
	)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, clientSession.Close())
		require.NoError(t, serverSession.Close())
		require.NoError(t, runtime.Close())
	})
	return clientSession
}

func scaffoldKindNames(kinds []integration.ScaffoldDefinition) []string {
	result := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, kind.Family+":"+kind.Kind)
	}
	return result
}

func decodeMCPStructuredContent(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	content, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, target), string(content))
}

func outputSchemaProperties(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	content, err := json.Marshal(tool.OutputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(content, &schema), string(content))
	require.NotEmpty(t, schema.Properties, string(content))
	return schema.Properties
}

func toolResultText(result *mcp.CallToolResult) string {
	var output bytes.Buffer
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			output.WriteString(text.Text)
		}
	}
	return output.String()
}

func actionTitles(actions []mcpCodeAction) []string {
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		result = append(result, action.Title)
	}
	return result
}

func mcpBoolPointer(value bool) *bool { return &value }
