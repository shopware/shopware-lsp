package inlay

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestTwigVariableInlayHintSummarizesControllerContext(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(`<?php
namespace App;
class Product {}
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(`<?php
namespace App;
class ProductController
{
    public function show(Product $product): mixed
    {
        return $this->render('product/show.html.twig', [
            'product' => $product,
            'count' => 1,
        ]);
    }
}
`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/product/show.html.twig",
		"{{ product }}",
		1,
	)
	hints, err := NewTwigVariableProvider(phpIndex).GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(t, []string{"Variables (2)"}, inlayHintLabels(hints))
	assert.Contains(t, hints[0].Tooltip, "count: 1")
	assert.Contains(t, hints[0].Tooltip, "product: App\\Product")
	assert.True(t, hints[0].PaddingRight)
	parts, ok := hints[0].Label.([]protocol.InlayHintLabelPart)
	require.True(t, ok)
	require.Len(t, parts, 1)
	assert.Equal(
		t,
		"Browse typed Twig variables and insert expressions",
		parts[0].Tooltip,
	)
	require.NotNil(t, parts[0].Command)
	assert.Equal(t, BrowseTwigVariablesCommand, parts[0].Command.Command)
	assert.Equal(t, "Browse Twig variables", parts[0].Command.Title)
	assert.Equal(
		t,
		[]interface{}{
			"file:///project/templates/product/show.html.twig",
			[]string{"count", "product"},
		},
		parts[0].Command.Arguments,
	)
}

func TestTwigVariableInlayHintOnlyAppearsAtDocumentTop(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Controller.php",
		[]byte(`<?php
function page() {
    return render('page.html.twig', ['value' => 1]);
}
`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		"\n{{ value }}",
		1,
	)
	request := inlayHintRequest(document)
	request.Range.Start.Line = 1
	hints, err := NewTwigVariableProvider(phpIndex).GetInlayHints(
		context.Background(),
		request,
	)
	require.NoError(t, err)
	assert.Empty(t, hints)
}
