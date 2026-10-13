package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIExtractFunctionSelectionPreview(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	path := filepath.Join(root, "Live.php")
	source := "<?php\nfunction run($a,$b) {\n$x=$a+1;\n$y=$b+2;\nreturn [$x,$y];\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	var output, errors bytes.Buffer
	err := New(Options{Version: "test"}).Run(context.Background(), []string{
		"-root", root, "-allow-unsupported-project", "codeaction",
		"-end-line", "4", "-end-column", "9", "-kind", "refactor.extract",
		"-title", "^Extract function 'extractedFunction'$", "-exec", "-d", path + ":3:1",
	}, strings.NewReader(""), &output, &errors)
	require.NoError(t, err, errors.String())
	assert.Contains(t, output.String(), "[$x, $y] = extractedFunction($a, $b);")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, source, string(unchanged))
}

func TestMCPExtractFunctionSelectionAndAllOccurrences(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		source, title, expected string
		args                    map[string]any
	}{
		{"<?php\nfunction run($a,$b) {\n$x=$a+1;\n$y=$b+2;\nreturn [$x,$y];\n}\n", "Extract function 'extractedFunction'", "[$x, $y] = extractedFunction($a, $b);", map[string]any{"line": 3, "column": 1, "endLine": 4, "endColumn": 9}},
		{"<?php\n$a=1;\n$x=$a+1;\n$y=$a+1;\n", "Extract variable '$extracted' (all 2 occurrences)", "$y=$extracted;", map[string]any{"line": 3, "column": 4}},
	} {
		t.Run(tc.title, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "Live.php")
			require.NoError(t, os.WriteFile(path, []byte(tc.source), 0600))
			session := connectMCPTestClient(t, root)
			args := tc.args
			args["path"], args["kind"] = "Live.php", "refactor.extract"
			listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "shopware_code_actions", Arguments: args})
			require.NoError(t, err)
			require.False(t, listed.IsError, toolResultText(listed))
			var actions codeActionsOutput
			decodeMCPStructuredContent(t, listed, &actions)
			require.Contains(t, actionTitles(actions.Actions), tc.title)
			args["title"] = tc.title
			applied, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "shopware_apply_code_action", Arguments: args})
			require.NoError(t, err)
			require.False(t, applied.IsError, toolResultText(applied))
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(got), tc.expected)
		})
	}
}

func TestCodeActionSelectionBoundaries(t *testing.T) {
	start := protocol.Position{Line: 2, Character: 4}
	rng, err := codeActionSelection(start, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, start, rng.End)
	rng, err = codeActionSelection(start, 4, 1)
	require.NoError(t, err)
	assert.Equal(t, protocol.Position{Line: 3}, rng.End)
	for _, end := range [][2]int{{-1, 2}, {2, 1}, {3, 2}, {0, -1}} {
		_, err := codeActionSelection(start, end[0], end[1])
		assert.Error(t, err)
	}
}
