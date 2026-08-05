package definition

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/style"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStyleClassDefinitionNavigatesFromTwigToNestedSCSS(t *testing.T) {
	root := t.TempDir()
	stylePath := filepath.Join(root, "component.scss")
	index, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		stylePath,
		[]byte(".sw-card {\n  &__title { color: red; }\n}"),
	)))

	templatePath := filepath.Join(root, "page.html.twig")
	source := `<div class="sw-card__title"></div>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "sw-card__title") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewStyleClassDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)

	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(stylePath), locations[0].URI)
	assert.Equal(t, 1, locations[0].Range.Start.Line)
	assert.Equal(t, 2, locations[0].Range.Start.Character)
}

func TestStyleClassDefinitionUsesLiveVueTemplateAndStyle(t *testing.T) {
	index, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	path := filepath.Join(t.TempDir(), "component.vue")
	source := `<template><div class="sw-panel"></div></template>
<style lang="scss">.sw-panel { color: red; }</style>`
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "sw-panel") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewStyleClassDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)

	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(path), locations[0].URI)
	assert.Equal(t, 1, locations[0].Range.Start.Line)
}
