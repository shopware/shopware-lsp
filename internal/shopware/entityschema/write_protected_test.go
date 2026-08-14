package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestWriteProtectedFlagsImportAsTypedScopesAndRender(t *testing.T) {
	const source = `<?php declare(strict_types=1);

namespace Acme\Example;

use Acme\Scope\CustomScope;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslatedField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\TranslationsAssociationField;

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
            (new StringField('internal', 'internal'))->addFlags(new WriteProtected()),
            (new StringField('system_value', 'systemValue'))->addFlags(new WriteProtected(Context::SYSTEM_SCOPE, 'custom')),
            (new StringField('dynamic', 'dynamic'))->addFlags(new WriteProtected(CustomScope::VALUE)),
            (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new WriteProtected('crud')),
            (new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class))->addFlags(new WriteProtected(Context::USER_SCOPE)),
            (new TranslatedField('name'))->addFlags(new WriteProtected(Context::CRUD_API_SCOPE)),
            (new TranslationsAssociationField(ExampleTranslationDefinition::class, 'acme_example_id'))->addFlags(new WriteProtected('system')),
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
	lookup := func(class string) (RelationTarget, bool) { return target, class == target.DefinitionClass }

	spec, err := ImportDefinition(source, lookup)
	require.NoError(t, err)
	require.True(t, fieldByProperty(t, spec.Fields, "internal").WriteProtected)
	require.Empty(t, fieldByProperty(t, spec.Fields, "internal").WriteProtectedScopes)
	require.Equal(t, []string{"system", "custom"}, fieldByProperty(t, spec.Fields, "systemValue").WriteProtectedScopes)
	dynamic := fieldByProperty(t, spec.Fields, "dynamic")
	require.False(t, dynamic.WriteProtected)
	require.Equal(t, []string{"new WriteProtected(CustomScope::VALUE)"}, dynamic.PreservedFlags)
	product := fieldByProperty(t, spec.Fields, "product")
	require.Equal(t, []string{"crud"}, product.WriteProtectedScopes)
	require.True(t, product.AssociationWriteProtected)
	require.Equal(t, []string{"user"}, product.AssociationWriteScopes)
	name := fieldByProperty(t, spec.Fields, "name")
	require.True(t, name.TranslationWriteProtected)
	require.Equal(t, []string{"crud"}, name.TranslationWriteScopes)
	require.NotNil(t, spec.Translation)
	require.True(t, spec.Translation.AssociationWriteProtected)
	require.Equal(t, []string{"system"}, spec.Translation.AssociationWriteScopes)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "new WriteProtected('system', 'custom')")
	require.Contains(t, rendered, "new WriteProtected(CustomScope::VALUE)")
	require.Contains(t, rendered, "new WriteProtected('user')")
	require.Contains(t, rendered, "new WriteProtected('crud')")

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	for index := range spec.Fields {
		spec.Fields[index].WriteProtected = false
		spec.Fields[index].WriteProtectedScopes = nil
		spec.Fields[index].AssociationWriteProtected = false
		spec.Fields[index].AssociationWriteScopes = nil
		spec.Fields[index].TranslationWriteProtected = false
		spec.Fields[index].TranslationWriteScopes = nil
	}
	spec.Translation.AssociationWriteProtected = false
	spec.Translation.AssociationWriteScopes = nil
	after, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(before, after), "write protection must not affect migration schema")
}

func TestHierarchyWriteProtectionRoundTrip(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "version", Kind: FieldVersion, Editable: true},
			{
				ID: "hierarchy", Kind: FieldHierarchy, PropertyName: "children",
				TargetDefinitionClass: `Acme\Example\ExampleDefinition`, TargetEntityClass: `Acme\Example\ExampleEntity`,
				TargetCollectionClass: `Acme\Example\ExampleCollection`, TargetEntityName: "acme_example",
				WriteProtected: true, WriteProtectedScopes: []string{"system"},
				AssociationWriteProtected: true, AssociationWriteScopes: []string{"user"},
				HierarchyChildrenProtected: true, HierarchyChildrenWriteScopes: []string{"crud"},
				HierarchyVersionAware: true, HierarchyVersionProtected: true,
				Editable: true,
			},
		},
	})
	require.Empty(t, ValidateSpec(spec))
	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Equal(t, 4, strings.Count(rendered, "new WriteProtected("))

	imported, err := ImportDefinition(rendered, func(class string) (RelationTarget, bool) {
		return RelationTarget{
			DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass, CollectionClass: spec.CollectionClass,
			EntityName: spec.EntityName, VersionAware: true,
		}, class == spec.DefinitionClass
	})
	require.NoError(t, err)
	hierarchy := fieldByProperty(t, imported.Fields, "children")
	require.Equal(t, []string{"system"}, hierarchy.WriteProtectedScopes)
	require.Equal(t, []string{"user"}, hierarchy.AssociationWriteScopes)
	require.Equal(t, []string{"crud"}, hierarchy.HierarchyChildrenWriteScopes)
	require.True(t, hierarchy.HierarchyVersionProtected)
}

func TestTranslationStorageWriteProtectionRendersTypedImport(t *testing.T) {
	const source = `<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

final class ExampleTranslationDefinition extends EntityTranslationDefinition
{
    public const ENTITY_NAME = 'acme_example_translation';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    public function getParentDefinitionClass(): string
    {
        return ExampleDefinition::class;
    }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            (new StringField('name', 'name'))->addFlags(new WriteProtected('user')),
        ]);
    }
}
`
	translation, err := ImportTranslationDefinition(source, nil)
	require.NoError(t, err)
	field := fieldByProperty(t, translation.Fields, "name")
	require.True(t, field.WriteProtected)
	require.Equal(t, []string{"user"}, field.WriteProtectedScopes)

	parent := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Translated: true, Editable: true},
		},
		Translation: &TranslationSpec{Enabled: true},
	})
	combined := AttachTranslation(parent, translation)
	rendered, err := RenderTranslationDefinition(combined)
	require.NoError(t, err)
	require.Contains(t, rendered, `use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected;`)
	require.Contains(t, rendered, "new WriteProtected('user')")
	require.Empty(t, php.Parse(rendered).Errors, rendered)
}

func TestWriteProtectionValidation(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", WriteProtectedScopes: []string{"system"}, Editable: true},
		},
	})
	requireIssueCode(t, ValidateSpec(spec), "entity.field.writeProtection.invalid")
}
