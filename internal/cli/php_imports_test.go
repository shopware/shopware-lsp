package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPPHPImportCleanupUsesProductionAnalysisAndEdits(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"quickfix", "source.organizeImports"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Live.php")
			source := "<?php\nuse Vendor\\Unused;\nclass Live {}\n"
			require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
			session := connectMCPTestClient(t, root)
			ctx := context.Background()
			diagnostics, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shopware_diagnostics", Arguments: map[string]any{"path": "Live.php", "severity": "hint"}})
			require.NoError(t, err)
			require.False(t, diagnostics.IsError, toolResultText(diagnostics))
			var diagnosticResult diagnosticsOutput
			decodeMCPStructuredContent(t, diagnostics, &diagnosticResult)
			require.Len(t, diagnosticResult.Diagnostics, 1)
			require.Equal(t, "php.unusedImport", diagnosticResult.Diagnostics[0].Code)
			require.Equal(t, 2, diagnosticResult.Diagnostics[0].Range.Start.Line)
			args := map[string]any{"path": "Live.php", "line": 2, "column": 5, "kind": kind}
			listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shopware_code_actions", Arguments: args})
			require.NoError(t, err)
			require.False(t, listed.IsError, toolResultText(listed))
			var actions codeActionsOutput
			decodeMCPStructuredContent(t, listed, &actions)
			title := "Organize Imports"
			if kind == "quickfix" {
				title = "Remove unused import 'Unused'"
			}
			require.Contains(t, actionTitles(actions.Actions), title)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, source, string(before))
			args["title"] = title
			applied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shopware_apply_code_action", Arguments: args})
			require.NoError(t, err)
			require.False(t, applied.IsError, toolResultText(applied))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "<?php\nclass Live {}\n", string(after))
		})
	}
}
