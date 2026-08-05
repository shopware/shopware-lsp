package completion

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigSeeCompletesPHPClassesMethodsAndTemplates(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "ProductController.php"),
		[]byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates", "product", "show.html.twig"),
		[]byte("product"),
	)))
	provider := NewTwigCompletionProvider(root, twigIndex, nil, phpIndex)

	classItems := twigTypesCompletionItems(
		t,
		provider,
		`{# @see App\Controller\Prod<caret> #}`,
	)
	classItem := requireCompletion(
		t,
		classItems,
		"App\\Controller\\ProductController",
	)
	assert.Equal(t, int(protocol.ClassCompletion), classItem.Kind)

	methodItems := twigTypesCompletionItems(
		t,
		provider,
		`{# @see App\Controller\ProductController::sh<caret> #}`,
	)
	method := requireCompletion(t, methodItems, "show")
	assert.Equal(t, int(protocol.MethodCompletion), method.Kind)

	templateItems := twigTypesCompletionItems(
		t,
		provider,
		`{# @see product/sh<caret> #}`,
	)
	template := requireCompletion(
		t,
		templateItems,
		"product/show.html.twig",
	)
	assert.Equal(t, int(protocol.FileCompletion), template.Kind)
}
