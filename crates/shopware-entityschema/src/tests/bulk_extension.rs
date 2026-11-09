//! Tests for bulk extension.

use std::collections::HashMap;

use super::inheritance::{
    backfill_free, field, php_ok, relation, require_issue_code, stored, validation_codes,
};
use crate::*;

fn bulk_notification_association(property: &str, reference: &str) -> FieldSpec {
    FieldSpec {
        property_name: property.into(),
        reference_storage_name: reference.into(),
        source_column: "id".into(),
        target_definition_class: "Shopware\\Core\\Framework\\Notification\\NotificationDefinition"
            .into(),
        target_entity_class: "Shopware\\Core\\Framework\\Notification\\NotificationEntity".into(),
        target_collection_class: "Shopware\\Core\\Framework\\Notification\\NotificationCollection"
            .into(),
        target_entity_name: "notification".into(),
        ..field(&format!("{property}-{reference}"), FIELD_ONE_TO_MANY)
    }
}

fn runtime_string(id: &str, property: &str, storage: &str) -> FieldSpec {
    FieldSpec {
        behavior: Some(FieldBehavior {
            runtime: true,
            ..FieldBehavior::default()
        }),
        ..stored(id, FIELD_STRING, property, storage)
    }
}

fn bulk(class_name: &str, targets: Vec<BulkExtensionTargetSpec>) -> EntitySpec {
    EntitySpec {
        mode: "new".into(),
        definition_kind: DEFINITION_BULK_EXTENSION.into(),
        namespace: "Acme\\Example".into(),
        class_name: class_name.into(),
        create_migration: true,
        bulk_extensions: targets,
        ..EntitySpec::default()
    }
}

#[test]
fn bulk_entity_extension_render_import_and_schema_round_trip() {
    let integration = "Shopware\\Core\\System\\Integration\\IntegrationDefinition";
    let user = "Shopware\\Core\\System\\User\\UserDefinition";
    let targets: HashMap<&str, RelationTarget> = HashMap::from([
        (
            integration,
            relation(
                integration,
                "Shopware\\Core\\System\\Integration\\IntegrationEntity",
                "Shopware\\Core\\System\\Integration\\IntegrationCollection",
                "integration",
            ),
        ),
        (
            user,
            relation(
                user,
                "Shopware\\Core\\System\\User\\UserEntity",
                "Shopware\\Core\\System\\User\\UserCollection",
                "user",
            ),
        ),
    ]);
    let lookup = |class: &str| targets.get(class.trim_matches('\\')).cloned();
    let spec = complete_spec(EntitySpec {
        namespace: "Acme\\Example\\Notification".into(),
        shopware_version: "6.7.1.0".into(),
        ..bulk(
            "Notification",
            vec![
                BulkExtensionTargetSpec {
                    id: "integration".into(),
                    entity_name: "integration".into(),
                    extended_definition_class: integration.into(),
                    extended_fields: targets[integration].fields.clone(),
                    fields: vec![bulk_notification_association(
                        "createdNotifications",
                        "created_by_integration_id",
                    )],
                    indexes: Vec::new(),
                },
                BulkExtensionTargetSpec {
                    id: "user".into(),
                    entity_name: "user".into(),
                    extended_definition_class: user.into(),
                    extended_fields: targets[user].fields.clone(),
                    fields: vec![
                        bulk_notification_association("createdNotifications", "created_by_user_id"),
                        runtime_string("display", "display", "acme_display"),
                    ],
                    indexes: Vec::new(),
                },
            ],
        )
    });
    assert_eq!(
        "Acme\\Example\\Notification\\NotificationBulkEntityExtension",
        spec.definition_class
    );
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );
    let source = render_definition(&spec).unwrap();
    assert!(php_ok(&source), "{source}");
    assert!(source.contains("class NotificationBulkEntityExtension extends BulkEntityExtension"));
    assert!(source.contains("yield IntegrationDefinition::ENTITY_NAME => ["));
    assert!(source.contains("yield UserDefinition::ENTITY_NAME => ["));
    assert_eq!(3, source.matches("new Extension()").count());

    let imported = import_bulk_extension(&source, Some(&lookup)).unwrap();
    assert_eq!(DEFINITION_BULK_EXTENSION, imported.definition_kind);
    assert_eq!("Notification", imported.class_name);
    assert_eq!(2, imported.bulk_extensions.len());
    for target in &imported.bulk_extensions {
        for field in &target.fields {
            assert_ne!(FIELD_LOCKED, field.kind, "{}", field.raw);
        }
    }

    let mut before = schema_entities_from_spec(&spec).unwrap();
    let mut after = schema_entities_from_spec(&imported).unwrap();
    for entity in before.iter_mut().chain(after.iter_mut()) {
        backfill_free(entity);
    }
    assert_eq!(before, after);
    assert_eq!(
        vec!["integration", "user"],
        vec![before[0].name.as_str(), before[1].name.as_str()]
    );
    assert!(before[0].external);
    assert!(before[0].columns.is_empty());
}

#[test]
fn bulk_entity_extension_literal_target_and_rewrite() {
    let spec = complete_spec(bulk(
        "Literal",
        vec![BulkExtensionTargetSpec {
            id: "custom".into(),
            entity_name: "custom_target".into(),
            fields: vec![FieldSpec {
                property_name: "children".into(),
                reference_storage_name: "parent_id".into(),
                source_column: "id".into(),
                target_definition_class: "Acme\\Example\\CustomTargetDefinition".into(),
                target_entity_class: "Acme\\Example\\CustomTargetEntity".into(),
                target_collection_class: "Acme\\Example\\CustomTargetCollection".into(),
                target_entity_name: "custom_target".into(),
                ..field("children", FIELD_ONE_TO_MANY)
            }],
            ..BulkExtensionTargetSpec::default()
        }],
    ));
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );
    let source = render_definition(&spec).unwrap();
    assert!(source.contains("yield 'custom_target' => ["));
    let source = format!(
        "{}    public function customBehavior(): bool {{ return true; }}\n}}\n",
        source.strip_suffix("}\n").unwrap()
    );

    let mut imported = import_bulk_extension(&source, None).unwrap();
    assert_eq!("custom_target", imported.bulk_extensions[0].entity_name);
    imported.bulk_extensions[0]
        .fields
        .push(runtime_string("runtime", "runtime", "runtime"));
    let rewritten = rewrite_definition(&source, &imported).unwrap();
    assert!(php_ok(&rewritten), "{rewritten}");
    assert!(rewritten.contains("function customBehavior"));
    assert!(rewritten.contains("new StringField('runtime', 'runtime')"));
}

#[test]
fn bulk_entity_extension_literal_target_uses_indexed_entity_metadata() {
    let source = r"<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;

class ProductBulkEntityExtension extends BulkEntityExtension
{
    public function collect(): \Generator
    {
        yield 'product' => [];
    }
}
";
    let target = RelationTarget {
        entity_class: String::new(),
        collection_class: String::new(),
        ..relation(
            "Shopware\\Core\\Content\\Product\\ProductDefinition",
            "",
            "",
            "product",
        )
    };
    let lookup_target = target.clone();
    let lookup =
        move |name: &str| (name == lookup_target.entity_name).then(|| lookup_target.clone());
    let spec = import_bulk_extension(source, Some(&lookup)).unwrap();
    assert_eq!(1, spec.bulk_extensions.len());
    assert_eq!(
        target.definition_class,
        spec.bulk_extensions[0].extended_definition_class
    );
    assert_eq!(target.fields, spec.bulk_extensions[0].extended_fields);
}

#[test]
fn bulk_entity_extension_rejects_persisted_scalar_target_field() {
    let spec = complete_spec(bulk(
        "Invalid",
        vec![BulkExtensionTargetSpec {
            id: "product".into(),
            entity_name: "product".into(),
            extended_definition_class: "Shopware\\Core\\Content\\Product\\ProductDefinition".into(),
            fields: vec![stored("label", FIELD_STRING, "label", "acme_label")],
            ..BulkExtensionTargetSpec::default()
        }],
    ));
    require_issue_code(
        &validate_spec(&spec),
        "entity.extension.field.runtime.required",
    );
}

#[test]
fn bulk_entity_extension_rejects_unsupported_shopware_version() {
    let mut spec = complete_spec(EntitySpec {
        shopware_version: "~6.6.9".into(),
        create_migration: false,
        ..bulk(
            "Catalog",
            vec![BulkExtensionTargetSpec {
                id: "product".into(),
                entity_name: "product".into(),
                extended_definition_class: "Shopware\\Core\\Content\\Product\\ProductDefinition"
                    .into(),
                fields: vec![FieldSpec {
                    property_name: "notes".into(),
                    target_definition_class: "Acme\\Example\\NoteDefinition".into(),
                    target_entity_name: "acme_note".into(),
                    reference_storage_name: "product_id".into(),
                    source_column: "id".into(),
                    ..field("notes", FIELD_ONE_TO_MANY)
                }],
                ..BulkExtensionTargetSpec::default()
            }],
        )
    });
    require_issue_code(
        &validate_spec(&spec),
        "entity.bulkExtension.version.unsupported",
    );
    spec.shopware_version = "~6.6.10".into();
    assert!(
        !validation_codes(&validate_spec(&spec))
            .contains(&"entity.bulkExtension.version.unsupported".to_owned())
    );
}

#[test]
fn bulk_entity_extension_preserves_custom_collect_method() {
    let source = r"<?php declare(strict_types=1);

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
";
    let spec = import_bulk_extension(source, None).unwrap();
    assert_eq!(DEFINITION_BULK_EXTENSION, spec.definition_kind);
    assert!(spec.bulk_extensions.is_empty());
    assert!(
        spec.collect_method_raw
            .contains("$this->fieldsFor('product')")
    );
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered), "{rendered}");
    assert!(rendered.contains(&spec.collect_method_raw));

    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert_eq!(source, rewritten);

    let mut conflict = spec.clone();
    conflict.bulk_extensions = vec![BulkExtensionTargetSpec {
        id: "product".into(),
        entity_name: "product".into(),
        ..BulkExtensionTargetSpec::default()
    }];
    require_issue_code(
        &validate_spec(&conflict),
        "entity.bulkExtension.collectRaw.conflict",
    );
    let mut creation = spec.clone();
    creation.mode = "new".into();
    require_issue_code(
        &validate_spec(&creation),
        "entity.bulkExtension.collectRaw.creation.unsupported",
    );
}
