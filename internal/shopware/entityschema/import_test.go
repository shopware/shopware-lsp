package entityschema

import (
	"strings"
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
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\UpdatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    public function getEntityName(): string { return self::ENTITY_NAME; }
    public function getEntityClass(): string { return ExampleEntity::class; }
    public function getCollectionClass(): string { return ExampleCollection::class; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
			(new StringField('name', 'name', 500))->removeFlag(ApiAware::class)->addFlags(new Required(), new ApiAware(), new AllowHtml())->setDescription('Display name'),
			(new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new Required()),
			(new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class, 'id', false))->setDescription('Product relation'),
			new CreatedAtField(),
			new UpdatedAtField(),
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
	require.Empty(t, spec.Fields[1].PreservedFlags)
	require.NotNil(t, spec.Fields[1].Metadata)
	require.True(t, *spec.Fields[1].Metadata.AllowHTML)
	require.Equal(t, []string{"->removeFlag(ApiAware::class)"}, spec.Fields[1].ModifiersBeforeFlags)
	require.Equal(t, []string{"->setDescription('Display name')"}, spec.Fields[1].ModifiersAfterFlags)
	require.Equal(t, FieldManyToOne, spec.Fields[2].Kind)
	require.Equal(t, "productId", spec.Fields[2].ForeignKeyPropertyName)
	require.Equal(t, "product", spec.Fields[2].TargetEntityName)
	require.Equal(t, []string{"->setDescription('Product relation')"}, spec.Fields[2].AssociationBeforeFlags)
	require.Equal(t, FieldLocked, spec.Fields[3].Kind)
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Len(t, entity.Columns, 5)
	require.Contains(t, entity.Columns, "created_at")
	require.Contains(t, entity.Columns, "updated_at")
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "new AllowHtml()")
	require.Contains(t, rewritten, "->removeFlag(ApiAware::class)->addFlags(")
	require.Contains(t, rewritten, ")->setDescription('Display name')")
	require.Contains(t, rewritten, "->setDescription('Product relation')")
	require.NotContains(t, rewritten, "new RestrictDelete()")
	require.NotContains(t, rewritten, "new SetNullOnDelete()")
	require.NotContains(t, rewritten, "new CreatedAtField")
	require.NotContains(t, rewritten, "new UpdatedAtField")
}

func TestImportedMultilineModifierIsIndentationStable(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
        (new JsonField('config', 'config'))->setDescription(
            'First line '
            . 'second line'
        ),
    ]); }
}`

	imported, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, imported.Fields[1].ModifiersBeforeFlags, 1)

	rendered, err := RenderDefinition(imported)
	require.NoError(t, err)
	roundTripped, err := ImportDefinition(rendered, nil)
	require.NoError(t, err)
	require.Equal(t, imported.Fields[1].ModifiersBeforeFlags, roundTripped.Fields[1].ModifiersBeforeFlags)
}

func TestImportVersionGatedLocalAssociationEnrichesTargetMetadata(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Acme\Target\TargetDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection {
        $target = Feature::isActive('v6.8.0.0')
            ? new OneToOneAssociationField('target', 'target_id', 'id', TargetDefinition::class, false)
            : new ManyToOneAssociationField('target', 'target_id', TargetDefinition::class);
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
            $target,
        ]);
    }
}`
	lookup := func(class string) (RelationTarget, bool) {
		require.Equal(t, `Acme\Target\TargetDefinition`, class)
		return RelationTarget{
			DefinitionClass: class, EntityClass: `Acme\Target\TargetEntity`,
			CollectionClass: `Acme\Target\TargetCollection`, EntityName: "acme_target",
			Fields: []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		}, true
	}

	imported, err := ImportDefinition(source, lookup)
	require.NoError(t, err)
	require.Len(t, imported.Fields, 2)
	require.Equal(t, `Acme\Target\TargetEntity`, imported.Fields[1].TargetEntityClass)
	require.Equal(t, `Acme\Target\TargetCollection`, imported.Fields[1].TargetCollectionClass)
	require.Equal(t, "acme_target", imported.Fields[1].TargetEntityName)
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

func TestImportDefinitionAcceptsLiteralLocalFieldArray(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    public function getEntityName(): string { return self::ENTITY_NAME; }
    protected function defineFields(): FieldCollection {
        $fields = [new IdField('id', 'id'), new StringField('name', 'name')];
        return new FieldCollection($fields);
    }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 2)
	require.Equal(t, FieldID, spec.Fields[0].Kind)
	require.Equal(t, FieldString, spec.Fields[1].Kind)

	mutated := strings.Replace(source, "return new FieldCollection($fields);", "$fields[] = new StringField('dynamic', 'dynamic');\n        return new FieldCollection($fields);", 1)
	_, err = ImportDefinition(mutated, nil)
	require.ErrorContains(t, err, "no literal array")
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

func TestImportAndAttachTranslationDefinition(t *testing.T) {
	parentSource := `<?php declare(strict_types=1);
namespace Acme\Example\Content\Blog;
use Acme\Example\Content\Blog\Aggregate\BlogTranslation\BlogTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslatedField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslationsAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class BlogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_blog';
    public function getEntityClass(): string { return BlogEntity::class; }
    public function getCollectionClass(): string { return BlogCollection::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required()),
        (new TranslatedField('name', true))->addFlags(new ApiAware()),
        new TranslatedField('internalLink'),
        (new TranslationsAssociationField(BlogTranslationDefinition::class, 'acme_blog_id'))->addFlags(new Required()),
    ]); }
    public function customParentMethod(): bool { return true; }
}`
	translationSource := `<?php declare(strict_types=1);
namespace Acme\Example\Content\Blog\Aggregate\BlogTranslation;
use Acme\Example\Content\Blog\BlogDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class BlogTranslationDefinition extends EntityTranslationDefinition {
    public const ENTITY_NAME = 'acme_blog_translation';
    public function getEntityClass(): string { return BlogTranslationEntity::class; }
    public function getCollectionClass(): string { return BlogTranslationCollection::class; }
    protected function getParentDefinitionClass(): string { return BlogDefinition::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new StringField('name', 'name', 500))->addFlags(new Required()),
        new IdField('internal_link', 'internalLink'),
    ]); }
    public function customTranslationMethod(): bool { return true; }
}`
	parent, err := ImportDefinition(parentSource, nil)
	require.NoError(t, err)
	require.NotNil(t, parent.Translation)
	require.Len(t, parent.Fields, 3)
	require.True(t, parent.Fields[1].Translated)

	translation, err := ImportTranslationDefinition(translationSource, nil)
	require.NoError(t, err)
	combined := AttachTranslation(parent, translation)
	require.Len(t, combined.Fields, 3)
	field := combined.Fields[1]
	require.Equal(t, FieldString, field.Kind)
	require.True(t, field.Translated)
	require.True(t, field.TranslationUseForSort)
	require.NotNil(t, field.TranslationAPIAware)
	require.True(t, *field.TranslationAPIAware)
	require.Equal(t, 500, field.MaxLength)
	require.True(t, field.Required)
	require.Equal(t, FieldBinaryID, combined.Fields[2].Kind)
	require.Equal(t, "internal_link", combined.Fields[2].StorageName)

	parentAfter, err := RewriteDefinition(parentSource, combined)
	require.NoError(t, err)
	require.Contains(t, parentAfter, "customParentMethod")
	require.Contains(t, parentAfter, "new TranslatedField('name', true)")
	translationAfter, err := RewriteTranslationDefinition(translationSource, combined)
	require.NoError(t, err)
	require.Contains(t, translationAfter, "customTranslationMethod")
	require.Contains(t, translationAfter, "new StringField('name', 'name', 500)")
	translationEntity, err := RenderTranslationEntity(combined)
	require.NoError(t, err)
	translationEntity = strings.TrimSuffix(translationEntity, "}\n") + "    public function customEntityMethod(): bool { return true; }\n}\n"
	next := combined
	next.Fields = append(next.Fields, FieldSpec{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Translated: true, Editable: true})
	translationEntityAfter, err := RewriteTranslationEntity(translationEntity, combined, next)
	require.NoError(t, err)
	require.Contains(t, translationEntityAfter, "customEntityMethod")
	require.Contains(t, translationEntityAfter, "protected ?string $description = null;")
}

func TestImportJsonPropertyMappingDoesNotTreatNestedFieldsAsFlags(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example\Content\Page;
use Shopware\Core\Framework\Api\Context\AdminApiSource;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField as NestedBoolField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class PageDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_page';
    protected function defineFields(): FieldCollection { return new FieldCollection([
		(new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
        (new JsonField('visibility', 'visibility', [
            new NestedBoolField('mobile', 'mobile'),
        ], []))->addFlags(new ApiAware(AdminApiSource::class)),
    ]); }
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 2)
	field := fieldByProperty(t, spec.Fields, "visibility")
	require.True(t, field.APIAware)
	require.Equal(t, []string{`Shopware\Core\Framework\Api\Context\AdminApiSource`}, field.APIAwareSources)
	require.Contains(t, field.JSONPropertyMappingExpression, `new \Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField`)
	require.Equal(t, "[]", field.JSONDefaultExpression)
	require.Empty(t, field.PreservedFlags)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, rendered, "new ApiAware(AdminApiSource::class)")
	require.Contains(t, rendered, `new \Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField('mobile', 'mobile')`)
	require.Contains(t, rendered, "], []))")

	roundTripped, err := ImportDefinition(rendered, nil)
	require.NoError(t, err)
	roundTrippedField := fieldByProperty(t, roundTripped.Fields, "visibility")
	require.Equal(t, field.APIAwareSources, roundTrippedField.APIAwareSources)
	require.Equal(t, field.JSONPropertyMappingExpression, roundTrippedField.JSONPropertyMappingExpression)
	require.Equal(t, field.JSONDefaultExpression, roundTrippedField.JSONDefaultExpression)
}
