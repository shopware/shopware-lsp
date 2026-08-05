package doctrine

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineCandidateSearchIsCaseInsensitiveWithoutNormalization(t *testing.T) {
	t.Parallel()

	require.NotZero(
		t,
		doctrineCandidates(indexer.NewParsedFile(
			"/project/Query.php",
			[]byte(`$manager->CrEaTeQuErY("SELECT p FROM Product p")`),
		))&doctrineDQLCandidate,
	)
	require.NotZero(
		t,
		doctrineCandidates(indexer.NewParsedFile(
			"/project/doctrine.yaml",
			[]byte("DOCTRINE:\n  DBAL:\n    TYPES:\n"),
		))&doctrineTypeCandidate,
	)
	require.NotZero(
		t,
		doctrineCandidates(indexer.NewParsedFile(
			"/project/doctrine.xml",
			[]byte(`<DBAL><Doctrine:Type name="money"/></DBAL>`),
		))&doctrineTypeCandidate,
	)
	require.Zero(
		t,
		doctrineCandidates(indexer.NewParsedFile(
			"/project/unrelated.php",
			[]byte(`<?php echo "types";`),
		))&doctrineTypeCandidate,
	)
}

func TestDoctrineCandidateMatcherCollectsAllFeatureFamilies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path    string
		content string
		want    doctrineCandidateFlags
	}{
		{
			path:    "/project/Entity.php",
			content: `use Doctrine\ORM\Mapping as ORM; #[ORM\Entity]`,
			want:    doctrineMappingCandidate,
		},
		{
			path:    "/project/Query.php",
			content: `$query = $em->CrEaTeQuErY("SELECT p FROM Product p");`,
			want:    doctrineDQLCandidate,
		},
		{
			path:    "/project/doctrine.php",
			content: `numericFunctions: ['distance' => Distance::class]`,
			want:    doctrineDQLFunctionCandidate,
		},
		{
			path:    "/project/types.php",
			content: `Type::OvErRiDeTyPe('money', MoneyType::class);`,
			want:    doctrineTypeCandidate,
		},
		{
			path: "/project/extension.php",
			content: `final class Extension { // DOCTRINE DBAL TYPES
			}`,
			want: doctrineTypeCandidate,
		},
		{
			path:    "/project/unrelated.php",
			content: `<?php echo "types";`,
			want:    0,
		},
		{
			path:    "/project/entity.yaml",
			content: "App.Entity:\n  TyPe: EmBeDdAbLe\n",
			want:    doctrineMappingCandidate,
		},
		{
			path:    "/project/doctrine.yml",
			content: "DOCTRINE:\n  DBAL:\n    TYPES:\n",
			want:    doctrineTypeCandidate,
		},
		{
			path:    "/project/mapping.xml",
			content: `<doctrine-mapping><entity/></doctrine-mapping>`,
			want:    doctrineMappingCandidate,
		},
		{
			path:    "/project/form.php",
			content: `use Symfony\Component\Form\FormTypeInterface;`,
			want:    0,
		},
		{
			path:    "/project/mixed-mapping.xml",
			content: `<DoCtRiNe-MaPpInG/>`,
			want:    0,
		},
		{
			path:    "/project/types.xml",
			content: `<DBAL><Doctrine:Type name="money"/></DBAL>`,
			want:    doctrineTypeCandidate,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			file := indexer.NewParsedFile(
				test.path,
				[]byte(test.content),
			)
			require.Equal(t, test.want, doctrineCandidates(file))
		})
	}
}

func TestDoctrineCandidateMatcherDoesNotAllocateWhileScanning(t *testing.T) {
	content := bytes.Repeat([]byte("ordinary application source\n"), 1024)
	content = append(content, []byte("CrEaTeQuErY")...)
	file := indexer.NewParsedFile("/project/Query.php", content)
	require.Zero(t, testing.AllocsPerRun(1000, func() {
		if doctrineCandidates(file)&doctrineDQLCandidate == 0 {
			panic("DQL marker was not found")
		}
	}))
}

func BenchmarkDoctrineCandidateMatcherPair(b *testing.B) {
	content := bytes.Repeat([]byte("ordinary application source\n"), 4096)
	content = append(content, []byte("CrEaTeQuErY ORM\\")...)

	b.Run("fused", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if matchDoctrineCandidatePair(
				content,
				doctrinePHPExactCandidates,
				doctrinePHPFoldCandidates,
			) == 0 {
				b.Fatal("candidate markers were not found")
			}
		}
	})
	b.Run("separate", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if doctrinePHPExactCandidates.match(content)|
				doctrinePHPFoldCandidates.match(content) == 0 {
				b.Fatal("candidate markers were not found")
			}
		}
	})
}

func TestDoctrineCandidateMatcherMatchesReferenceScreening(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	alphabet := []byte(
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" +
			"0123456789\\<@#$:[] \n",
	)
	markers := []string{
		"ORM\\",
		"Form\\",
		"#[Entity",
		"CrEaTeQuErY",
		"numericFunctions",
		"NuMeRiCfUnCtIoNs",
		"overrideTYPE",
		"Extension Doctrine DBAL Types",
		"type: EMBEDDABLE",
		"DOCTRINE: DBAL: TYPES:",
		"<entity",
		"<EnTiTy",
		"<DBAL><Doctrine:Type",
	}
	paths := []string{
		"/project/source.php",
		"/project/source.yaml",
		"/project/source.yml",
		"/project/source.xml",
		"/project/source.txt",
	}
	for iteration := 0; iteration < 2000; iteration++ {
		content := make([]byte, random.Intn(512))
		for index := range content {
			content[index] = alphabet[random.Intn(len(alphabet))]
		}
		if iteration%2 == 0 {
			content = append(
				content,
				markers[random.Intn(len(markers))]...,
			)
		}
		file := indexer.NewParsedFile(
			paths[iteration%len(paths)],
			content,
		)
		require.Equal(
			t,
			referenceDoctrineCandidates(file),
			doctrineCandidates(file),
			"iteration %d, path %s, content %q",
			iteration,
			file.Path,
			content,
		)
	}
}

func referenceDoctrineCandidates(
	file *indexer.ParsedFile,
) doctrineCandidateFlags {
	if file == nil {
		return 0
	}
	containsExact := func(patterns ...string) bool {
		for _, pattern := range patterns {
			if bytes.Contains(file.Content, []byte(pattern)) {
				return true
			}
		}
		return false
	}
	lower := bytes.ToLower(file.Content)
	containsFold := func(patterns ...string) bool {
		for _, pattern := range patterns {
			if bytes.Contains(
				lower,
				[]byte(strings.ToLower(pattern)),
			) {
				return true
			}
		}
		return false
	}

	var result doctrineCandidateFlags
	switch file.Extension() {
	case ".php":
		if containsExact(
			"ORM\\",
			"ODM\\",
			"Doctrine\\ORM\\Mapping",
			"Doctrine\\ODM\\",
			"@Entity",
			"@Document",
			"#[Entity",
			"#[Document",
		) {
			result |= doctrineMappingCandidate
		}
		if containsExact("$dql") ||
			containsFold("createquery", "setdql") {
			result |= doctrineDQLCandidate
		}
		if containsExact(
			"stringFunctions",
			"numericFunctions",
			"datetimeFunctions",
		) {
			result |= doctrineDQLFunctionCandidate
		}
		if containsFold(
			"addtype",
			"overridetype",
			"gettyperegistry",
		) || containsFold("extension") &&
			containsFold("doctrine") &&
			containsFold("dbal") &&
			containsFold("types") {
			result |= doctrineTypeCandidate
		}
	case ".yaml", ".yml":
		if containsFold(
			"type: entity",
			"type: embeddable",
			"repositoryclass:",
			"targetentity:",
		) {
			result |= doctrineMappingCandidate
		}
		if containsFold("doctrine:") &&
			containsFold("dbal:") &&
			containsFold("types:") {
			result |= doctrineTypeCandidate
		}
	case ".xml":
		if containsExact(
			"doctrine-mapping",
			"<entity",
			"<document",
		) {
			result |= doctrineMappingCandidate
		}
		if containsFold("dbal") &&
			containsFold("<doctrine:type", "<type") {
			result |= doctrineTypeCandidate
		}
	}
	return result
}

func TestIndexCollectsAttributeAnnotationXMLAndYAMLMetadata(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	phpSource := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
use App\Repository\ProductRepository;

#[ORM\Embeddable]
class Address {
    #[ORM\Column(name: 'city_name', type: 'string')]
    private string $city;
}

#[ORM\Entity(repositoryClass: ProductRepository::class)]
#[ORM\Table(name: 'products')]
#[ORM\HasLifecycleCallbacks]
class Product {
    #[ORM\Id]
    #[ORM\Column(type: 'integer')]
    private int $id;

    #[ORM\ManyToOne(targetEntity: Category::class)]
    private ?Category $category = null;

    #[ORM\Embedded(class: Address::class, columnPrefix: 'shipping_')]
    private Address $shippingAddress;

    #[ORM\PrePersist]
    public function initialize(): void {}
}

/**
 * @ORM\Entity(repositoryClass="App\Repository\LegacyRepository")
 * @ORM\Table(name="legacy_products")
 */
class LegacyProduct {
    /** @ORM\Column(type="string", name="legacy_name") */
    private $name;
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/Entity/Product.php",
		[]byte(phpSource),
	)))

	xmlSource := `<doctrine-mapping>
  <entity name="App\Entity\XmlOrder" table="orders" repository-class="App\Repository\OrderRepository">
    <id name="id" type="integer"/>
    <many-to-one field="customer" target-entity="Customer"/>
    <lifecycle-callbacks>
      <lifecycle-callback type="prePersist" method="prepare"/>
    </lifecycle-callbacks>
  </entity>
  <embedded-document name="App\Document\Address">
    <field name="city" type="string"/>
  </embedded-document>
  <embedded name="App\Document\Profile">
    <field name="label" type="string"/>
  </embedded>
</doctrine-mapping>`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/doctrine/XmlOrder.orm.xml",
		[]byte(xmlSource),
	)))

	yamlSource := `App\Entity\YamlInvoice:
  type: entity
  table: invoices
  repositoryClass: App\Repository\InvoiceRepository
  id:
    id:
      type: uuid
  fields:
    number:
      type: string
      column: invoice_number
  manyToOne:
    customer:
      targetEntity: Customer
  lifecycleCallbacks:
    prePersist: [initialize]
`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/doctrine/YamlInvoice.orm.yaml",
		[]byte(yamlSource),
	)))

	product, found, err := idx.Model("App\\Entity\\Product")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "products", product.Table)
	assert.Equal(t, "App\\Repository\\ProductRepository", product.Repository)
	require.Len(t, product.Callbacks, 1)
	assert.Equal(t, "initialize", product.Callbacks[0].Method)

	fields, err := idx.Fields(product.Class)
	require.NoError(t, err)
	requireField(t, fields, "id", "integer", "")
	requireField(t, fields, "category", "", "App\\Entity\\Category")
	embedded := requireField(
		t,
		fields,
		"shippingAddress.city",
		"string",
		"",
	)
	assert.Equal(t, "shipping_city_name", embedded.Column)

	legacy, found, err := idx.Model("App\\Entity\\LegacyProduct")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "legacy_products", legacy.Table)
	assert.Equal(t, "App\\Repository\\LegacyRepository", legacy.Repository)
	requireField(t, legacy.Fields, "name", "string", "")

	xml, found, err := idx.Model("App\\Entity\\XmlOrder")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "orders", xml.Table)
	assert.Equal(t, "App\\Repository\\OrderRepository", xml.Repository)
	requireField(t, xml.Fields, "customer", "", "App\\Entity\\Customer")
	require.Len(t, xml.Callbacks, 1)
	for className, fieldName := range map[string]string{
		"App\\Document\\Address": "city",
		"App\\Document\\Profile": "label",
	} {
		embeddedDocument, embeddedFound, embeddedErr := idx.Model(className)
		require.NoError(t, embeddedErr)
		require.True(t, embeddedFound)
		assert.Equal(t, EmbeddableModel, embeddedDocument.Kind)
		requireField(t, embeddedDocument.Fields, fieldName, "string", "")
	}

	yaml, found, err := idx.Model("App\\Entity\\YamlInvoice")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "invoices", yaml.Table)
	assert.Equal(t, "App\\Repository\\InvoiceRepository", yaml.Repository)
	requireField(t, yaml.Fields, "number", "string", "")
	requireField(t, yaml.Fields, "customer", "", "App\\Entity\\Customer")
	require.Len(t, yaml.Callbacks, 1)
}

func TestIndexRemovalAndPersistence(t *testing.T) {
	cache := t.TempDir()
	idx, err := NewIndex(cache)
	require.NoError(t, err)
	source := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}`
	path := "/project/src/Product.php"
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cache)
	require.NoError(t, err)
	model, found, err := restored.Model("App\\Product")
	require.NoError(t, err)
	require.True(t, found)
	requireField(t, model.Fields, "name", "string", "")
	require.NoError(t, restored.RemovedFiles([]string{path}))
	_, found, err = restored.Model("App\\Product")
	require.NoError(t, err)
	assert.False(t, found)
	require.NoError(t, restored.Close())
}

func TestModelsPreserveEmptyCollectionsAcrossCacheAndRestore(t *testing.T) {
	cache := t.TempDir()
	path := "/project/config/doctrine/Empty.orm.xml"
	source := `<doctrine-mapping>
  <entity name="App\Entity\Empty"/>
</doctrine-mapping>`

	idx, err := NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	first, err := idx.Models()
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NotNil(t, first[0].Fields)
	require.Empty(t, first[0].Fields)

	cached, err := idx.Models()
	require.NoError(t, err)
	require.Equal(t, first, cached)
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	reopened, err := restored.Models()
	require.NoError(t, err)
	require.Equal(t, first, reopened)
}

func TestIndexParsesDiscriminatorMapsAcrossFormats(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	sources := map[string]string{
		"/project/src/AttributeBase.php": `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\InheritanceType('SINGLE_TABLE')]
#[ORM\DiscriminatorColumn(name: 'kind', type: 'string')]
#[ORM\DiscriminatorMap([
    'base' => AttributeBase::class,
    'child' => AttributeChild::class,
])]
class AttributeBase {}
class AttributeChild extends AttributeBase {}
`,
		"/project/src/AnnotationBase.php": `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
/**
 * @ORM\Entity
 * @ORM\InheritanceType("JOINED")
 * @ORM\DiscriminatorMap({"base" = "AnnotationBase", "child" = "AnnotationChild"})
 */
class AnnotationBase {}
class AnnotationChild extends AnnotationBase {}
`,
		"/project/config/XmlBase.orm.xml": `<doctrine-mapping>
  <entity name="App\Entity\XmlBase" inheritance-type="SINGLE_TABLE">
    <discriminator-column name="kind" type="string"/>
    <discriminator-map>
      <discriminator-mapping value="base" class="App\Entity\XmlBase"/>
      <discriminator-mapping value="child" class="App\Entity\XmlChild"/>
    </discriminator-map>
  </entity>
</doctrine-mapping>`,
		"/project/config/YamlBase.orm.yaml": `App\Entity\YamlBase:
  type: entity
  inheritanceType: JOINED
  discriminatorColumn:
    name: kind
    type: string
  discriminatorMap:
    base: App\Entity\YamlBase
    child: App\Entity\YamlChild
`,
	}
	for path, source := range sources {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	for _, test := range []struct {
		class       string
		inheritance string
		column      string
		child       string
	}{
		{
			class:       "App\\Entity\\AttributeBase",
			inheritance: "SINGLE_TABLE",
			column:      "kind",
			child:       "App\\Entity\\AttributeChild",
		},
		{
			class:       "App\\Entity\\AnnotationBase",
			inheritance: "JOINED",
			child:       "App\\Entity\\AnnotationChild",
		},
		{
			class:       "App\\Entity\\XmlBase",
			inheritance: "SINGLE_TABLE",
			column:      "kind",
			child:       "App\\Entity\\XmlChild",
		},
		{
			class:       "App\\Entity\\YamlBase",
			inheritance: "JOINED",
			column:      "kind",
			child:       "App\\Entity\\YamlChild",
		},
	} {
		model, found, modelErr := idx.Model(test.class)
		require.NoError(t, modelErr)
		require.True(t, found, test.class)
		require.Equal(t, test.inheritance, model.InheritanceType)
		require.Equal(t, test.column, model.DiscriminatorColumn)
		require.Len(t, model.DiscriminatorMap, 2)
		require.Equal(t, "child", model.DiscriminatorMap[1].Value)
		require.Equal(t, test.child, model.DiscriminatorMap[1].Class)
		require.NotZero(t, model.DiscriminatorMap[1].ClassRange.Len())
	}
}

func requireField(
	t *testing.T,
	fields []Field,
	name,
	fieldType,
	relation string,
) Field {
	t.Helper()
	for _, field := range fields {
		if field.Name != name {
			continue
		}
		if fieldType != "" {
			assert.Equal(t, fieldType, field.Type)
		}
		if relation != "" {
			assert.Equal(t, relation, field.Relation)
		}
		return field
	}
	require.Failf(t, "missing Doctrine field", "%s in %#v", name, fields)
	return Field{}
}
