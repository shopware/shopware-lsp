//! Port of Go `extension_test.go`.

use super::inheritance::{backfill_free, field, php_ok, relation, require_issue_code, s, stored};
use crate::*;

const PRODUCT_DEFINITION: &str = "Shopware\\Core\\Content\\Product\\ProductDefinition";

pub(super) fn extension_to_one(property: &str, storage: &str) -> FieldSpec {
    FieldSpec {
        foreign_key_property_name: format!("{property}Id"),
        target_definition_class: "Shopware\\Core\\Content\\Category\\CategoryDefinition".into(),
        target_entity_class: "Shopware\\Core\\Content\\Category\\CategoryEntity".into(),
        target_collection_class: "Shopware\\Core\\Content\\Category\\CategoryCollection".into(),
        target_entity_name: "category".into(),
        reference_field: "id".into(),
        reference_storage_name: "id".into(),
        delete_behavior: DELETE_SET_NULL.into(),
        ..stored(property, FIELD_MANY_TO_ONE, property, storage)
    }
}

fn product_extension(mode: &str, class_name: &str) -> EntitySpec {
    EntitySpec {
        mode: mode.into(),
        definition_kind: DEFINITION_EXTENSION.into(),
        namespace: "Acme\\Example".into(),
        class_name: class_name.into(),
        entity_name: "product".into(),
        extended_definition_class: PRODUCT_DEFINITION.into(),
        ..EntitySpec::default()
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

fn product_relation() -> RelationTarget {
    relation(
        PRODUCT_DEFINITION,
        "Shopware\\Core\\Content\\Product\\ProductEntity",
        "Shopware\\Core\\Content\\Product\\ProductCollection",
        "product",
    )
}

#[test]
fn entity_extension_renders_imports_and_migrates_external_table() {
    let target = product_relation();
    let spec = complete_spec(EntitySpec {
        mode: "new".into(),
        definition_kind: DEFINITION_EXTENSION.into(),
        namespace: "Acme\\Example\\Extension\\Content\\Product".into(),
        class_name: "ProductCustom".into(),
        entity_name: target.entity_name.clone(),
        extended_definition_class: target.definition_class.clone(),
        shopware_version: "~6.7.0".into(),
        fields: vec![
            extension_to_one("category", "acme_category_id"),
            FieldSpec {
                property_name: "relatedProducts".into(),
                reference_storage_name: "parent_id".into(),
                source_column: "id".into(),
                target_definition_class: target.definition_class.clone(),
                target_entity_class: target.entity_class.clone(),
                target_collection_class: target.collection_class.clone(),
                target_entity_name: target.entity_name.clone(),
                ..field("related", FIELD_ONE_TO_MANY)
            },
        ],
        ..EntitySpec::default()
    });
    assert_eq!(
        "Acme\\Example\\Extension\\Content\\Product\\ProductCustomExtension",
        spec.definition_class
    );
    assert!(spec.entity_class.is_empty());
    assert!(spec.collection_class.is_empty());
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );

    let source = render_definition(&spec).unwrap();
    assert!(php_ok(&source), "{source}");
    assert!(source.contains("extends EntityExtension"));
    assert!(source.contains("public function extendFields(FieldCollection $collection): void"));
    assert!(source.contains("return ProductDefinition::ENTITY_NAME;"));
    assert!(!source.contains("getDefinitionClass"));
    assert_eq!(
        3,
        source.matches("new Extension()").count(),
        "the paired foreign key and both associations need the extension flag"
    );
    assert!(!source.contains("CreatedAtField"));
    assert!(!source.contains("UpdatedAtField"));

    let lookup_target = target.clone();
    let lookup =
        move |class: &str| (class == lookup_target.definition_class).then(|| lookup_target.clone());
    let imported = import_extension(&source, Some(&lookup)).unwrap();
    assert!(imported.protection_method_raw.is_empty());
    assert_eq!(DEFINITION_EXTENSION, imported.definition_kind);
    assert_eq!(target.definition_class, imported.extended_definition_class);
    assert_eq!(target.entity_name, imported.entity_name);
    assert_eq!(2, imported.fields.len());
    for field in &imported.fields {
        assert_ne!(FIELD_LOCKED, field.kind, "{}", field.raw);
    }

    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(before, after);
    assert!(before.external);
    assert_eq!("product", before.name);
    assert!(before.columns.contains_key("acme_category_id"));
    assert!(!before.columns.contains_key("id"));
    assert!(!before.columns.contains_key("created_at"));

    let mut next = empty_schema();
    merge_spec_schema(&mut next, &spec).unwrap();
    let (statements, diff) = migration_statements(&empty_schema(), &next, &[]);
    let statements = statements.unwrap();
    assert!(diff.created_entities.is_empty());
    assert_eq!(1, diff.added_columns.len());
    let joined = statements.join("\n");
    assert!(joined.contains("ALTER TABLE `product` ADD COLUMN `acme_category_id` BINARY(16) NULL"));
    assert!(joined.contains("REFERENCES `category` (`id`) ON DELETE SET NULL"));
    assert!(!joined.contains("CREATE TABLE"));
}

#[test]
fn entity_extension_imports_shopware67_literal_entity_name_target() {
    let source = r"<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

class ProductExtension extends EntityExtension
{
    public function extendFields(FieldCollection $collection): void
    {
    }

    public function getEntityName(): string
    {
        return 'product';
    }
}
";
    let target = RelationTarget {
        version_aware: true,
        inheritance_aware: true,
        ..product_relation()
    };
    let lookup_target = target.clone();
    let lookup =
        move |name: &str| (name == lookup_target.entity_name).then(|| lookup_target.clone());
    let spec = import_extension(source, Some(&lookup)).unwrap();
    assert_eq!(target.definition_class, spec.extended_definition_class);
    assert_eq!(target.entity_name, spec.entity_name);
    assert_eq!(target.fields, spec.extended_fields);
}

#[test]
fn entity_extension_rejects_impure_literal_looking_target_methods() {
    let target = RelationTarget {
        definition_class: PRODUCT_DEFINITION.into(),
        entity_name: "product".into(),
        ..RelationTarget::default()
    };
    let lookup = move |name: &str| {
        (name == target.entity_name || name == target.definition_class).then(|| target.clone())
    };
    for (name, body) in [
        (
            "statement before return",
            "$this->audit(); return 'product';",
        ),
        ("compound expression", "return 'pro' . 'duct';"),
    ] {
        let source = format!(
            r"<?php namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ProductExtension extends EntityExtension {{
    public function getEntityName(): string {{ {body} }}
    public function extendFields(FieldCollection $collection): void {{}}
}}"
        );
        let error = import_extension(&source, Some(&lookup)).unwrap_err();
        assert!(
            error.to_string().contains("entity extension target"),
            "{name}: {error}"
        );
    }
}

#[test]
fn entity_extension_indexes_can_combine_owned_and_indexed_target_columns() {
    let spec = complete_spec(EntitySpec {
        namespace: "Acme\\Example\\Extension".into(),
        extended_fields: vec![
            RelationTargetField {
                property_name: "id".into(),
                storage_name: "id".into(),
                primary: true,
            },
            RelationTargetField {
                property_name: "createdAt".into(),
                storage_name: "created_at".into(),
                primary: false,
            },
        ],
        fields: vec![extension_to_one("category", "acme_category_id")],
        indexes: vec![IndexSpec {
            name: "uniq.product.acme_category_id_id".into(),
            kind: INDEX_UNIQUE.into(),
            columns: s(&["acme_category_id", "id"]),
            translation: false,
        }],
        ..product_extension("edit", "Product")
    });
    assert!(
        validate_spec(&spec).is_empty(),
        "{:?}",
        validate_spec(&spec)
    );
    let contribution = schema_from_spec(&spec).unwrap();
    assert_eq!(
        s(&["acme_category_id", "id"]),
        contribution.indexes["uniq.product.acme_category_id_id"].columns
    );
    assert!(
        !contribution.columns.contains_key("id"),
        "the extension must not claim ownership of an existing target column"
    );

    let mut schema = empty_schema();
    schema.entities.insert("product".into(), contribution);
    let product = schema.entities.get_mut("product").unwrap();
    product.indexes.insert(
        "idx.other_extension".into(),
        Index {
            name: "idx.other_extension".into(),
            unique: false,
            columns: s(&["other_extension_column", "id"]),
        },
    );
    product.indexes.insert(
        "idx.product.id".into(),
        Index {
            name: "idx.product.id".into(),
            unique: false,
            columns: s(&["id"]),
        },
    );
    assert_eq!(
        spec.indexes,
        index_specs_from_entities(&spec, &schema),
        "only indexes owned through this extension's columns are restored"
    );

    let mut invalid = spec.clone();
    invalid.indexes = vec![IndexSpec {
        name: "idx.product.id".into(),
        kind: INDEX_NORMAL.into(),
        columns: s(&["id"]),
        translation: false,
    }];
    require_issue_code(
        &validate_spec(&invalid),
        "entity.extension.index.ownedColumn.required",
    );
    invalid.indexes[0].columns = s(&["missing"]);
    require_issue_code(&validate_spec(&invalid), "entity.index.column.unknown");
    invalid.fields[0].storage_name = "id".into();
    invalid.indexes.clear();
    require_issue_code(
        &validate_spec(&invalid),
        "entity.extension.column.duplicate",
    );
}

#[test]
fn entity_extension_before67_includes_legacy_target_method() {
    let spec = complete_spec(EntitySpec {
        shopware_version: "~6.6.0".into(),
        ..product_extension("new", "Product")
    });
    let source = render_definition(&spec).unwrap();
    assert!(source.contains("public function getEntityName(): string"));
    assert!(source.contains("public function getDefinitionClass(): string"));
    assert!(source.contains("return ProductDefinition::class;"));
}

#[test]
fn association_only_entity_extension_produces_no_database_statements() {
    let spec = complete_spec(EntitySpec {
        fields: vec![FieldSpec {
            property_name: "children".into(),
            reference_storage_name: "parent_id".into(),
            source_column: "id".into(),
            target_definition_class: PRODUCT_DEFINITION.into(),
            target_entity_class: "Shopware\\Core\\Content\\Product\\ProductEntity".into(),
            target_collection_class: "Shopware\\Core\\Content\\Product\\ProductCollection".into(),
            target_entity_name: "product".into(),
            ..field("children", FIELD_ONE_TO_MANY)
        }],
        ..product_extension("new", "Product")
    });
    let mut next = empty_schema();
    merge_spec_schema(&mut next, &spec).unwrap();
    assert!(next.entities["product"].external);
    assert!(next.entities["product"].columns.is_empty());
    let (statements, diff) = migration_statements(&empty_schema(), &next, &[]);
    assert!(statements.unwrap().is_empty());
    assert!(!diff.database_changed());
}

#[test]
fn entity_extension_rewrite_preserves_custom_members() {
    let spec = complete_spec(EntitySpec {
        shopware_version: "6.7.1.0".into(),
        fields: vec![runtime_string("note", "note", "acme_note")],
        ..product_extension("new", "Product")
    });
    let source = render_definition(&spec).unwrap();
    let source = format!(
        "{}    public function customBehavior(): bool {{ return true; }}\n}}\n",
        source.strip_suffix("}\n").unwrap()
    );
    let lookup = |class: &str| {
        Some(RelationTarget {
            definition_class: class.to_owned(),
            entity_name: "product".into(),
            ..RelationTarget::default()
        })
    };
    let previous = import_extension(&source, Some(&lookup)).unwrap();
    let mut next = previous.clone();
    next.shopware_version = "6.7.1.0".into();
    next.fields
        .push(runtime_string("label", "label", "acme_label"));
    let rewritten = rewrite_definition(&source, &next).unwrap();
    assert!(php_ok(&rewritten), "{rewritten}");
    assert!(rewritten.contains("customBehavior"));
    assert!(rewritten.contains("new StringField('acme_label', 'label'"));
    assert_eq!(1, rewritten.matches("function getEntityName").count());
}

#[test]
fn entity_extension_rewrite_retargets_managed_methods_and_imports() {
    let previous = complete_spec(EntitySpec {
        shopware_version: "~6.6.0".into(),
        fields: vec![runtime_string("note", "note", "acme_note")],
        ..product_extension("edit", "Catalog")
    });
    let source = render_definition(&previous).unwrap();
    let source = format!(
        "{}    public function customBehavior(): bool {{ return true; }}\n}}\n",
        source.strip_suffix("}\n").unwrap()
    );
    let mut next = previous.clone();
    next.entity_name = "category".into();
    next.extended_definition_class = "Shopware\\Core\\Content\\Category\\CategoryDefinition".into();
    let rewritten = rewrite_definition_from(&source, &previous, &next).unwrap();
    assert!(php_ok(&rewritten), "{rewritten}");
    assert!(!rewritten.contains("ProductDefinition"));
    assert!(rewritten.contains("use Shopware\\Core\\Content\\Category\\CategoryDefinition;"));
    assert!(rewritten.contains("return CategoryDefinition::ENTITY_NAME;"));
    assert!(rewritten.contains("return CategoryDefinition::class;"));
    assert!(rewritten.contains("customBehavior"));
}

#[test]
fn entity_extension_schema_contributions_merge_and_replace_independently() {
    let extension = |class_name: &str, property: &str, storage: &str| {
        complete_spec(EntitySpec {
            fields: vec![extension_to_one(property, storage)],
            ..product_extension("edit", class_name)
        })
    };
    let first = extension("First", "first", "acme_first");
    let second = extension("Second", "second", "acme_second");
    let mut schema = empty_schema();
    merge_spec_schema(&mut schema, &first).unwrap();
    merge_spec_schema(&mut schema, &second).unwrap();
    assert!(schema.entities["product"].external);
    assert!(
        schema.entities["product"]
            .columns
            .contains_key("acme_first")
    );
    assert!(
        schema.entities["product"]
            .columns
            .contains_key("acme_second")
    );

    let replacement = extension("First", "renamed", "acme_renamed");
    replace_spec_schema(&mut schema, Some(&first), &replacement).unwrap();
    assert!(
        !schema.entities["product"]
            .columns
            .contains_key("acme_first")
    );
    assert!(
        schema.entities["product"]
            .columns
            .contains_key("acme_renamed")
    );
    assert!(
        schema.entities["product"]
            .columns
            .contains_key("acme_second")
    );
}

#[test]
fn entity_extension_rejects_runtime_invalid_stored_fields() {
    let base = complete_spec(product_extension("new", "Product"));
    let mut stored_scalar = base.clone();
    stored_scalar.fields = vec![stored("label", FIELD_STRING, "label", "acme_label")];
    require_issue_code(
        &validate_spec(&stored_scalar),
        "entity.extension.field.runtime.required",
    );

    let mut standalone_fk = base.clone();
    standalone_fk.fields = vec![FieldSpec {
        target_definition_class: "Shopware\\Core\\Content\\Category\\CategoryDefinition".into(),
        target_entity_name: "category".into(),
        reference_storage_name: "id".into(),
        ..stored(
            "category",
            FIELD_FOREIGN_KEY,
            "categoryId",
            "acme_category_id",
        )
    }];
    require_issue_code(
        &validate_spec(&standalone_fk),
        "entity.extension.foreignKey.association.required",
    );

    let mut runtime_scalar = stored_scalar.clone();
    runtime_scalar.fields[0].behavior = Some(FieldBehavior {
        runtime: true,
        ..FieldBehavior::default()
    });
    assert!(
        validate_spec(&runtime_scalar).is_empty(),
        "{:?}",
        validate_spec(&runtime_scalar)
    );
    let entity = schema_from_spec(&runtime_scalar).unwrap();
    assert!(entity.columns.is_empty());
    let definition = render_definition(&runtime_scalar).unwrap();
    assert!(definition.contains("new Runtime()"));
}

#[test]
fn entity_extension_protections_round_trip_and_rewrite() {
    let spec = complete_spec(EntitySpec {
        read_protected: true,
        read_protection_scopes: s(&["system", "user"]),
        write_protected: true,
        write_protection_scopes: s(&["system"]),
        ..product_extension("new", "Product")
    });
    let source = render_definition(&spec).unwrap();
    assert!(php_ok(&source), "{source}");
    assert!(
        source
            .contains("function extendProtections(EntityProtectionCollection $protections): void")
    );
    assert!(source.contains("$protections->add(new ReadProtection('system', 'user'));"));
    assert!(source.contains("$protections->add(new WriteProtection('system'));"));

    let lookup = |class: &str| {
        Some(RelationTarget {
            definition_class: class.to_owned(),
            entity_name: "product".into(),
            ..RelationTarget::default()
        })
    };
    let mut imported = import_extension(&source, Some(&lookup)).unwrap();
    assert!(imported.read_protected);
    assert_eq!(s(&["system", "user"]), imported.read_protection_scopes);
    assert!(imported.write_protected);
    assert_eq!(s(&["system"]), imported.write_protection_scopes);

    imported.write_protected = false;
    imported.write_protection_scopes.clear();
    let (_, class) = crate::rewrite::parsed_php_class(&source).unwrap();
    assert!(crate::rewrite::safe_extension_protection_method(
        crate::rewrite::named_method(&class, "extendProtections").as_ref()
    ));
    let rewritten = rewrite_definition(&source, &imported).unwrap();
    assert!(rewritten.contains("new ReadProtection('system', 'user')"));
    assert!(!rewritten.contains("new WriteProtection"));

    let custom = source.replacen(
        "    public function extendProtections(EntityProtectionCollection $protections): void\n    {",
        "    public function extendProtections(EntityProtectionCollection $protections): void\n    {\n        // keep custom protection logic",
        1,
    );
    let mut locked = import_extension(&custom, Some(&lookup)).unwrap();
    assert!(
        locked
            .protection_method_raw
            .contains("keep custom protection logic")
    );
    locked
        .fields
        .push(runtime_string("runtime", "runtime", "runtime"));
    let rewritten = rewrite_definition(&custom, &locked).unwrap();
    assert!(rewritten.contains("keep custom protection logic"));
}

#[test]
fn patch_entity_extension_service_configuration() {
    for path in ["services.yaml", "services.xml"] {
        let result = patch_tagged_service_configuration(
            path,
            "",
            "Acme\\Example\\ProductExtension",
            "shopware.entity.extension",
        )
        .unwrap();
        assert!(result.contains("Acme\\Example\\ProductExtension"));
        assert!(result.contains("shopware.entity.extension"));
    }
}
