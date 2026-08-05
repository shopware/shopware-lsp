package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXMLRoutes(t *testing.T) {
	source := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<routes xmlns="http://symfony.com/schema/routing">
    <route id="product.show" path="/products/{id&lt;\d+&gt;}">
        <default key="_controller">App\Controller\ProductController::show</default>
    </route>
    <route id="product.edit" path="/products/{id}/edit" methods="GET|POST" controller="App\Controller\ProductController::edit"/>
    <import resource="attributes.yaml" type="attribute"/>
</routes>`)

	routes, err := ParseXMLRoutes("/project/config/routes.xml", source)
	require.NoError(t, err)
	require.Len(t, routes, 2)
	assert.Equal(t, "product.show", routes[0].Name)
	assert.Equal(t, `/products/{id<\d+>}`, routes[0].Path)
	assert.Equal(t, "App\\Controller\\ProductController::show", routes[0].Controller)
	assert.Equal(t, []string{"id"}, routes[0].Parameters())
	assert.Equal(t, "product.edit", routes[1].Name)
	assert.Equal(t, []string{"GET", "POST"}, routes[1].Methods)
	assert.Positive(t, routes[0].Line)
}

func TestRouteIndexerIndexesXML(t *testing.T) {
	idx, err := NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/routes.xml",
		[]byte(`<routes><route id="catalog" path="/catalog/{page}"/></routes>`),
	)))
	routes, err := idx.GetRoute("catalog")
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, []string{"page"}, routes[0].Parameters())
}

func TestRouteParametersNormalizeInlineMetadata(t *testing.T) {
	route := Route{Path: `/orders/{id<\d+>}/{!page<\d+>?1}/{id}`}
	assert.Equal(t, []string{"id", "page"}, route.Parameters())
}
