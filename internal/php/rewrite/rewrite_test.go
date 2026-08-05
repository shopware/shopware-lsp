package phprewrite

import (
	"testing"

	"github.com/stretchr/testify/require"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	sourcerewrite "github.com/shopware/shopware-lsp/internal/rewrite"
)

func testEditor(t *testing.T, source string) (*Editor, *phpsyntax.Node) {
	t.Helper()
	parsed := phpparser.Parse(source)
	require.Empty(t, parsed.Errors)
	require.NotNil(t, parsed.Tree)
	require.NotNil(t, parsed.Tree.Root)
	return NewEditor(source, parsed.Tree.Root), parsed.Tree.Root
}

func applyTestEditor(t *testing.T, source string, editor *Editor) string {
	t.Helper()
	edits, err := editor.Finish()
	require.NoError(t, err)
	updated, err := sourcerewrite.Apply(source, edits)
	require.NoError(t, err)
	parsed := phpparser.Parse(updated)
	require.Empty(t, parsed.Errors, updated)
	return updated
}
