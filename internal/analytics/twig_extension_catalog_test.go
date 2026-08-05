package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigExtensionCatalogExposesTypedCallablesTagsAndFilters(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	path := filepath.Join(root, "src", "Twig", "AppExtension.php")
	source := `<?php
namespace App\Twig;

use Twig\Extension\AbstractExtension;
use Twig\TokenParser\TokenParserInterface;
use Twig\TwigFilter;
use Twig\TwigFunction;
use Twig\TwigTest;

final class AppExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('product_url', [$this, 'productUrl']),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('money', $this->money(...)),
        ];
    }

    public function getTests(): array
    {
        return [
            new TwigTest('sellable', [$this, 'isSellable']),
        ];
    }

    public function productUrl(
        string $id,
        bool $absolute = false,
        string $_internal = '',
    ): string {
        return '';
    }

    public function money(float $value): string
    {
        return '';
    }

    public function isSellable(object $product): bool
    {
        return true;
    }
}

/** @deprecated Use ModernTokenParser instead. */
final class LegacyTokenParser implements TokenParserInterface
{
    public function getTag(): string
    {
        return 'catalog';
    }
}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, twigIndex.Index(parsed))

	provider := NewTwigExtensionCatalogProvider(twigIndex, phpIndex)
	entries, err := provider.Catalog(
		context.Background(),
		TwigExtensionCatalogRequest{},
	)
	require.NoError(t, err)

	productURL := twigExtensionCatalogEntry(
		t,
		entries,
		"function",
		"product_url",
	)
	assert.Equal(t, "App\\Twig\\AppExtension", productURL.ClassName)
	assert.Equal(t, "productUrl", productURL.MethodName)
	assert.Equal(t, "$this->productUrl", productURL.Callable)
	assert.Equal(t, "product_url($id, $absolute, $_internal)", productURL.Usage)
	assert.Equal(t, []TwigExtensionCatalogParameter{
		{Name: "id", Type: "string"},
		{Name: "absolute", Type: "bool", Optional: true},
	}, productURL.Parameters)
	assert.Equal(t, uriutil.FileURI(path), productURL.FileURI)
	assert.Positive(t, productURL.SourceLine)

	money := twigExtensionCatalogEntry(t, entries, "filter", "money")
	assert.Equal(t, "App\\Twig\\AppExtension", money.ClassName)
	assert.Equal(t, "money", money.MethodName)
	assert.Equal(t, []TwigExtensionCatalogParameter{
		{Name: "value", Type: "float"},
	}, money.Parameters)

	sellable := twigExtensionCatalogEntry(t, entries, "test", "sellable")
	assert.Equal(t, "App\\Twig\\AppExtension", sellable.ClassName)
	assert.Equal(t, "isSellable", sellable.MethodName)

	tag := twigExtensionCatalogEntry(t, entries, "tag", "catalog")
	assert.Equal(t, "App\\Twig\\LegacyTokenParser", tag.ClassName)
	assert.True(t, tag.Deprecated)
	assert.Contains(t, tag.Deprecation, "ModernTokenParser")

	onlyFunctions := false
	includeFunctions := true
	filtered, err := provider.Catalog(
		context.Background(),
		TwigExtensionCatalogRequest{
			Search:           "PRODUCT",
			IncludeFilters:   &onlyFunctions,
			IncludeFunctions: &includeFunctions,
			IncludeTests:     &onlyFunctions,
			IncludeTags:      &onlyFunctions,
		},
	)
	require.NoError(t, err)
	require.Equal(t, []TwigExtensionCatalogEntry{productURL}, filtered)

	require.NoError(t, twigIndex.Close())
	restored, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredEntries, err := NewTwigExtensionCatalogProvider(
		restored,
		phpIndex,
	).Catalog(context.Background(), TwigExtensionCatalogRequest{})
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
}

func twigExtensionCatalogEntry(
	t *testing.T,
	entries []TwigExtensionCatalogEntry,
	kind,
	name string,
) TwigExtensionCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Type == kind && entry.Name == name {
			return entry
		}
	}
	t.Fatalf(
		"Twig %s %q not found in %#v",
		kind,
		name,
		entries,
	)
	return TwigExtensionCatalogEntry{}
}
