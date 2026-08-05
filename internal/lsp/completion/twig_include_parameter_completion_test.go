package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigIncludeParameterCompletionAcrossSupportedForms(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/card.html.twig",
		[]byte(`{% props title, subtitle = 'Fallback' %}
{{ product.name }}`),
	)))
	provider := NewTwigIncludeParameterCompletionProvider(index, nil)

	for _, source := range []string{
		`{{ include('card.html.twig', {ti`,
		`{% include 'card.html.twig' with {ti`,
		`{% embed 'card.html.twig' with {ti`,
	} {
		items := twigIncludeParameterCompletions(t, provider, source)
		requireCompletion(t, items, "title")
		requireCompletion(t, items, "subtitle")
		requireCompletion(t, items, "product")
	}
}

func TestTwigIncludeParameterCompletionExcludesAlreadyProvidedKeys(
	t *testing.T,
) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/card.html.twig",
		[]byte(`{{ title }} {{ subtitle }}`),
	)))
	provider := NewTwigIncludeParameterCompletionProvider(index, nil)
	items := twigIncludeParameterCompletions(
		t,
		provider,
		`{{ include('card.html.twig', {'title': value, su`,
	)
	requireCompletion(t, items, "subtitle")
	for _, item := range items {
		require.NotEqual(t, "title", item.Label)
	}
}

func TestTwigIncludeParameterCompletionUsesInheritedContract(
	t *testing.T,
) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		"/project/templates/base.html.twig": `{{ baseValue }}`,
		"/project/templates/card.html.twig": `{% extends 'base.html.twig' %}
{{ ownValue }}`,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	items := twigIncludeParameterCompletions(
		t,
		NewTwigIncludeParameterCompletionProvider(index, nil),
		`{% include 'card.html.twig' with {ba`,
	)
	requireCompletion(t, items, "baseValue")
	requireCompletion(t, items, "ownValue")
}

func TestTwigIncludeParameterCompletionCarriesPHPType(t *testing.T) {
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/templates/card.html.twig",
		[]byte(`{{ product.name }}`),
	)))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Controller.php",
		[]byte(`<?php
namespace App;
class Product {}
class Controller {
    public function card(Product $product) {
        return $this->render('card.html.twig', ['product' => $product]);
    }
}`),
	)))
	items := twigIncludeParameterCompletions(
		t,
		NewTwigIncludeParameterCompletionProvider(twigIndex, phpIndex),
		`{{ include('card.html.twig', {pro`,
	)
	for _, item := range items {
		if item.Label == "product" {
			require.Equal(t, "App\\Product", item.Detail)
			return
		}
	}
	t.Fatal("product completion not found")
}

func TestTwigIncludeParameterCompletionUsesUnsavedTargetOverlay(
	t *testing.T,
) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	path := filepath.Join(root, "templates", "recursive.html.twig")
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(`{{ stale }}`),
	)))
	source := `{{ fresh }}
{{ include('recursive.html.twig', {fr`
	document := lsp.NewTextDocument(
		"file://"+path,
		source,
		1,
	)
	offset := uint32(len(source))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewTwigIncludeParameterCompletionProvider(
		index,
		nil,
	).GetCompletions(context.Background(), &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				offset - 1,
			),
		},
	})
	requireCompletion(t, items, "fresh")
	for _, item := range items {
		require.NotEqual(t, "stale", item.Label)
	}
}

func twigIncludeParameterCompletions(
	t *testing.T,
	provider *TwigIncludeParameterCompletionProvider,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(len(source))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(nodeOffset)
	require.NotNil(t, node, strings.TrimSpace(source))
	return provider.GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}
