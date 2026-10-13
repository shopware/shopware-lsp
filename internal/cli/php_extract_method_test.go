package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPPHPExtractMethodUsesProductionEdits(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"refactor.extract"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Live.php")
			source := "<?php\nclass Demo { public function run($a) {\nreturn $a;\n} }\n"
			require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
			session := connectMCPTestClient(t, root)
			ctx := context.Background()
			args := map[string]any{"path": "Live.php", "line": 3, "column": 1, "kind": kind}
			listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "shopware_code_actions", Arguments: args})
			require.NoError(t, err)
			require.False(t, listed.IsError, toolResultText(listed))
			var actions codeActionsOutput
			decodeMCPStructuredContent(t, listed, &actions)
			title := "Extract method 'extractedMethod'"
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
			require.Contains(t, string(after), "return $this->extractedMethod($a);")
			require.Contains(t, string(after), "private function extractedMethod($a)")
		})
	}
}
