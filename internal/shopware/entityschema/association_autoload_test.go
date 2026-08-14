package entityschema

import (
	"reflect"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestToOneAssociationAutoloadImportsNativeDefaultsAndNamedArguments(t *testing.T) {
	const source = `<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField;

final class ExampleDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'acme_example';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new FkField('product_id', 'productId', ProductDefinition::class),
            new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', autoload: true),
            new FkField('fallback_product_id', 'fallbackProductId', ProductDefinition::class),
            new ManyToOneAssociationField('fallbackProduct', 'fallback_product_id', ProductDefinition::class),
            new FkField('featured_product_id', 'featuredProductId', ProductDefinition::class),
            new OneToOneAssociationField('featuredProduct', 'featured_product_id', 'id', ProductDefinition::class),
        ]);
    }
}
`
	target := RelationTarget{
		DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		EntityClass:     `Shopware\Core\Content\Product\ProductEntity`,
		CollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
		EntityName:      "product",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}
	lookup := func(class string) (RelationTarget, bool) {
		return target, class == target.DefinitionClass
	}

	spec, err := ImportDefinition(source, lookup)
	require.NoError(t, err)
	require.True(t, fieldByProperty(t, spec.Fields, "product").AssociationAutoload)
	require.False(t, fieldByProperty(t, spec.Fields, "fallbackProduct").AssociationAutoload, "ManyToOneAssociationField defaults autoload to false")
	require.True(t, fieldByProperty(t, spec.Fields, "featuredProduct").AssociationAutoload, "OneToOneAssociationField defaults autoload to true")

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors)
	require.Contains(t, rendered, "new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', true)")
	require.Contains(t, rendered, "new ManyToOneAssociationField('fallbackProduct', 'fallback_product_id', ProductDefinition::class, 'id', false)")
	require.Contains(t, rendered, "new OneToOneAssociationField('featuredProduct', 'featured_product_id', 'id', ProductDefinition::class, true)")

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	for index := range spec.Fields {
		spec.Fields[index].AssociationAutoload = false
	}
	after, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(before, after), "autoload must not affect migration schema")
}

func TestAssociationAutoloadValidationRejectsToManyFields(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{
				ID: "products", Kind: FieldOneToMany, PropertyName: "products",
				TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
				TargetCollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
				ReferenceStorageName:  "example_id", SourceColumn: "id", AssociationAutoload: true, Editable: true,
			},
		},
	})
	requireIssueCode(t, ValidateSpec(spec), "entity.association.autoload.unsupported")
}
