package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteResourceCodeLenses(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	controllerDir := filepath.Join(root, "src", "Controller")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.MkdirAll(controllerDir, 0o755))
	routeFile := filepath.Join(configDir, "catalog.yaml")
	require.NoError(t, os.WriteFile(routeFile, []byte("catalog: {}\n"), 0o644))
	var controllers []string
	for _, name := range []string{
		"CatalogController.php",
		"ProductController.php",
	} {
		path := filepath.Join(controllerDir, name)
		require.NoError(t, os.WriteFile(path, []byte("<?php"), 0o644))
		controllers = append(controllers, path)
	}

	provider := NewRouteResourceCodeLensProvider(nil)
	for _, fixture := range []struct {
		name     string
		path     string
		source   string
		title    string
		line     int
		expected []string
	}{
		{
			name:     "YAML file import",
			path:     filepath.Join(configDir, "routes.yaml"),
			source:   "catalog:\n  resource: catalog.yaml\n",
			title:    "Open routing resource",
			line:     1,
			expected: []string{routeFile},
		},
		{
			name: "nested YAML directory",
			path: filepath.Join(configDir, "routes.yaml"),
			source: `controllers:
  resource:
    path: ../src/Controller/
    namespace: App\Controller
  type: attribute
`,
			title:    "Open 2 matching route files",
			line:     2,
			expected: controllers,
		},
		{
			name:     "XML directory",
			path:     filepath.Join(configDir, "routes.xml"),
			source:   `<routes><import resource="../src/Controller/" type="attribute"/></routes>`,
			title:    "Open 2 matching route files",
			line:     0,
			expected: controllers,
		},
		{
			name: "PHP configurator directory",
			path: filepath.Join(configDir, "routes.php"),
			source: `<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('../src/Controller/', 'attribute');
};`,
			title:    "Open 2 matching route files",
			line:     3,
			expected: controllers,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			lenses := relatedCodeLensesFor(
				t,
				provider,
				fixture.path,
				fixture.source,
			)
			require.Len(t, lenses, 1)
			lens := relatedLensByTitle(t, lenses, fixture.title)
			assert.Equal(t, fixture.line, lens.Range.Start.Line)
			var expected []string
			for _, path := range fixture.expected {
				expected = append(expected, relatedTarget(path, 1))
			}
			assert.ElementsMatch(t, expected, relatedLensTargets(t, lens))
		})
	}
}

func TestRouteResourceCodeLensIgnoresServiceResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.yaml")
	lenses := relatedCodeLensesFor(
		t,
		NewRouteResourceCodeLensProvider(nil),
		path,
		"services:\n  App\\:\n    resource: ../src/\n",
	)
	assert.Empty(t, lenses)
}

func TestRouteResourceCodeLensLinksImportedFileBackToImports(
	t *testing.T,
) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"CatalogController.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte("<?php final class CatalogController {}"),
		0o644,
	))

	index, err := symfony.NewRouteIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	var expected []string
	for _, name := range []string{"routes.yaml", "admin_routes.xml"} {
		configPath := filepath.Join(configDir, name)
		source := ""
		switch filepath.Ext(name) {
		case ".yaml":
			source = "controllers:\n  resource: ../src/Controller/\n  type: attribute\n"
		case ".xml":
			source = `<routes>
  <import resource="../src/Controller/" type="attribute"/>
</routes>`
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
		require.NoError(t, os.WriteFile(configPath, []byte(source), 0o644))
		require.NoError(t, index.Index(indexer.NewParsedFile(
			configPath,
			[]byte(source),
		)))
		expected = append(expected, relatedTarget(configPath, 2))
	}

	lenses := relatedCodeLensesFor(
		t,
		NewRouteResourceCodeLensProvider(index),
		controllerPath,
		"<?php final class CatalogController {}",
	)
	require.Len(t, lenses, 1)
	lens := relatedLensByTitle(t, lenses, "Open 2 routing imports")
	assert.Equal(t, 0, lens.Range.Start.Line)
	assert.ElementsMatch(t, expected, relatedLensTargets(t, lens))
}

func TestRouteResourceCodeLensSupportsLegacyBundleResources(
	t *testing.T,
) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".php-cache"))
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
	controllerPath := filepath.Join(
		bundleRoot,
		"Controller",
		"CatalogController.php",
	)
	controllerSource := "<?php final class CatalogController {}"
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))

	routeIndex, err := symfony.NewRouteIndexer(
		filepath.Join(root, ".route-cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	configPath := filepath.Join(root, "config", "routes.yaml")
	configSource := "controllers:\n  resource: '@FooBundle/Controller/'\n  type: attribute\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(
		configPath,
		[]byte(configSource),
		0o644,
	))
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(configSource),
	)))
	provider := NewRouteResourceCodeLensProvider(routeIndex, phpIndex)

	forward := relatedCodeLensesFor(
		t,
		provider,
		configPath,
		configSource,
	)
	require.Len(t, forward, 1)
	assert.Equal(t, []string{
		relatedTarget(controllerPath, 1),
	}, relatedLensTargets(t, relatedLensByTitle(
		t,
		forward,
		"Open routing resource",
	)))

	reverse := relatedCodeLensesFor(
		t,
		provider,
		controllerPath,
		controllerSource,
	)
	require.Len(t, reverse, 1)
	assert.Equal(t, []string{
		relatedTarget(configPath, 2),
	}, relatedLensTargets(t, relatedLensByTitle(
		t,
		reverse,
		"Open routing import",
	)))
}
