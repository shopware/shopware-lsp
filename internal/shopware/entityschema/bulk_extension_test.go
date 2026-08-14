package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestBulkEntityExtensionRenderImportAndSchemaRoundTrip(t *testing.T) {
	targets := map[string]RelationTarget{
		`Shopware\Core\System\Integration\IntegrationDefinition`: {
			DefinitionClass: `Shopware\Core\System\Integration\IntegrationDefinition`,
			EntityClass:     `Shopware\Core\System\Integration\IntegrationEntity`,
			CollectionClass: `Shopware\Core\System\Integration\IntegrationCollection`,
			EntityName:      "integration",
			Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		},
		`Shopware\Core\System\User\UserDefinition`: {
			DefinitionClass: `Shopware\Core\System\User\UserDefinition`,
			EntityClass:     `Shopware\Core\System\User\UserEntity`,
			CollectionClass: `Shopware\Core\System\User\UserCollection`,
			EntityName:      "user",
			Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		},
	}
	lookup := func(class string) (RelationTarget, bool) {
		target, found := targets[strings.Trim(class, `\`)]
		return target, found
	}
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionBulkExtension,
		Namespace: `Acme\Example\Notification`, ClassName: "Notification",
		ShopwareVersion: "6.7.1.0", CreateMigration: true,
		BulkExtensions: []BulkExtensionTargetSpec{
			{
				ID: "integration", EntityName: "integration",
				ExtendedDefinitionClass: `Shopware\Core\System\Integration\IntegrationDefinition`,
				ExtendedFields:          targets[`Shopware\Core\System\Integration\IntegrationDefinition`].Fields,
				Fields:                  []FieldSpec{bulkNotificationAssociation("createdNotifications", "created_by_integration_id")},
			},
			{
				ID: "user", EntityName: "user",
				ExtendedDefinitionClass: `Shopware\Core\System\User\UserDefinition`,
				ExtendedFields:          targets[`Shopware\Core\System\User\UserDefinition`].Fields,
				Fields: []FieldSpec{
					bulkNotificationAssociation("createdNotifications", "created_by_user_id"),
					{ID: "display", Kind: FieldString, PropertyName: "display", StorageName: "acme_display", Behavior: &FieldBehavior{Runtime: true}, Editable: true},
				},
			},
		},
	})

	require.Equal(t, `Acme\Example\Notification\NotificationBulkEntityExtension`, spec.DefinitionClass)
	require.Empty(t, ValidateSpec(spec))
	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(source).Errors, source)
	require.Contains(t, source, "class NotificationBulkEntityExtension extends BulkEntityExtension")
	require.Contains(t, source, "yield IntegrationDefinition::ENTITY_NAME => [")
	require.Contains(t, source, "yield UserDefinition::ENTITY_NAME => [")
	require.Equal(t, 3, strings.Count(source, "new Extension()"))

	imported, err := ImportBulkExtension(source, lookup)
	require.NoError(t, err)
	require.Equal(t, DefinitionBulkExtension, imported.DefinitionKind)
	require.Equal(t, "Notification", imported.ClassName)
	require.Len(t, imported.BulkExtensions, 2)
	for _, target := range imported.BulkExtensions {
		for _, field := range target.Fields {
			require.NotEqual(t, FieldLocked, field.Kind, field.Raw)
		}
	}

	before, err := SchemaEntitiesFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaEntitiesFromSpec(imported)
	require.NoError(t, err)
	for index := range before {
		before[index].BackfillFree()
		after[index].BackfillFree()
	}
	require.True(t, reflect.DeepEqual(before, after), "schema mismatch\nbefore: %#v\nafter: %#v", before, after)
	require.Equal(t, []string{"integration", "user"}, []string{before[0].Name, before[1].Name})
	require.True(t, before[0].External)
	require.Empty(t, before[0].Columns)
}

func TestBulkEntityExtensionLiteralTargetAndRewrite(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionBulkExtension,
		Namespace: `Acme\Example`, ClassName: "Literal", CreateMigration: true,
		BulkExtensions: []BulkExtensionTargetSpec{{
			ID: "custom", EntityName: "custom_target",
			Fields: []FieldSpec{{
				ID: "children", Kind: FieldOneToMany, PropertyName: "children",
				ReferenceStorageName: "parent_id", SourceColumn: "id",
				TargetDefinitionClass: `Acme\Example\CustomTargetDefinition`,
				TargetEntityClass:     `Acme\Example\CustomTargetEntity`,
				TargetCollectionClass: `Acme\Example\CustomTargetCollection`,
				TargetEntityName:      "custom_target", Editable: true,
			}},
		}},
	})
	require.Empty(t, ValidateSpec(spec))
	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, source, "yield 'custom_target' => [")
	source = strings.TrimSuffix(source, "}\n") + "    public function customBehavior(): bool { return true; }\n}\n"

	imported, err := ImportBulkExtension(source, nil)
	require.NoError(t, err)
	require.Equal(t, "custom_target", imported.BulkExtensions[0].EntityName)
	imported.BulkExtensions[0].Fields = append(imported.BulkExtensions[0].Fields, FieldSpec{
		ID: "runtime", Kind: FieldString, PropertyName: "runtime", StorageName: "runtime",
		Behavior: &FieldBehavior{Runtime: true}, Editable: true,
	})
	rewritten, err := RewriteDefinition(source, imported)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rewritten).Errors, rewritten)
	require.Contains(t, rewritten, "function customBehavior")
	require.Contains(t, rewritten, "new StringField('runtime', 'runtime')")
}

func TestBulkEntityExtensionLiteralTargetUsesIndexedEntityMetadata(t *testing.T) {
	const source = `<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;

class ProductBulkEntityExtension extends BulkEntityExtension
{
    public function collect(): \Generator
    {
        yield 'product' => [];
    }
}
`
	target := RelationTarget{
		DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		EntityName:      "product",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}
	lookup := func(classOrEntityName string) (RelationTarget, bool) {
		if classOrEntityName == target.EntityName {
			return target, true
		}
		return RelationTarget{}, false
	}

	spec, err := ImportBulkExtension(source, lookup)
	require.NoError(t, err)
	require.Len(t, spec.BulkExtensions, 1)
	require.Equal(t, target.DefinitionClass, spec.BulkExtensions[0].ExtendedDefinitionClass)
	require.Equal(t, target.Fields, spec.BulkExtensions[0].ExtendedFields)
}

func TestBulkEntityExtensionRejectsPersistedScalarTargetField(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionBulkExtension,
		Namespace: `Acme\Example`, ClassName: "Invalid", CreateMigration: true,
		BulkExtensions: []BulkExtensionTargetSpec{{
			ID: "product", EntityName: "product",
			ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
			Fields:                  []FieldSpec{{ID: "label", Kind: FieldString, PropertyName: "label", StorageName: "acme_label", Editable: true}},
		}},
	})
	requireIssueCode(t, ValidateSpec(spec), "entity.extension.field.runtime.required")
}

func TestBulkEntityExtensionRejectsUnsupportedShopwareVersion(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionBulkExtension,
		Namespace: "Acme\\Example", ClassName: "Catalog", ShopwareVersion: "~6.6.9",
		BulkExtensions: []BulkExtensionTargetSpec{{
			ID: "product", EntityName: "product",
			ExtendedDefinitionClass: "Shopware\\Core\\Content\\Product\\ProductDefinition",
			Fields: []FieldSpec{{
				ID: "notes", Kind: FieldOneToMany, PropertyName: "notes",
				TargetDefinitionClass: "Acme\\Example\\NoteDefinition", TargetEntityName: "acme_note",
				ReferenceStorageName: "product_id", SourceColumn: "id", Editable: true,
			}},
		}},
	})
	requireIssueCode(t, ValidateSpec(spec), "entity.bulkExtension.version.unsupported")

	spec.ShopwareVersion = "~6.6.10"
	require.NotContains(t, validationCodes(ValidateSpec(spec)), "entity.bulkExtension.version.unsupported")
}

func TestBulkEntityExtensionPreservesCustomCollectMethod(t *testing.T) {
	source := `<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;

class DynamicBulkEntityExtension extends BulkEntityExtension
{
    public function collect(): \Generator
    {
        $fields = $this->fieldsFor('product');

        yield 'product' => $fields;
    }

    private function fieldsFor(string $entity): array
    {
        return [];
    }
}
`
	spec, err := ImportBulkExtension(source, nil)
	require.NoError(t, err)
	require.Equal(t, DefinitionBulkExtension, spec.DefinitionKind)
	require.Empty(t, spec.BulkExtensions)
	require.Contains(t, spec.CollectMethodRaw, `$this->fieldsFor('product')`)
	require.Empty(t, ValidateSpec(spec))

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, spec.CollectMethodRaw)

	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Equal(t, source, rewritten)

	conflict := spec
	conflict.BulkExtensions = []BulkExtensionTargetSpec{{ID: "product", EntityName: "product"}}
	requireIssueCode(t, ValidateSpec(conflict), "entity.bulkExtension.collectRaw.conflict")
	creation := spec
	creation.Mode = "new"
	requireIssueCode(t, ValidateSpec(creation), "entity.bulkExtension.collectRaw.creation.unsupported")
}

func bulkNotificationAssociation(property, reference string) FieldSpec {
	return FieldSpec{
		ID: property + "-" + reference, Kind: FieldOneToMany, PropertyName: property,
		ReferenceStorageName: reference, SourceColumn: "id",
		TargetDefinitionClass: `Shopware\Core\Framework\Notification\NotificationDefinition`,
		TargetEntityClass:     `Shopware\Core\Framework\Notification\NotificationEntity`,
		TargetCollectionClass: `Shopware\Core\Framework\Notification\NotificationCollection`,
		TargetEntityName:      "notification", Editable: true,
	}
}
