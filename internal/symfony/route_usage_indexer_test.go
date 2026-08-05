package symfony

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteUsageIndexerPersistsStaticTwigHTMLURLs(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join("/project", "templates", "catalog.twig")
	source := `<a href="/catalog/42?preview=1">Catalog</a>
<form action="/catalog/42"></form>
<a href="{{ path('catalog.show') }}">Dynamic</a>
<a href="https://example.test/catalog/42">External</a>`
	idx, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	htmlUsages, err := idx.GetHTMLRouteUsages()
	require.NoError(t, err)
	require.Len(t, htmlUsages, 3)
	assert.Equal(t, []int{1, 2, 4}, []int{
		htmlUsages[0].Line,
		htmlUsages[1].Line,
		htmlUsages[2].Line,
	})
	for _, usage := range htmlUsages {
		assert.Equal(t, "/catalog/42", usage.Path)
	}
	require.NoError(t, idx.Close())

	restored, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredUsages, err := restored.GetHTMLRouteUsages()
	require.NoError(t, err)
	assert.Equal(t, htmlUsages, restoredUsages)
}

func TestRouteUsageIndexerPersistsTwigRouteComparisons(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join("/project", "templates", "navigation.twig")
	source := `{% if app.request.attributes.get('_route') == 'catalog.show' %}{% endif %}
{% if app.request.attributes.get('_route') in ['catalog.list'] %}{% endif %}
{% if app.request.attributes.get('_route') starts with 'catalog.' %}{% endif %}`
	idx, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	showUsages, err := idx.GetRoute("catalog.show")
	require.NoError(t, err)
	require.Len(t, showUsages, 1)
	assert.Equal(t, path, showUsages[0].File)
	assert.Equal(t, 1, showUsages[0].Line)
	listUsages, err := idx.GetRoute("catalog.list")
	require.NoError(t, err)
	require.Len(t, listUsages, 1)
	assert.Equal(t, 2, listUsages[0].Line)
	prefixUsages, err := idx.GetRoute("catalog.")
	require.NoError(t, err)
	assert.Empty(t, prefixUsages)
	require.NoError(t, idx.Close())

	restored, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredShow, err := restored.GetRoute("catalog.show")
	require.NoError(t, err)
	assert.Equal(t, showUsages, restoredShow)
	restoredList, err := restored.GetRoute("catalog.list")
	require.NoError(t, err)
	assert.Equal(t, listUsages, restoredList)
}

func TestRouteUsageIndexerPreservesRepeatedOccurrencesAndCacheRestore(
	t *testing.T,
) {
	cache := t.TempDir()
	path := filepath.Join("/project", "templates", "repeated.twig")
	source := `{{ path('catalog.show') }}
{{ path('catalog.show') }}
<a href="/catalog/42">First</a>
<a href="/catalog/42">Second</a>`
	idx, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	usages, err := idx.GetRoute("catalog.show")
	require.NoError(t, err)
	require.Len(t, usages, 2)
	assert.Equal(t, []int{1, 2}, []int{usages[0].Line, usages[1].Line})
	html, err := idx.GetHTMLRouteUsages()
	require.NoError(t, err)
	require.Len(t, html, 2)
	assert.Equal(t, []int{3, 4}, []int{html[0].Line, html[1].Line})
	require.NoError(t, idx.Close())

	restored, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredUsages, err := restored.GetRoute("catalog.show")
	require.NoError(t, err)
	assert.Equal(t, usages, restoredUsages)
	restoredHTML, err := restored.GetHTMLRouteUsages()
	require.NoError(t, err)
	assert.Equal(t, html, restoredHTML)
}

func TestRouteUsageIndexerPersistsEveryTwigControllerOccurrence(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join("/project", "templates", "navigation.twig")
	source := `{{ controller('App\\Controller\\NavController::menu') }}
{{ render(controller('\\App\\Controller\\NavController::menu')) }}
{{ controller('app.navigation:menu') }}`
	idx, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	reference, ok := ParseControllerReference(
		`App\Controller\NavController::menu`,
	)
	require.True(t, ok)
	usages, err := idx.GetControllerUsages(reference)
	require.NoError(t, err)
	require.Len(t, usages, 2)
	assert.Equal(t, path, usages[0].File)
	assert.Equal(t, `App\Controller\NavController::menu`, usages[0].Controller)
	assert.NotEqual(t, usages[0].Range, usages[1].Range)
	require.NoError(t, idx.Close())

	restored, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredUsages, err := restored.GetControllerUsages(reference)
	require.NoError(t, err)
	assert.Equal(t, usages, restoredUsages)
	all, err := restored.GetAllControllerUsages()
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestRouteUsageIndexerUsesTypedPHPRouteGenerators(t *testing.T) {
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/AbstractController.php": `<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function redirectToRoute(string $route): mixed {}
}
`,
		"/project/vendor/UrlGeneratorInterface.php": `<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface {
    public function generate(string $name): string;
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	idx, err := NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)
	path := "/project/src/Controller.php"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php
namespace App;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\Routing\Generator\UrlGeneratorInterface;
final class Other { public function generate(string $name): string {} }
final class Controller extends AbstractController {
    public function run(UrlGeneratorInterface $router, Other $other): void {
        $this->redirectToRoute('controller.route');
        $router->generate('router.route');
        $other->generate('unrelated.route');
    }
}
`),
	)))
	controller, err := idx.GetRoute("controller.route")
	require.NoError(t, err)
	require.Len(t, controller, 1)
	router, err := idx.GetRoute("router.route")
	require.NoError(t, err)
	require.Len(t, router, 1)
	unrelated, err := idx.GetRoute("unrelated.route")
	require.NoError(t, err)
	assert.Empty(t, unrelated)
}

func TestRouteUsageSourceGateIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	require.True(t, containsFoldASCIIBytes(
		[]byte("$this->ReDiReCtToRoUtE('example');"),
		"redirecttoroute",
	))
	require.True(t, containsFoldASCIIBytes(
		[]byte("$router->GeNeRaTe('example');"),
		"generate",
	))
	require.False(t, containsFoldASCIIBytes(
		[]byte("$service->translate('example');"),
		"generate",
	))
}
