package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/style"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSCSSVariableAnalyzerUsesLiveAndIndexedDeclarations(t *testing.T) {
	idx, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/_variables.scss",
		[]byte("$project-value: red;\n$items: ();"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/theme.json",
		[]byte(`{"config":{"fields":{"theme-color":{"type":"color"}}}}`),
	)))

	source := `.use {
  color: $project_value;
  background: $theme-color;
  border-color: $later;
}
$later: red;
@mixin paint($color) { color: $color; }
@include paint($color: $project-value);
@each $item in $items { color: $item; }
.module { z-index: math.$pi; }
.missing { color: $missing; }`
	document := diagnosticsDocument(
		"file:///project/main.scss", []byte(source),
	)
	problems, err := NewSCSSVariableAnalyzer(idx).Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, SCSSVariableUnknownCode, problems[0].ID)
	assert.Equal(t, "SCSS variable '$missing' is not defined", problems[0].Message)
	assert.Equal(t, "$missing", source[problems[0].Range.Start:problems[0].Range.End])
}

func TestSCSSVariableAnalyzerDoesNotUseStaleCurrentFileDeclaration(t *testing.T) {
	idx, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/main.scss", []byte("$removed: red;"),
	)))

	document := diagnosticsDocument(
		"file:///project/main.scss", []byte(".use { color: $removed; }"),
	)
	problems, err := NewSCSSVariableAnalyzer(idx).Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, SCSSVariableUnknownCode, problems[0].ID)
}

func TestSCSSVariableAnalyzerWaitsForCompleteWorkspaceIndex(t *testing.T) {
	idx, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	document := diagnosticsDocument(
		"file:///project/main.scss", []byte(".use { color: $missing; }"),
	)

	problems, err := NewSCSSVariableAnalyzer(
		idx, staticSCSSIndexReadiness(false),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)

	problems, err = NewSCSSVariableAnalyzer(
		idx, staticSCSSIndexReadiness(true),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
}

type staticSCSSIndexReadiness bool

func (ready staticSCSSIndexReadiness) Ready(context.Context) (bool, error) {
	return bool(ready), nil
}
