package entityschema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImportDefinitionCombinesForeignKeyAndAssociation(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example\Entity;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowHtml;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    public function getEntityName(): string { return self::ENTITY_NAME; }
    public function getEntityClass(): string { return ExampleEntity::class; }
    public function getCollectionClass(): string { return ExampleCollection::class; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
			(new StringField('name', 'name', 500))->removeFlag(ApiAware::class)->addFlags(new Required(), new ApiAware(), new AllowHtml(), new Inherited())->setDescription('Display name'),
            (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new Required()),
			(new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', false))->setDescription('Product relation'),
            customFieldFactory(),
        ]);
    }
}`
	lookup := func(class string) (RelationTarget, bool) {
		require.Equal(t, `Shopware\Core\Content\Product\ProductDefinition`, class)
		return RelationTarget{
			DefinitionClass: class, EntityClass: `Shopware\Core\Content\Product\ProductEntity`,
			CollectionClass: `Shopware\Core\Content\Product\ProductCollection`, EntityName: "product",
			Fields: []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		}, true
	}
	spec, err := ImportDefinition(source, lookup)
	require.NoError(t, err)
	require.Equal(t, "acme_example", spec.EntityName)
	require.Equal(t, `Acme\Example\Entity\ExampleEntity`, spec.EntityClass)
	require.Len(t, spec.Fields, 4)
	require.Equal(t, FieldString, spec.Fields[1].Kind)
	require.Equal(t, 500, spec.Fields[1].MaxLength)
	require.Equal(t, []string{"new AllowHtml()", "new Inherited()"}, spec.Fields[1].PreservedFlags)
	require.Equal(t, []string{"->removeFlag(ApiAware::class)"}, spec.Fields[1].ModifiersBeforeFlags)
	require.Equal(t, []string{"->setDescription('Display name')"}, spec.Fields[1].ModifiersAfterFlags)
	require.Equal(t, FieldManyToOne, spec.Fields[2].Kind)
	require.Equal(t, "productId", spec.Fields[2].ForeignKeyPropertyName)
	require.Equal(t, "product", spec.Fields[2].TargetEntityName)
	require.Equal(t, []string{"->setDescription('Product relation')"}, spec.Fields[2].AssociationBeforeFlags)
	require.Equal(t, FieldLocked, spec.Fields[3].Kind)
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Len(t, entity.OpaqueFields, 1)
	require.Contains(t, entity.OpaqueFields[0].Raw, "customFieldFactory")
	require.Equal(t, []string{"new AllowHtml()", "new Inherited()"}, entity.Columns["name"].Flags)
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "new AllowHtml()")
	require.Contains(t, rewritten, "new Inherited()")
	require.Contains(t, rewritten, "->removeFlag(ApiAware::class)->addFlags(")
	require.Contains(t, rewritten, ")->setDescription('Display name')")
	require.Contains(t, rewritten, "->setDescription('Product relation')")
	require.NotContains(t, rewritten, "new RestrictDelete()")
	require.NotContains(t, rewritten, "new SetNullOnDelete()")
}

func TestIndexSpecsFromEntityExcludesOnlyGeneratedRelationIndexes(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "relation", Kind: FieldManyToOne, PropertyName: "product", StorageName: "product_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityClass: `Shopware\Core\Content\Product\ProductEntity`, TargetEntityName: "product", Editable: true},
		},
	})
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	entity.Indexes["uniq.acme_example.product"] = Index{Name: "uniq.acme_example.product", Unique: true, Columns: []string{"product_id"}}
	indexes := IndexSpecsFromEntity(spec, entity)
	require.Equal(t, []IndexSpec{{Name: "uniq.acme_example.product", Kind: IndexUnique, Columns: []string{"product_id"}}}, indexes)
}

func TestImportDefinitionInfersExternalRelationMetadataWithoutIndex(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\System\Language\LanguageDefinition;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new FkField('language_id', 'languageId', LanguageDefinition::class),
        new ManyToOneAssociationField('language', 'language_id', LanguageDefinition::class, 'id', false),
    ]); }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 2)
	relation := spec.Fields[1]
	require.Equal(t, "language", relation.TargetEntityName)
	require.Equal(t, `Shopware\Core\System\Language\LanguageEntity`, relation.TargetEntityClass)
	require.Equal(t, `Shopware\Core\System\Language\LanguageCollection`, relation.TargetCollectionClass)
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Equal(t, "language", entity.ForeignKeys["fk.acme_example.language_id"].ReferenceEntity)
}
