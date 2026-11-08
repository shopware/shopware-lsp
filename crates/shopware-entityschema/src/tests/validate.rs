use super::render::example_spec;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn validation_codes(issues: &[ValidationIssue]) -> Vec<String> {
    issues.iter().map(|issue| issue.code.clone()).collect()
}

fn base(mode: &str, fields: Vec<FieldSpec>) -> EntitySpec {
    EntitySpec {
        mode: s(mode),
        namespace: s("Acme\\Example"),
        class_name: s("Example"),
        entity_name: s("acme_example"),
        fields,
        ..EntitySpec::default()
    }
}

fn id_field() -> FieldSpec {
    FieldSpec {
        id: s("id"),
        kind: s(FIELD_ID),
        editable: true,
        ..FieldSpec::default()
    }
}

fn scalar(id: &str, kind: &str, property: &str, storage: &str) -> FieldSpec {
    FieldSpec {
        id: s(id),
        kind: s(kind),
        property_name: s(property),
        storage_name: s(storage),
        editable: true,
        ..FieldSpec::default()
    }
}

#[test]
fn validate_spec_rejects_invalid_advanced_field_settings() {
    let spec = complete_spec(base(
        "new",
        vec![
            id_field(),
            FieldSpec {
                max_length: 20000,
                search_ranking: -1.0,
                ..scalar("name", FIELD_STRING, "name", "name")
            },
            FieldSpec {
                min: Some(10),
                max: Some(5),
                ..scalar("position", FIELD_INT, "position", "position")
            },
            FieldSpec {
                id: s("children"),
                kind: s(FIELD_ONE_TO_MANY),
                property_name: s("children"),
                target_definition_class: s("Acme\\Example\\ChildDefinition"),
                target_collection_class: s("Acme\\Example\\ChildCollection"),
                reference_storage_name: s("parent_id"),
                source_column: s("id"),
                delete_behavior: s("invalid"),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("related"),
                kind: s(FIELD_MANY_TO_MANY),
                property_name: s("related"),
                target_definition_class: s("Acme\\Example\\ChildDefinition"),
                target_collection_class: s("Acme\\Example\\ChildCollection"),
                mapping_definition_class: s("Acme\\Example\\MappingDefinition"),
                mapping_local_column: s("example_id"),
                mapping_reference_column: s("child_id"),
                source_column: s("id"),
                reference_field: s("id"),
                association_search_rank: f64::INFINITY,
                editable: true,
                ..FieldSpec::default()
            },
        ],
    ));
    let codes = validation_codes(&validate_spec(&spec));
    for code in [
        "entity.field.length.invalid",
        "entity.field.searchRanking.invalid",
        "entity.field.range.invalid",
        "entity.relation.delete.invalid",
        "entity.association.searchRanking.invalid",
    ] {
        assert!(codes.iter().any(|candidate| candidate == code), "{code}");
    }
}

#[test]
fn validate_spec_requires_reference_version_for_required_relation() {
    let target = "Shopware\\Core\\Content\\Product\\ProductDefinition";
    let mut spec = complete_spec(base(
        "new",
        vec![
            id_field(),
            FieldSpec {
                id: s("product-version"),
                kind: s(FIELD_REFERENCE_VERSION),
                property_name: s("productVersionId"),
                storage_name: s("product_version_id"),
                target_definition_class: s(target),
                target_entity_name: s("product"),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("product"),
                kind: s(FIELD_MANY_TO_ONE),
                property_name: s("product"),
                foreign_key_property_name: s("productId"),
                storage_name: s("product_id"),
                required: true,
                delete_behavior: s(DELETE_RESTRICT),
                target_definition_class: s(target),
                target_entity_class: s("Shopware\\Core\\Content\\Product\\ProductEntity"),
                target_entity_name: s("product"),
                reference_field: s("id"),
                reference_storage_name: s("id"),
                editable: true,
                ..FieldSpec::default()
            },
        ],
    ));
    assert!(
        validation_codes(&validate_spec(&spec)).contains(&s("entity.referenceVersion.required"))
    );
    spec.fields[1].required = true;
    assert!(
        !validation_codes(&validate_spec(&spec)).contains(&s("entity.referenceVersion.required"))
    );
}

#[test]
fn validate_spec_keeps_translation_columns_out_of_parent_indexes() {
    let mut spec = complete_spec(EntitySpec {
        indexes: vec![IndexSpec {
            name: s("idx.acme_example.name"),
            kind: s(INDEX_NORMAL),
            columns: vec![s("name")],
            translation: false,
        }],
        ..base(
            "new",
            vec![
                id_field(),
                FieldSpec {
                    translated: true,
                    ..scalar("name", FIELD_STRING, "name", "name")
                },
            ],
        )
    });
    assert!(validation_codes(&validate_spec(&spec)).contains(&s("entity.index.column.translated")));
    spec.indexes.clear();
    assert_eq!(validate_spec(&spec), vec![]);

    spec.fields[1].kind = s(FIELD_MANY_TO_ONE);
    assert!(
        validation_codes(&validate_spec(&spec)).contains(&s("entity.translation.kind.unsupported"))
    );
}

#[test]
fn validate_spec_accepts_legal_core_aliases_and_association_shapes() {
    let target = "Acme\\Example\\TargetDefinition";
    let runtime = Some(FieldBehavior {
        runtime: true,
        ..FieldBehavior::default()
    });
    let raw = |field: FieldSpec, raw: &str| FieldSpec {
        raw: s(raw),
        ..field
    };
    let reference = |id: &str, property: &str, storage: &str| FieldSpec {
        target_definition_class: s(target),
        target_entity_name: s("target"),
        ..scalar(id, FIELD_REFERENCE_VERSION, property, storage)
    };
    let spec = complete_spec(base(
        "edit",
        vec![
            id_field(),
            raw(
                scalar("icon-raw", FIELD_BLOB, "iconRaw", "icon"),
                "new BlobField('icon', 'iconRaw')",
            ),
            raw(
                FieldSpec {
                    behavior: runtime.clone(),
                    ..scalar("icon-runtime", FIELD_STRING, "icon", "icon")
                },
                "runtime icon",
            ),
            raw(
                FieldSpec {
                    behavior: runtime,
                    ..scalar("runtime-config", FIELD_JSON, "fieldConfig", "fieldConfig")
                },
                "runtime config",
            ),
            raw(
                reference("first-reference", "firstVersionId", "first_version_id"),
                "first reference",
            ),
            raw(
                reference("second-reference", "secondVersionId", "second_version_id"),
                "second reference",
            ),
            FieldSpec {
                id: s("qualified-relation"),
                kind: s(FIELD_MANY_TO_ONE),
                property_name: s("qualified"),
                storage_name: s("target.id"),
                uses_existing_column: true,
                target_definition_class: s(target),
                target_entity_class: s("Acme\\Example\\TargetEntity"),
                target_entity_name: s("target"),
                reference_field: s("id"),
                reference_storage_name: s("id"),
                raw: s("qualified relation"),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("persona-one-to-many"),
                kind: s(FIELD_ONE_TO_MANY),
                property_name: s("personaPromotions"),
                target_definition_class: s(target),
                target_collection_class: s("Acme\\Example\\TargetCollection"),
                reference_storage_name: s("rule_id"),
                source_column: s("id"),
                raw: s("one to many"),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("persona-many-to-many"),
                kind: s(FIELD_MANY_TO_MANY),
                property_name: s("personaPromotions"),
                target_definition_class: s(target),
                target_collection_class: s("Acme\\Example\\TargetCollection"),
                mapping_definition_class: s("Acme\\Example\\TargetMappingDefinition"),
                mapping_local_column: s("rule_id"),
                mapping_reference_column: s("target_id"),
                source_column: s("id"),
                reference_field: s("id"),
                raw: s("many to many"),
                editable: true,
                ..FieldSpec::default()
            },
        ],
    ));
    assert_eq!(validate_spec(&spec), vec![]);
}

#[test]
fn validate_spec_still_rejects_new_duplicate_properties_and_columns() {
    let spec = complete_spec(base(
        "new",
        vec![
            id_field(),
            scalar("first", FIELD_BOOL, "active", "active"),
            scalar("second", FIELD_BOOL, "active", "active"),
        ],
    ));
    let codes = validation_codes(&validate_spec(&spec));
    assert!(codes.contains(&s("entity.field.property.duplicate")));
    assert!(codes.contains(&s("entity.field.storage.duplicate")));
}

#[test]
fn validate_spec_allows_inherited_translation_association_without_entity_hierarchy() {
    let mut spec = complete_spec(base(
        "edit",
        vec![
            id_field(),
            FieldSpec {
                translated: true,
                ..scalar("name", FIELD_STRING, "name", "name")
            },
        ],
    ));
    spec.translation.as_mut().unwrap().association_inherited = true;
    assert_eq!(validate_spec(&spec), vec![]);
}

#[test]
fn validate_spec_allows_leading_underscores_in_dal_storage_identities() {
    let mut spec = example_spec();
    spec.entity_name = s("_acme_entity");
    spec.fields[0].storage_name = s("_id");
    spec.translation = Some(TranslationSpec {
        enabled: true,
        entity_name: s("_acme_entity_translation"),
        definition_class: s("Acme\\Example\\AcmeEntityTranslationDefinition"),
        entity_class: s("Acme\\Example\\AcmeEntityTranslationEntity"),
        collection_class: s("Acme\\Example\\AcmeEntityTranslationCollection"),
        parent_definition_class: spec.definition_class.clone(),
        parent_storage_name: s("_acme_entity_id"),
        parent_property_name: s("acmeEntity"),
        association_property: s("translations"),
        association_local_field: s("_id"),
        ..TranslationSpec::default()
    });
    let codes = validation_codes(&validate_spec(&spec));
    for code in [
        "entity.name.invalid",
        "entity.field.storage.invalid",
        "entity.translation.name.invalid",
        "entity.translation.parentStorage.invalid",
        "entity.translation.localField.invalid",
    ] {
        assert!(!codes.contains(&s(code)), "{code}");
    }
}
