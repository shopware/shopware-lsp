package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateUsageCatalogCorrelatesEveryUsageKind(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, routeIndex, serviceIndex, componentIndex :=
		newTemplateUsageIndexes(t, root, cache, phpIndex)

	targetPath := writeAnalyticsFixture(
		t,
		root,
		"templates/layout/base.html.twig",
		`{% block body %}{% endblock %}`,
	)
	indexTwigAnalyticsFixture(t, twigIndex, targetPath)

	referenceFixtures := map[string]string{
		"templates/page/include.html.twig": `{% include 'layout/base.html.twig' %}`,
		"templates/page/embed.html.twig":   `{% embed 'layout/base.html.twig' %}{% endembed %}`,
		"templates/page/extends.html.twig": `{% extends 'layout/base.html.twig' %}`,
		"templates/page/import.html.twig":  `{% import 'layout/base.html.twig' as layout %}`,
		"templates/page/use.html.twig":     `{% use 'layout/base.html.twig' %}`,
		"templates/page/form.html.twig":    `{% form_theme form 'layout/base.html.twig' %}`,
	}
	for relative, source := range referenceFixtures {
		path := writeAnalyticsFixture(t, root, relative, source)
		indexTwigAnalyticsFixture(t, twigIndex, path)
	}

	controllerPath := writeAnalyticsFixture(
		t,
		root,
		"src/Controller/PageController.php",
		`<?php
namespace App\Controller;

use Symfony\Component\Routing\Attribute\Route;

final class PageController
{
    #[Route('/page', name: 'app.page', methods: ['GET'])]
    public function index(): array
    {
        return $this->render('layout/base.html.twig');
    }
}
`,
	)
	controllerSource, err := os.ReadFile(controllerPath)
	require.NoError(t, err)
	controllerFile := indexer.NewParsedFile(controllerPath, controllerSource)
	require.NoError(t, phpIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(controllerFile))
	require.NoError(t, routeIndex.Index(controllerFile))

	componentPath := writeAnalyticsFixture(
		t,
		root,
		"src/Twig/LayoutComponent.php",
		`<?php
namespace App\Twig;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent(name: 'Layout', template: 'layout/base.html.twig')]
final class LayoutComponent {}
`,
	)
	componentSource, err := os.ReadFile(componentPath)
	require.NoError(t, err)
	componentFile := indexer.NewParsedFile(componentPath, componentSource)
	require.NoError(t, phpIndex.Index(componentFile))
	require.NoError(t, componentIndex.Index(componentFile))

	componentUsagePath := writeAnalyticsFixture(
		t,
		root,
		"templates/page/component.html.twig",
		`{{ component('Layout') }}`,
	)
	componentUsageSource, err := os.ReadFile(componentUsagePath)
	require.NoError(t, err)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		componentUsagePath,
		componentUsageSource,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		componentUsagePath,
		componentUsageSource,
	)))

	provider := NewTwigTemplateUsageCatalogProvider(
		root,
		twigIndex,
		phpIndex,
		routeIndex,
		serviceIndex,
		componentIndex,
	)
	request := TwigTemplateUsageCatalogRequest{
		Template: "templates/layout/base.html.twig",
		FileGlob: "templates/**/base.html.twig",
	}
	entries, err := provider.Catalog(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	assert.Equal(t, "layout/base.html.twig", entry.Template)
	assert.Equal(t, []TwigTemplateSourceLocation{{
		FileURI: uriutil.FileURI(targetPath),
		Line:    1,
	}}, entry.Files)
	require.Len(t, entry.Controllers, 1)
	assert.Equal(
		t,
		"App\\Controller\\PageController::index",
		entry.Controllers[0].Controller,
	)
	assert.Equal(t, uriutil.FileURI(controllerPath), entry.Controllers[0].FileURI)
	assert.Equal(t, 9, entry.Controllers[0].Line)
	require.Equal(t, []TwigTemplateRouteEntry{{
		Name:    "app.page",
		Path:    "/page",
		Methods: []string{"GET"},
	}}, entry.Controllers[0].Routes)
	assertTemplateUsageFile(
		t,
		entry.Includes,
		filepath.Join(root, "templates/page/include.html.twig"),
	)
	assertTemplateUsageFile(
		t,
		entry.Embeds,
		filepath.Join(root, "templates/page/embed.html.twig"),
	)
	assertTemplateUsageFile(
		t,
		entry.Extends,
		filepath.Join(root, "templates/page/extends.html.twig"),
	)
	assertTemplateUsageFile(
		t,
		entry.Imports,
		filepath.Join(root, "templates/page/import.html.twig"),
	)
	assertTemplateUsageFile(
		t,
		entry.Uses,
		filepath.Join(root, "templates/page/use.html.twig"),
	)
	assertTemplateUsageFile(
		t,
		entry.FormThemes,
		filepath.Join(root, "templates/page/form.html.twig"),
	)
	require.Len(t, entry.Components, 1)
	assert.Equal(t, "Layout", entry.Components[0].Component)
	assert.Equal(t, "component()", entry.Components[0].Syntax)
	assert.Equal(
		t,
		uriutil.FileURI(componentUsagePath),
		entry.Components[0].FileURI,
	)

	partial, err := provider.Catalog(
		context.Background(),
		TwigTemplateUsageCatalogRequest{Template: "BASE.HTML"},
	)
	require.NoError(t, err)
	require.Len(t, partial, 1)
	assert.Equal(t, entry, partial[0])

	_, err = provider.Catalog(
		context.Background(),
		TwigTemplateUsageCatalogRequest{},
	)
	assert.ErrorContains(t, err, "at least one")

	require.NoError(t, twigIndex.Close())
	require.NoError(t, routeIndex.Close())
	require.NoError(t, serviceIndex.Close())
	require.NoError(t, componentIndex.Close())

	restoredTwig, restoredRoutes, restoredServices, restoredComponents :=
		newTemplateUsageIndexes(t, root, cache, phpIndex)
	t.Cleanup(func() { require.NoError(t, restoredTwig.Close()) })
	t.Cleanup(func() { require.NoError(t, restoredRoutes.Close()) })
	t.Cleanup(func() { require.NoError(t, restoredServices.Close()) })
	t.Cleanup(func() { require.NoError(t, restoredComponents.Close()) })
	restoredEntries, err := NewTwigTemplateUsageCatalogProvider(
		root,
		restoredTwig,
		phpIndex,
		restoredRoutes,
		restoredServices,
		restoredComponents,
	).Catalog(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
}

func newTemplateUsageIndexes(
	t *testing.T,
	root,
	cache string,
	phpIndex *php.PHPIndex,
) (
	*twig.TwigIndexer,
	*symfony.RouteIndexer,
	*symfony.ServiceIndex,
	*twigcomponent.Index,
) {
	t.Helper()
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	routeIndex, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	serviceIndex.SetPHPIndex(phpIndex)
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	componentIndex.SetDependencies(phpIndex, serviceIndex, twigIndex)
	return twigIndex, routeIndex, serviceIndex, componentIndex
}

func writeAnalyticsFixture(
	t *testing.T,
	root,
	relative,
	source string,
) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	return path
}

func indexTwigAnalyticsFixture(
	t *testing.T,
	index *twig.TwigIndexer,
	path string,
) {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, index.Index(indexer.NewParsedFile(path, source)))
}

func assertTemplateUsageFile(
	t *testing.T,
	usages []TwigTemplateReferenceUsage,
	path string,
) {
	t.Helper()
	require.Len(t, usages, 1)
	assert.Equal(t, uriutil.FileURI(path), usages[0].FileURI)
	assert.Equal(t, 1, usages[0].Line)
}
