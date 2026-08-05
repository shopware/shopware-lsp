package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteDefinitionNavigatesRoutingResources(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	controllerDir := filepath.Join(root, "src", "Controller")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.MkdirAll(controllerDir, 0o755))
	routeFile := filepath.Join(configDir, "catalog.yaml")
	controllerFile := filepath.Join(
		controllerDir,
		"CatalogController.php",
	)
	require.NoError(t, os.WriteFile(
		routeFile,
		[]byte("catalog: { path: /catalog }\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		controllerFile,
		[]byte("<?php final class CatalogController {}"),
		0o644,
	))

	for _, fixture := range []struct {
		name     string
		path     string
		source   string
		marker   string
		expected string
	}{
		{
			name:     "YAML route file",
			path:     filepath.Join(configDir, "routes.yaml"),
			source:   "catalog:\n  resource: catalog.yaml\n",
			marker:   "catalog.yaml",
			expected: routeFile,
		},
		{
			name:     "nested YAML attribute directory",
			path:     filepath.Join(configDir, "routes.yaml"),
			source:   "controllers:\n  resource:\n    path: ../src/Controller/\n    namespace: App\\Controller\n  type: attribute\n",
			marker:   "../src",
			expected: controllerFile,
		},
		{
			name:     "XML attribute directory",
			path:     filepath.Join(configDir, "routes.xml"),
			source:   `<routes><import resource="../src/Controller/" type="attribute"/></routes>`,
			marker:   "../src",
			expected: controllerFile,
		},
		{
			name: "PHP configurator directory",
			path: filepath.Join(configDir, "routes.php"),
			source: `<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('../src/Controller/', 'attribute');
};`,
			marker:   "../src",
			expected: controllerFile,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(fixture.path),
				fixture.source,
				1,
			)
			offset := uint32(strings.Index(fixture.source, fixture.marker) + 2)
			locations := NewRouteDefinitionProvider(nil).GetDefinition(
				context.Background(),
				securityDefinitionRequest(
					document,
					document.SyntaxTree.Root.NodeAtOffset(offset),
					offset,
				),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(fixture.expected), locations[0].URI)
		})
	}
}

func TestRouteDefinitionNavigatesLegacyBundleResource(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	interfacePath := filepath.Join(root, "BundleInterface.php")
	interfaceSource := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
`
	bundleRoot := filepath.Join(root, "vendor", "acme", "foo-bundle")
	bundlePath := filepath.Join(bundleRoot, "FooBundle.php")
	bundleSource := `<?php
namespace Acme\Foo;
final class FooBundle implements \Symfony\Component\HttpKernel\Bundle\BundleInterface {}
`
	for path, source := range map[string]string{
		interfacePath: interfaceSource,
		bundlePath:    bundleSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	targetPath := filepath.Join(
		bundleRoot,
		"Resources",
		"config",
		"routes.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o755))
	require.NoError(t, os.WriteFile(
		targetPath,
		[]byte("<routes/>"),
		0o644,
	))
	source := `<routes><import resource="@FooBundle/Resources/config/routes.xml"/></routes>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "routes.xml")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "@FooBundle") + 3)
	locations := NewRouteDefinitionProvider(nil, phpIndex).GetDefinition(
		context.Background(),
		securityDefinitionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(offset),
			offset,
		),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(targetPath), locations[0].URI)
}

func TestRouteDefinitionMatchesConcreteTwigHTMLURL(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("catalog.show:\n    path: /catalog/{id}\n"),
	)))
	source := `<a href="/catalog/42?preview=1">Catalog</a>`
	document := lsp.NewTextDocument("file:///project/view.twig", source, 1)
	offset := uint32(strings.Index(source, "/catalog/42") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
}

func TestRouteDefinitionMatchesAbsoluteTwigHTMLURL(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("catalog.show:\n    path: /catalog/{id}\n"),
	)))
	source := `<a href="https://shop.example/catalog/foo.bar?preview=1#details">Catalog</a>`
	document := lsp.NewTextDocument("file:///project/view.twig", source, 1)
	offset := uint32(strings.Index(source, "/catalog/foo.bar") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
}

func TestRouteDefinitionMatchesJavaScriptRequestURL(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("catalog.show:\n    path: /catalog/{id}\n"),
	)))
	source := `fetch('https://shop.example/catalog/foo.bar?preview=1#details')`
	document := lsp.NewTextDocument(
		"file:///project/request.js",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "/catalog/foo.bar") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
}

func TestRouteDefinitionFromTwigRouteComparison(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("catalog.show:\n    path: /catalog/{id}\n"),
	)))
	source := `{% if app.request.attributes.get('_route') == 'catalog.show' %}{% endif %}`
	document := lsp.NewTextDocument(
		"file:///project/view.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "catalog.show") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
}

func TestPHPDocRouteAssistantTagDefinition(t *testing.T) {
	cacheDir := t.TempDir()
	routeIndex, err := symfony.NewRouteIndexer(
		filepath.Join(cacheDir, "routes"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	routePath := filepath.Join(cacheDir, "routes.yaml")
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		routePath,
		[]byte("catalog.show:\n  path: /catalog/{id}\n"),
	)))
	phpIndex, err := php.NewPHPIndex(filepath.Join(cacheDir, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "RouteAware.php"),
		[]byte(`<?php
interface RouteAware
{
    /** @param string $route #Route */
    public function open(string $route): void;
}
class RouteConsumer implements RouteAware
{
    public function open(string $route): void {}
}
`),
	)))
	source := `<?php
$consumer = new RouteConsumer();
$consumer->open('catalog.show');
`
	document := lsp.NewTextDocument(
		"file:///project/src/Usage.php",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "catalog.show") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/src/Usage.php",
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewRouteDefinitionProvider(routeIndex).GetDefinition(
		ctx,
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
}

func TestRouteParameterDefinitionSupportsTwigIdentifierHashKeys(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("product.show:\n    path: /products/{slug}\n"),
	)))
	for _, test := range []struct {
		source string
		marker string
		found  bool
	}{
		{
			source: `{{ path('product.show', {'slug': product.slug}) }}`,
			marker: "slug':", found: true,
		},
		{
			source: `{{ path('product.show', {slug: product.slug}) }}`,
			marker: "slug:", found: true,
		},
		{
			source: `{{ path('product.show', {slug}) }}`,
			marker: "slug}", found: true,
		},
		{
			source: `{{ path('product.show', {missing: product.slug}) }}`,
			marker: "missing", found: false,
		},
		{
			source: `{{ path('product.show', {slug: product.value}) }}`,
			marker: "value", found: false,
		},
	} {
		t.Run(test.source+"/"+test.marker, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/view.twig",
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.marker) + 1)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			require.NotNil(t, node)
			locations := NewRouteDefinitionProvider(index).GetDefinition(
				context.Background(),
				securityDefinitionRequest(document, node, offset),
			)
			if !test.found {
				assert.Empty(t, locations)
				return
			}
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
			assert.Equal(t, 0, locations[0].Range.Start.Line)
		})
	}
}

func TestRouteParameterDefinitionSupportsPHPArrayKeys(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte("product.show:\n    path: /products/{slug}\n"),
	)))
	source := `<?php $this->redirectToRoute('product.show', ['slug' => $slug]);`
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "'slug'") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	require.NotNil(t, node)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)

	valueOffset := uint32(strings.LastIndex(source, "$slug") + 2)
	valueNode := document.SyntaxTree.Root.NodeAtOffset(valueOffset)
	require.NotNil(t, valueNode)
	assert.Empty(t, NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, valueNode, valueOffset),
	))
}

func TestRouteDefinitionNavigatesToRoutingConfiguratorRoute(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.php"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte(`<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;

return static function (RoutingConfigurator $routes): void {
    $routes->namePrefix('api_')->prefix('/api')
        ->add('product.show', '/products/{id}');
};`),
	)))
	source := `{{ path('api_product.show', {id: product.id}) }}`
	document := lsp.NewTextDocument(
		"file:///project/view.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "api_product.show") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewRouteDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(routePath), locations[0].URI)
	assert.Equal(t, 5, locations[0].Range.Start.Line)
}

func TestRouteDefinitionNavigatesPHPAttributePathPrefix(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	routePath := "/project/config/routes.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		routePath,
		[]byte(`catalog.root:
    path: /catalog/products
catalog.show:
    path: /catalog/products/{id}
catalog.other:
    path: /catalog/reviews
catalog.current:
    path: /catalog/products/detail
`),
	)))
	for _, source := range []string{
		`<?php #[Route('/catalog/products/detail')] function show() {}`,
		`<?php #[Route(name: 'current', path: '/catalog/products/detail')] function show() {}`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/src/Controller.php",
			source,
			1,
		)
		offset := uint32(
			strings.Index(source, "/catalog/products/detail") +
				len("/catalog/prod"),
		)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := NewRouteDefinitionProvider(index).GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.Len(t, locations, 2)
		for _, location := range locations {
			assert.Equal(t, uriutil.FileURI(routePath), location.URI)
		}
		assert.Equal(t, 0, locations[0].Range.Start.Line)
		assert.Equal(t, 2, locations[1].Range.Start.Line)
	}
}

func TestRouteDefinitionIgnoresFinalAndNonPathAttributeStrings(t *testing.T) {
	index, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte("catalog.root:\n    path: /catalog/products\n"),
	)))
	for _, source := range []string{
		`<?php #[Route('/catalog/products')] function show() {}`,
		`<?php #[Route(name: '/catalog/products')] function show() {}`,
		`<?php #[Cache('/catalog/products/detail')] function show() {}`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/src/Controller.php",
			source,
			1,
		)
		offset := uint32(strings.LastIndex(source, "products") + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		assert.Empty(t, NewRouteDefinitionProvider(index).GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		))
	}
}
