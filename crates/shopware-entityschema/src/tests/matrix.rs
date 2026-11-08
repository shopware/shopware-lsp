use std::collections::BTreeMap;

use crate::model::{
    foreign_key_columns, foreign_key_reference_columns, generated_database_object_name,
};
use crate::*;

/// Clears transient migration-only data before comparing schema shapes.
pub(super) fn backfill_free(entity: &mut Entity) {
    for column in entity.columns.values_mut() {
        column.backfill_sql.clear();
    }
}

fn php_errors(source: &str) -> usize {
    swlsp_parser_php::parse(source).errors.len()
}

fn s(value: &str) -> String {
    value.to_owned()
}

fn product_target() -> RelationTarget {
    RelationTarget {
        definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
        entity_class: s("Shopware\\Core\\Content\\Product\\ProductEntity"),
        collection_class: s("Shopware\\Core\\Content\\Product\\ProductCollection"),
        entity_name: s("product"),
        fields: vec![RelationTargetField {
            property_name: s("id"),
            storage_name: s("id"),
            primary: true,
        }],
        version_aware: true,
        ..RelationTarget::default()
    }
}

fn column(name: &str, sql_type: &str) -> Column {
    Column {
        name: s(name),
        sql_type: s(sql_type),
        ..Column::default()
    }
}

#[test]
fn supported_field_matrix_renders_imports_and_round_trips() {
    let target = product_target();
    let relation = |id: &str, kind: &str, property: &str| FieldSpec {
        id: s(id),
        kind: s(kind),
        property_name: s(property),
        target_definition_class: target.definition_class.clone(),
        target_entity_class: target.entity_class.clone(),
        target_collection_class: target.collection_class.clone(),
        target_entity_name: target.entity_name.clone(),
        editable: true,
        ..FieldSpec::default()
    };
    let scalar = |id: &str, kind: &str, property: &str, storage: &str| FieldSpec {
        id: s(id),
        kind: s(kind),
        property_name: s(property),
        storage_name: s(storage),
        editable: true,
        ..FieldSpec::default()
    };
    let fields = vec![
        FieldSpec {
            id: s("id"),
            kind: s(FIELD_ID),
            editable: true,
            ..FieldSpec::default()
        },
        FieldSpec {
            id: s("auto"),
            kind: s(FIELD_AUTO_INCREMENT),
            editable: true,
            ..FieldSpec::default()
        },
        FieldSpec {
            id: s("version"),
            kind: s(FIELD_VERSION),
            api_aware: true,
            editable: true,
            ..FieldSpec::default()
        },
        FieldSpec {
            id: s("reference-version"),
            kind: s(FIELD_REFERENCE_VERSION),
            property_name: s("productVersionId"),
            storage_name: s("product_version_id"),
            target_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
            target_entity_class: s("Shopware\\Core\\Content\\Product\\ProductEntity"),
            target_entity_name: s("product"),
            required: true,
            primary: true,
            editable: true,
            ..FieldSpec::default()
        },
        FieldSpec {
            max_length: 500,
            required: true,
            search_ranking: 0.5,
            migration_default: s("'existing'"),
            ..scalar("string", FIELD_STRING, "title", "title")
        },
        scalar("long", FIELD_LONG_TEXT, "description", "description"),
        scalar("blob", FIELD_BLOB, "payloadRaw", "payload_raw"),
        scalar("int", FIELD_INT, "position", "position"),
        scalar("float", FIELD_FLOAT, "factor", "factor"),
        scalar("bool", FIELD_BOOL, "active", "active"),
        scalar("date", FIELD_DATE, "validFrom", "valid_from"),
        scalar("datetime", FIELD_DATE_TIME, "publishedAt", "published_at"),
        scalar("json", FIELD_JSON, "config", "config"),
        FieldSpec {
            element_type_class: s(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\StringField",
            ),
            ..scalar("list", FIELD_LIST, "tags", "tags")
        },
        scalar("object", FIELD_OBJECT, "metadata", "metadata"),
        FieldSpec {
            foreign_key_property_name: s("productId"),
            storage_name: s("product_id"),
            association_api_aware: true,
            ..relation("many-one", FIELD_MANY_TO_ONE, "product")
        },
        FieldSpec {
            foreign_key_property_name: s("featuredProductId"),
            storage_name: s("featured_product_id"),
            ..relation("one-one", FIELD_ONE_TO_ONE, "featuredProduct")
        },
        FieldSpec {
            storage_name: s("id"),
            reference_field: s("extension_id"),
            reference_storage_name: s("extension_id"),
            uses_existing_column: true,
            association_search_rank: 0.5,
            ..relation("inverse-one", FIELD_ONE_TO_ONE, "inverseProduct")
        },
        FieldSpec {
            reference_storage_name: s("extension_id"),
            delete_behavior: s(DELETE_CASCADE),
            ..relation("one-many", FIELD_ONE_TO_MANY, "products")
        },
        FieldSpec {
            mapping_definition_class: s("Acme\\Example\\ExampleProductDefinition"),
            mapping_local_column: s("example_id"),
            mapping_reference_column: s("product_id"),
            source_column: s("id"),
            reference_field: s("id"),
            association_api_aware: true,
            ..relation("many-many", FIELD_MANY_TO_MANY, "relatedProducts")
        },
    ];
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example"),
        class_name: s("Example"),
        entity_name: s("acme_example"),
        fields,
        create_migration: true,
        ..EntitySpec::default()
    });
    assert_eq!(validate_spec(&spec), vec![]);

    let definition = render_definition(&spec).unwrap();
    let entity_source = render_entity(&spec).unwrap();
    for source in [&definition, &entity_source] {
        assert_eq!(php_errors(source), 0, "{source}");
    }
    assert!(definition.contains("new AutoIncrementField()"));
    assert!(definition.contains("new VersionField()"));
    assert!(
        definition
            .contains("new ReferenceVersionField(ProductDefinition::class, 'product_version_id')")
    );
    assert!(definition.contains(
        "ReferenceVersionField(ProductDefinition::class, 'product_version_id'))->addFlags(new Required(), new PrimaryKey())"
    ));
    assert!(definition.contains("new ListField('tags', 'tags', StringField::class)"));
    assert!(
        definition.contains("new OneToOneAssociationField('inverseProduct', 'id', 'extension_id'")
    );
    assert!(definition.contains("new ManyToManyAssociationField("));
    assert_eq!(
        definition.matches("new FkField(").count(),
        2,
        "the inverse one-to-one must not create an FK field"
    );
    assert!(entity_source.contains("protected ?object $payloadRaw = null;"));
    assert!(!entity_source.contains("$versionId"));
    assert!(!entity_source.contains("$productVersionId"));

    let lookup = |class: &str| (class == target.definition_class).then(|| target.clone());
    let imported = import_definition(&definition, Some(&lookup)).unwrap();
    assert_eq!(imported.fields.len(), spec.fields.len());
    for field in &imported.fields {
        assert_ne!(
            field.kind, FIELD_LOCKED,
            "unexpected locked field: {}",
            field.raw
        );
    }
    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(before, after);
}

#[test]
fn version_aware_relation_uses_composite_foreign_key_and_index() {
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example"),
        class_name: s("Example"),
        entity_name: s("acme_example"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("product-version"),
                kind: s(FIELD_REFERENCE_VERSION),
                property_name: s("productVersionId"),
                storage_name: s("product_version_id"),
                target_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
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
                target_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
                target_entity_class: s("Shopware\\Core\\Content\\Product\\ProductEntity"),
                target_entity_name: s("product"),
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let entity = schema_from_spec(&spec).unwrap();
    let fk = &entity.foreign_keys["fk.acme_example.product_id"];
    assert_eq!(
        foreign_key_columns(fk),
        vec!["product_id", "product_version_id"]
    );
    assert_eq!(foreign_key_reference_columns(fk), vec!["id", "version_id"]);
    assert_eq!(
        entity.indexes["idx.acme_example.product_id"].columns,
        vec!["product_id", "product_version_id"]
    );

    let mut next = empty_schema();
    next.entities.insert(entity.name.clone(), entity);
    let (statements, _) = migration_statements(&empty_schema(), &next, &[]);
    let joined = statements.unwrap().join("\n");
    assert!(joined.contains(
        "FOREIGN KEY (`product_id`, `product_version_id`) REFERENCES `product` (`id`, `version_id`)"
    ));
}

#[test]
fn generated_relation_object_names_stay_within_mysql_limit_and_remain_structural() {
    let entity_name = format!("a{}", "_entity".repeat(8));
    let storage_name = format!("b{}", "_column".repeat(8));
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example"),
        class_name: s("LongMapping"),
        entity_name: entity_name.clone(),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("target"),
                kind: s(FIELD_FOREIGN_KEY),
                property_name: s("targetId"),
                storage_name: storage_name.clone(),
                target_definition_class: s("Acme\\Target\\TargetDefinition"),
                target_entity_name: s("target"),
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let entity = schema_from_spec(&spec).unwrap();
    assert_eq!(entity.foreign_keys.len(), 1);
    assert_eq!(entity.indexes.len(), 1);
    for name in entity.foreign_keys.keys() {
        assert!(name.len() <= 64);
        assert_eq!(
            *name,
            generated_database_object_name("fk", &entity_name, &storage_name)
        );
    }
    for name in entity.indexes.keys() {
        assert!(name.len() <= 64);
        assert_eq!(
            *name,
            generated_database_object_name("idx", &entity_name, &storage_name)
        );
    }
    assert!(
        index_specs_from_entity(&spec, &entity).is_empty(),
        "generated relation index must not become a custom designer index"
    );
}

#[test]
fn migration_matrix_covers_columns_indexes_foreign_keys_and_json_constraints() {
    let mut before = empty_schema();
    before.entities.insert(
        s("example"),
        Entity {
            name: s("example"),
            columns: BTreeMap::from([
                (
                    s("id"),
                    Column {
                        not_null: true,
                        primary_key: true,
                        ..column("id", "BINARY(16)")
                    },
                ),
                (s("payload"), column("payload", "JSON")),
                (s("name"), column("name", "VARCHAR(255)")),
            ]),
            indexes: BTreeMap::from([(
                s("idx.example.name"),
                Index {
                    name: s("idx.example.name"),
                    unique: false,
                    columns: vec![s("name")],
                },
            )]),
            ..Entity::default()
        },
    );
    let mut after = before.clone();
    let entity = after.entities.get_mut("example").unwrap();
    entity
        .columns
        .insert(s("payload"), column("payload", "LONGTEXT"));
    entity.columns.insert(
        s("name"),
        Column {
            not_null: true,
            backfill_sql: s("'unknown'"),
            ..column("name", "VARCHAR(500)")
        },
    );
    entity
        .columns
        .insert(s("settings"), column("settings", "JSON"));
    entity.columns.insert(
        s("active"),
        Column {
            not_null: true,
            backfill_sql: s("0"),
            ..column("active", "TINYINT(1)")
        },
    );
    entity.columns.insert(
        s("auto_increment"),
        Column {
            not_null: true,
            auto_increment: true,
            ..column("auto_increment", "BIGINT UNSIGNED")
        },
    );
    entity
        .columns
        .insert(s("product_id"), column("product_id", "BINARY(16)"));
    entity.indexes.remove("idx.example.name");
    entity.indexes.insert(
        s("uniq.example.name"),
        Index {
            name: s("uniq.example.name"),
            unique: true,
            columns: vec![s("name")],
        },
    );
    entity.indexes.insert(
        s("idx.example.product_id"),
        Index {
            name: s("idx.example.product_id"),
            unique: false,
            columns: vec![s("product_id")],
        },
    );
    entity.foreign_keys.insert(
        s("fk.example.product_id"),
        ForeignKey {
            name: s("fk.example.product_id"),
            column: s("product_id"),
            reference_entity: s("product"),
            reference_column: s("id"),
            on_delete: s(DELETE_SET_NULL),
            on_update: s("cascade"),
            ..ForeignKey::default()
        },
    );

    let (statements, diff) = migration_statements(&before, &after, &[]);
    let statements = statements.unwrap();
    assert!(validate_migration(&before, &after, &diff, &[]).is_empty());
    let joined = statements.join("\n");
    for expected in [
        "DROP CHECK `json.example.payload`",
        "MODIFY COLUMN `payload` LONGTEXT NULL",
        "UPDATE `example` SET `name` = 'unknown'",
        "ADD COLUMN `settings` JSON NULL",
        "ADD CONSTRAINT `json.example.settings` CHECK",
        "ADD COLUMN `active` TINYINT(1) NULL",
        "ADD COLUMN `auto_increment` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT UNIQUE",
        "DROP INDEX `idx.example.name`",
        "ADD UNIQUE INDEX `uniq.example.name`",
        "ADD CONSTRAINT `fk.example.product_id` FOREIGN KEY",
    ] {
        assert!(joined.contains(expected), "{expected}\n{joined}");
    }
    let migration = render_migration("Acme\\Example", "Migration1UpdateExample", 1, &statements);
    assert!(!migration.contains("updateDestructive"));
    assert_eq!(php_errors(&migration), 0);
}

#[test]
fn json_column_rename_replaces_named_constraint() {
    let mut before = empty_schema();
    before.entities.insert(
        s("example"),
        Entity {
            name: s("example"),
            columns: BTreeMap::from([(s("old_payload"), column("old_payload", "JSON"))]),
            ..Entity::default()
        },
    );
    let mut after = empty_schema();
    after.entities.insert(
        s("example"),
        Entity {
            name: s("example"),
            columns: BTreeMap::from([(s("payload"), column("payload", "JSON"))]),
            ..Entity::default()
        },
    );
    let decisions = [Decision {
        kind: s("columnRename"),
        entity: s("example"),
        from: s("old_payload"),
        to: s("payload"),
        ..Decision::default()
    }];
    let joined = migration_statements(&before, &after, &decisions)
        .0
        .unwrap()
        .join("\n");
    assert!(joined.contains("DROP CHECK `json.example.old_payload`"));
    assert!(joined.contains("CHANGE COLUMN `old_payload` `payload` JSON NULL"));
    assert!(joined.contains("ADD CONSTRAINT `json.example.payload` CHECK"));
}

#[test]
fn adding_version_field_backfills_and_rebuilds_composite_primary_key() {
    let mut before = empty_schema();
    before.entities.insert(
        s("example"),
        Entity {
            name: s("example"),
            columns: BTreeMap::from([(
                s("id"),
                Column {
                    not_null: true,
                    primary_key: true,
                    ..column("id", "BINARY(16)")
                },
            )]),
            ..Entity::default()
        },
    );
    let mut after = before.clone();
    after.entities.get_mut("example").unwrap().columns.insert(
        s("version_id"),
        Column {
            not_null: true,
            primary_key: true,
            backfill_sql: s("UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')"),
            ..column("version_id", "BINARY(16)")
        },
    );
    let (statements, diff) = migration_statements(&before, &after, &[]);
    assert_eq!(diff.changed_primary_keys.len(), 1);
    let joined = statements.unwrap().join("\n");
    assert!(joined.contains("DROP PRIMARY KEY"));
    assert!(joined.contains("ADD COLUMN `version_id` BINARY(16) NULL"));
    assert!(
        joined.contains(
            "UPDATE `example` SET `version_id` = UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')"
        )
    );
    assert!(joined.contains("ADD PRIMARY KEY (`id`, `version_id`)"));
    assert!(
        joined.find("DROP PRIMARY KEY").unwrap() < joined.find("ADD COLUMN `version_id`").unwrap()
    );
    assert!(
        joined.find("ADD COLUMN `version_id`").unwrap() < joined.find("ADD PRIMARY KEY").unwrap()
    );
}
