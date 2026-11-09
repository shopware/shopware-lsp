//! Tests for specialized fields.

use std::collections::HashMap;

use super::inheritance::{
    backfill_free, example_spec, field, field_by_property, php_ok, require_issue_code,
    validation_codes,
};
use crate::*;

fn template(id: &str) -> SpecializedFieldTemplate {
    specialized_field_templates()
        .into_iter()
        .find(|template| template.id == id)
        .unwrap_or_default()
}

#[test]
fn specialized_class_based_fields_remain_editable_and_round_trip() {
    let source = r"<?php declare(strict_types=1);
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
}";
    let spec = import_definition(source, None).unwrap();
    assert_eq!(9, spec.fields.len());
    for field in &spec.fields {
        assert_ne!(FIELD_LOCKED, field.kind, "{}", field.raw);
        assert!(field.editable);
        if field.kind != FIELD_ID {
            assert!(field.implementation.is_some());
        }
    }
    assert_eq!(
        FIELD_JSON,
        field_by_property(&spec.fields, "customFields").kind
    );
    assert_eq!(96, field_by_property(&spec.fields, "number").max_length);
    assert_eq!(
        FIELD_FOREIGN_KEY,
        field_by_property(&spec.fields, "createdById").kind
    );
    assert_eq!(
        "state_machine_state",
        field_by_property(&spec.fields, "stateId").target_entity_name
    );
    assert!(
        field_by_property(&spec.fields, "locked")
            .implementation
            .unwrap()
            .implicit_computed
    );

    let mut entity = schema_from_spec(&spec).unwrap();
    assert_eq!("JSON", entity.columns["custom_fields"].sql_type);
    assert_eq!("VARCHAR(96)", entity.columns["number"].sql_type);
    assert_eq!("BINARY(16)", entity.columns["created_by_id"].sql_type);
    assert_eq!("TINYINT(1)", entity.columns["locked"].sql_type);

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered), "{rendered}");
    assert!(rendered.contains("new CustomFields('custom_fields', 'customFields')"));
    assert!(rendered.contains("new NumberRangeField('number', 'number', 96)"));
    assert!(rendered.contains("new CreatedByField([\\Shopware\\Core\\Framework\\Context::SYSTEM_SCOPE, \\Shopware\\Core\\Framework\\Context::CRUD_API_SCOPE])"));
    assert!(rendered.contains("new StateMachineStateField('state_id', 'stateId', \\Shopware\\Core\\Checkout\\Order\\OrderStates::STATE_MACHINE)"));
    assert!(rendered.contains("new LockedField(false)"));

    let round_tripped = import_definition(&rendered, None).unwrap();
    let mut after = schema_from_spec(&round_tripped).unwrap();
    backfill_free(&mut entity);
    backfill_free(&mut after);
    assert_eq!(entity, after);

    let entity_source = render_entity(&spec).unwrap();
    assert!(
        !entity_source.contains("$customFields"),
        "custom JSON entity members stay owned by existing custom code or traits"
    );
    assert!(entity_source.contains("use EntityCustomFieldsTrait;"));
    assert!(
        !entity_source.contains("$price"),
        "structured JSON entity members stay owned by existing custom code"
    );
    assert!(entity_source.contains("protected string $number;"));
    assert!(entity_source.contains("public function isLocked(): ?bool"));
    assert!(!entity_source.contains("public function setLocked("));
}

#[test]
fn custom_fields_template_uses_and_rewrites_entity_trait() {
    let mut custom = template("specialized:CustomFields").field;
    assert!(custom.implementation.is_some());
    assert_eq!(
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCustomFieldsTrait",
        custom.implementation.as_ref().unwrap().entity_trait
    );
    custom.id = "custom-fields".into();
    let mut spec = example_spec();
    spec.indexes.clear();
    spec.fields = vec![field("id", FIELD_ID), custom];

    let entity_source = render_entity(&spec).unwrap();
    assert!(
        entity_source.contains(
            "use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCustomFieldsTrait;"
        )
    );
    assert!(entity_source.contains("use EntityCustomFieldsTrait;"));
    assert!(!entity_source.contains("$customFields"));

    let mut next = spec.clone();
    next.fields.truncate(1);
    let rewritten = rewrite_entity(&entity_source, &spec, &next).unwrap();
    assert!(!rewritten.contains("EntityCustomFieldsTrait"));
    assert!(php_ok(&rewritten), "{rewritten}");
}

#[test]
fn translated_custom_fields_use_trait_on_both_entities() {
    let mut custom = template("specialized:CustomFields").field;
    custom.id = "custom-fields".into();
    custom.translated = true;
    let mut spec = example_spec();
    spec.indexes.clear();
    spec.fields = vec![field("id", FIELD_ID), custom];
    let spec = complete_spec(spec);

    let parent = render_entity(&spec).unwrap();
    let translation = render_translation_entity(&spec).unwrap();
    for source in [parent, translation] {
        assert!(source.contains("use EntityCustomFieldsTrait;"), "{source}");
        assert!(!source.contains("$customFields"));
        assert!(php_ok(&source), "{source}");
    }
}

#[test]
fn specialized_field_templates_cover_importer_catalog() {
    let templates = specialized_field_templates();
    assert_eq!(
        crate::specialized_fields::descriptors().len(),
        templates.len()
    );
    let mut by_id: HashMap<String, SpecializedFieldTemplate> = HashMap::new();
    for template in templates {
        assert!(!template.id.is_empty());
        assert!(!template.label.is_empty());
        let implementation = template.field.implementation.as_ref().unwrap();
        assert!(!implementation.class.is_empty());
        assert!(implementation.manage_entity);
        by_id.insert(template.id.clone(), template);
    }
    let implementation = |id: &str| by_id[id].field.implementation.clone().unwrap();
    assert_eq!(
        "custom_fields",
        by_id["specialized:CustomFields"].field.storage_name
    );
    assert_eq!(
        "array",
        implementation("specialized:CustomFields").entity_type
    );
    assert_eq!(
        "Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\CalculatedPrice",
        implementation("specialized:CalculatedPriceField").entity_type
    );
    assert_eq!(
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Pricing\\PriceCollection",
        implementation("specialized:PriceField").entity_type
    );
    assert!(
        implementation("specialized:CheapestPriceField")
            .entity_type
            .contains("CheapestPriceContainer")
    );
    assert_eq!(
        1,
        implementation("specialized:ManyToManyIdField").minimum_additional_arguments
    );
    assert_eq!(
        1,
        implementation("specialized:StateMachineStateField").minimum_additional_arguments
    );
    assert_eq!(
        "6.7.1",
        by_id["specialized:MeasurementUnitsField"].minimum_shopware_version
    );
}

#[test]
fn specialized_field_templates_respect_shopware_version() {
    let template = template("specialized:MeasurementUnitsField");
    let class = template
        .field
        .implementation
        .as_ref()
        .unwrap()
        .class
        .clone();
    assert!(!specialized_field_supported(&class, "~6.7.0"));
    assert!(specialized_field_supported(&class, "6.7.1.0"));
    assert!(specialized_field_supported(&class, "dev-trunk"));

    let mut spec = example_spec();
    spec.shopware_version = "6.7.0".into();
    spec.fields.push(template.field.clone());
    require_issue_code(
        &validate_spec(&spec),
        "entity.field.implementation.version.unsupported",
    );
    spec.shopware_version = "6.7.1".into();
    assert!(
        !validation_codes(&validate_spec(&spec))
            .contains(&"entity.field.implementation.version.unsupported".to_owned())
    );
}

#[test]
fn every_specialized_field_template_renders_valid_php() {
    for template in specialized_field_templates() {
        let mut specialized = template.field.clone();
        specialized.id = "specialized".into();
        if specialized.storage_name.is_empty() {
            specialized.storage_name = "specialized_value".into();
        }
        if specialized.property_name.is_empty() {
            specialized.property_name = "specializedValue".into();
        }
        let implementation = specialized.implementation.as_mut().unwrap();
        while (implementation.additional_arguments.len() as i64)
            < implementation.minimum_additional_arguments
        {
            implementation.additional_arguments.push("'value'".into());
        }
        let mut spec = example_spec();
        spec.indexes.clear();
        spec.fields = vec![field("id", FIELD_ID), specialized];
        assert!(
            validate_spec(&spec).is_empty(),
            "{}: {:?}",
            template.id,
            validate_spec(&spec)
        );
        let definition = render_definition(&spec).unwrap();
        assert!(php_ok(&definition), "{definition}");
        let entity = render_entity(&spec).unwrap();
        assert!(php_ok(&entity), "{entity}");
        if template.id == "specialized:CheapestPriceField" {
            assert!(entity.contains(
                "protected CheapestPrice|CheapestPriceContainer|null $specializedValue = null;"
            ));
        }
        if template.id == "specialized:ConfigJsonField" {
            assert!(
                entity.contains(
                    "protected array|bool|float|int|string|null $specializedValue = null;"
                )
            );
        }
    }
}
