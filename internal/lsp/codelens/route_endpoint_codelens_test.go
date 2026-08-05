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

func TestRouteEndpointCodeLensesCoverPHPYAMLAndXML(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)

	controllerPath := filepath.Join(root, "src", "CatalogController.php")
	controllerSource := `<?php
namespace App\Controller;
final class CatalogController {
    public function show(): void {}
    public function create(): void {}
    public function __invoke(): void {}
}
`
	servicePath := filepath.Join(root, "config", "services.yaml")
	serviceSource := `services:
  app.catalog_controller:
    class: App\Controller\CatalogController
`
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(servicePath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		servicePath,
		[]byte(serviceSource),
		0o644,
	))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))

	provider := NewRouteEndpointCodeLensProvider(serviceIndex, phpIndex)
	yamlPath := filepath.Join(root, "config", "routes.yaml")
	yamlSource := `catalog.show:
  path: /catalog/{id}
  methods: [GET]
  controller: App\Controller\CatalogController::show
catalog.create:
  path: /catalog
  methods: GET|POST
  controller: app.catalog_controller:create
catalog.invoke:
  path: /catalog/invoke
  controller: App\Controller\CatalogController
catalog.no_controller:
  path: /catalog/orphan
_internal:
  path: /_internal
  controller: App\Controller\CatalogController::show
`
	yamlLenses := relatedCodeLensesFor(
		t,
		provider,
		yamlPath,
		yamlSource,
	)
	require.Len(t, yamlLenses, 3)
	assert.Equal(t, []string{
		"GET /catalog/{id} · catalog.show",
		"GET|POST /catalog · catalog.create",
		"ANY /catalog/invoke · catalog.invoke",
	}, relatedLensTitles(yamlLenses))
	assert.Equal(t, 0, yamlLenses[0].Range.Start.Line)
	assert.Equal(t, 4, yamlLenses[1].Range.Start.Line)
	assert.Equal(t, 8, yamlLenses[2].Range.Start.Line)
	assert.Equal(
		t,
		[]string{relatedTarget(controllerPath, 4)},
		relatedLensTargets(t, yamlLenses[0]),
	)
	assert.Equal(
		t,
		[]string{relatedTarget(controllerPath, 5)},
		relatedLensTargets(t, yamlLenses[1]),
	)
	assert.Equal(
		t,
		[]string{relatedTarget(controllerPath, 6)},
		relatedLensTargets(t, yamlLenses[2]),
	)

	xmlPath := filepath.Join(root, "config", "routes.xml")
	xmlSource := `<routes>
  <route id="catalog.xml" path="/catalog.xml"
         methods="PUT, PATCH"
         controller="app.catalog_controller:show"/>
  <route id="_internal" path="/_internal"
         controller="App\Controller\CatalogController::show"/>
</routes>`
	xmlLenses := relatedCodeLensesFor(
		t,
		provider,
		xmlPath,
		xmlSource,
	)
	require.Len(t, xmlLenses, 1)
	assert.Equal(
		t,
		"PUT|PATCH /catalog.xml · catalog.xml",
		xmlLenses[0].Command.Title,
	)
	assert.Equal(
		t,
		[]string{relatedTarget(controllerPath, 4)},
		relatedLensTargets(t, xmlLenses[0]),
	)

	phpPath := filepath.Join(root, "src", "EndpointController.php")
	phpSource := `<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request;
final class EndpointController {
    #[Route('/endpoint', name: 'endpoint.show', methods: [Request::METHOD_PATCH])]
    public function show(): void {}
}
`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	phpLenses := relatedCodeLensesFor(t, provider, phpPath, phpSource)
	require.Len(t, phpLenses, 1)
	assert.Equal(
		t,
		"PATCH /endpoint · endpoint.show",
		phpLenses[0].Command.Title,
	)
	assert.Equal(t, 5, phpLenses[0].Range.Start.Line)
	assert.Equal(
		t,
		[]string{relatedTarget(phpPath, 7)},
		relatedLensTargets(t, phpLenses[0]),
	)
}

func TestRouteEndpointCodeLensesUseUnsavedRouteMetadata(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	controllerPath := filepath.Join(root, "src", "CatalogController.php")
	controllerSource := `<?php
namespace App\Controller;
final class CatalogController {
    public function show(): void {}
}`
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)))

	provider := NewRouteEndpointCodeLensProvider(nil, phpIndex)
	lenses := relatedCodeLensesFor(
		t,
		provider,
		filepath.Join(root, "config", "routes.yaml"),
		`catalog.show:
  path: /unsaved
  methods: [HEAD]
  controller: App\Controller\CatalogController::show
`,
	)
	require.Len(t, lenses, 1)
	assert.Equal(
		t,
		"HEAD /unsaved · catalog.show",
		lenses[0].Command.Title,
	)
}
