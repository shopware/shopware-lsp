package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryBuilderTracksRepositoryRootAndRelationAliases(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	source := `<?php
namespace App\Repository;
class ProductRepository {
    public function search(): void {
        $qb = $this->createQueryBuilder('p');
        $qb->leftJoin('p.category', 'c');
        $qb->andWhere('p.name = :name AND c.title = :title');
        $qb->setParameter('');
    }
}`
	path := "/project/src/Repository/ProductRepository.php"
	root := phpparser.Parse(source).Tree.Root
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	for _, test := range []struct {
		needle string
		labels []string
	}{
		{
			needle: "'p.name =",
			labels: []string{"p.id", "p.name", "c.title"},
		},
		{
			needle: "setParameter('",
			labels: []string{"name", "title"},
		},
	} {
		offset := uint32(strings.Index(source, test.needle) + len(test.needle))
		node := root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			root,
		)
		completions := idx.QueryCompletionsAt(ctx, root, node, offset)
		values := make([]string, 0, len(completions))
		for _, completion := range completions {
			values = append(values, completion.Label)
		}
		for _, label := range test.labels {
			assert.Contains(t, values, label)
		}
	}

	offset := uint32(strings.Index(source, "p.name") + 3)
	node := root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	reference, found := idx.QueryFieldReferenceAt(ctx, root, node, offset)
	require.True(t, found)
	assert.Equal(t, "p", reference.Alias)
	assert.Equal(t, "name", reference.Field)
	assert.Equal(t, "App\\Entity\\Product", reference.Entity)
}

func TestQueryBuilderUsesServiceEntityRepositoryConstructorEntity(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	source := `<?php
namespace App\Repository;
use App\Entity\Product;
use Doctrine\Bundle\DoctrineBundle\Repository\ServiceEntityRepository;
class ServiceProductRepository extends ServiceEntityRepository {
    public function __construct($registry) {
        parent::__construct($registry, Product::class);
    }
    public function search(): void {
        $qb = $this->createQueryBuilder('product');
        $qb->andWhere('product.');
    }
}`
	path := "/project/src/Repository/ServiceProductRepository.php"
	root := phpparser.Parse(source).Tree.Root
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	offset := uint32(strings.Index(source, "product.'") + len("product."))
	node := root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	completions := idx.QueryCompletionsAt(ctx, root, node, offset)
	var labels []string
	for _, completion := range completions {
		labels = append(labels, completion.Label)
	}
	assert.Contains(t, labels, "product.name")
}

func TestQueryBuilderTracksFluentAndNestedChains(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	source := `<?php
namespace App\Repository;
class ProductRepository {
    public function directChain(): void {
        $this->createQueryBuilder('p')
            ->leftJoin('p.category', 'c')
            ->andWhere('c.');
    }
    public function assignedChain(): void {
        $qb = $this->createQueryBuilder('p')
            ->rightJoin('p.category', 'c');
        $qb->andWhere('c.');
    }
    public function explicitFrom($entityManager): void {
        $entityManager->createQueryBuilder()
            ->from(\App\Entity\Product::class, 'p')
            ->andWhere('p.');
    }
    public function nestedExpression(): void {
        $qb = $this->createQueryBuilder('p');
        $qb->andWhere($qb->expr()->eq('p.', ':name'));
    }
    public function indexBy(): void {
        $this->createQueryBuilder('p', 'p.');
    }
    public function inferredParameter(): void {
        $qb = $this->createQueryBuilder('p');
        $qb->andWhere('p.name = ');
    }
    public function fromIndexBy($entityManager): void {
        $qb = $entityManager->createQueryBuilder();
        $qb->from(\App\Entity\Product::class, 'p', 'p.');
    }
    public function joinCondition(): void {
        $qb = $this->createQueryBuilder('p');
        $qb->rightJoin('p.category', 'c', null, 'c.', 'c.');
    }
}`
	path := "/project/src/Repository/ProductRepository.php"
	root := phpparser.Parse(source).Tree.Root
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	for _, test := range []struct {
		needle     string
		occurrence int
		labels     []string
	}{
		{
			needle:     "andWhere('c.');",
			occurrence: 0,
			labels:     []string{"c.title"},
		},
		{
			needle:     "andWhere('c.');",
			occurrence: 1,
			labels:     []string{"c.title"},
		},
		{needle: "andWhere('p.');", labels: []string{"p.name"}},
		{needle: "eq('p.',", labels: []string{"p.name"}},
		{
			needle: "createQueryBuilder('p', 'p.');",
			labels: []string{"p.name"},
		},
		{
			needle: "andWhere('p.name = ');",
			labels: []string{":name", ":p_name", ":pName"},
		},
		{
			needle: "from(\\App\\Entity\\Product::class, 'p', 'p.');",
			labels: []string{"p.name"},
		},
		{
			needle: "rightJoin('p.category', 'c', null, 'c.', 'c.');",
			labels: []string{"c.title"},
		},
	} {
		position := 0
		for occurrence := 0; occurrence <= test.occurrence; occurrence++ {
			next := strings.Index(source[position:], test.needle)
			require.NotEqual(t, -1, next)
			position += next
			if occurrence != test.occurrence {
				position += len(test.needle)
			}
		}
		require.NotEqual(t, -1, position)
		offset := uint32(position + strings.LastIndex(test.needle, "'"))
		if strings.Contains(test.needle, "eq('p.'") {
			offset = uint32(position + strings.Index(test.needle, "p.") + 2)
		}
		if strings.Contains(test.needle, "createQueryBuilder('p', 'p.'") {
			offset = uint32(position + strings.LastIndex(test.needle, "p.") + 2)
		}
		if strings.Contains(test.needle, "p.name = ") {
			offset = uint32(position + strings.Index(test.needle, "p.name = ") +
				len("p.name = "))
		}
		if strings.Contains(test.needle, "from(") {
			offset = uint32(position + strings.LastIndex(test.needle, "p.") + 2)
		}
		if strings.Contains(test.needle, "rightJoin(") {
			offset = uint32(position + strings.LastIndex(test.needle, "c.") + 2)
		}
		node := root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			root,
		)
		completions := idx.QueryCompletionsAt(ctx, root, node, offset)
		var labels []string
		for _, completion := range completions {
			labels = append(labels, completion.Label)
		}
		for _, label := range test.labels {
			assert.Contains(t, labels, label, test.needle)
		}
	}
}

func queryBuilderFixture(t *testing.T) (*Index, *php.PHPIndex) {
	t.Helper()
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	entitySource := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
use App\Repository\ProductRepository;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    #[ORM\Id]
    #[ORM\Column(type: 'integer')]
    private int $id;
    #[ORM\Column(type: 'string')]
    private string $name;
    #[ORM\ManyToOne(targetEntity: Category::class)]
    private Category $category;
}
#[ORM\Entity]
class Category {
    #[ORM\Column(type: 'string')]
    private string $title;
}`
	parsed := indexer.NewParsedFile(
		"/project/src/Entity/Product.php",
		[]byte(entitySource),
	)
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, idx.Index(parsed))
	return idx, phpIndex
}
