package entityschema

import (
	"reflect"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestSpecializedClassBasedFieldsRemainEditableAndRoundTrip(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Checkout\Cart\Price\Struct\CalculatedPrice;
use Shopware\Core\Checkout\Order\OrderStates;
use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CalculatedPriceField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ChildCountField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedByField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\LockedField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\PasswordField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StateMachineStateField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\System\NumberRange\DataAbstractionLayer\NumberRangeField;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new CustomFields())->addFlags(new ApiAware()),
        (new NumberRangeField('number', 'number', 96))->addFlags(new Required()),
        new PasswordField('password', 'password', \PASSWORD_DEFAULT, [], PasswordField::FOR_ADMIN),
        new ChildCountField(),
        (new CreatedByField([Context::SYSTEM_SCOPE, Context::CRUD_API_SCOPE]))->addFlags(new ApiAware()),
        (new StateMachineStateField('state_id', 'stateId', OrderStates::STATE_MACHINE))->addFlags(new Required()),
        new CalculatedPriceField('price', 'price'),
        new LockedField(false),
    ]); }
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 9)
	for _, field := range spec.Fields {
		require.NotEqual(t, FieldLocked, field.Kind, field.Raw)
		require.True(t, field.Editable)
		if field.Kind != FieldID {
			require.NotNil(t, field.Implementation)
		}
	}
	require.Equal(t, FieldJSON, fieldByProperty(t, spec.Fields, "customFields").Kind)
	require.Equal(t, 96, fieldByProperty(t, spec.Fields, "number").MaxLength)
	require.Equal(t, FieldForeignKey, fieldByProperty(t, spec.Fields, "createdById").Kind)
	require.Equal(t, "state_machine_state", fieldByProperty(t, spec.Fields, "stateId").TargetEntityName)
	require.True(t, fieldByProperty(t, spec.Fields, "locked").Implementation.ImplicitComputed)

	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Equal(t, "JSON", entity.Columns["custom_fields"].SQLType)
	require.Equal(t, "VARCHAR(96)", entity.Columns["number"].SQLType)
	require.Equal(t, "BINARY(16)", entity.Columns["created_by_id"].SQLType)
	require.Equal(t, "TINYINT(1)", entity.Columns["locked"].SQLType)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "new CustomFields('custom_fields', 'customFields')")
	require.Contains(t, rendered, "new NumberRangeField('number', 'number', 96)")
	require.Contains(t, rendered, `new CreatedByField([\Shopware\Core\Framework\Context::SYSTEM_SCOPE, \Shopware\Core\Framework\Context::CRUD_API_SCOPE])`)
	require.Contains(t, rendered, `new StateMachineStateField('state_id', 'stateId', \Shopware\Core\Checkout\Order\OrderStates::STATE_MACHINE)`)
	require.Contains(t, rendered, "new LockedField(false)")

	roundTripped, err := ImportDefinition(rendered, nil)
	require.NoError(t, err)
	after, err := SchemaFromSpec(roundTripped)
	require.NoError(t, err)
	entity.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(entity, after))

	entitySource, err := RenderEntity(spec)
	require.NoError(t, err)
	require.NotContains(t, entitySource, "$customFields", "custom JSON entity members stay owned by existing custom code or traits")
	require.Contains(t, entitySource, "use EntityCustomFieldsTrait;")
	require.NotContains(t, entitySource, "$price", "structured JSON entity members stay owned by existing custom code")
	require.Contains(t, entitySource, "protected string $number;")
	require.Contains(t, entitySource, "public function isLocked(): ?bool")
	require.NotContains(t, entitySource, "public function setLocked(")
}

func TestCustomFieldsTemplateUsesAndRewritesEntityTrait(t *testing.T) {
	var custom FieldSpec
	for _, template := range SpecializedFieldTemplates() {
		if template.ID == "specialized:CustomFields" {
			custom = template.Field
			break
		}
	}
	require.NotNil(t, custom.Implementation)
	require.Equal(t, `Shopware\Core\Framework\DataAbstractionLayer\EntityCustomFieldsTrait`, custom.Implementation.EntityTrait)
	custom.ID = "custom-fields"
	spec := exampleSpec()
	spec.Indexes = nil
	spec.Fields = []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}, custom}

	entitySource, err := RenderEntity(spec)
	require.NoError(t, err)
	require.Contains(t, entitySource, "use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCustomFieldsTrait;")
	require.Contains(t, entitySource, "use EntityCustomFieldsTrait;")
	require.NotContains(t, entitySource, "$customFields")

	next := spec
	next.Fields = next.Fields[:1]
	rewritten, err := RewriteEntity(entitySource, spec, next)
	require.NoError(t, err)
	require.NotContains(t, rewritten, "EntityCustomFieldsTrait")
	require.Empty(t, php.Parse(rewritten).Errors, rewritten)
}

func TestTranslatedCustomFieldsUseTraitOnBothEntities(t *testing.T) {
	var custom FieldSpec
	for _, template := range SpecializedFieldTemplates() {
		if template.ID == "specialized:CustomFields" {
			custom = template.Field
			break
		}
	}
	custom.ID = "custom-fields"
	custom.Translated = true
	spec := exampleSpec()
	spec.Indexes = nil
	spec.Fields = []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}, custom}
	spec = CompleteSpec(spec)

	parent, err := RenderEntity(spec)
	require.NoError(t, err)
	translation, err := RenderTranslationEntity(spec)
	require.NoError(t, err)
	for _, source := range []string{parent, translation} {
		require.Contains(t, source, "use EntityCustomFieldsTrait;")
		require.NotContains(t, source, "$customFields")
		require.Empty(t, php.Parse(source).Errors, source)
	}
}

func TestSpecializedFieldTemplatesCoverImporterCatalog(t *testing.T) {
	templates := SpecializedFieldTemplates()
	require.Len(t, templates, len(specializedFieldDescriptors))
	byID := make(map[string]SpecializedFieldTemplate, len(templates))
	for _, template := range templates {
		require.NotEmpty(t, template.ID)
		require.NotEmpty(t, template.Label)
		require.NotNil(t, template.Field.Implementation)
		require.NotEmpty(t, template.Field.Implementation.Class)
		require.True(t, template.Field.Implementation.ManageEntity)
		byID[template.ID] = template
	}
	require.Equal(t, "custom_fields", byID["specialized:CustomFields"].Field.StorageName)
	require.Equal(t, "array", byID["specialized:CustomFields"].Field.Implementation.EntityType)
	require.Equal(t, `Shopware\Core\Checkout\Cart\Price\Struct\CalculatedPrice`, byID["specialized:CalculatedPriceField"].Field.Implementation.EntityType)
	require.Equal(t, `Shopware\Core\Framework\DataAbstractionLayer\Pricing\PriceCollection`, byID["specialized:PriceField"].Field.Implementation.EntityType)
	require.Contains(t, byID["specialized:CheapestPriceField"].Field.Implementation.EntityType, "CheapestPriceContainer")
	require.Equal(t, 1, byID["specialized:ManyToManyIdField"].Field.Implementation.MinimumAdditionalArguments)
	require.Equal(t, 1, byID["specialized:StateMachineStateField"].Field.Implementation.MinimumAdditionalArguments)
	require.Equal(t, "6.7.1", byID["specialized:MeasurementUnitsField"].MinimumShopwareVersion)
}

func TestSpecializedFieldTemplatesRespectShopwareVersion(t *testing.T) {
	template := SpecializedFieldTemplate{}
	for _, candidate := range SpecializedFieldTemplates() {
		if candidate.ID == "specialized:MeasurementUnitsField" {
			template = candidate
			break
		}
	}
	require.NotNil(t, template.Field.Implementation)
	require.False(t, SpecializedFieldSupported(template.Field.Implementation.Class, "~6.7.0"))
	require.True(t, SpecializedFieldSupported(template.Field.Implementation.Class, "6.7.1.0"))
	require.True(t, SpecializedFieldSupported(template.Field.Implementation.Class, "dev-trunk"))

	spec := exampleSpec()
	spec.ShopwareVersion = "6.7.0"
	spec.Fields = append(spec.Fields, template.Field)
	requireIssueCode(t, ValidateSpec(spec), "entity.field.implementation.version.unsupported")
	spec.ShopwareVersion = "6.7.1"
	require.NotContains(t, validationCodes(ValidateSpec(spec)), "entity.field.implementation.version.unsupported")
}

func TestEverySpecializedFieldTemplateRendersValidPHP(t *testing.T) {
	for _, template := range SpecializedFieldTemplates() {
		t.Run(template.ID, func(t *testing.T) {
			field := template.Field
			field.ID = "specialized"
			if field.StorageName == "" {
				field.StorageName = "specialized_value"
			}
			if field.PropertyName == "" {
				field.PropertyName = "specializedValue"
			}
			for len(field.Implementation.AdditionalArguments) < field.Implementation.MinimumAdditionalArguments {
				field.Implementation.AdditionalArguments = append(field.Implementation.AdditionalArguments, "'value'")
			}
			spec := exampleSpec()
			spec.Indexes = nil
			spec.Fields = []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}, field}
			require.Empty(t, ValidateSpec(spec))
			definition, err := RenderDefinition(spec)
			require.NoError(t, err)
			require.Empty(t, php.Parse(definition).Errors, definition)
			entity, err := RenderEntity(spec)
			require.NoError(t, err)
			require.Empty(t, php.Parse(entity).Errors, entity)
			if template.ID == "specialized:CheapestPriceField" {
				require.Contains(t, entity, "protected CheapestPrice|CheapestPriceContainer|null $specializedValue = null;")
			}
			if template.ID == "specialized:ConfigJsonField" {
				require.Contains(t, entity, "protected array|bool|float|int|string|null $specializedValue = null;")
			}
		})
	}
}
