package entityschema

import (
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestDefinitionBehaviorImportsRendersRewritesAndChangesSchema(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Field;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogItemDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog_item';
    public function getEntityClass(): string { return CatalogItemEntity::class; }
    public function getCollectionClass(): string { return CatalogItemCollection::class; }
    protected function getParentDefinitionClass(): ?string { return CatalogDefinition::class; }
    public function isVersionAware(): bool { return true; }
    protected function defaultFields(): array { return [(new CreatedAtField())->addFlags(new ApiAware())]; }
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode'), new CustomFields()]; }
    public function getRestrictDeleteMetaFields(): FieldCollection {
        return $this->getFields()->filter(static fn (Field $field): bool => \in_array($field->getPropertyName(), ['id', 'createdAt'], true));
    }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
    public function custom(): bool { return true; }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.NotNil(t, spec.DefinitionBehavior)
	require.Equal(t, `Acme\Catalog\CatalogDefinition`, spec.DefinitionBehavior.ParentDefinitionClass)
	require.NotNil(t, spec.DefinitionBehavior.VersionAware)
	require.True(t, *spec.DefinitionBehavior.VersionAware)
	require.True(t, spec.DefinitionBehavior.OverrideDefaultFields)
	require.Len(t, spec.DefinitionBehavior.DefaultFields, 1)
	require.Equal(t, FieldCreatedAt, spec.DefinitionBehavior.DefaultFields[0].Kind)
	require.True(t, spec.DefinitionBehavior.OverrideBaseFields)
	require.Len(t, spec.DefinitionBehavior.BaseFields, 2)
	require.Equal(t, FieldString, spec.DefinitionBehavior.BaseFields[0].Kind)
	require.Equal(t, []string{"id", "createdAt"}, spec.DefinitionBehavior.RestrictDeleteMetaProperties)
	require.Empty(t, ValidateSpec(spec))

	schema, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Contains(t, schema.Columns, "created_at")
	require.Contains(t, schema.Columns, "base_code")
	require.Contains(t, schema.Columns, "custom_fields")
	require.NotContains(t, schema.Columns, "updated_at")

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "return CatalogDefinition::class")
	require.Contains(t, rendered, "public function isVersionAware(): bool")
	require.Contains(t, rendered, "protected function defaultFields(): array")
	require.Contains(t, rendered, "new CreatedAtField()")
	require.Contains(t, rendered, "protected function getBaseFields(): array")
	require.Contains(t, rendered, "new StringField('base_code', 'baseCode')")
	require.Contains(t, rendered, "getRestrictDeleteMetaFields")
	entity, err := RenderEntity(spec)
	require.NoError(t, err)
	require.Contains(t, entity, "protected ?string $baseCode = null;")
	require.Contains(t, entity, "public function getBaseCode(): ?string")
	require.Contains(t, entity, "use EntityCustomFieldsTrait;")

	parent := `Acme\Catalog\RootCatalogDefinition`
	spec.DefinitionBehavior.ParentDefinitionClass = parent
	falseValue := false
	spec.DefinitionBehavior.VersionAware = &falseValue
	spec.DefinitionBehavior.DefaultFields = nil
	spec.DefinitionBehavior.BaseFields = []FieldSpec{{Kind: FieldString, StorageName: "base_label", PropertyName: "baseLabel", Editable: true}}
	spec.DefinitionBehavior.RestrictDeleteMetaProperties = []string{"id"}
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "return RootCatalogDefinition::class")
	require.Contains(t, rewritten, "return false;")
	require.Contains(t, rewritten, "return [\n        ];")
	require.Contains(t, rewritten, "new StringField('base_label', 'baseLabel')")
	require.Contains(t, rewritten, "['id']")
	require.Contains(t, rewritten, "public function custom(): bool { return true; }")
}

func TestDefinitionBehaviorPreservesNonLiteralMethodsAsLockedSource(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function isInheritanceAware(): bool { $this->audit(); return true; }
    public function isVersionAware(): bool { return true && $this->configuredVersioning(); }
    protected function getParentDefinitionClass(): ?string { $this->audit(); return RootDefinition::class; }
    protected function defaultFields(): array { $this->audit(); return [new IdField('default_id', 'defaultId')]; }
    protected function getBaseFields(): array { $this->audit(); return [new IdField('base_id', 'baseId')]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.NotNil(t, spec.DefinitionBehavior)
	require.NotEmpty(t, spec.DefinitionBehavior.InheritanceAwareMethodRaw)
	require.NotEmpty(t, spec.DefinitionBehavior.VersionAwareMethodRaw)
	require.NotEmpty(t, spec.DefinitionBehavior.ParentDefinitionMethodRaw)
	require.NotEmpty(t, spec.DefinitionBehavior.DefaultFieldsMethodRaw)
	require.NotEmpty(t, spec.DefinitionBehavior.BaseFieldsMethodRaw)
	require.True(t, spec.DefinitionBehavior.OverrideDefaultFields)
	require.True(t, spec.DefinitionBehavior.OverrideBaseFields)
	require.Empty(t, ValidateSpec(spec))

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, rendered, spec.DefinitionBehavior.InheritanceAwareMethodRaw)
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, spec.DefinitionBehavior.InheritanceAwareMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionBehavior.VersionAwareMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionBehavior.ParentDefinitionMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionBehavior.DefaultFieldsMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionBehavior.BaseFieldsMethodRaw)

	creation := spec
	creation.Mode = "new"
	requireIssueCode(t, ValidateSpec(creation), "entity.definitionBehavior.raw.creation.unsupported")
	conflict := spec
	conflict.DefinitionBehavior.ParentDefinitionClass = `Acme\Catalog\RootDefinition`
	requireIssueCode(t, ValidateSpec(conflict), "entity.definitionBehavior.raw.conflict")
	baseConflict := spec
	baseConflict.DefinitionBehavior.BaseFields = []FieldSpec{{Kind: FieldString, StorageName: "base", PropertyName: "base", Editable: true}}
	requireIssueCode(t, ValidateSpec(baseConflict), "entity.definitionBehavior.raw.conflict")
	missingOverride := exampleSpec()
	missingOverride.DefinitionBehavior = &DefinitionBehaviorSpec{
		BaseFields: []FieldSpec{{Kind: FieldString, StorageName: "base", PropertyName: "base", Editable: true}},
	}
	requireIssueCode(t, ValidateSpec(missingOverride), "entity.definitionBehavior.baseFields.override.missing")
}

func TestMappingAndTranslationExplicitVersionAwarenessRoundTrip(t *testing.T) {
	mapping := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping, Namespace: `Acme\Catalog`, ClassName: "CatalogMap", EntityName: "acme_catalog_map",
		DefinitionBehavior: &DefinitionBehaviorSpec{VersionAware: boolPointer(true)},
		Fields:             []FieldSpec{{ID: "left", Kind: FieldBinaryID, StorageName: "left_id", PropertyName: "leftId", Required: true, Primary: true, Editable: true}},
	})
	source, err := RenderDefinition(mapping)
	require.NoError(t, err)
	require.Contains(t, source, "public function isVersionAware(): bool")
	imported, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.NotNil(t, imported.DefinitionBehavior.VersionAware)
	require.True(t, *imported.DefinitionBehavior.VersionAware)

	parent := exampleSpec()
	parent.Translation = &TranslationSpec{
		Enabled: true, EntityName: "acme_catalog_item_translation",
		DefinitionClass: `Acme\Catalog\CatalogItemTranslationDefinition`, EntityClass: `Acme\Catalog\CatalogItemTranslationEntity`,
		CollectionClass: `Acme\Catalog\CatalogItemTranslationCollection`, ParentDefinitionClass: parent.DefinitionClass,
		ParentStorageName: "acme_catalog_item_id", ParentPropertyName: "catalogItem", AssociationProperty: "translations", AssociationLocalField: "id",
		DefinitionBehavior: &DefinitionBehaviorSpec{VersionAware: boolPointer(true)},
	}
	parent.Fields = append(parent.Fields, FieldSpec{ID: "label", Kind: FieldString, StorageName: "label", PropertyName: "label", Translated: true, Editable: true})
	translationSource, err := RenderTranslationDefinition(parent)
	require.NoError(t, err)
	require.Contains(t, translationSource, "public function isVersionAware(): bool")
	translation, err := ImportTranslationDefinition(translationSource, nil)
	require.NoError(t, err)
	require.NotNil(t, translation.Spec.DefinitionBehavior.VersionAware)
	require.True(t, *translation.Spec.DefinitionBehavior.VersionAware)
}

func TestBaseFieldChangesRewriteGeneratedEntityMembersAndTraits(t *testing.T) {
	customFields := FieldSpec{
		ID: "base-custom-fields", Kind: FieldJSON, StorageName: "custom_fields", PropertyName: "customFields", Editable: true,
		Implementation: &FieldImplementation{
			Class: `Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields`, ConstructorMode: constructorStorageProperty,
			FixedStorageName: "custom_fields", FixedPropertyName: "customFields", EntityType: "array",
			EntityTrait: `Shopware\Core\Framework\DataAbstractionLayer\EntityCustomFieldsTrait`, ManageEntity: true,
		},
	}
	previous := exampleSpec()
	previous.Mode = "edit"
	previous.DefinitionBehavior = &DefinitionBehaviorSpec{
		OverrideBaseFields: true,
		BaseFields: []FieldSpec{
			{ID: "base-code", Kind: FieldString, StorageName: "base_code", PropertyName: "baseCode", Editable: true},
			customFields,
		},
	}
	previous = CompleteSpec(previous)
	source, err := RenderEntity(previous)
	require.NoError(t, err)
	require.Contains(t, source, "$baseCode")
	require.Contains(t, source, "use EntityCustomFieldsTrait;")

	next := previous
	next.DefinitionBehavior = &DefinitionBehaviorSpec{
		OverrideBaseFields: true,
		BaseFields:         []FieldSpec{{ID: "base-label", Kind: FieldString, StorageName: "base_label", PropertyName: "baseLabel", Editable: true}},
	}
	rewritten, err := RewriteEntity(source, previous, next)
	require.NoError(t, err)
	require.NotContains(t, rewritten, "$baseCode")
	require.NotContains(t, rewritten, "EntityCustomFieldsTrait")
	require.Contains(t, rewritten, "$baseLabel")
}

func TestTranslationBaseFieldChangesRewriteGeneratedMembers(t *testing.T) {
	previous := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Article", EntityName: "acme_article",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, StorageName: "name", PropertyName: "name", Translated: true, Editable: true},
		},
	})
	previous.Translation.DefinitionBehavior = &DefinitionBehaviorSpec{
		OverrideBaseFields: true,
		BaseFields:         []FieldSpec{{ID: "locale", Kind: FieldString, StorageName: "locale", PropertyName: "locale", Editable: true}},
	}
	previous = CompleteSpec(previous)
	source, err := RenderTranslationEntity(previous)
	require.NoError(t, err)
	require.Contains(t, source, "$locale")

	next := previous
	next.Translation = cloneTranslation(previous.Translation)
	next.Translation.DefinitionBehavior = &DefinitionBehaviorSpec{
		OverrideBaseFields: true,
		BaseFields:         []FieldSpec{{ID: "region", Kind: FieldString, StorageName: "region", PropertyName: "region", Editable: true}},
	}
	rewritten, err := RewriteTranslationEntity(source, previous, next)
	require.NoError(t, err)
	require.NotContains(t, rewritten, "$locale")
	require.Contains(t, rewritten, "$region")
}
