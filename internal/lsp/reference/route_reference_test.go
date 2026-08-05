package reference

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteReferencesConnectNamesAndConcreteTwigHTMLURLs(t *testing.T) {
	cache := t.TempDir()
	routes, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	usages, err := symfony.NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, usages.Close()) })
	routePath := filepath.Join("/project", "config", "routes.yaml")
	firstPath := filepath.Join("/project", "templates", "first.twig")
	secondPath := filepath.Join("/project", "templates", "second.twig")
	thirdPath := filepath.Join("/project", "templates", "third.twig")
	fourthPath := filepath.Join("/project", "templates", "fourth.twig")
	routeSource := "product.show:\n    path: /products/{id}\n"
	firstSource := `{{ path('product.show', {'id': product.id}) }}`
	secondSource := `<a href="/products/42">Product</a>`
	thirdSource := `<a href="https://shop.example/products/foo.bar?preview=1#details">Product</a>`
	fourthSource := `{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}`
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		routePath,
		[]byte(routeSource),
	)))
	for path, source := range map[string]string{
		firstPath:  firstSource,
		secondPath: secondSource,
		thirdPath:  thirdSource,
		fourthPath: fourthSource,
	} {
		require.NoError(t, usages.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(secondPath),
		secondSource,
		1,
	)
	offset := uint32(strings.Index(secondSource, "/products/42") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewRouteReferenceProvider(
		routes,
		usages,
	).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      node,
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 5)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(routePath),
		uriutil.FileURI(firstPath),
		uriutil.FileURI(secondPath),
		uriutil.FileURI(thirdPath),
		uriutil.FileURI(fourthPath),
	}, []string{
		locations[0].URI,
		locations[1].URI,
		locations[2].URI,
		locations[3].URI,
		locations[4].URI,
	})
}

func TestRouteReferencesPreserveRepeatedSameFileOccurrences(t *testing.T) {
	cache := t.TempDir()
	routes, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	usages, err := symfony.NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, usages.Close()) })

	routePath := filepath.Join("/project", "config", "routes.yaml")
	templatePath := filepath.Join("/project", "templates", "repeated.twig")
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		routePath,
		[]byte("product.show:\n    path: /products/{id}\n"),
	)))
	require.NoError(t, usages.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("{{ path('product.show') }}\n{{ path('product.show') }}"),
	)))

	indexedRoutes, err := routes.GetRoute("product.show")
	require.NoError(t, err)
	locations, err := NewRouteReferenceProvider(
		routes,
		usages,
	).referencesForRoutes(indexedRoutes, false)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	assert.Equal(t, uriutil.FileURI(templatePath), locations[0].URI)
	assert.Equal(t, uriutil.FileURI(templatePath), locations[1].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
	assert.Equal(t, 1, locations[1].Range.Start.Line)
}
