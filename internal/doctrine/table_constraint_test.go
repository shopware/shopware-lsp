package doctrine

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php/parser"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/require"
)

func TestTableConstraintsAcrossMappingFormats(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		source string
		root   func(string) *cst.Node
	}{
		{
			name: "PHP attributes",
			path: "/project/src/Product.php",
			source: `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;

#[ORM\Entity]
#[ORM\Index(name: 'search_idx', fields: ['name', 'category'])]
#[ORM\UniqueConstraint(
    name: 'email_unique',
    columns: ['email_address'],
)]
class Product
{
    #[ORM\Column]
    private string $name;

    #[ORM\Column]
    private string $category;

    #[ORM\Column(name: 'email_address')]
    private string $email;
}`,
			root: func(source string) *cst.Node {
				return phpparser.Parse(source).Tree.Root
			},
		},
		{
			name: "XML",
			path: "/project/config/doctrine/Product.orm.xml",
			source: `<doctrine-mapping>
  <entity name="App\Entity\Product">
    <indexes>
      <index name="search_idx" fields="name, category"/>
    </indexes>
    <unique-constraints>
      <unique-constraint name="email_unique" columns="email_address"/>
    </unique-constraints>
    <field name="name"/>
    <field name="category"/>
    <field name="email" column="email_address"/>
  </entity>
</doctrine-mapping>`,
			root: func(source string) *cst.Node {
				return xmlparser.Parse(source).Tree.Root
			},
		},
		{
			name: "YAML",
			path: "/project/config/doctrine/Product.orm.yaml",
			source: `App\Entity\Product:
  type: entity
  indexes:
    search_idx:
      fields: [name, category]
  uniqueConstraints:
    email_unique:
      columns:
        - email_address
  fields:
    name: { type: string }
    category: { type: string }
    email:
      type: string
      column: email_address
`,
			root: func(source string) *cst.Node {
				return yamlparser.Parse(source).Tree.Root
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := test.root(test.source)
			models := ModelsInDocument(
				test.path,
				root,
				test.source,
			)
			require.Len(t, models, 1)
			require.Len(t, models[0].TableConstraints, 2)
			index := models[0].TableConstraints[0]
			require.Equal(t, IndexConstraint, index.Kind)
			require.Equal(t, "search_idx", index.Name)
			require.Equal(t, "name", index.Fields[0].Name)
			require.Equal(t, "category", index.Fields[1].Name)
			unique := models[0].TableConstraints[1]
			require.Equal(t, UniqueConstraint, unique.Kind)
			require.Equal(t, "email_unique", unique.Name)
			require.Equal(
				t,
				"email_address",
				unique.Columns[0].Name,
			)

			references := MappingReferencesInDocument(
				test.path,
				root,
				test.source,
			)
			var constraintReferences []MappingReference
			for _, reference := range references {
				if reference.Role == MappingConstraintField ||
					reference.Role == MappingConstraintColumn {
					constraintReferences = append(
						constraintReferences,
						reference,
					)
				}
			}
			require.Len(t, constraintReferences, 3)
			require.Equal(
				t,
				MappingConstraintColumn,
				constraintReferences[2].Role,
			)
			require.Equal(t, "email", constraintReferences[2].Field)
			require.Equal(
				t,
				"email_address",
				test.source[constraintReferences[2].Range.Start:constraintReferences[2].Range.End],
			)
		})
	}
}

func TestLegacyAnnotationTableConstraints(t *testing.T) {
	source := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;

/**
 * @ORM\Entity
 * @ORM\Table(
 *   indexes={@ORM\Index(name="search_idx", fields={"name", "category"})},
 *   uniqueConstraints={
 *     @ORM\UniqueConstraint(name="email_unique", columns={"email_address"})
 *   }
 * )
 */
class Product
{
    /** @ORM\Column */
    private string $name;

    /** @ORM\Column */
    private string $category;

    /** @ORM\Column(name="email_address") */
    private string $email;
}`
	root := phpparser.Parse(source).Tree.Root
	models := ModelsInDocument(
		"/project/src/Product.php",
		root,
		source,
	)
	require.Len(t, models, 1)
	require.Len(t, models[0].TableConstraints, 2)
	require.Equal(
		t,
		"name",
		models[0].TableConstraints[0].Fields[0].Name,
	)
	require.Equal(
		t,
		"email_address",
		models[0].TableConstraints[1].Columns[0].Name,
	)
	reference, found := MappingReferenceAt(
		"/project/src/Product.php",
		root,
		source,
		uint32(strings.Index(source, `"email_address"`)+2),
	)
	require.True(t, found)
	require.Equal(t, MappingConstraintColumn, reference.Role)
	require.Equal(t, "email", reference.Field)
}
