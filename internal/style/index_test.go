package style

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexConnectsSCSSDeclarationsAndTwigUsages(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/component.scss",
		[]byte(".sw-card { &__title { color: red; } }"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/page.html.twig",
		[]byte(`<div class="sw-card sw-card__title"></div>`),
	)))

	declarations, err := idx.Declarations("sw-card__title")
	require.NoError(t, err)
	require.Len(t, declarations, 1)
	assert.Equal(t, "/project/component.scss", declarations[0].File)
	assert.Equal(t, 0, declarations[0].Start.Line)
	assert.Positive(t, declarations[0].Start.Character)

	usages, err := idx.Usages("sw-card__title")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	assert.Equal(t, "/project/page.html.twig", usages[0].File)
	assert.Positive(t, usages[0].Start.Character)

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/page.html.twig",
		[]byte(`<div class="other"></div>`),
	)))
	usages, err = idx.Usages("sw-card__title")
	require.NoError(t, err)
	assert.Empty(t, usages)
}

func TestIndexSupportsVueTemplateAndStyleSections(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	source := `<template><div class="sw-panel"></div></template>
<style lang="scss">.sw-panel { color: red; }</style>`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/component.vue",
		[]byte(source),
	)))

	declarations, err := idx.Declarations("sw-panel")
	require.NoError(t, err)
	require.Len(t, declarations, 1)
	usages, err := idx.Usages("sw-panel")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	assert.Equal(t, 1, declarations[0].Start.Line)
	assert.Equal(t, 0, usages[0].Start.Line)
}

func TestIndexTracksGlobalAndThemeSCSSVariables(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	variablesPath := "/project/_variables.scss"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		variablesPath,
		[]byte(`$brand-primary: #0042a0;
.scope { $local-only: red; }`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/theme.json",
		[]byte(`{"config":{"fields":{
            "theme-color":{"type":"color"},
            "not-in-scss":{"type":"text","scss":false}
        }}}`),
	)))

	exists, err := idx.HasVariableDeclaration("brand_primary", "")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = idx.HasVariableDeclaration("theme-color", "")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = idx.HasVariableDeclaration("local-only", "")
	require.NoError(t, err)
	assert.False(t, exists)
	exists, err = idx.HasVariableDeclaration("not-in-scss", "")
	require.NoError(t, err)
	assert.False(t, exists)

	declarations, err := idx.VariableDeclarations("brand-primary")
	require.NoError(t, err)
	require.Len(t, declarations, 1)
	assert.Equal(t, variablesPath, declarations[0].File)
	assert.Equal(t, 0, declarations[0].Start.Line)
	assert.Equal(t, 0, declarations[0].Start.Character)

	exists, err = idx.HasVariableDeclaration("brand-primary", variablesPath)
	require.NoError(t, err)
	assert.False(t, exists, "the open document must not resolve against its stale disk entry")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		variablesPath, []byte("$replacement: blue;"),
	)))
	exists, err = idx.HasVariableDeclaration("brand-primary", "")
	require.NoError(t, err)
	assert.False(t, exists)
	exists, err = idx.HasVariableDeclaration("replacement", "")
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, idx.RemovedFiles([]string{"/project/theme.json"}))
	exists, err = idx.HasVariableDeclaration("theme-color", "")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestIndexRestoresSCSSVariables(t *testing.T) {
	configDir := t.TempDir()
	idx, err := NewIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/_variables.scss", []byte("$persisted: red;"),
	)))
	require.NoError(t, idx.Close())

	restored, err := NewIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	exists, err := restored.HasVariableDeclaration("persisted", "")
	require.NoError(t, err)
	assert.True(t, exists)
}
