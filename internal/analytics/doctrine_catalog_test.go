package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineCatalogExposesEntitiesFieldsAndFilters(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	path := filepath.Join(root, "src", "Entity", "Product.php")
	source := `<?php
namespace App\Entity;

use App\Repository\ProductRepository;
use Doctrine\ORM\Mapping as ORM;

#[ORM\MappedSuperclass]
abstract class BaseModel
{
    #[ORM\Id]
    #[ORM\Column(type: 'integer')]
    protected int $id;
}

#[ORM\Embeddable]
final class Address
{
    #[ORM\Column(name: 'city_name', type: 'string')]
    public string $city;
}

#[ORM\Entity(repositoryClass: ProductRepository::class)]
#[ORM\Table(name: 'products')]
#[ORM\Index(fields: ['status'])]
#[ORM\UniqueConstraint(columns: ['status'])]
final class Product extends BaseModel
{
    #[ORM\Column(type: 'string', enumType: ProductStatus::class)]
    public ProductStatus $status;

    #[ORM\ManyToOne(targetEntity: Category::class)]
    public ?Category $category = null;

    #[ORM\Embedded(class: Address::class, columnPrefix: 'shipping_')]
    public Address $shipping;
}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	index, err := doctrine.NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	provider := NewDoctrineCatalogProvider(root, index)

	entities, err := provider.Entities(
		context.Background(),
		DoctrineEntityCatalogRequest{},
	)
	require.NoError(t, err)
	require.Len(t, entities, 3)

	product := doctrineEntityCatalogEntry(
		t,
		entities,
		"App\\Entity\\Product",
	)
	assert.Equal(t, "entity", product.Kind)
	assert.Equal(t, "phpAttribute", product.Source)
	assert.Equal(t, "App\\Entity\\BaseModel", product.Parent)
	assert.Equal(t, "App\\Repository\\ProductRepository", product.Repository)
	assert.Equal(t, "products", product.Table)
	assert.Equal(t, uriutil.FileURI(path), product.FileURI)
	assert.Equal(t, 26, product.SourceLine)
	assert.Equal(t, 5, product.FieldCount)
	assert.Equal(t, 1, product.IndexCount)
	assert.Equal(t, 1, product.UniqueConstraintCount)

	filtered, err := provider.Entities(
		context.Background(),
		DoctrineEntityCatalogRequest{
			Query:    "PRODUCTREPOSITORY",
			Kind:     "entity",
			FileGlob: "src/**/Product.php",
		},
	)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "App\\Entity\\Product", filtered[0].Class)

	mapped, err := provider.Entities(
		context.Background(),
		DoctrineEntityCatalogRequest{Kind: "mapped-superclass"},
	)
	require.NoError(t, err)
	require.Len(t, mapped, 1)
	assert.Equal(t, "App\\Entity\\BaseModel", mapped[0].Class)

	fields, err := provider.Fields(
		context.Background(),
		DoctrineFieldCatalogRequest{ClassName: `\App\Entity\Product`},
	)
	require.NoError(t, err)
	require.Len(t, fields, 5)
	id := doctrineFieldCatalogEntry(t, fields, "id")
	assert.Equal(t, "integer", id.Type)
	assert.Equal(t, "int", id.PHPType)
	assert.Equal(t, []string{"int"}, id.PropertyTypes)
	assert.Equal(t, "App\\Entity\\BaseModel", id.DeclaringClass)

	status := doctrineFieldCatalogEntry(t, fields, "status")
	assert.Equal(t, "string", status.Type)
	assert.Equal(t, "App\\Entity\\ProductStatus", status.EnumType)
	assert.Equal(t, "App\\Entity\\ProductStatus", status.PHPType)

	category := doctrineFieldCatalogEntry(t, fields, "category")
	assert.Equal(t, "App\\Entity\\Category", category.Relation)
	assert.Equal(t, "ManyToOne", category.RelationType)
	assert.Equal(t, uriutil.FileURI(path), category.FileURI)
	assert.Positive(t, category.SourceLine)

	city := doctrineFieldCatalogEntry(t, fields, "shipping.city")
	assert.Equal(t, "shipping_city_name", city.Column)
	assert.Equal(t, "App\\Entity\\Address", city.DeclaringClass)

	_, err = provider.Fields(
		context.Background(),
		DoctrineFieldCatalogRequest{ClassName: "App\\Entity\\Missing"},
	)
	assert.ErrorContains(t, err, "was not found")

	require.NoError(t, index.Close())
	restored, err := doctrine.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredProvider := NewDoctrineCatalogProvider(root, restored)
	restoredEntities, err := restoredProvider.Entities(
		context.Background(),
		DoctrineEntityCatalogRequest{},
	)
	require.NoError(t, err)
	assert.Equal(t, entities, restoredEntities)
	restoredFields, err := restoredProvider.Fields(
		context.Background(),
		DoctrineFieldCatalogRequest{ClassName: "App\\Entity\\Product"},
	)
	require.NoError(t, err)
	assert.Equal(t, fields, restoredFields)
}

func TestDoctrinePropertyTypesPreserveIntersectionMembers(t *testing.T) {
	assert.Equal(
		t,
		[]string{
			"App\\Serializable&App\\Countable",
			"App\\Egg",
			"null",
		},
		doctrinePropertyTypes(
			"App\\Serializable&App\\Countable|App\\Egg|null",
		),
	)
}

func doctrineEntityCatalogEntry(
	t *testing.T,
	entries []DoctrineEntityCatalogEntry,
	class string,
) DoctrineEntityCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Class == class {
			return entry
		}
	}
	t.Fatalf("Doctrine entity %q not found in %#v", class, entries)
	return DoctrineEntityCatalogEntry{}
}

func doctrineFieldCatalogEntry(
	t *testing.T,
	entries []DoctrineFieldCatalogEntry,
	name string,
) DoctrineFieldCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("Doctrine field %q not found in %#v", name, entries)
	return DoctrineFieldCatalogEntry{}
}
