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
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestMCPServerAdvertisesAnalysisAndWriteTools(t *testing.T) {
	root := t.TempDir()
	session := connectMCPTestClient(t, root)

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	byName := make(map[string]*mcp.Tool, len(tools.Tools))
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"shopware_diagnostics",
		"shopware_code_actions",
		"shopware_apply_code_action",
		"shopware_hover",
		"shopware_definition",
		"shopware_references",
		"shopware_workspace_symbols",
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
	require.True(t, byName["shopware_diagnostics"].Annotations.ReadOnlyHint)
	require.False(t, byName["shopware_apply_code_action"].Annotations.ReadOnlyHint)
	require.NotNil(t, byName["shopware_apply_code_action"].Annotations.DestructiveHint)
	require.True(t, *byName["shopware_apply_code_action"].Annotations.DestructiveHint)
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

func connectMCPTestClient(t *testing.T, root string) *mcp.ClientSession {
	t.Helper()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	runner := New(Options{Version: "test"})
	runner.root = root
	runner.errOut = &bytes.Buffer{}
	runtime := &mcpRuntime{runner: runner, root: root}
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
