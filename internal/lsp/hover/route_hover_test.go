package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteHover(t *testing.T) {
	idx, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/routes.xml",
		[]byte(`<routes><route id="product.show" path="/products/{id}" controller="App\Controller\ProductController::show"/></routes>`),
	)))
	provider := NewRouteHoverProvider(idx)

	source := `{{ path('product.show') }}`
	document := lsp.NewTextDocument("file:///project/view.twig", source, 1)
	offset := uint32(strings.Index(source, "product.show") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	hover, err := provider.GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:  document,
			Language:  document.SyntaxLanguage,
			LineIndex: document.LineIndex,
			Root:      document.SyntaxTree.Root,
			Node:      node,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "**Symfony route** `product.show`")
	assert.Contains(t, hover.Contents.Value, "- Path: `/products/{id}`")
	assert.Contains(t, hover.Contents.Value, "- Parameters: `id`")
	assert.Contains(
		t,
		hover.Contents.Value,
		"- Controller: `App\\Controller\\ProductController::show`",
	)
	assert.NotNil(t, hover.Range)

	comparisonSource := `{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}`
	comparisonDocument := lsp.NewTextDocument(
		"file:///project/view.twig",
		comparisonSource,
		1,
	)
	comparisonOffset := uint32(
		strings.Index(comparisonSource, "product.show") + 2,
	)
	comparisonNode := comparisonDocument.SyntaxTree.Root.NodeAtOffset(
		comparisonOffset,
	)
	comparisonParams := &protocol.HoverParams{}
	comparisonParams.TextDocument.URI = comparisonDocument.URI
	comparisonHover, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: comparisonParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:  comparisonDocument,
				LineIndex: comparisonDocument.LineIndex,
				Root:      comparisonDocument.SyntaxTree.Root,
				Node:      comparisonNode,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, comparisonHover)
	assert.Contains(
		t,
		comparisonHover.Contents.Value,
		"**Symfony route** `product.show`",
	)

	htmlSource := `<a href="/products/42">Product</a>`
	htmlDocument := lsp.NewTextDocument(
		"file:///project/view.twig",
		htmlSource,
		1,
	)
	htmlOffset := uint32(strings.Index(htmlSource, "/products/42") + 2)
	htmlNode := htmlDocument.SyntaxTree.Root.NodeAtOffset(htmlOffset)
	htmlParams := &protocol.HoverParams{}
	htmlParams.TextDocument.URI = htmlDocument.URI
	htmlHover, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: htmlParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:  htmlDocument,
				LineIndex: htmlDocument.LineIndex,
				Root:      htmlDocument.SyntaxTree.Root,
				Node:      htmlNode,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, htmlHover)
	assert.Contains(
		t,
		htmlHover.Contents.Value,
		"**Symfony route URL** `/products/42`",
	)
	assert.Contains(t, htmlHover.Contents.Value, "- Name: `product.show`")
}
