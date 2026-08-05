package analytics

import (
	"context"
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateVariableCatalogMergesTypesSourcesAndProperties(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	twigIndex.SetDependencies(phpIndex, nil)

	phpPath := writeAnalyticsFixture(
		t,
		root,
		"src/Controller/ProductController.php",
		`<?php
namespace App;

final class Product
{
    public static string $ignoredStatic = '';
    public string $name = '';

    public function getPrice(): float { return 0.0; }
    public function refresh(): void {}
    public function setName(string $name): void {}
    public function __call(string $name, array $arguments): mixed {}
}

final class ProductController
{
    public function show(Product $product): array
    {
        return $this->render('catalog/show.html.twig', [
            'product' => $product,
            'count' => 1,
        ]);
    }
}
`,
	)
	phpSource, err := os.ReadFile(phpPath)
	require.NoError(t, err)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))

	templatePath := writeAnalyticsFixture(
		t,
		root,
		"templates/catalog/show.html.twig",
		`{# @var annotated \App\Product #}
{{ product.name }}
{{ annotated.price }}
{{ missing }}
`,
	)
	templateSource, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		templateSource,
	)))

	globalPath := writeAnalyticsFixture(
		t,
		root,
		"config/packages/twig.yaml",
		`twig:
  globals:
    shop_name: 'Demo'
`,
	)
	globalSource, err := os.ReadFile(globalPath)
	require.NoError(t, err)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		globalPath,
		globalSource,
	)))

	provider := NewTwigTemplateVariableCatalogProvider(
		root,
		twigIndex,
		phpIndex,
		nil,
	)
	request := TwigTemplateVariableCatalogRequest{
		Template: "templates/catalog/show.html.twig",
		FileGlob: "templates/**/show.html.twig",
	}
	entries, err := provider.Catalog(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	assert.Equal(t, "catalog/show.html.twig", entry.Template)
	require.Equal(t, []TwigTemplateSourceLocation{{
		FileURI: uriutil.FileURI(templatePath),
		Line:    1,
	}}, entry.Files)

	product := twigTemplateVariableEntry(t, entry.Variables, "product")
	assert.Equal(t, "App\\Product", product.Type)
	assert.Contains(t, product.Types, "App\\Product")
	assertTwigVariableProperty(t, product.Properties, "name", "property")
	assertTwigVariableProperty(t, product.Properties, "price", "getter")
	assertTwigVariableProperty(t, product.Properties, "refresh", "method")
	assert.NotContains(t, twigVariablePropertyNames(product.Properties), "setName")
	assert.NotContains(t, twigVariablePropertyNames(product.Properties), "__call")
	assert.NotContains(t, twigVariablePropertyNames(product.Properties), "ignoredStatic")
	assertTwigVariableSource(t, product.Sources, "controller")
	assertTwigVariableSource(t, product.Sources, "templateInput")

	annotated := twigTemplateVariableEntry(t, entry.Variables, "annotated")
	assert.Equal(t, "App\\Product", annotated.Type)
	assertTwigVariableSource(t, annotated.Sources, "annotation")
	assertTwigVariableProperty(t, annotated.Properties, "price", "getter")

	missing := twigTemplateVariableEntry(t, entry.Variables, "missing")
	assert.Equal(t, "unknown", missing.Type)
	assertTwigVariableSource(t, missing.Sources, "templateInput")

	count := twigTemplateVariableEntry(t, entry.Variables, "count")
	assert.Equal(t, "1", count.Type)
	assertTwigVariableSource(t, count.Sources, "controller")

	global := twigTemplateVariableEntry(t, entry.Variables, "shop_name")
	assert.Equal(t, "string", global.Type)
	assertTwigVariableSource(t, global.Sources, "global")

	_, err = provider.Catalog(
		context.Background(),
		TwigTemplateVariableCatalogRequest{},
	)
	assert.ErrorContains(t, err, "at least one")

	require.NoError(t, twigIndex.Close())
	restored, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restored.SetDependencies(phpIndex, nil)
	restoredEntries, err := NewTwigTemplateVariableCatalogProvider(
		root,
		restored,
		phpIndex,
		nil,
	).Catalog(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
}

func twigTemplateVariableEntry(
	t *testing.T,
	entries []TwigTemplateVariableEntry,
	name string,
) TwigTemplateVariableEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("Twig variable %q not found in %#v", name, entries)
	return TwigTemplateVariableEntry{}
}

func assertTwigVariableProperty(
	t *testing.T,
	properties []TwigTemplateVariableProperty,
	name,
	kind string,
) {
	t.Helper()
	for _, property := range properties {
		if property.Name == name {
			assert.Equal(t, kind, property.Kind)
			return
		}
	}
	t.Fatalf("Twig property %q not found in %#v", name, properties)
}

func twigVariablePropertyNames(
	properties []TwigTemplateVariableProperty,
) []string {
	result := make([]string, 0, len(properties))
	for _, property := range properties {
		result = append(result, property.Name)
	}
	return result
}

func assertTwigVariableSource(
	t *testing.T,
	sources []TwigTemplateVariableSource,
	kind string,
) {
	t.Helper()
	for _, source := range sources {
		if source.Kind == kind {
			return
		}
	}
	t.Fatalf("Twig variable source %q not found in %#v", kind, sources)
}
