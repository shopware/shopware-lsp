package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type doctrineDefinitionAliasProvider struct {
	aliases map[string][]string
}

func (provider doctrineDefinitionAliasProvider) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	return provider.aliases, 1
}

func TestDoctrineDefinitionNavigatesEntityAndMappedField(t *testing.T) {
	root := t.TempDir()
	entityPath := filepath.Join(root, "src", "Product.php")
	usagePath := filepath.Join(root, "src", "Usage.php")
	parserPath := filepath.Join(root, "vendor", "doctrine", "Parser.php")
	functionPath := filepath.Join(
		root,
		"vendor",
		"doctrine",
		"Functions",
		"MinFunction.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(entityPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(functionPath), 0o755))
	entitySource := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}`
	usageSource := `<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $manager->getRepository('App\Product')->findBy(['name' => 'value']);
    $manager->getRepository('LegacyBundle:Product');
}
class QueryRepository extends \Doctrine\Bundle\DoctrineBundle\Repository\ServiceEntityRepository {
    public function __construct($registry) {
        parent::__construct($registry, Product::class);
    }
    public function query(): void {
        $qb = $this->createQueryBuilder('product');
        $qb->andWhere('product.name = :name');
    }
}
class ProductRepository {
    public function load(): void {
        $this->findOneByName('value');
    }
}
$dql = 'SELECT MIN(p.name) FROM App\Product p WHERE p.name = :name';`
	functionSource := `<?php
namespace Doctrine\ORM\Query;
class Parser {
    private static $numericFunctions = [
        'min' => Functions\MinFunction::class,
    ];
}`
	minFunctionSource := `<?php
namespace Doctrine\ORM\Query\Functions;
class MinFunction {}`
	require.NoError(t, os.WriteFile(entityPath, []byte(entitySource), 0o644))
	require.NoError(t, os.WriteFile(usagePath, []byte(usageSource), 0o644))
	require.NoError(t, os.WriteFile(
		parserPath,
		[]byte(functionSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		functionPath,
		[]byte(minFunctionSource),
		0o644,
	))

	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	doctrineIndex.SetNamespaceAliasProvider(
		doctrineDefinitionAliasProvider{aliases: map[string][]string{
			"LegacyBundle": {"App"},
		}},
	)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	stubs := `<?php
namespace Doctrine\Persistence;
interface ObjectRepository { public function findBy(array $criteria): array; }
interface ObjectManager { public function getRepository(string $class): ObjectRepository; }`
	for path, source := range map[string]string{
		filepath.Join(root, "vendor", "doctrine.php"): stubs,
		entityPath:   entitySource,
		usagePath:    usageSource,
		parserPath:   functionSource,
		functionPath: minFunctionSource,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, doctrineIndex.Index(parsed))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(usagePath),
		usageSource,
		1,
	)
	provider := NewDoctrineDefinitionProvider(doctrineIndex, phpIndex)

	for _, test := range []struct {
		needle string
		offset int
	}{
		{"App\\Product", strings.Index(usageSource, "App\\Product") + 2},
		{
			"legacy shortcut",
			strings.Index(usageSource, "LegacyBundle:Product") + 4,
		},
		{"criteria name", strings.Index(usageSource, "'name'") + 2},
		{
			"DQL name",
			strings.Index(usageSource, "product.name") +
				len("product.") + 2,
		},
		{
			"standalone DQL entity",
			strings.LastIndex(usageSource, "App\\Product") + 2,
		},
		{
			"standalone DQL field",
			strings.LastIndex(usageSource, "p.name") + len("p.") + 2,
		},
		{
			"magic name",
			strings.Index(usageSource, "findOneByName") +
				len("findOneBy") + 2,
		},
	} {
		offset := uint32(test.offset)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			usagePath,
			1,
			node,
			document.SyntaxTree.Root,
		)
		locations := provider.GetDefinition(
			ctx,
			securityDefinitionRequest(document, node, offset),
		)
		require.NotEmpty(t, locations, test.needle)
		assert.Equal(t, uriutil.FileURI(entityPath), locations[0].URI)
	}

	functionOffset := uint32(strings.LastIndex(usageSource, "MIN(") + 1)
	functionNode := document.SyntaxTree.Root.NodeAtOffset(functionOffset)
	functionLocations := provider.GetDefinition(
		context.Background(),
		securityDefinitionRequest(
			document,
			functionNode,
			functionOffset,
		),
	)
	require.NotEmpty(t, functionLocations)
	assert.Equal(t, uriutil.FileURI(functionPath), functionLocations[0].URI)
}

func TestPHPDocEntityAssistantTagDefinition(t *testing.T) {
	root := t.TempDir()
	entityPath := filepath.Join(root, "src", "Product.php")
	entitySource := `<?php
namespace App;
#[\Doctrine\ORM\Mapping\Entity]
class Product {}
`
	assistantPath := filepath.Join(root, "src", "EntityAssistant.php")
	assistantSource := `<?php
/** @param string $entity #Entity */
function resolve_entity(string $entity): void {}
`
	doctrineIndex, err := doctrine.NewIndex(filepath.Join(root, "doctrine"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		entityPath:    entitySource,
		assistantPath: assistantSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, doctrineIndex.Index(parsed))
	}
	usagePath := filepath.Join(root, "src", "Usage.php")
	usage := "<?php resolve_entity('App\\Product');"
	document := lsp.NewTextDocument(uriutil.FileURI(usagePath), usage, 1)
	offset := uint32(strings.Index(usage, "App\\Product") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		usagePath,
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewDoctrineDefinitionProvider(
		doctrineIndex,
		phpIndex,
	).GetDefinition(
		ctx,
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(entityPath), locations[0].URI)
}

func TestDoctrineDefinitionNavigatesDBALTablesAndColumns(t *testing.T) {
	root := t.TempDir()
	entityPath := filepath.Join(root, "src", "Product.php")
	mappingPath := filepath.Join(root, "config", "Product.orm.xml")
	usagePath := filepath.Join(root, "src", "Database.php")
	stubPath := filepath.Join(root, "vendor", "dbal.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(entityPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mappingPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(stubPath), 0o755))
	entitySource := `<?php
namespace App;
class Product {
    private string $name;
}`
	mappingSource := `<doctrine-mapping>
  <entity name="App\Product" table="cms_products">
    <field name="name" column="product_name" type="string"/>
  </entity>
</doctrine-mapping>`
	stubSource := `<?php
namespace Doctrine\DBAL;
class Connection {
    public function insert(string $table, array $data): void {}
}`
	usageSource := `<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
    $connection->insert('cms_products', ['product_name' => 'value']);
}`
	for path, source := range map[string]string{
		entityPath:  entitySource,
		mappingPath: mappingSource,
		stubPath:    stubSource,
		usagePath:   usageSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		entityPath:  entitySource,
		mappingPath: mappingSource,
		stubPath:    stubSource,
		usagePath:   usageSource,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, doctrineIndex.Index(parsed))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(usagePath),
		usageSource,
		1,
	)
	provider := NewDoctrineDefinitionProvider(doctrineIndex, phpIndex)
	for _, needle := range []string{"cms_products", "product_name"} {
		offset := uint32(strings.Index(usageSource, needle) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			usagePath,
			1,
			node,
			document.SyntaxTree.Root,
		)
		locations := provider.GetDefinition(
			ctx,
			securityDefinitionRequest(document, node, offset),
		)
		require.NotEmpty(t, locations, needle)
		var uris []string
		for _, location := range locations {
			uris = append(uris, location.URI)
		}
		if needle == "cms_products" {
			assert.Contains(t, uris, uriutil.FileURI(entityPath))
		} else {
			assert.Contains(t, uris, uriutil.FileURI(mappingPath))
		}
	}
}

func TestDoctrineDefinitionNavigatesExternalMappingReferences(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "src", "Models.php")
	typeBasePath := filepath.Join(root, "vendor", "Type.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(modelPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(typeBasePath), 0o755))
	modelSource := `<?php
namespace App;
class Category {}
class Product {
    private string $status;
    private Category $category;
    public function prepare(): void {}
}
class ChildProduct extends Product {}
class ProductRepository {}
class MoneyType extends \Doctrine\DBAL\Types\Type {
    public function getName(): string { return 'currency_amount'; }
}`
	typeBaseSource := `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`
	require.NoError(t, os.WriteFile(
		modelPath,
		[]byte(modelSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		typeBasePath,
		[]byte(typeBaseSource),
		0o644,
	))
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		modelPath:    modelSource,
		typeBasePath: typeBaseSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "config", "packages", "doctrine.yaml"),
		[]byte(`doctrine:
  dbal:
    types:
      configured_currency: App\MoneyType
`),
	)))
	mappingPath := filepath.Join(root, "config", "Product.orm.xml")
	mappingSource := `<doctrine-mapping>
  <entity name="App\Product" repository-class="App\ProductRepository">
    <field name="status" type="currency_amount"/>
    <field name="status" type="configured_currency"/>
    <discriminator-map>
      <discriminator-mapping value="child" class="App\ChildProduct"/>
    </discriminator-map>
    <many-to-one field="category" target-entity="App\Category"/>
    <lifecycle-callbacks>
      <lifecycle-callback type="prePersist" method="prepare"/>
    </lifecycle-callbacks>
  </entity>
</doctrine-mapping>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(mappingPath),
		mappingSource,
		1,
	)
	provider := NewDoctrineDefinitionProvider(doctrineIndex, phpIndex)
	for _, needle := range []string{
		"App\\Product",
		"App\\ProductRepository",
		`name="status`,
		`type="currency_amount`,
		`type="configured_currency`,
		"App\\ChildProduct",
		"App\\Category",
		`method="prepare`,
	} {
		offset := uint32(strings.Index(mappingSource, needle) + len(needle) - 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.NotEmpty(t, locations, needle)
		assert.Equal(t, uriutil.FileURI(modelPath), locations[0].URI)
	}
}

func TestDoctrineDefinitionNavigatesTypeRegistrationClass(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	typePath := "/project/src/MoneyType.php"
	for path, source := range map[string]string{
		"/project/vendor/Type.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		typePath: `<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<?php
use App\Doctrine\MoneyType;
use Doctrine\DBAL\Types\Type;

Type::addType('money', MoneyType::class);`
	document := lsp.NewTextDocument(
		"file:///project/bootstrap.php",
		source,
		1,
	)
	for _, needle := range []string{"money", "MoneyType::class"} {
		offset := uint32(strings.Index(source, needle) + 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := NewDoctrineDefinitionProvider(
			doctrineIndex,
			phpIndex,
		).GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.Len(t, locations, 1)
		require.Equal(t, uriutil.FileURI(typePath), locations[0].URI)
	}
}

func TestDoctrineDefinitionNavigatesTableConstraintMembers(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	path := "/project/src/Product.php"
	source := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;

#[ORM\Entity]
#[ORM\Index(fields: ['name'])]
#[ORM\UniqueConstraint(columns: ['email_address'])]
class Product
{
    #[ORM\Column]
    private string $name;

    #[ORM\Column(name: 'email_address')]
    private string $email;
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	provider := NewDoctrineDefinitionProvider(doctrineIndex, phpIndex)
	for _, needle := range []string{
		"'name']",
		"'email_address']",
	} {
		offset := uint32(strings.Index(source, needle) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.Len(t, locations, 1, needle)
		require.Equal(t, uriutil.FileURI(path), locations[0].URI)
	}
}
