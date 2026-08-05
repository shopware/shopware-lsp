package hover

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
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineHoverDescribesMappedField(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	entity := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column(name: 'product_name', type: 'string')]
    private string $name;
}`
	parsed := indexer.NewParsedFile("/project/src/Product.php", []byte(entity))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))
	source := `<?php
$manager->getRepository(\App\Product::class)->findBy(['name' => 'value']);`
	path := "/project/src/Usage.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.Index(source, "name") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDoctrineHoverProvider(doctrineIndex).GetHover(
		ctx,
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Doctrine field")
	assert.Contains(t, result.Contents.Value, "product_name")
	assert.Contains(t, result.Contents.Value, "string")
}

func TestDoctrineHoverDescribesQueryBuilderField(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	entity := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column(name: 'product_name', type: 'string')]
    private string $name;
}`
	parsed := indexer.NewParsedFile("/project/src/Product.php", []byte(entity))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))
	source := `<?php
namespace App;
class ProductRepository extends \Doctrine\Bundle\DoctrineBundle\Repository\ServiceEntityRepository {
    public function __construct($registry) {
        parent::__construct($registry, Product::class);
    }
    public function query(): void {
        $qb = $this->createQueryBuilder('p');
        $qb->andWhere('p.name = :name');
    }
}
$dql = 'SELECT p FROM App\Product p WHERE p.name = :name';`
	path := "/project/src/ProductRepository.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewDoctrineHoverProvider(doctrineIndex)
	for _, offset := range []uint32{
		uint32(strings.Index(source, "p.name") + len("p.") + 2),
		uint32(strings.LastIndex(source, "p.name") + len("p.") + 2),
	} {
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		result, hoverErr := provider.GetHover(
			ctx,
			&lsp.HoverRequest{
				HoverParams: params,
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
		require.NoError(t, hoverErr)
		require.NotNil(t, result)
		assert.Contains(t, result.Contents.Value, "App\\Product::name")
		assert.Contains(t, result.Contents.Value, "product_name")
	}

	entityOffset := uint32(strings.LastIndex(source, "App\\Product") + 3)
	entityNode := document.SyntaxTree.Root.NodeAtOffset(entityOffset)
	entityLine, entityCharacter := document.LineIndex.PositionUTF16(entityOffset)
	entityParams := &protocol.HoverParams{}
	entityParams.TextDocument.URI = document.URI
	entityParams.Position.Line = int(entityLine)
	entityParams.Position.Character = int(entityCharacter)
	entityHover, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: entityParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            entityNode,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, entityHover)
	assert.Contains(t, entityHover.Contents.Value, "Doctrine entity")
	assert.Contains(t, entityHover.Contents.Value, "App\\Product")
}

func TestDoctrineHoverDescribesDQLFunction(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	functions := `<?php
namespace Doctrine\ORM\Query;
class Parser {
    private static $numericFunctions = [
        'min' => Functions\MinFunction::class,
    ];
}`
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine/orm/Parser.php",
		[]byte(functions),
	)))
	source := `<?php
$dql = 'SELECT MIN(p.name) FROM App\Product p';`
	path := "/project/src/Search.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.Index(source, "MIN(") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, hoverErr := NewDoctrineHoverProvider(doctrineIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NoError(t, hoverErr)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Doctrine DQL function")
	assert.Contains(t, result.Contents.Value, "MIN()")
	assert.Contains(
		t,
		result.Contents.Value,
		"Doctrine\\ORM\\Query\\Functions\\MinFunction",
	)
}

func TestDoctrineHoverDescribesDBALTablesAndColumns(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
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
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine-dbal.php",
		[]byte(stubs),
	)))
	source := `<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
    $connection->insert('cms_products', ['product_name' => 'value']);
}`
	path := "/project/src/Database.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewDoctrineHoverProvider(doctrineIndex)
	for _, test := range []struct {
		needle   string
		contains []string
	}{
		{
			needle:   "cms_products",
			contains: []string{"Doctrine entity", "App\\Product", "cms_products"},
		},
		{
			needle: "product_name",
			contains: []string{
				"Doctrine field",
				"App\\Product::name",
				"product_name",
			},
		},
	} {
		offset := uint32(strings.Index(source, test.needle) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		result, hoverErr := provider.GetHover(
			ctx,
			&lsp.HoverRequest{
				HoverParams: params,
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
		require.NoError(t, hoverErr)
		require.NotNil(t, result)
		for _, value := range test.contains {
			assert.Contains(t, result.Contents.Value, value)
		}
	}
}

func TestDoctrineHoverDescribesExternalMappingValues(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Type.php",
		[]byte(`<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}
`),
	)))
	model := `<?php
namespace App;
class Product {
    private string $name;
}
class MoneyType extends \Doctrine\DBAL\Types\Type {
    public function getName(): string { return 'currency_amount'; }
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(model),
	)))
	source := `<doctrine-mapping><entity name="App\Product"><field name="name" type="currency_amount"/></entity></doctrine-mapping>`
	path := "/project/config/Product.orm.xml"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.Index(source, `name="name`) + len(`name="name`) - 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDoctrineHoverProvider(
		doctrineIndex,
		phpIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Doctrine mapped property")
	assert.Contains(t, result.Contents.Value, "App\\Product::$name")
	assert.Contains(t, result.Contents.Value, "string")

	typeOffset := uint32(strings.Index(source, "currency_amount") + 2)
	typeNode := document.SyntaxTree.Root.NodeAtOffset(typeOffset)
	typeLine, typeCharacter := document.LineIndex.PositionUTF16(typeOffset)
	typeParams := &protocol.HoverParams{}
	typeParams.TextDocument.URI = document.URI
	typeParams.Position.Line = int(typeLine)
	typeParams.Position.Character = int(typeCharacter)
	typeResult, err := NewDoctrineHoverProvider(
		doctrineIndex,
		phpIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: typeParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            typeNode,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, typeResult)
	assert.Contains(t, typeResult.Contents.Value, "currency_amount")
	assert.Contains(t, typeResult.Contents.Value, "App\\MoneyType")
}

func TestDoctrineHoverDescribesTypeRegistration(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := `<?php
use App\Doctrine\MoneyType;

return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => MoneyType::class]],
    ]);
};`
	document := lsp.NewTextDocument(
		"file:///project/config/packages/doctrine.php",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "'money'") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDoctrineHoverProvider(
		doctrineIndex,
		phpIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(
		t,
		result.Contents.Value,
		"Doctrine DBAL type registration",
	)
	require.Contains(t, result.Contents.Value, "money")
	require.Contains(t, result.Contents.Value, "App\\Doctrine\\MoneyType")
}

func TestDoctrineHoverDescribesTableConstraintColumn(t *testing.T) {
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
#[ORM\UniqueConstraint(columns: ['email_address'])]
class Product {
    #[ORM\Column(name: 'email_address')]
    private string $email;
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "'email_address']") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDoctrineHoverProvider(
		doctrineIndex,
		phpIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(
		t,
		result.Contents.Value,
		"table-constraint column",
	)
	require.Contains(t, result.Contents.Value, "App\\Product::$email")
}

func TestDoctrineHoverDescribesDiscriminatorMapping(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	source := "App\\ChildProduct"
	result := NewDoctrineHoverProvider(doctrineIndex).mappingHover(
		doctrine.MappingReference{
			Role:  doctrine.MappingDiscriminatorClass,
			Name:  "App\\ChildProduct",
			Owner: "App\\Product",
			Field: "child",
			Range: cst.TextRange{Start: 0, End: uint32(len(source))},
		},
		"/project/config/Product.orm.yaml",
		cst.NewLineIndex(source),
	)
	require.NotNil(t, result)
	require.Contains(t, result.Contents.Value, "discriminator class")
	require.Contains(t, result.Contents.Value, "child")
	require.Contains(t, result.Contents.Value, "App\\Product")
}
