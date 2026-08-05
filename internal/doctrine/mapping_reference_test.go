package doctrine

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMappingReferencesCoverPHPXMLAndYAMLValues(t *testing.T) {
	tests := []struct {
		path   string
		source string
		values map[MappingReferenceRole]string
	}{
		{
			path: "/project/src/Product.php",
			source: `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: ProductRepository::class)]
class Product {
    #[ORM\Column(type: 'strng', enumType: Status::class)]
    private Status $status;
    #[ORM\ManyToOne(targetEntity: Category::class)]
    private Category $category;
}`,
			values: map[MappingReferenceRole]string{
				MappingRepositoryClass: "App\\ProductRepository",
				MappingTargetClass:     "App\\Category",
				MappingEnumClass:       "App\\Status",
				MappingType:            "strng",
			},
		},
		{
			path: "/project/src/LegacyProduct.php",
			source: `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
/** @ORM\Entity(repositoryClass="ProductRepository") */
class LegacyProduct {
    /** @ORM\Column(type="strng", enumType="Status") */
    private $status;
    /** @ORM\ManyToOne(targetEntity="Category") */
    private $category;
}`,
			values: map[MappingReferenceRole]string{
				MappingRepositoryClass: "App\\ProductRepository",
				MappingTargetClass:     "App\\Category",
				MappingEnumClass:       "App\\Status",
				MappingType:            "strng",
			},
		},
		{
			path: "/project/config/Product.orm.xml",
			source: `<doctrine-mapping>
  <entity name="App\Product" repository-class="App\ProductRepository">
    <field name="status" type="strng" enum-type="App\Status"/>
    <many-to-one field="category" target-entity="App\Category"/>
    <lifecycle-callbacks>
      <lifecycle-callback type="prePersist" method="prepare"/>
    </lifecycle-callbacks>
  </entity>
</doctrine-mapping>`,
			values: map[MappingReferenceRole]string{
				MappingModelClass:      "App\\Product",
				MappingRepositoryClass: "App\\ProductRepository",
				MappingTargetClass:     "App\\Category",
				MappingEnumClass:       "App\\Status",
				MappingType:            "strng",
				MappingProperty:        "status",
				MappingLifecycleMethod: "prepare",
			},
		},
		{
			path: "/project/config/Product.orm.yaml",
			source: `App\Product:
  type: entity
  repositoryClass: App\ProductRepository
  fields:
    status:
      type: strng
      enumType: App\Status
  manyToOne:
    category:
      targetEntity: App\Category
  lifecycleCallbacks:
    prePersist: [prepare]
`,
			values: map[MappingReferenceRole]string{
				MappingModelClass:      "App\\Product",
				MappingRepositoryClass: "App\\ProductRepository",
				MappingTargetClass:     "App\\Category",
				MappingEnumClass:       "App\\Status",
				MappingType:            "strng",
				MappingProperty:        "status",
				MappingLifecycleMethod: "prepare",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			parsed := indexer.NewParsedFile(
				test.path,
				[]byte(test.source),
			)
			tree := parsed.SyntaxTree()
			require.NotNil(t, tree)
			references := MappingReferencesInDocument(
				test.path,
				tree.Root,
				test.source,
			)
			for role, expected := range test.values {
				found := false
				for _, reference := range references {
					if reference.Role != role ||
						reference.Name != expected {
						continue
					}
					found = true
					require.NotZero(t, reference.Range.Len())
					sourceValue := test.source[reference.Range.Start:reference.Range.End]
					assert.Contains(
						t,
						strings.ToLower(sourceValue),
						strings.ToLower(shortMappingValue(expected)),
					)
					break
				}
				assert.Truef(
					t,
					found,
					"missing role %d=%s in %#v",
					role,
					expected,
					references,
				)
			}
		})
	}
}

func shortMappingValue(value string) string {
	if position := strings.LastIndex(value, `\`); position >= 0 {
		return value[position+1:]
	}
	return value
}

func TestEmptyYAMLMappingTypeReference(t *testing.T) {
	source := `App\Product:
  type: entity
  fields:
    status:
      type:
`
	path := "/project/config/Product.orm.yaml"
	parsed := indexer.NewParsedFile(path, []byte(source))
	tree := parsed.SyntaxTree()
	require.NotNil(t, tree)
	offset := uint32(strings.LastIndex(source, "type:") + len("type:"))
	reference, found := MappingReferenceAt(
		path,
		tree.Root,
		source,
		offset,
	)
	require.True(t, found)
	assert.Equal(t, MappingType, reference.Role)
	assert.Equal(t, "App\\Product", reference.Owner)
}

func TestXMLMappingReferenceRecoversEmptyAttributeContexts(t *testing.T) {
	tests := []struct {
		name   string
		source string
		needle string
		role   MappingReferenceRole
		owner  string
	}{
		{
			name:   "model",
			source: `<doctrine-mapping><entity name=""/></doctrine-mapping>`,
			needle: `name="`,
			role:   MappingModelClass,
		},
		{
			name:   "embedded document model",
			source: `<doctrine-mapping><embedded-document name=""/></doctrine-mapping>`,
			needle: `name="`,
			role:   MappingModelClass,
		},
		{
			name:   "repository",
			source: `<doctrine-mapping><entity name="App\Product" repository-class=""/></doctrine-mapping>`,
			needle: `repository-class="`,
			role:   MappingRepositoryClass,
			owner:  "App\\Product",
		},
		{
			name:   "property",
			source: `<doctrine-mapping><entity name="App\Product"><field name=""/></entity></doctrine-mapping>`,
			needle: `<field name="`,
			role:   MappingProperty,
			owner:  "App\\Product",
		},
		{
			name:   "type",
			source: `<doctrine-mapping><entity name="App\Product"><field name="status" type=""/></entity></doctrine-mapping>`,
			needle: `type="`,
			role:   MappingType,
			owner:  "App\\Product",
		},
		{
			name:   "target",
			source: `<doctrine-mapping><entity name="App\Product"><many-to-one field="category" target-entity=""/></entity></doctrine-mapping>`,
			needle: `target-entity="`,
			role:   MappingTargetClass,
			owner:  "App\\Product",
		},
		{
			name:   "embedded class",
			source: `<doctrine-mapping><entity name="App\Product"><embedded name="address" class=""/></entity></doctrine-mapping>`,
			needle: `class="`,
			role:   MappingEmbeddedClass,
			owner:  "App\\Product",
		},
		{
			name:   "enum",
			source: `<doctrine-mapping><entity name="App\Product"><field name="status" enum-type=""/></entity></doctrine-mapping>`,
			needle: `enum-type="`,
			role:   MappingEnumClass,
			owner:  "App\\Product",
		},
		{
			name:   "lifecycle",
			source: `<doctrine-mapping><entity name="App\Product"><lifecycle-callbacks><lifecycle-callback type="prePersist" method=""/></lifecycle-callbacks></entity></doctrine-mapping>`,
			needle: `method="`,
			role:   MappingLifecycleMethod,
			owner:  "App\\Product",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "/project/config/Product.orm.xml"
			parsed := indexer.NewParsedFile(path, []byte(test.source))
			offset := uint32(
				strings.Index(test.source, test.needle) +
					len(test.needle),
			)
			reference, found := MappingReferenceAt(
				path,
				parsed.SyntaxTree().Root,
				test.source,
				offset,
			)
			require.True(t, found)
			assert.Equal(t, test.role, reference.Role)
			assert.Equal(t, test.owner, reference.Owner)
			assert.Equal(t, cst.TextRange{
				Start: offset,
				End:   offset,
			}, reference.Range)
		})
	}
}

func TestDoctrineXMLMappingRootPatternLimitsReferencesAndModels(t *testing.T) {
	for _, test := range []struct {
		root  string
		valid bool
	}{
		{root: "doctrine-mapping", valid: true},
		{root: "doctrine-foo-mapping", valid: true},
		{root: "doctrine-mongodb-mapping", valid: true},
		{root: "doctrine1-foo-mapping", valid: false},
		{root: "doctrine1-mapping", valid: false},
		{root: "doctrinemapping", valid: false},
		{root: "doctrine-", valid: false},
	} {
		t.Run(test.root, func(t *testing.T) {
			source := "<" + test.root +
				`><entity name="App\Product"/></` +
				test.root + ">"
			path := "/project/config/Product.orm.xml"
			parsed := indexer.NewParsedFile(path, []byte(source))
			models := ModelsInDocument(
				path,
				parsed.SyntaxTree().Root,
				source,
			)
			assert.Equal(t, test.valid, len(models) == 1)
			offset := uint32(strings.Index(source, "App\\Product") + 3)
			_, found := MappingReferenceAt(
				path,
				parsed.SyntaxTree().Root,
				source,
				offset,
			)
			assert.Equal(t, test.valid, found)
		})
	}
}
