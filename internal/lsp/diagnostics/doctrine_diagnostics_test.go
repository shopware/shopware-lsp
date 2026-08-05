package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type doctrineDiagnosticsAliasProvider struct {
	aliases map[string][]string
}

func (provider doctrineDiagnosticsAliasProvider) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	return provider.aliases, 1
}

func TestDoctrineDiagnosticsReportUnknownEntityAndField(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	stubs := `<?php
namespace Doctrine\Persistence;
interface ObjectRepository { public function findBy(array $criteria): array; }
interface ObjectManager { public function getRepository(string $class): ObjectRepository; }`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine.php",
		[]byte(stubs),
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
	doctrineIndex.SetNamespaceAliasProvider(
		doctrineDiagnosticsAliasProvider{aliases: map[string][]string{
			"LegacyBundle": {"App"},
		}},
	)

	source := []byte(`<?php
namespace App;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void {
    $manager->getRepository('LegacyBundle:Prodcut');
    $manager->getRepository('LegacyBundle:Product');
    $manager->getRepository(Product::class)->findBy(['naem' => 'value']);
    $manager->getRepository(Product::class)->findBy(['name' => 'value']);
}
class ProductRepository {
    public function load(): void {
        $this->findByNaem('value');
    }
}`)
	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file:///project/src/Usage.php", source),
	)
	require.NoError(t, err)
	require.Len(t, result, 3)
	codes := []any{result[0].ID, result[1].ID, result[2].ID}
	assert.Contains(t, codes, missingDoctrineEntityCode)
	assert.Contains(t, codes, missingDoctrineFieldCode)
	assert.Contains(t, codes, missingDoctrineMagicFieldCode)
	for _, diagnostic := range result {
		suggestions := diagnostic.Payload.(map[string]any)["suggestions"]
		switch diagnostic.ID {
		case missingDoctrineEntityCode:
			assert.Contains(t, suggestions, "LegacyBundle:Product")
		case missingDoctrineFieldCode:
			assert.Contains(t, suggestions, "name")
		default:
			assert.Contains(t, suggestions, "findByName")
		}
	}
}

func TestDoctrineDiagnosticsValidatePHPDocEntityAssistantReferences(
	t *testing.T,
) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	entity := indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(`<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {}
`),
	)
	require.NoError(t, phpIndex.Index(entity))
	require.NoError(t, doctrineIndex.Index(entity))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/EntityAssistant.php",
		[]byte(`<?php
namespace App;
/** @param string $entity #Entity */
function resolve_entity(string $entity): void {}
`),
	)))

	document := lsp.NewTextDocument(
		"file:///project/src/Usage.php",
		`<?php
namespace App;
resolve_entity('App\Prodcut');
resolve_entity('App\Product');
`,
		1,
	)
	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingDoctrineEntityCode, result[0].ID)
	assert.Equal(
		t,
		"App\\Prodcut",
		problemRangeText(document, result[0].Range),
	)
	assert.Contains(
		t,
		problemSuggestionStrings(result[0]),
		"App\\Product",
	)
}

func TestDoctrineDiagnosticsValidateStandaloneDQL(t *testing.T) {
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
    #[ORM\Column(type: 'string')]
    private string $name;
}`
	parsed := indexer.NewParsedFile("/project/src/Product.php", []byte(entity))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))

	source := []byte(`<?php
$dql = "SELECT p FROM App\\Prodcut p";
$dql = 'SELECT p FROM App\Product p WHERE p.naem = :name';
$dynamic = "name";
$dql = "SELECT p FROM App\\Product p WHERE p.name = $dynamic";
`)
	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file:///project/src/Search.php", source),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range result {
		codes[diagnostic.ID] = diagnostic
	}
	entityDiagnostic, found := codes[missingDoctrineEntityCode]
	require.True(t, found)
	assert.Contains(
		t,
		entityDiagnostic.Payload.(map[string]any)["suggestions"],
		`App\\Product`,
	)
	fieldDiagnostic, found := codes[missingDoctrineFieldCode]
	require.True(t, found)
	assert.Contains(
		t,
		fieldDiagnostic.Payload.(map[string]any)["suggestions"],
		"name",
	)
}

func TestDoctrineDiagnosticsValidateDBALTablesAndColumns(t *testing.T) {
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
	source := []byte(`<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
    $connection->insert('cms_prodcts', []);
    $connection->insert('cms_products', ['prodct_name' => 'value']);
}`)
	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file:///project/src/Database.php", source),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range result {
		codes[diagnostic.ID] = diagnostic
	}
	tableDiagnostic, found := codes[missingDoctrineTableCode]
	require.True(t, found)
	assert.Contains(
		t,
		tableDiagnostic.Payload.(map[string]any)["suggestions"],
		"cms_products",
	)
	columnDiagnostic, found := codes[missingDoctrineColumnCode]
	require.True(t, found)
	assert.Contains(
		t,
		columnDiagnostic.Payload.(map[string]any)["suggestions"],
		"product_name",
	)
}

func TestDoctrineDiagnosticsValidateExternalMappings(t *testing.T) {
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
	models := `<?php
namespace App;
class Status {}
class Category {}
class ProductRepository {}
class MoneyType extends \Doctrine\DBAL\Types\Type {
    public function getName(): string { return 'currency_amount'; }
}
class Product {
    private Status $status;
    private Category $category;
    public function prepare(): void {}
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Models.php",
		[]byte(models),
	)))
	mapping := []byte(`<doctrine-mapping>
  <entity name="App\Product" repository-class="App\ProductRepository">
    <field name="sttaus" type="currency_amunt" enum-type="App\Status"/>
    <many-to-one field="category" target-entity="App\Catgory"/>
    <lifecycle-callbacks>
      <lifecycle-callback type="prePersist" method="preprae"/>
    </lifecycle-callbacks>
  </entity>
</doctrine-mapping>`)
	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/Product.orm.xml",
			mapping,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 4)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range result {
		codes[diagnostic.ID] = diagnostic
	}
	for _, code := range []lsp.DiagnosticID{
		missingDoctrineMappingClass,
		missingDoctrinePropertyCode,
		missingDoctrineCallbackCode,
		unknownDoctrineTypeCode,
	} {
		assert.Contains(t, codes, code)
	}
	assert.Contains(
		t,
		codes[missingDoctrineMappingClass].Payload.(map[string]any)["suggestions"],
		"App\\Category",
	)
	assert.Contains(
		t,
		codes[missingDoctrinePropertyCode].Payload.(map[string]any)["suggestions"],
		"status",
	)
	assert.Contains(
		t,
		codes[missingDoctrineCallbackCode].Payload.(map[string]any)["suggestions"],
		"prepare",
	)
	assert.Contains(
		t,
		codes[unknownDoctrineTypeCode].Payload.(map[string]any)["suggestions"],
		"currency_amount",
	)
}

func TestDoctrineDiagnosticsRespectManagerScopedAndConfiguredTypes(
	t *testing.T,
) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/types.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}
namespace Doctrine\ODM\MongoDB\Types;
abstract class Type {}`,
		"/project/src/Models.php": `<?php
namespace App;
class Product {
    private string $configured;
    private string $mongo;
}
class MoneyType extends \Doctrine\DBAL\Types\Type {}
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
	diagnostics, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/Product.orm.xml",
			[]byte(`<doctrine-mapping>
  <entity name="App\Product">
    <field name="configured" type="configured_currency"/>
    <field name="mongo" type="mongo_currency"/>
  </entity>
</doctrine-mapping>`),
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	require.Equal(t, unknownDoctrineTypeCode, diagnostics[0].ID)
	require.Contains(t, diagnostics[0].Message, "mongo_currency")
}

func TestDoctrineDiagnosticsValidateTypeRegistrationClasses(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/Type.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		"/project/src/Types.php": `<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}
class NotAType {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	diagnostics, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/packages/doctrine.php",
			[]byte(`<?php
use Doctrine\DBAL\Types\Type;

Type::addType('valid', \App\Doctrine\MoneyType::class);
Type::addType('missing', \App\Doctrine\MonyType::class);
Type::overrideType('invalid', \App\Doctrine\NotAType::class);`),
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 2)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range diagnostics {
		codes[diagnostic.ID] = diagnostic
	}
	require.Contains(t, codes, missingDoctrineTypeClassCode)
	require.Contains(t, codes, invalidDoctrineTypeClassCode)
	require.Contains(
		t,
		codes[missingDoctrineTypeClassCode].
			Payload.(map[string]any)["suggestions"],
		"App\\Doctrine\\MoneyType",
	)
}

func TestDoctrineDiagnosticsValidateTableConstraintMembers(t *testing.T) {
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
#[ORM\Index(fields: ['nmae'])]
#[ORM\UniqueConstraint(columns: ['email_adress'])]
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
	diagnostics, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(path),
			[]byte(source),
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 2)
	byCode := make(map[any]lsp.Problem)
	for _, diagnostic := range diagnostics {
		byCode[diagnostic.ID] = diagnostic
	}
	require.Contains(t, byCode, missingDoctrineConstraintFieldCode)
	require.Contains(t, byCode, missingDoctrineConstraintColumnCode)
	require.Contains(
		t,
		byCode[missingDoctrineConstraintFieldCode].
			Payload.(map[string]any)["suggestions"],
		"name",
	)
	require.Contains(
		t,
		byCode[missingDoctrineConstraintColumnCode].
			Payload.(map[string]any)["suggestions"],
		"email_address",
	)
}

func TestDoctrineDiagnosticsValidateDiscriminatorClasses(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Models.php",
		[]byte(`<?php
namespace App;
class BaseModel {}
class ChildModel extends BaseModel {}
class OtherModel {}
`),
	)))
	diagnostics, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/BaseModel.orm.yaml",
			[]byte(`App\BaseModel:
  type: entity
  discriminatorMap:
    valid: App\ChildModel
    missing: App\ChldModel
    invalid: App\OtherModel
`),
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 2)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range diagnostics {
		codes[diagnostic.ID] = diagnostic
	}
	require.Contains(t, codes, missingDoctrineMappingClass)
	require.Contains(t, codes, invalidDoctrineDiscriminatorClassCode)
	require.Contains(
		t,
		codes[missingDoctrineMappingClass].
			Payload.(map[string]any)["suggestions"],
		"App\\ChildModel",
	)
}
