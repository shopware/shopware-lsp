package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestMCPServerAdvertisesAnalysisAndWriteTools(t *testing.T) {
	root := t.TempDir()
	session := connectMCPTestClient(t, root)
	initializeResult := session.InitializeResult()
	require.NotNil(t, initializeResult)
	require.Contains(t, initializeResult.Instructions, "always use shopware_scaffold")
	leadingInstructions := initializeResult.Instructions[:min(512, len(initializeResult.Instructions))]
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
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_bootstrap"]), "spec")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_preview"]), "revision")
	require.Contains(t, outputSchemaProperties(t, byName["shopware_entity_schema_apply"]), "diff")
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
	var bootstrap scaffold.EntitySchemaBootstrapResponse
	decodeMCPStructuredContent(t, bootstrapResult, &bootstrap)
	require.Equal(t, "custom/plugins/Example", bootstrap.Plugin.RootURI)
	require.Equal(t, "custom/plugins/Example/src/Content/Example", bootstrap.Spec.DirectoryURI)
	require.NotEmpty(t, bootstrap.FieldTypes)

	previewResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "shopware_entity_schema_preview",
		Arguments: map[string]any{"spec": bootstrap.Spec},
	})
	require.NoError(t, err)
	require.False(t, previewResult.IsError, toolResultText(previewResult))
	var preview scaffold.EntitySchemaPreviewResponse
	decodeMCPStructuredContent(t, previewResult, &preview)
	require.NotEmpty(t, preview.Revision)
	require.Empty(t, preview.Issues)
	require.NotEmpty(t, preview.Files)
	require.NotZero(t, preview.MigrationTimestamp)

	bootstrap.Spec.MigrationTimestamp = preview.MigrationTimestamp
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

func scaffoldKindNames(kinds []mcpScaffoldKind) []string {
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
