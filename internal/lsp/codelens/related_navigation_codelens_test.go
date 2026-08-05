package codelens

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestRelatedNavigationCodeLensesLinkControllerRouteAndTemplate(
	t *testing.T,
) {
	root := t.TempDir()
	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"ProductController.php",
	)
	templatePath := filepath.Join(
		root,
		"templates",
		"product",
		"show.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	controllerSource := `<?php
namespace App\Controller;

use Symfony\Component\Routing\Attribute\Route;

final class ProductController
{
    #[Route('/products/{id}', name: 'product.show')]
    public function show(): void
    {
        $this->render('product/show.html.twig');
    }
}
`
	templateSource := "{% block content %}Product{% endblock %}\n"
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(templateSource),
		0o644,
	))

	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	routeIndex, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })

	controllerFile := indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)
	require.NoError(t, phpIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(controllerFile))
	require.NoError(t, routeIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(templateSource),
	)))

	provider := NewRelatedNavigationCodeLensProvider(
		twigIndex,
		phpIndex,
		routeIndex,
		nil,
	)
	controllerLenses := relatedCodeLensesFor(
		t,
		provider,
		controllerPath,
		controllerSource,
	)
	require.Len(t, controllerLenses, 2)
	assert.ElementsMatch(t, []string{
		"Open related template",
		"Open route definition",
	}, relatedLensTitles(controllerLenses))
	assert.Equal(t, []string{
		relatedTarget(templatePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, controllerLenses, "Open related template"),
	))
	assert.Equal(t, []string{
		relatedTarget(controllerPath, 8),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, controllerLenses, "Open route definition"),
	))

	templateLenses := relatedCodeLensesFor(
		t,
		provider,
		templatePath,
		templateSource,
	)
	require.Len(t, templateLenses, 2)
	assert.Equal(t, []string{
		"Open rendering PHP location",
		"Open related route",
	}, relatedLensTitles(templateLenses))
	assert.Equal(t, []string{
		relatedTarget(controllerPath, 11),
	}, relatedLensTargets(t, templateLenses[0]))
	assert.Equal(t, []string{
		relatedTarget(controllerPath, 8),
	}, relatedLensTargets(t, templateLenses[1]))
}

func TestRelatedNavigationCodeLensesResolveServiceControllerRoutes(
	t *testing.T,
) {
	root := t.TempDir()
	controllerPath := filepath.Join(root, "src", "ProductController.php")
	templatePath := filepath.Join(
		root,
		"templates",
		"product",
		"show.html.twig",
	)
	servicePath := filepath.Join(root, "config", "services.yaml")
	routePath := filepath.Join(root, "config", "routes.yaml")
	for _, path := range []string{
		controllerPath,
		templatePath,
		servicePath,
		routePath,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	controllerSource := `<?php
namespace App\Controller;

final class ProductController
{
    public function show(): void
    {
        $this->render('product/show.html.twig');
    }
}
`
	templateSource := "Product\n"
	serviceSource := `services:
  app.product_controller:
    class: App\Controller\ProductController
`
	routeSource := `product.show:
  path: /products/{id}
  controller: app.product_controller:show
`
	for path, source := range map[string]string{
		controllerPath: controllerSource,
		templatePath:   templateSource,
		servicePath:    serviceSource,
		routePath:      routeSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	routeIndex, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })

	controllerFile := indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)
	require.NoError(t, phpIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(templateSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		routePath,
		[]byte(routeSource),
	)))

	provider := NewRelatedNavigationCodeLensProvider(
		twigIndex,
		phpIndex,
		routeIndex,
		serviceIndex,
	)
	controllerLenses := relatedCodeLensesFor(
		t,
		provider,
		controllerPath,
		controllerSource,
	)
	assert.Equal(t, []string{
		relatedTarget(routePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, controllerLenses, "Open route definition"),
	))
	templateLenses := relatedCodeLensesFor(
		t,
		provider,
		templatePath,
		templateSource,
	)
	assert.Equal(t, []string{
		relatedTarget(routePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, templateLenses, "Open related route"),
	))
}

func relatedCodeLensesFor(
	t *testing.T,
	provider lsp.CodeLensProvider,
	path,
	source string,
) []protocol.CodeLens {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	params := &protocol.CodeLensParams{}
	params.TextDocument.URI = document.URI
	lenses, err := provider.GetCodeLenses(
		context.Background(),
		&lsp.CodeLensRequest{
			CodeLensParams: params,
			Document:       document,
		},
	)
	require.NoError(t, err)
	return lenses
}

func relatedLensTitles(lenses []protocol.CodeLens) []string {
	titles := make([]string, 0, len(lenses))
	for _, lens := range lenses {
		titles = append(titles, lens.Command.Title)
	}
	return titles
}

func relatedLensTargets(
	t *testing.T,
	lens protocol.CodeLens,
) []string {
	t.Helper()
	require.NotNil(t, lens.Command)
	require.Equal(t, "shopware.openReferences", lens.Command.Command)
	require.Len(t, lens.Command.Arguments, 1)
	targets, ok := lens.Command.Arguments[0].([]string)
	require.True(t, ok)
	return targets
}

func relatedLensByTitle(
	t *testing.T,
	lenses []protocol.CodeLens,
	title string,
) protocol.CodeLens {
	t.Helper()
	for _, lens := range lenses {
		if lens.Command != nil && lens.Command.Title == title {
			return lens
		}
	}
	require.FailNow(t, "code lens not found", "title: %s", title)
	return protocol.CodeLens{}
}
