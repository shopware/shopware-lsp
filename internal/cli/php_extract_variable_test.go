package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPPHPExtractVariableUsesProductionEdits(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"refactor.extract"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Live.php")
			source := "<?php\nreturn build();\n"
			require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
			session := connectMCPTestClient(t, root)
			ctx := context.Background()
			args := map[string]any{"path": "Live.php", "line": 2, "column": 9, "kind": kind}
			listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shopware_code_actions", Arguments: args})
			require.NoError(t, err)
			require.False(t, listed.IsError, toolResultText(listed))
			var actions codeActionsOutput
			decodeMCPStructuredContent(t, listed, &actions)
			title := "Extract variable '$extracted'"
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
			require.Equal(t, "<?php\n$extracted = build();\nreturn $extracted;\n", string(after))
		})
	}
}
