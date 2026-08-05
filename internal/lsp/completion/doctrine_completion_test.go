package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type doctrineCompletionAliasProvider struct {
	aliases map[string][]string
}

func (provider doctrineCompletionAliasProvider) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	return provider.aliases, 1
}

func TestDoctrineEntityAndCriteriaFieldCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	source := `<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $manager->getRepository(Pro);
    $manager->getRepository(Product::class)->findBy(['']);
}

class QueryRepository extends \Doctrine\Bundle\DoctrineBundle\Repository\ServiceEntityRepository {
    public function __construct($registry) {
        parent::__construct($registry, Product::class);
    }
    public function query(): void {
        $qb = $this->createQueryBuilder('product');
        $qb->andWhere('product.');
    }
    public function fluentQuery(): void {
        $this->createQueryBuilder('product')->andWhere('product.');
    }
}
class ProductRepository {
    public function load(): void {
        $this->findByNa();
    }
}`
	path := "/project/src/Usage.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewDoctrineCompletionProvider(doctrineIndex)

	for _, test := range []struct {
		offset int
		label  string
	}{
		{strings.Index(source, "Pro);") + 2, "Product"},
		{strings.LastIndex(source, "''") + 1, "name"},
		{
			strings.Index(source, "product.'") + len("product."),
			"product.name",
		},
		{
			strings.LastIndex(source, "product.'") + len("product."),
			"product.name",
		},
		{strings.Index(source, "findByNa") + len("findByNa"), "findByName"},
	} {
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(test.offset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			consoleCompletionRequest(document, node),
		)
		requireCompletion(t, items, test.label)
	}
}

func TestDoctrineEntityCompletionIncludesLegacyNamespaceShortcuts(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	doctrineIndex.SetNamespaceAliasProvider(
		doctrineCompletionAliasProvider{aliases: map[string][]string{
			"LegacyBundle": {"App"},
		}},
	)
	source := `<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $manager->getRepository('LegacyBundle:Pro');
}`
	path := "/project/src/LegacyUsage.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(
		strings.Index(source, "LegacyBundle:Pro") +
			len("LegacyBundle:Pro"),
	)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	item := requireCompletion(
		t,
		NewDoctrineCompletionProvider(
			doctrineIndex,
			phpIndex,
		).GetCompletions(
			ctx,
			doctrineCompletionRequestAt(document, node, offset),
		),
		"LegacyBundle:Product",
	)
	assert.Contains(t, item.Detail, "App\\Product")
}

func TestPHPDocEntityAssistantTagCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/EntityAssistant.php",
		[]byte(`<?php
/** @param string $entity #Entity */
function resolve_entity(string $entity): void {}
`),
	)))
	source := "<?php resolve_entity('App\\Pro');"
	path := "/project/src/Usage.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.Index(source, "App\\Pro") + len("App\\Pro"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	item := requireCompletion(
		t,
		NewDoctrineCompletionProvider(
			doctrineIndex,
			phpIndex,
		).GetCompletions(
			ctx,
			doctrineCompletionRequestAt(document, node, offset),
		),
		"App\\Product",
	)
	edit, ok := item.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "App\\Product", edit.NewText)
	assert.Equal(
		t,
		"App\\Pro",
		completionRangeText(document, edit.Range),
	)
}

func TestDoctrineStandaloneDQLCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	functions := `<?php
namespace Doctrine\ORM\Query;
class Parser {
    private static $numericFunctions = [
        'min' => Functions\MinFunction::class,
    ];
}`
	functionFile := indexer.NewParsedFile(
		"/project/vendor/doctrine/orm/Parser.php",
		[]byte(functions),
	)
	require.NoError(t, phpIndex.Index(functionFile))
	require.NoError(t, doctrineIndex.Index(functionFile))
	source := `<?php
$dql = "SELECT p FROM App\\Product p WHERE p.";
$dql = "";
$dql = "SELECT ";
`
	path := "/project/src/Search.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)

	fieldOffset := uint32(strings.Index(source, `p."`) + len("p."))
	fieldNode := document.SyntaxTree.Root.NodeAtOffset(fieldOffset)
	fieldItems := provider.GetCompletions(
		context.Background(),
		doctrineCompletionRequestAt(document, fieldNode, fieldOffset),
	)
	requireCompletion(t, fieldItems, "p.name")

	entityOffset := uint32(strings.Index(source, "FROM App") + len("FROM App"))
	entityNode := document.SyntaxTree.Root.NodeAtOffset(entityOffset)
	entityItems := provider.GetCompletions(
		context.Background(),
		doctrineCompletionRequestAt(document, entityNode, entityOffset),
	)
	var entityItem *protocol.CompletionItem
	for position := range entityItems {
		if entityItems[position].Label == "App\\Product" {
			entityItem = &entityItems[position]
			break
		}
	}
	require.NotNil(t, entityItem)
	assert.Equal(t, int(protocol.ClassCompletion), entityItem.Kind)
	assert.Equal(t, `App\\Product`, entityItem.InsertText)

	emptyOffset := uint32(strings.LastIndex(source, `""`) + 1)
	emptyNode := document.SyntaxTree.Root.NodeAtOffset(emptyOffset)
	emptyItems := provider.GetCompletions(
		context.Background(),
		doctrineCompletionRequestAt(document, emptyNode, emptyOffset),
	)
	requireCompletion(t, emptyItems, "SELECT")
	for _, item := range emptyItems {
		if item.Label == "SELECT" {
			assert.Equal(t, int(protocol.KeywordCompletion), item.Kind)
		}
	}

	functionOffset := uint32(strings.LastIndex(source, "SELECT ") + len("SELECT "))
	functionNode := document.SyntaxTree.Root.NodeAtOffset(functionOffset)
	functionItems := provider.GetCompletions(
		context.Background(),
		doctrineCompletionRequestAt(document, functionNode, functionOffset),
	)
	functionItem := requireCompletion(t, functionItems, "MIN")
	assert.Equal(t, int(protocol.FunctionCompletion), functionItem.Kind)
	assert.Equal(t, "MIN(", functionItem.InsertText)
}

func TestDoctrineDBALCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	mapping := `<doctrine-mapping>
  <entity name="App\Product" table="cms_products">
    <field name="name" column="product_name" type="string"/>
  </entity>
</doctrine-mapping>`
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		"/project/config/Product.orm.xml",
		[]byte(mapping),
	)))
	stubs := `<?php
namespace Doctrine\DBAL;
class Connection {
    public function insert(string $table, array $data): void {}
}
namespace Doctrine\DBAL\Query;
class QueryBuilder {
    public function leftJoin(string $from, string $table, string $alias): self {}
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine-dbal.php",
		[]byte(stubs),
	)))
	source := `<?php
use Doctrine\DBAL\Connection;
use Doctrine\DBAL\Query\QueryBuilder;
function write(Connection $connection, QueryBuilder $builder): void {
    $connection->insert('cms_', []);
    $connection->insert('cms_products', ['']);
    $builder->leftJoin('p', 'cms_products', '');
}`
	path := "/project/src/Database.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)
	for _, test := range []struct {
		offset int
		label  string
		kind   protocol.CompletionItemKind
	}{
		{
			offset: strings.Index(source, "'cms_'") + len("'cms_"),
			label:  "cms_products",
			kind:   protocol.StructCompletion,
		},
		{
			offset: strings.Index(source, "['']") + 2,
			label:  "product_name",
			kind:   protocol.FieldCompletion,
		},
		{
			offset: strings.LastIndex(source, "''") + 1,
			label:  "p_cms_products",
			kind:   protocol.VariableCompletion,
		},
	} {
		offset := uint32(test.offset)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			doctrineCompletionRequestAt(document, node, offset),
		)
		item := requireCompletion(t, items, test.label)
		assert.Equal(t, int(test.kind), item.Kind)
	}
}

func doctrineCompletionRequestAt(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.CompletionRequest {
	request := consoleCompletionRequest(document, node)
	line, character := document.LineIndex.PositionUTF16(offset)
	request.Position.Line = int(line)
	request.Position.Character = int(character)
	return request
}

func TestDoctrineMappingCompletionAcrossXMLAndYAML(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	for path, source := range map[string]string{
		"/project/vendor/dbal.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		"/project/vendor/mongodb.php": `<?php
namespace Doctrine\ODM\MongoDB\Types;
abstract class Type {}`,
		"/project/src/Category.php": `<?php
namespace App;
class Category {}
class Status {}
class Product {
    private Status $status;
    private Category $category;
    public function prepare(): void {}
}
class ChildProduct extends Product {}
class ProductRepository {}
class MoneyType extends \Doctrine\DBAL\Types\Type {
    public function getName(): string { return 'currency_amount'; }
}
class MongoMoneyType extends \Doctrine\ODM\MongoDB\Types\Type {
    public function getName(): string { return 'mongo_currency'; }
}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		"/project/config/packages/doctrine.yaml",
		[]byte(`doctrine:
  dbal:
    types:
      configured_currency: App\MoneyType
`),
	)))
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)
	tests := []struct {
		path   string
		source string
		needle string
		label  string
	}{
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name=""/></doctrine-mapping>`,
			needle: `name="`,
			label:  "App\\Category",
		},
		{
			path:   "/project/config/Product.mongodb.xml",
			source: `<doctrine-mapping><embedded-document name=""/></doctrine-mapping>`,
			needle: `name="`,
			label:  "App\\Category",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product" repository-class=""/></doctrine-mapping>`,
			needle: `repository-class="`,
			label:  "App\\ProductRepository",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><field name=""/></entity></doctrine-mapping>`,
			needle: `<field name="`,
			label:  "status",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><field name="status" type=""/></entity></doctrine-mapping>`,
			needle: `type="`,
			label:  "string",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><many-to-one field="category" target-entity=""/></entity></doctrine-mapping>`,
			needle: `target-entity="`,
			label:  "App\\Category",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><embedded name="category" class=""/></entity></doctrine-mapping>`,
			needle: `class="`,
			label:  "App\\Category",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><field name="status" enum-type=""/></entity></doctrine-mapping>`,
			needle: `enum-type="`,
			label:  "App\\Status",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><lifecycle-callbacks><lifecycle-callback type="prePersist" method=""/></lifecycle-callbacks></entity></doctrine-mapping>`,
			needle: `method="`,
			label:  "prepare",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><field name="status" type="str"/></entity></doctrine-mapping>`,
			needle: `type="str`,
			label:  "string",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><field name="sta" type="string"/></entity></doctrine-mapping>`,
			needle: `name="sta`,
			label:  "status",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><many-to-one field="category" target-entity="App\Cat"/></entity></doctrine-mapping>`,
			needle: `target-entity="App\Cat`,
			label:  "App\\Category",
		},
		{
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><lifecycle-callbacks><lifecycle-callback type="prePersist" method="pre"/></lifecycle-callbacks></entity></doctrine-mapping>`,
			needle: `method="pre`,
			label:  "prepare",
		},
		{
			path: "/project/config/Product.orm.yaml",
			source: `App\Product:
  type: entity
  fields:
    status:
      type:
`,
			needle: "      type:",
			label:  "currency_amount",
		},
		{
			path: "/project/config/Product.orm.yaml",
			source: `App\Product:
  type: entity
  fields:
    status:
      type:
`,
			needle: "      type:",
			label:  "configured_currency",
		},
		{
			path: "/project/config/Product.mongodb.yaml",
			source: `App\Product:
  type: document
  fields:
    status:
      type:
`,
			needle: "      type:",
			label:  "mongo_currency",
		},
		{
			path: "/project/config/Product.orm.yaml",
			source: `App\Product:
  type: entity
  discriminatorMap:
    child:
`,
			needle: "    child:",
			label:  "App\\ChildProduct",
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+test.path,
			test.source,
			1,
		)
		offset := uint32(
			strings.Index(test.source, test.needle) + len(test.needle),
		)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					Language:        document.SyntaxLanguage,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node:            node,
				},
			},
		)
		requireCompletion(t, items, test.label)
		if strings.Contains(test.path, ".orm.") &&
			test.label == "currency_amount" {
			for _, item := range items {
				require.NotEqual(t, "mongo_currency", item.Label)
			}
		}
	}
}

func TestDoctrineTypeRegistrationClassCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	for path, source := range map[string]string{
		"/project/vendor/dbal.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		"/project/src/Types.php": `<?php
namespace App;
class Product {}
class MoneyType extends \Doctrine\DBAL\Types\Type {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)
	tests := []struct {
		path       string
		source     string
		offset     uint32
		label      string
		insertText string
	}{
		{
			path: "/project/config/packages/doctrine.yaml",
			source: `doctrine:
  dbal:
    types:
      money:
        class:
`,
			offset: uint32(len(`doctrine:
  dbal:
    types:
      money:
        class:`)),
			label: "App\\MoneyType",
		},
		{
			path:   "/project/config/packages/doctrine.xml",
			source: `<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>`,
			offset: uint32(strings.Index(`<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>`, `class=""`) + len(`class="`)),
			label:  "App\\MoneyType",
		},
		{
			path: "/project/config/packages/doctrine.php",
			source: `<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => '']],
    ]);
};`,
			offset: uint32(strings.Index(`<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => '']],
    ]);
};`, `=> '']`) + len(`=> '`)),
			label: "App\\MoneyType",
		},
		{
			path: "/project/config/packages/doctrine.php",
			source: `<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => ]],
    ]);
};`,
			offset: uint32(strings.Index(`<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => ]],
    ]);
};`, `=> ]]`) + len(`=> `)),
			label:      "MoneyType",
			insertText: `\App\MoneyType::class`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', );`, ")")),
			label:      "MoneyType",
			insertText: `\App\MoneyType::class`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', );`, ")")),
			label:      "MoneyType",
			insertText: `new \App\MoneyType()`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', new );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', new );`, ")")),
			label:      "MoneyType",
			insertText: `\App\MoneyType()`,
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+test.path,
			test.source,
			1,
		)
		node := document.SyntaxTree.Root.NodeAtOffset(test.offset)
		items := provider.GetCompletions(
			context.Background(),
			doctrineCompletionRequestAt(document, node, test.offset),
		)
		item := requireCompletion(t, items, test.label)
		require.Equal(t, "Doctrine DBAL type class", item.Detail)
		if test.insertText != "" {
			require.Equal(t, test.insertText, item.InsertText)
		}
		for _, candidate := range items {
			require.NotEqual(t, "App\\Product", candidate.Label)
		}
	}
}

func TestDoctrineTableConstraintCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)
	tests := []struct {
		name   string
		path   string
		source string
		needle string
		label  string
	}{
		{
			name: "PHP field",
			path: "/project/src/Product.php",
			source: `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\Index(fields: [''])]
class Product {
    #[ORM\Column]
    private string $name;
}`,
			needle: "fields: ['",
			label:  "name",
		},
		{
			name: "PHP column",
			path: "/project/src/Product.php",
			source: `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\UniqueConstraint(columns: [''])]
class Product {
    #[ORM\Column(name: 'email_address')]
    private string $email;
}`,
			needle: "columns: ['",
			label:  "email_address",
		},
		{
			name:   "XML column",
			path:   "/project/config/Product.orm.xml",
			source: `<doctrine-mapping><entity name="App\Product"><indexes><index columns=""/></indexes><field name="email" column="email_address"/></entity></doctrine-mapping>`,
			needle: `columns="`,
			label:  "email_address",
		},
		{
			name: "YAML field",
			path: "/project/config/Product.orm.yaml",
			source: `App\Product:
  type: entity
  indexes:
    search:
      fields: ['']
  fields:
    name: { type: string }
`,
			needle: "fields: ['",
			label:  "name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file://"+test.path,
				test.source,
				1,
			)
			offset := uint32(
				strings.Index(test.source, test.needle) +
					len(test.needle),
			)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			items := provider.GetCompletions(
				context.Background(),
				doctrineCompletionRequestAt(document, node, offset),
			)
			requireCompletion(t, items, test.label)
		})
	}
}

func doctrineCompletionFixture(
	t *testing.T,
) (*doctrine.Index, *php.PHPIndex) {
	t.Helper()
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine.php",
		[]byte(`<?php
namespace Doctrine\Persistence;
interface ObjectRepository { public function findBy(array $criteria): array; }
interface ObjectManager {
    public function getRepository(string $className): ObjectRepository;
}`),
	)))
	entity := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}`
	parsed := indexer.NewParsedFile("/project/src/Product.php", []byte(entity))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))
	return doctrineIndex, phpIndex
}
