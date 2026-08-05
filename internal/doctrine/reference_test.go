package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestReferenceAtRecognizesTypedManagerAndCriteriaFields(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	stubs := `<?php
namespace Doctrine\Persistence;
interface ObjectRepository {
    public function findBy(array $criteria): array;
    public function findOneBy(array $criteria): ?object;
}
interface ObjectManager {
    public function getRepository(string $className): ObjectRepository;
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine.php",
		[]byte(stubs),
	)))

	entitySource := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}
class ProductRepository {}`
	parsedEntity := indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(entitySource),
	)
	require.NoError(t, phpIndex.Index(parsedEntity))
	require.NoError(t, idx.Index(parsedEntity))

	source := `<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $manager->getRepository(Product::class)->findBy(['name' => 'value']);
    $manager->getRepository('App\Product');
}`
	path := "/project/src/UseProduct.php"
	root := phpparser.Parse(source).Tree.Root

	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		value := phpquery.StringValue(literal)
		if value != "name" && value != "App\\Product" {
			continue
		}
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			literal,
			root,
		)
		reference, found := idx.ReferenceAt(ctx, root, literal)
		require.True(t, found, value)
		if value == "name" {
			require.Equal(t, FieldReference, reference.Role)
			require.Equal(t, "App\\Product", reference.Entity)
		} else {
			require.Equal(t, EntityReference, reference.Role)
			require.Equal(t, "App\\Product", reference.Name)
		}
	}

	offset := strings.Index(source, "Product::class") + 2
	node := root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	reference, found := idx.ReferenceAt(ctx, root, node)
	require.True(t, found)
	require.Equal(t, EntityReference, reference.Role)
	require.Equal(t, ClassConstantReference, reference.Kind)
}

func TestEntityReferencesInDocumentCoversPluginRepositorySignatures(
	t *testing.T,
) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine-signatures.php",
		[]byte(`<?php
namespace Doctrine\Persistence;
interface ManagerRegistry {
    public function getRepository(string $class): object;
    public function getManagerForClass(string $class): object;
}

namespace Doctrine\ORM;
class QueryBuilder {
    public function update(string $class): self {}
}
class Cache {
    public function containsEntity(string $class): bool {}
}
`),
	)))
	source := `<?php
namespace App;

use Doctrine\ORM\Cache;
use Doctrine\ORM\QueryBuilder;
use Doctrine\Persistence\ManagerRegistry;

function load(
    ManagerRegistry $registry,
    QueryBuilder $builder,
    Cache $cache,
    OtherRegistry $other,
): void {
    $registry->getRepository(Product::class);
    $registry->getManagerForClass('App\Product');
    $builder->update(Product::class);
    $cache->containsEntity(Product::class);
    $other->getManagerForClass(Product::class);
}
`
	path := "/project/src/UseProduct.php"
	root := phpparser.Parse(source).Tree.Root
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		root,
		root,
	)
	references := idx.EntityReferencesInDocument(ctx, root)
	require.Len(t, references, 4)
	for _, reference := range references {
		require.Equal(t, EntityReference, reference.Role)
		require.Equal(t, "App\\Product", reference.Name)
		require.NotZero(t, ReferenceRange(reference).Len())
	}
}
