package symfony

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteResourceReferenceAt(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		path     string
		source   string
		marker   string
		expected RouteResourceReference
		notFound bool
	}{
		{
			name:   "scalar YAML attribute resource",
			path:   "/project/config/routes.yaml",
			source: "controllers:\n  resource: ../src/Controller/\n  type: attribute\n",
			marker: "../src",
			expected: RouteResourceReference{
				Path:   "../src/Controller/",
				Loader: "attribute",
			},
		},
		{
			name: "nested YAML resource",
			path: "/project/config/routes.yaml",
			source: `controllers:
  resource:
    path: ../src/Controller/
    namespace: App\Controller
  type: attribute
`,
			marker: "../src",
			expected: RouteResourceReference{
				Path:      "../src/Controller/",
				Loader:    "attribute",
				Namespace: `App\Controller`,
				Nested:    true,
			},
		},
		{
			name:   "YAML route file import",
			path:   "/project/config/routes.yaml",
			source: "catalog:\n  resource: routes/catalog.yaml\n",
			marker: "catalog.yaml",
			expected: RouteResourceReference{
				Path: "routes/catalog.yaml",
			},
		},
		{
			name:     "YAML service prototype",
			path:     "/project/config/services.yaml",
			source:   "services:\n  App\\:\n    resource: ../src/\n",
			marker:   "../src",
			notFound: true,
		},
		{
			name:   "XML attribute resource",
			path:   "/project/config/routes.xml",
			source: `<routes><import resource="../src/Controller/" type="attribute"/></routes>`,
			marker: "../src",
			expected: RouteResourceReference{
				Path:   "../src/Controller/",
				Loader: "attribute",
			},
		},
		{
			name: "PHP configurator resource",
			path: "/project/config/routes.php",
			source: `<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('../src/Controller/', 'attribute');
};`,
			marker: "../src",
			expected: RouteResourceReference{
				Path:   "../src/Controller/",
				Loader: "attribute",
			},
		},
		{
			name: "unrelated PHP import method",
			path: "/project/config/routes.php",
			source: `<?php
return static function (object $loader): void {
    $loader->import('../src/Controller/', 'attribute');
};`,
			marker:   "../src",
			notFound: true,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file := indexer.NewParsedFile(
				fixture.path,
				[]byte(fixture.source),
			)
			offset := strings.Index(fixture.source, fixture.marker) + 2
			require.GreaterOrEqual(t, offset, 2)
			reference, found := RouteResourceReferenceAt(
				file.SyntaxTree().Root.NodeAtOffset(uint32(offset)),
			)
			if fixture.notFound {
				assert.False(t, found)
				return
			}
			require.True(t, found)
			assert.Equal(t, fixture.expected.Path, reference.Path)
			assert.Equal(t, fixture.expected.Loader, reference.Loader)
			assert.Equal(t, fixture.expected.Namespace, reference.Namespace)
			assert.Equal(t, fixture.expected.Nested, reference.Nested)
			assert.Equal(
				t,
				fixture.expected.Path,
				fixture.source[reference.Range.Start:reference.Range.End],
			)
		})
	}
}

func TestRouteResourceFilesResolveDirectoriesAndRecursiveGlobs(
	t *testing.T,
) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "routes.yaml")
	controllerDir := filepath.Join(root, "src")
	for path, content := range map[string]string{
		filepath.Join(controllerDir, "Controller", "CatalogController.php"): "<?php",
		filepath.Join(controllerDir, "Api", "AdminController.php"):          "<?php",
		filepath.Join(controllerDir, "Controller", "Helper.php"):            "<?php",
		filepath.Join(controllerDir, "Controller", "notes.txt"):             "text",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	directoryFiles := RouteResourceFiles(
		configPath,
		RouteResourceReference{
			Path:   "../src/",
			Loader: "attribute",
		},
	)
	require.Len(t, directoryFiles, 3)
	assert.Equal(t, filepath.Join(
		controllerDir,
		"Api",
		"AdminController.php",
	), directoryFiles[0])

	globFiles := RouteResourceFiles(
		configPath,
		RouteResourceReference{
			Path:   "../src/{Controller,Api}/**/*Controller.php",
			Loader: "attribute",
		},
	)
	assert.Equal(t, []string{
		filepath.Join(
			controllerDir,
			"Api",
			"AdminController.php",
		),
		filepath.Join(
			controllerDir,
			"Controller",
			"CatalogController.php",
		),
	}, globFiles)
}

func TestRouteIndexerPersistsAndReplacesResourceImports(t *testing.T) {
	cache := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "routes.yaml")
	source := "controllers:\n  resource: ../src/Controller/\n  type: attribute\n"

	index, err := NewRouteIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(source),
	)))
	imports, err := index.GetRouteResourceImports()
	require.NoError(t, err)
	require.Len(t, imports, 1)
	assert.Equal(t, "../src/Controller/", imports[0].Path)
	assert.Equal(t, configPath, imports[0].FilePath)
	require.NoError(t, index.Close())

	restored, err := NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	imports, err = restored.GetRouteResourceImports()
	require.NoError(t, err)
	require.Len(t, imports, 1)
	assert.Equal(t, "attribute", imports[0].Loader)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		configPath,
		[]byte("catalog:\n  path: /catalog\n"),
	)))
	imports, err = restored.GetRouteResourceImports()
	require.NoError(t, err)
	assert.Empty(t, imports)
}

func TestRouteResourceMatchesWithoutDirectoryEnumeration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "routes.yaml")
	controller := filepath.Join(
		root,
		"src",
		"Api",
		"CatalogController.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(controller), 0o755))
	require.NoError(t, os.WriteFile(controller, []byte("<?php"), 0o644))

	assert.True(t, RouteResourceMatches(
		configPath,
		controller,
		RouteResourceReference{
			Path:   "../src/",
			Loader: "attribute",
		},
	))
	assert.True(t, RouteResourceMatches(
		configPath,
		controller,
		RouteResourceReference{
			Path:   "../src/**/Catalog*.php",
			Loader: "attribute",
		},
	))
	assert.False(t, RouteResourceMatches(
		configPath,
		filepath.Join(root, "other", "CatalogController.php"),
		RouteResourceReference{
			Path:   "../src/",
			Loader: "attribute",
		},
	))
}
