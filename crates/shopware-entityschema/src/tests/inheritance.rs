//! Port of Go `inheritance_test.go` plus helpers shared by batch-A tests.

use crate::*;

pub(super) fn field_by_property(fields: &[FieldSpec], property: &str) -> FieldSpec {
    fields
        .iter()
        .find(|field| field.property_name == property)
        .cloned()
        .unwrap_or_else(|| panic!("field not found: {property}"))
}

pub(super) fn require_issue_code(issues: &[ValidationIssue], code: &str) {
    assert!(
        issues.iter().any(|issue| issue.code == code),
        "validation issue not found: {code}: {issues:?}"
    );
}

pub(super) fn validation_codes(issues: &[ValidationIssue]) -> Vec<String> {
    issues.iter().map(|issue| issue.code.clone()).collect()
}

pub(super) fn backfill_free(entity: &mut Entity) {
    for column in entity.columns.values_mut() {
        column.backfill_sql.clear();
    }
}

pub(super) fn php_ok(source: &str) -> bool {
    swlsp_parser_php::parse(source).errors.is_empty()
}

pub(super) fn s(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

pub(super) fn relation(
    definition: &str,
    entity: &str,
    collection: &str,
    name: &str,
) -> RelationTarget {
    RelationTarget {
        definition_class: definition.to_owned(),
        entity_class: entity.to_owned(),
        collection_class: collection.to_owned(),
        entity_name: name.to_owned(),
        fields: vec![RelationTargetField {
            property_name: "id".to_owned(),
            storage_name: "id".to_owned(),
            primary: true,
        }],
        ..RelationTarget::default()
    }
}

pub(super) fn field(id: &str, kind: &str) -> FieldSpec {
    FieldSpec {
        id: id.to_owned(),
        kind: kind.to_owned(),
        editable: true,
        ..FieldSpec::default()
    }
}

pub(super) fn stored(id: &str, kind: &str, property: &str, storage: &str) -> FieldSpec {
    FieldSpec {
        property_name: property.to_owned(),
        storage_name: storage.to_owned(),
        ..field(id, kind)
    }
}

pub(super) fn example_spec() -> EntitySpec {
    complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Example\\Entity\\Catalog".into(),
        class_name: "CatalogItem".into(),
        entity_name: "acme_catalog_item".into(),
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                required: true,
                api_aware: true,
                max_length: 500,
                ..stored("name", FIELD_STRING, "name", "name")
            },
            FieldSpec {
                required: true,
                ..stored("active", FIELD_BOOL, "active", "active")
            },
            stored("payload", FIELD_JSON, "payload", "payload"),
            FieldSpec {
                foreign_key_property_name: "manufacturerId".into(),
                target_definition_class: "Shopware\\Core\\Content\\Product\\Aggregate\\ProductManufacturer\\ProductManufacturerDefinition".into(),
                target_entity_class: "Shopware\\Core\\Content\\Product\\Aggregate\\ProductManufacturer\\ProductManufacturerEntity".into(),
                target_entity_name: "product_manufacturer".into(),
                reference_field: "id".into(),
                reference_storage_name: "id".into(),
                delete_behavior: DELETE_SET_NULL.into(),
                ..stored("manufacturer", FIELD_MANY_TO_ONE, "manufacturer", "manufacturer_id")
            },
        ],
        indexes: vec![IndexSpec {
            name: "uniq.acme_catalog_item.name".into(),
            kind: INDEX_UNIQUE.into(),
            columns: s(&["name"]),
            translation: false,
        }],
        ..EntitySpec::default()
    })
}

#[test]
fn inheritance_aware_definition_renders_imports_and_round_trips() {
    let target = relation(
        "Acme\\Catalog\\ManufacturerDefinition",
        "Acme\\Catalog\\ManufacturerEntity",
        "Acme\\Catalog\\ManufacturerCollection",
        "acme_manufacturer",
    );
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Product".into(),
        entity_name: "acme_product".into(),
        inheritance_aware: true,
        fields: vec![
            field("id", FIELD_ID),
            FieldSpec {
                property_name: "variants".into(),
                ..field("hierarchy", FIELD_HIERARCHY)
            },
            FieldSpec {
                inherited: true,
                ..stored("number", FIELD_STRING, "number", "number")
            },
            FieldSpec {
                foreign_key_property_name: "manufacturerId".into(),
                target_definition_class: target.definition_class.clone(),
                target_entity_class: target.entity_class.clone(),
                target_collection_class: target.collection_class.clone(),
                target_entity_name: target.entity_name.clone(),
                reference_field: "id".into(),
                reference_storage_name: "id".into(),
                delete_behavior: DELETE_SET_NULL.into(),
                inherited: true,
                inherited_foreign_key: "manufacturer_id".into(),
                association_inherited: true,
                reverse_inherited_property: "manufacturer".into(),
                ..stored(
                    "manufacturer",
                    FIELD_MANY_TO_ONE,
                    "manufacturer",
                    "manufacturer_id",
                )
            },
        ],
        ..EntitySpec::default()
    });
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );

    let definition = render_definition(&spec).unwrap();
    assert!(php_ok(&definition), "{definition}");
    assert!(definition.contains("public function isInheritanceAware(): bool"));
    assert!(definition.contains("return true;"));
    assert!(definition.contains("new StringField('number', 'number'))->addFlags(new Inherited())"));
    assert!(definition.contains("new FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class))->addFlags(new Inherited('manufacturer_id'))"));
    assert!(definition.contains("new Inherited(), new ReverseInherited('manufacturer')"));
    assert_eq!(
        1,
        definition
            .matches("use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\Inherited;")
            .count()
    );
    assert_eq!(1, definition.matches("use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\ReverseInherited;").count());

    let spec_clone = spec.clone();
    let lookup = move |class: &str| -> Option<RelationTarget> {
        if class == target.definition_class {
            return Some(target.clone());
        }
        if class == spec_clone.definition_class {
            return Some(RelationTarget {
                definition_class: spec_clone.definition_class.clone(),
                entity_class: spec_clone.entity_class.clone(),
                collection_class: spec_clone.collection_class.clone(),
                entity_name: spec_clone.entity_name.clone(),
                inheritance_aware: true,
                ..RelationTarget::default()
            });
        }
        None
    };
    let imported = import_definition(&definition, Some(&lookup)).unwrap();
    assert!(imported.inheritance_aware);
    let number = field_by_property(&imported.fields, "number");
    assert!(number.inherited);
    assert!(
        !number
            .preserved_flags
            .contains(&"new Inherited()".to_owned())
    );
    let manufacturer = field_by_property(&imported.fields, "manufacturer");
    assert!(manufacturer.inherited);
    assert_eq!("manufacturer_id", manufacturer.inherited_foreign_key);
    assert!(manufacturer.association_inherited);
    assert_eq!("manufacturer", manufacturer.reverse_inherited_property);
    assert!(
        !manufacturer
            .preserved_flags
            .contains(&"new Inherited()".to_owned())
    );
    assert!(
        !manufacturer
            .association_flags
            .contains(&"new ReverseInherited('manufacturer')".to_owned())
    );

    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(
        before, after,
        "inheritance metadata changed the database schema"
    );
}

#[test]
fn translated_inheritance_keeps_facade_and_storage_flags_separate() {
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Product".into(),
        entity_name: "acme_product".into(),
        inheritance_aware: true,
        fields: vec![
            field("id", FIELD_ID),
            field("hierarchy", FIELD_HIERARCHY),
            FieldSpec {
                required: true,
                translated: true,
                inherited: true,
                translation_inherited: true,
                ..stored("name", FIELD_STRING, "name", "name")
            },
        ],
        ..EntitySpec::default()
    });
    let parent = render_definition(&spec).unwrap();
    let translation = render_translation_definition(&spec).unwrap();
    assert!(parent.contains("new TranslatedField('name'))->addFlags(new Inherited())"));
    assert!(
        translation.contains(
            "new StringField('name', 'name'))->addFlags(new Required(), new Inherited())"
        )
    );

    let spec_clone = spec.clone();
    let lookup = move |class: &str| -> Option<RelationTarget> {
        (class == spec_clone.definition_class).then(|| RelationTarget {
            definition_class: spec_clone.definition_class.clone(),
            entity_class: spec_clone.entity_class.clone(),
            collection_class: spec_clone.collection_class.clone(),
            entity_name: spec_clone.entity_name.clone(),
            inheritance_aware: true,
            ..RelationTarget::default()
        })
    };
    let imported_parent = import_definition(&parent, Some(&lookup)).unwrap();
    let imported_translation = import_translation_definition(&translation, Some(&lookup)).unwrap();
    let attached = attach_translation(imported_parent, imported_translation);
    let name = field_by_property(&attached.fields, "name");
    assert!(name.inherited, "translation-table storage flag");
    assert!(
        name.translation_inherited,
        "parent TranslatedField facade flag"
    );
}

#[test]
fn rewrite_inheritance_aware_method_is_safe_and_reversible() {
    let base = complete_spec(EntitySpec {
        mode: "edit".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Product".into(),
        entity_name: "acme_product".into(),
        fields: vec![field("id", FIELD_ID), field("hierarchy", FIELD_HIERARCHY)],
        ..EntitySpec::default()
    });
    let source = render_definition(&base).unwrap();
    assert!(!source.contains("isInheritanceAware"));

    let mut enabled = base.clone();
    enabled.inheritance_aware = true;
    let rewritten = rewrite_definition(&source, &enabled).unwrap();
    assert!(rewritten.contains("public function isInheritanceAware(): bool"));
    assert_eq!(1, rewritten.matches("function isInheritanceAware").count());

    let disabled = rewrite_definition(&rewritten, &base).unwrap();
    assert!(!disabled.contains("isInheritanceAware"));

    let customized = rewritten.replacen(
        "    public function isInheritanceAware(): bool",
        "    /** Keep this contract documented. */\n    public function isInheritanceAware(): bool",
        1,
    );
    let error = rewrite_definition(&customized, &base).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("cannot remove customized isInheritanceAware method"),
        "{error}"
    );
}

#[test]
fn inheritance_validation_requires_entity_hierarchy() {
    let without_hierarchy = complete_spec(EntitySpec {
        mode: "new".into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "Product".into(),
        entity_name: "acme_product".into(),
        inheritance_aware: true,
        fields: vec![field("id", FIELD_ID)],
        ..EntitySpec::default()
    });
    require_issue_code(
        &validate_spec(&without_hierarchy),
        "entity.inheritance.hierarchy.required",
    );

    let mapping = complete_spec(EntitySpec {
        mode: "new".into(),
        definition_kind: DEFINITION_MAPPING.into(),
        namespace: "Acme\\Catalog".into(),
        class_name: "ProductTag".into(),
        entity_name: "acme_product_tag".into(),
        inheritance_aware: true,
        fields: vec![FieldSpec {
            target_definition_class: "Acme\\Catalog\\ProductDefinition".into(),
            target_entity_name: "acme_product".into(),
            required: true,
            primary: true,
            ..stored("product", FIELD_FOREIGN_KEY, "productId", "product_id")
        }],
        ..EntitySpec::default()
    });
    require_issue_code(
        &validate_spec(&mapping),
        "entity.inheritance.owner.unsupported",
    );
}
