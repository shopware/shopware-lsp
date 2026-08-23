package twig

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestRenderBlockReferencesSupportPositionalAndNamedArguments(t *testing.T) {
	source := `<?php
$this->renderBlock('page.html.twig', 'content', []);
$this->renderBlockView(
    block: 'sidebar',
    parameters: [],
    view: 'named.html.twig',
);
$this->renderBlock($dynamic, 'ignored', []);
$this->renderBlock('page.html.twig', $dynamic, []);
`
	references := RenderBlockReferencesInPHP(
		phpparser.Parse(source).Tree.Root,
	)
	require.Len(t, references, 2)
	require.Equal(t, "page.html.twig", references[0].Template)
	require.Equal(t, "content", references[0].Block)
	require.Equal(
		t,
		"content",
		source[references[0].BlockRange.Start:references[0].BlockRange.End],
	)
	require.Equal(t, "named.html.twig", references[1].Template)
	require.Equal(t, "sidebar", references[1].Block)
}

func TestGetTemplateBlocksFollowsExtendsAndRestoresRanges(t *testing.T) {
	cache := t.TempDir()
	index, err := NewTwigIndexer(cache)
	require.NoError(t, err)

	root := t.TempDir()
	basePath := filepath.Join(root, "templates", "base.html.twig")
	childPath := filepath.Join(root, "templates", "child.html.twig")
	require.NoError(t, index.Index(indexer.NewParsedFile(
		basePath,
		[]byte(`{## Main content. ##}
{% block content %}{% endblock %}
{% block sidebar %}{% endblock %}`),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		childPath,
		[]byte(`{% extends 'base.html.twig' %}
{% block content %}{% endblock %}
{% block child %}{% endblock %}`),
	)))

	blocks, err := index.GetTemplateBlocks("child.html.twig")
	require.NoError(t, err)
	require.Len(t, blocks, 4)
	var names []string
	for _, block := range blocks {
		names = append(names, block.Name)
		require.NotZero(t, block.Range.Len())
	}
	require.ElementsMatch(t, []string{
		"content",
		"content",
		"sidebar",
		"child",
	}, names)
	var documented *TemplateBlock
	for index := range blocks {
		if blocks[index].FilePath == basePath && blocks[index].Name == "content" {
			documented = &blocks[index]
			break
		}
	}
	require.NotNil(t, documented)
	require.Equal(t, "Main content.", documented.Documentation)
	require.Equal(t, 2, documented.Line)
	require.NoError(t, index.Close())

	restored, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredBlocks, err := restored.GetTemplateBlocks("child.html.twig")
	require.NoError(t, err)
	require.Equal(t, blocks, restoredBlocks)
}
