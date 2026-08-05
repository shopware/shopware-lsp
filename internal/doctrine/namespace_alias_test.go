package doctrine

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testDoctrineNamespaceProvider struct {
	aliases  map[string][]string
	revision uint64
}

func (provider *testDoctrineNamespaceProvider) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	return provider.aliases, provider.revision
}

func TestDoctrineNamespaceAliasesResolveConfiguredAndBundleShortcuts(
	t *testing.T,
) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	for path, source := range map[string]string{
		"/project/src/Entity/Product.php": `<?php
namespace Acme\ShopBundle\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column]
    public string $name;
}`,
		"/project/src/Entity/Nested/Order.php": `<?php
namespace Acme\ShopBundle\Entity\Nested;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Order {}`,
		"/project/src/Document/Page.php": `<?php
namespace Acme\ShopBundle\Document;
use Doctrine\ODM\MongoDB\Mapping\Annotations as ODM;
/** @ODM\Document */
class Page {}`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := &testDoctrineNamespaceProvider{
		aliases: map[string][]string{
			"LegacyBundle": {"Acme\\ShopBundle\\Entity"},
			"DocsBundle":   {"Acme\\ShopBundle\\Document"},
		},
		revision: 1,
	}
	idx.SetNamespaceAliasProvider(provider)

	aliases, err := idx.ModelAliases()
	require.NoError(t, err)
	assert.Contains(t, aliases, ModelAlias{
		Name:      "LegacyBundle:Product",
		Class:     "Acme\\ShopBundle\\Entity\\Product",
		Namespace: "Acme\\ShopBundle\\Entity",
	})
	assert.Contains(t, aliases, ModelAlias{
		Name:      "AcmeShopBundle:Nested\\Order",
		Class:     "Acme\\ShopBundle\\Entity\\Nested\\Order",
		Namespace: "Acme\\ShopBundle\\Entity",
		Weak:      true,
	})

	for shortcut, expected := range map[string]string{
		"LegacyBundle:Product":         "Acme\\ShopBundle\\Entity\\Product",
		"LegacyBundle:Nested\\Order":   "Acme\\ShopBundle\\Entity\\Nested\\Order",
		"DocsBundle:Page":              "Acme\\ShopBundle\\Document\\Page",
		"AcmeShopBundle:Nested\\Order": "Acme\\ShopBundle\\Entity\\Nested\\Order",
	} {
		actual, found, resolveErr := idx.ResolveModelName(shortcut)
		require.NoError(t, resolveErr)
		require.True(t, found, shortcut)
		assert.Equal(t, expected, actual)
	}
	model, found, err := idx.Model("LegacyBundle:Product")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Acme\\ShopBundle\\Entity\\Product", model.Class)
	fields, err := idx.Fields("LegacyBundle:Product")
	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "name", fields[0].Name)

	provider.aliases = map[string][]string{
		"RenamedBundle": {"Acme\\ShopBundle\\Entity"},
	}
	provider.revision++
	_, found, err = idx.ResolveModelName("LegacyBundle:Product")
	require.NoError(t, err)
	assert.False(t, found)
	resolved, found, err := idx.ResolveModelName("RenamedBundle:Product")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Acme\\ShopBundle\\Entity\\Product", resolved)
}
