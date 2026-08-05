package doctrine

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestDoctrineTypeExtensionMethodGate(t *testing.T) {
	t.Parallel()
	require.True(t, isManagerExtensionMethod("getrepository"))
	require.True(t, isRepositoryExtensionMethod("findbyname"))
	require.True(t, isRepositoryExtensionMethod("countbyactive"))
	require.False(t, isRepositoryExtensionMethod("unrelated"))
}

func TestDoctrineMethodClassificationDoesNotAllocate(t *testing.T) {
	var method string
	allocations := testing.AllocsPerRun(100, func() {
		method = canonicalDoctrineMethod("FindByProductNumber")
	})
	require.Zero(t, allocations)
	require.Equal(t, "findby*", method)
}

func TestPHPTypeExtensionInfersManagerAndRepositoryResults(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/Entity.php",
		[]byte(`<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {}
class ProductRepository {}`),
	)))

	extension := NewPHPTypeExtension(idx)
	snapshot := semantic.NewSnapshot(1, nil)
	entity := types.Named("App\\Product")
	repository := types.Named(objectRepositoryClass, entity)

	fact, found := extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "findOneBy",
		Receiver: repository,
		Arguments: []inference.CallArgument{{
			Type: types.Array(types.Mixed(), types.Mixed()),
		}},
	})
	require.True(t, found)
	require.Equal(t, "App\\Product|null", fact.Type.String())

	fact, found = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "findBy",
		Receiver: repository,
	})
	require.True(t, found)
	require.Equal(t, "list<App\\Product>", fact.Type.String())

	fact, found = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "find",
		Receiver: types.Named("App\\ProductRepository"),
	})
	require.True(t, found)
	require.Equal(t, "App\\Product|null", fact.Type.String())
}

func TestPHPTypeExtensionIntegratesWithSemanticAnalysis(t *testing.T) {
	doctrineIndex, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpIndex.RegisterTypeExtension(NewPHPTypeExtension(doctrineIndex))
	doctrineIndex.SetNamespaceAliasProvider(&testDoctrineNamespaceProvider{
		aliases: map[string][]string{
			"LegacyBundle": {"App"},
		},
		revision: 1,
	})

	stubs := `<?php
namespace Doctrine\Persistence;
interface ObjectRepository {
    public function findOneBy(array $criteria): ?object;
    public function findBy(array $criteria): array;
}
interface ObjectManager {
    public function getRepository(string $class): ObjectRepository;
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine.php",
		[]byte(stubs),
	)))
	entity := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    public function getId(): int { return 1; }
}
class ProductRepository {
    public function special(): int { return 1; }
}`
	parsed := indexer.NewParsedFile("/project/src/Product.php", []byte(entity))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))

	source := `<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $repository = $manager->getRepository(Product::class);
    $product = $repository->findOneBy(['id' => 1]);
    $products = $repository->findByName('demo');
    $legacyRepository = $manager->getRepository('LegacyBundle:Product');
    $legacyProduct = $legacyRepository->findOneBy(['id' => 1]);
    $managedProduct = $manager->find(Product::class, 1);
    $legacyManagedProduct = $manager->find('LegacyBundle:Product', 1);
    $managedId = $manager->find(Product::class, 1)->getId();
    $special = $manager->getRepository(Product::class)->special();
}`
	root := phpparser.Parse(source).Tree.Root
	document := phpIndex.AnalyzeDocument(
		"/project/src/Usage.php",
		1,
		root,
	)
	for _, call := range phpquery.Calls(root) {
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "getrepository":
			require.Equal(
				t,
				"App\\ProductRepository",
				document.TypeOf(call).Type.String(),
			)
		case "find":
			require.Equal(
				t,
				"App\\Product|null",
				document.TypeOf(call).Type.String(),
			)
		case "findoneby":
			require.Equal(
				t,
				"App\\Product|null",
				document.TypeOf(call).Type.String(),
			)
		case "findbyname":
			require.Equal(
				t,
				"list<App\\Product>",
				document.TypeOf(call).Type.String(),
			)
		case "getid", "special":
			require.Equal(t, "int", document.TypeOf(call).Type.String())
		}
	}
}
