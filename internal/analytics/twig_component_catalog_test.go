package analytics

import (
	"context"
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigComponentCatalogAggregatesMetadataSyntaxAndLocations(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	classPath := writeAnalyticsFixture(
		t,
		root,
		"src/Twig/Component/Search.php",
		`<?php
namespace App\Twig\Component;

use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;

#[AsLiveComponent(name: 'Search', template: 'components/Search.html.twig')]
final class Search
{
    #[LiveProp(writable: true)]
    public string $query = '';

    public function getResults(): array
    {
        return [];
    }
}
`,
	)
	classSource, err := os.ReadFile(classPath)
	require.NoError(t, err)
	classFile := indexer.NewParsedFile(classPath, classSource)
	require.NoError(t, phpIndex.Index(classFile))
	require.NoError(t, componentIndex.Index(classFile))

	templatePath := writeAnalyticsFixture(
		t,
		root,
		"templates/components/Search.html.twig",
		`{% props title = 'Products' %}
{% block footer %}{% endblock %}
{{ title }} {{ query }}
`,
	)
	templateSource, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	templateFile := indexer.NewParsedFile(templatePath, templateSource)
	require.NoError(t, twigIndex.Index(templateFile))
	require.NoError(t, componentIndex.Index(templateFile))

	usagePath := writeAnalyticsFixture(
		t,
		root,
		"templates/page.html.twig",
		`{{ component('Search') }}`,
	)
	usageSource, err := os.ReadFile(usagePath)
	require.NoError(t, err)
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		usagePath,
		usageSource,
	)))

	provider := NewTwigComponentCatalogProvider(componentIndex)
	entries, err := provider.Catalog(
		context.Background(),
		TwigComponentCatalogRequest{Search: "sea"},
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	assert.Equal(t, "Search", entry.Name)
	assert.Equal(t, "<twig:Search></twig:Search>", entry.Syntax.HTMLTag)
	assert.Equal(t, "{{ component('Search') }}", entry.Syntax.Function)
	assert.Equal(
		t,
		"{% component 'Search' %}{% block footer %}{% endblock %}{% endcomponent %}",
		entry.Syntax.Composition,
	)
	require.Len(t, entry.Declarations, 2)
	declaration := twigComponentDeclarationEntry(
		t,
		entry.Declarations,
		"attribute",
	)
	assert.Equal(t, "App\\Twig\\Component\\Search", declaration.Class)
	assert.Equal(t, "components/Search.html.twig", declaration.Template)
	assert.True(t, declaration.Live)
	assert.Equal(t, uriutil.FileURI(classPath), declaration.FileURI)
	assert.Positive(t, declaration.SourceLine)
	require.Equal(t, []TwigComponentTemplateEntry{{
		Template: "components/Search.html.twig",
		FileURI:  uriutil.FileURI(templatePath),
	}}, entry.Templates)

	query := twigComponentPropEntry(t, entry.Props, "query")
	assert.Equal(t, "string", query.Type)
	assert.True(t, query.Live)
	assert.True(t, query.Writable)
	title := twigComponentPropEntry(t, entry.Props, "title")
	assert.Equal(t, "'Products'", title.DefaultValue)
	results := twigComponentPropEntry(t, entry.Computed, "results")
	assert.Equal(t, "array", results.Type)

	require.Len(t, entry.Blocks, 1)
	assert.Equal(t, "footer", entry.Blocks[0].Name)
	assert.Equal(t, "{{ block('footer') }}", entry.Blocks[0].Print)
	assert.Equal(t, uriutil.FileURI(templatePath), entry.Blocks[0].FileURI)
	require.Len(t, entry.Usages, 1)
	assert.Equal(t, "component()", entry.Usages[0].Syntax)
	assert.Equal(t, uriutil.FileURI(usagePath), entry.Usages[0].FileURI)

	require.NoError(t, componentIndex.Close())
	require.NoError(t, twigIndex.Close())
	restoredTwig, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restoredTwig.Close()) })
	restoredComponents, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restoredComponents.Close()) })
	restoredComponents.SetDependencies(phpIndex, nil, restoredTwig)
	restoredEntries, err := NewTwigComponentCatalogProvider(
		restoredComponents,
	).Catalog(
		context.Background(),
		TwigComponentCatalogRequest{Search: "sea"},
	)
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
}

func twigComponentPropEntry(
	t *testing.T,
	entries []TwigComponentPropEntry,
	name string,
) TwigComponentPropEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("Twig component prop %q not found in %#v", name, entries)
	return TwigComponentPropEntry{}
}

func twigComponentDeclarationEntry(
	t *testing.T,
	entries []TwigComponentDeclarationEntry,
	source string,
) TwigComponentDeclarationEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf(
		"Twig component declaration source %q not found in %#v",
		source,
		entries,
	)
	return TwigComponentDeclarationEntry{}
}
