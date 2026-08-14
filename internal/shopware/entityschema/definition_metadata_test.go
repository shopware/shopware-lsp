package entityschema

import (
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestDefinitionMetadataImportsRendersAndRewrites(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Catalog;
use Acme\Catalog\Hydration\CatalogHydrator;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { return '6.7.1.0'; }
    public function getDefaults(): array { return ['active' => true, 'kind' => self::KIND_DEFAULT, 'config' => []]; }
    public function getChildDefaults(): array { return ['kind' => self::KIND_DEFAULT]; }
    public function getHydratorClass(): string { return CatalogHydrator::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
    public function custom(): bool { return true; }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Equal(t, "6.7.1.0", spec.DefinitionMetadata.Since)
	require.Equal(t, []DefinitionDefaultSpec{
		{PropertyName: "active", ValueExpression: "true"},
		{PropertyName: "kind", ValueExpression: "self::KIND_DEFAULT"},
		{PropertyName: "config", ValueExpression: "[]"},
	}, spec.DefinitionMetadata.Defaults)
	require.Equal(t, []DefinitionDefaultSpec{{PropertyName: "kind", ValueExpression: "self::KIND_DEFAULT"}}, spec.DefinitionMetadata.ChildDefaults)
	require.Equal(t, `Acme\Catalog\Hydration\CatalogHydrator`, spec.DefinitionMetadata.HydratorClass)
	require.Empty(t, ValidateSpec(spec))

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "public function getDefaults(): array")
	require.Contains(t, rendered, "'kind' => self::KIND_DEFAULT")
	require.Contains(t, rendered, "return CatalogHydrator::class")

	spec.DefinitionMetadata.Since = "6.7.2.0"
	spec.DefinitionMetadata.Defaults[0].ValueExpression = "false"
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "return '6.7.2.0';")
	require.Contains(t, rewritten, "'active' => false")
	require.Contains(t, rewritten, "public function custom(): bool { return true; }")
}

func TestDefinitionMetadataPreservesCustomMethodsAndValidatesConflicts(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { $this->audit(); return '6.7.0.0'; }
    public function getDefaults(): array { $this->audit(); return ['active' => true]; }
    public function getChildDefaults(): array { return ['active' => true] + $this->computedDefaults(); }
    public function getHydratorClass(): string { $this->audit(); return CatalogHydrator::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.NotEmpty(t, spec.DefinitionMetadata.SinceMethodRaw)
	require.NotEmpty(t, spec.DefinitionMetadata.DefaultsMethodRaw)
	require.NotEmpty(t, spec.DefinitionMetadata.ChildDefaultsMethodRaw)
	require.NotEmpty(t, spec.DefinitionMetadata.HydratorMethodRaw)
	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, rendered, spec.DefinitionMetadata.DefaultsMethodRaw)
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, spec.DefinitionMetadata.DefaultsMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionMetadata.SinceMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionMetadata.ChildDefaultsMethodRaw)
	require.Contains(t, rewritten, spec.DefinitionMetadata.HydratorMethodRaw)

	conflict := spec
	conflict.DefinitionMetadata.Defaults = []DefinitionDefaultSpec{{PropertyName: "active", ValueExpression: "true"}}
	requireIssueCode(t, ValidateSpec(conflict), "entity.definitionMetadata.raw.conflict")
	conflict.DefinitionMetadata.DefaultsMethodRaw = ""
	conflict.DefinitionMetadata.Defaults[0].ValueExpression = "broken("
	requireIssueCode(t, ValidateSpec(conflict), "entity.definitionMetadata.default.expression.invalid")
}

func TestTranslationDefinitionMetadataRoundTrips(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogTranslationDefinition extends EntityTranslationDefinition {
    public const ENTITY_NAME = 'acme_catalog_translation';
    public function getEntityClass(): string { return CatalogTranslationEntity::class; }
    public function getCollectionClass(): string { return CatalogTranslationCollection::class; }
    protected function getParentDefinitionClass(): string { return CatalogDefinition::class; }
    public function since(): ?string { return '6.7.1.0'; }
    public function getDefaults(): array { return ['label' => 'default']; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new StringField('label', 'label')]); }
}`
	imported, err := ImportTranslationDefinition(source, nil)
	require.NoError(t, err)
	require.Equal(t, "6.7.1.0", imported.Spec.DefinitionMetadata.Since)
	require.Equal(t, []DefinitionDefaultSpec{{PropertyName: "label", ValueExpression: "'default'"}}, imported.Spec.DefinitionMetadata.Defaults)

	parent := exampleSpec()
	parent.Translation = &TranslationSpec{
		Enabled: true, EntityName: "acme_catalog_translation",
		DefinitionClass: imported.Spec.DefinitionClass, EntityClass: imported.Spec.EntityClass,
		CollectionClass: imported.Spec.CollectionClass, ParentDefinitionClass: parent.DefinitionClass,
		ParentStorageName: "acme_catalog_item_id", ParentPropertyName: "catalogItem", AssociationProperty: "translations",
		AssociationLocalField: "acme_catalog_item_id",
	}
	parent.Fields = append(parent.Fields, FieldSpec{ID: "label", Kind: FieldString, PropertyName: "label", StorageName: "label", Translated: true, Editable: true})
	imported.Spec.ParentDefinitionClass = parent.DefinitionClass
	parent = AttachTranslation(parent, imported)
	rendered, err := RenderTranslationDefinition(parent)
	require.NoError(t, err)
	require.Contains(t, rendered, "public function since(): ?string")
	require.Contains(t, rendered, "'label' => 'default'")
}
