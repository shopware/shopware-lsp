use std::collections::BTreeMap;

use super::matrix::backfill_free;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn php_errors(source: &str) -> usize {
    swlsp_parser_php::parse(source).errors.len()
}

fn require_issue_code(issues: &[ValidationIssue], code: &str) {
    assert!(
        issues.iter().any(|issue| issue.code == code),
        "validation issue not found: {code} in {issues:?}"
    );
}

fn target(
    definition: &str,
    entity: &str,
    collection: &str,
    name: &str,
    version_aware: bool,
) -> RelationTarget {
    RelationTarget {
        definition_class: s(definition),
        entity_class: s(entity),
        collection_class: s(collection),
        entity_name: s(name),
        fields: vec![RelationTargetField {
            property_name: s("id"),
            storage_name: s("id"),
            primary: true,
        }],
        version_aware,
        ..RelationTarget::default()
    }
}

fn many_to_one(id: &str, property: &str, target: &RelationTarget) -> FieldSpec {
    FieldSpec {
        id: s(id),
        kind: s(FIELD_MANY_TO_ONE),
        property_name: s(property),
        foreign_key_property_name: format!("{property}Id"),
        storage_name: format!("{property}_id"),
        target_definition_class: target.definition_class.clone(),
        target_entity_class: target.entity_class.clone(),
        target_collection_class: target.collection_class.clone(),
        target_entity_name: target.entity_name.clone(),
        reference_field: s("id"),
        reference_storage_name: s("id"),
        required: true,
        primary: true,
        delete_behavior: s(DELETE_RESTRICT),
        editable: true,
        ..FieldSpec::default()
    }
}

#[test]
fn mapping_definition_round_trips_composite_foreign_keys_without_implicit_timestamps() {
    let product = target(
        "Shopware\\Core\\Content\\Product\\ProductDefinition",
        "Shopware\\Core\\Content\\Product\\ProductEntity",
        "Shopware\\Core\\Content\\Product\\ProductCollection",
        "product",
        true,
    );
    let tag = target(
        "Shopware\\Core\\System\\Tag\\TagDefinition",
        "Shopware\\Core\\System\\Tag\\TagEntity",
        "Shopware\\Core\\System\\Tag\\TagCollection",
        "tag",
        false,
    );
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        definition_kind: s(DEFINITION_MAPPING),
        namespace: s("Acme\\Example\\Content\\ProductTag"),
        class_name: s("ProductTag"),
        entity_name: s("acme_product_tag"),
        fields: vec![
            many_to_one("product", "product", &product),
            FieldSpec {
                id: s("product-version"),
                kind: s(FIELD_REFERENCE_VERSION),
                property_name: s("productVersionId"),
                storage_name: s("product_version_id"),
                target_definition_class: product.definition_class.clone(),
                target_entity_name: product.entity_name.clone(),
                required: true,
                primary: true,
                editable: true,
                ..FieldSpec::default()
            },
            many_to_one("tag", "tag", &tag),
        ],
        ..EntitySpec::default()
    });
    assert!(spec.entity_class.is_empty());
    assert!(spec.collection_class.is_empty());

    let definition = render_definition(&spec).unwrap();
    assert_eq!(php_errors(&definition), 0, "{definition}");
    assert!(definition.contains("extends MappingEntityDefinition"));
    assert!(!definition.contains("getEntityClass"));
    assert!(!definition.contains("getCollectionClass"));
    assert!(!definition.contains("CreatedAtField"));
    assert!(!definition.contains("UpdatedAtField"));
    assert!(
        definition
            .contains("new ReferenceVersionField(ProductDefinition::class, 'product_version_id')")
    );

    let lookup = |class: &str| {
        if class == product.definition_class {
            Some(product.clone())
        } else if class == tag.definition_class {
            Some(tag.clone())
        } else {
            None
        }
    };
    let imported = import_definition(&definition, Some(&lookup)).unwrap();
    assert_eq!(imported.definition_kind, DEFINITION_MAPPING);
    assert!(imported.entity_class.is_empty());
    assert!(imported.collection_class.is_empty());

    let mut before = schema_from_spec(&spec).unwrap();
    let mut after = schema_from_spec(&imported).unwrap();
    backfill_free(&mut before);
    backfill_free(&mut after);
    assert_eq!(before, after);
    assert!(!before.columns.contains_key("created_at"));
    assert!(!before.columns.contains_key("updated_at"));
    assert_eq!(before.columns.len(), 3);
    assert!(before.columns["product_id"].primary_key);
    assert!(before.columns["product_version_id"].primary_key);
    assert!(before.columns["tag_id"].primary_key);
}

#[test]
fn render_definition_imports_standalone_foreign_key_target() {
    let spec = complete_spec(EntitySpec {
        namespace: s("Acme\\Example"),
        class_name: s("Example"),
        entity_name: s("acme_example"),
        fields: vec![
            FieldSpec {
                kind: s(FIELD_ID),
                api_aware: true,
                search_ranking: 100.0,
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                kind: s(FIELD_FOREIGN_KEY),
                storage_name: s("product_id"),
                property_name: s("productId"),
                target_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let source = render_definition(&spec).unwrap();
    assert!(source.contains("use Shopware\\Core\\Content\\Product\\ProductDefinition;"));
    assert!(source.contains("new ApiAware()"));
    assert!(source.contains("new SearchRanking(100)"));

    let round_tripped = import_definition(&source, None).unwrap();
    assert!(round_tripped.fields[0].api_aware);
    assert_eq!(round_tripped.fields[0].search_ranking, 100.0);
    assert_eq!(
        round_tripped.fields[1].target_definition_class,
        "Shopware\\Core\\Content\\Product\\ProductDefinition"
    );
}

#[test]
fn mapping_definition_supports_standalone_foreign_key_and_explicit_timestamps() {
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        definition_kind: s(DEFINITION_MAPPING),
        namespace: s("Acme\\Example\\Mapping"),
        class_name: s("ExampleMapping"),
        entity_name: s("acme_example_mapping"),
        fields: vec![
            FieldSpec {
                id: s("source"),
                kind: s(FIELD_FOREIGN_KEY),
                property_name: s("sourceId"),
                storage_name: s("source_id"),
                target_definition_class: s("Acme\\Example\\SourceDefinition"),
                target_entity_name: s("acme_source"),
                required: true,
                primary: true,
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("created"),
                kind: s(FIELD_CREATED_AT),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("updated"),
                kind: s(FIELD_UPDATED_AT),
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    let definition = render_definition(&spec).unwrap();
    assert!(
        definition.contains("new FkField('source_id', 'sourceId', SourceDefinition::class, 'id')")
    );
    assert!(definition.contains("new CreatedAtField()"));
    assert!(definition.contains("new UpdatedAtField()"));
    let entity = schema_from_spec(&spec).unwrap();
    assert!(entity.columns.contains_key("created_at"));
    assert!(entity.columns.contains_key("updated_at"));
    assert_eq!(
        entity.foreign_keys["fk.acme_example_mapping.source_id"].on_delete,
        DELETE_RESTRICT
    );
}

#[test]
fn imported_mapping_definition_preserves_optional_entity_and_collection_classes() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition;
class ExampleMappingDefinition extends MappingEntityDefinition {
    public const ENTITY_NAME = 'acme_mapping';
    public function getEntityClass(): string { return ExampleMappingEntity::class; }
    public function getCollectionClass(): string { return ExampleMappingCollection::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
    ]); }
}"#;
    let imported = import_definition(source, None).unwrap();
    assert_eq!(imported.entity_class, "Acme\\Example\\ExampleMappingEntity");
    assert_eq!(
        imported.collection_class,
        "Acme\\Example\\ExampleMappingCollection"
    );

    let rendered = render_definition(&imported).unwrap();
    assert!(rendered.contains("public function getEntityClass(): string"));
    assert!(rendered.contains("public function getCollectionClass(): string"));

    let round_tripped = import_definition(&rendered, None).unwrap();
    assert_eq!(imported.entity_class, round_tripped.entity_class);
    assert_eq!(imported.collection_class, round_tripped.collection_class);
}

#[test]
fn mapping_definition_rejects_translation_schema_generation() {
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        definition_kind: s(DEFINITION_MAPPING),
        namespace: s("Acme\\Example\\Mapping"),
        class_name: s("ExampleMapping"),
        entity_name: s("acme_example_mapping"),
        translation: Some(TranslationSpec {
            enabled: true,
            ..TranslationSpec::default()
        }),
        fields: vec![FieldSpec {
            id: s("id"),
            kind: s(FIELD_ID),
            editable: true,
            ..FieldSpec::default()
        }],
        ..EntitySpec::default()
    });
    let error = schema_entities_from_spec(&spec).unwrap_err();
    assert_eq!(
        error.to_string(),
        "mapping definitions cannot own translation bundles"
    );
}

pub(super) fn example_spec() -> EntitySpec {
    let field = |id: &str, kind: &str, property: &str| FieldSpec {
        id: s(id),
        kind: s(kind),
        property_name: s(property),
        storage_name: s(property),
        editable: true,
        ..FieldSpec::default()
    };
    complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example\\Entity\\Catalog"),
        class_name: s("CatalogItem"),
        entity_name: s("acme_catalog_item"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                required: true,
                api_aware: true,
                max_length: 500,
                ..field("name", FIELD_STRING, "name")
            },
            FieldSpec {
                required: true,
                ..field("active", FIELD_BOOL, "active")
            },
            field("payload", FIELD_JSON, "payload"),
            FieldSpec {
                id: s("manufacturer"),
                kind: s(FIELD_MANY_TO_ONE),
                property_name: s("manufacturer"),
                foreign_key_property_name: s("manufacturerId"),
                storage_name: s("manufacturer_id"),
                target_definition_class: s(
                    "Shopware\\Core\\Content\\Product\\Aggregate\\ProductManufacturer\\ProductManufacturerDefinition",
                ),
                target_entity_class: s(
                    "Shopware\\Core\\Content\\Product\\Aggregate\\ProductManufacturer\\ProductManufacturerEntity",
                ),
                target_entity_name: s("product_manufacturer"),
                reference_field: s("id"),
                reference_storage_name: s("id"),
                delete_behavior: s(DELETE_SET_NULL),
                editable: true,
                ..FieldSpec::default()
            },
        ],
        indexes: vec![IndexSpec {
            name: s("uniq.acme_catalog_item.name"),
            kind: s(INDEX_UNIQUE),
            columns: vec![s("name")],
            translation: false,
        }],
        ..EntitySpec::default()
    })
}

#[test]
fn render_entity_bundle_and_migration_use_only_update() {
    let spec = example_spec();
    let definition = render_definition(&spec).unwrap();
    let entity = render_entity(&spec).unwrap();
    let collection = render_collection(&spec).unwrap();
    for source in [&definition, &entity, &collection] {
        assert_eq!(php_errors(source), 0);
    }
    assert!(definition.contains("new FkField('manufacturer_id', 'manufacturerId'"));
    assert!(definition.contains("addFlags(new SetNullOnDelete())"));
    assert!(!definition.contains("CreatedAtField"));
    assert!(!definition.contains("UpdatedAtField"));
    assert!(entity.contains("protected ?ProductManufacturerEntity $manufacturer = null;"));
    assert!(!entity.contains("$createdAt"));
    assert!(!entity.contains("$updatedAt"));

    let entity_schema = schema_from_spec(&spec).unwrap();
    assert_eq!(
        entity_schema.columns["created_at"],
        Column {
            name: s("created_at"),
            sql_type: s("DATETIME(3)"),
            not_null: true,
            backfill_sql: s("CURRENT_TIMESTAMP(3)"),
            ..Column::default()
        }
    );
    assert_eq!(
        entity_schema.columns["updated_at"],
        Column {
            name: s("updated_at"),
            sql_type: s("DATETIME(3)"),
            ..Column::default()
        }
    );
    let mut next = empty_schema();
    next.entities
        .insert(spec.entity_name.clone(), entity_schema);
    let (statements, diff) = migration_statements(&empty_schema(), &next, &[]);
    let statements = statements.unwrap();
    assert!(diff.database_changed());
    let migration = render_migration(
        "Acme\\Example",
        "Migration1700000000CreateCatalogItem",
        1700000000,
        &statements,
    );
    assert_eq!(php_errors(&migration), 0);
    assert!(migration.contains("public function update(Connection $connection): void"));
    assert!(!migration.contains("updateDestructive"));
    assert!(migration.contains("CREATE TABLE IF NOT EXISTS `acme_catalog_item`"));
    assert!(migration.contains("UNIQUE KEY `uniq.acme_catalog_item.name`"));
}

#[test]
fn render_translation_bundle_and_version_aware_schema() {
    let spec = complete_spec(EntitySpec {
        mode: s("new"),
        namespace: s("Acme\\Example\\Content\\Blog"),
        class_name: s("Blog"),
        entity_name: s("acme_blog"),
        fields: vec![
            FieldSpec {
                id: s("id"),
                kind: s(FIELD_ID),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("version"),
                kind: s(FIELD_VERSION),
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("name"),
                kind: s(FIELD_STRING),
                property_name: s("name"),
                storage_name: s("name"),
                required: true,
                api_aware: true,
                search_ranking: 500.0,
                translated: true,
                editable: true,
                ..FieldSpec::default()
            },
            FieldSpec {
                id: s("description"),
                kind: s(FIELD_LONG_TEXT),
                property_name: s("description"),
                storage_name: s("description"),
                translated: true,
                editable: true,
                ..FieldSpec::default()
            },
        ],
        ..EntitySpec::default()
    });
    assert!(spec.translation.as_ref().unwrap().association_required);

    let parent_definition = render_definition(&spec).unwrap();
    let parent_entity = render_entity(&spec).unwrap();
    let translation_definition = render_translation_definition(&spec).unwrap();
    let translation_entity = render_translation_entity(&spec).unwrap();
    let translation_collection = render_translation_collection(&spec).unwrap();
    for source in [
        &parent_definition,
        &parent_entity,
        &translation_definition,
        &translation_entity,
        &translation_collection,
    ] {
        assert_eq!(php_errors(source), 0, "{source}");
    }
    assert!(parent_definition.contains("new TranslatedField('name')"));
    assert!(parent_definition.contains(
        "new TranslationsAssociationField(BlogTranslationDefinition::class, 'acme_blog_id')"
    ));
    assert!(!parent_definition.contains("new StringField('name'"));
    assert!(parent_entity.contains("protected ?string $name = null;"));
    assert!(parent_entity.contains("protected ?BlogTranslationCollection $translations = null;"));
    assert!(translation_definition.contains("extends EntityTranslationDefinition"));
    assert!(translation_definition.contains("return BlogDefinition::class;"));
    assert!(translation_definition.contains("new StringField('name', 'name')"));
    assert!(translation_entity.contains("extends TranslationEntity"));
    assert!(translation_entity.contains("protected string $acmeBlogId;"));
    assert!(translation_entity.contains("protected string $acmeBlogVersionId;"));

    let entities = schema_entities_from_spec(&spec).unwrap();
    assert_eq!(entities.len(), 2);
    let parent_schema = &entities[0];
    let translation_schema = &entities[1];
    assert!(!parent_schema.columns.contains_key("name"));
    assert!(translation_schema.columns.contains_key("name"));
    assert!(translation_schema.columns["acme_blog_id"].primary_key);
    assert!(translation_schema.columns["acme_blog_version_id"].primary_key);
    assert!(translation_schema.columns["language_id"].primary_key);
    let parent_fk = &translation_schema.foreign_keys["fk.acme_blog_translation.acme_blog_id"];
    assert_eq!(
        parent_fk.columns,
        vec!["acme_blog_id", "acme_blog_version_id"]
    );
    assert_eq!(parent_fk.reference_columns, vec!["id", "version_id"]);

    let mut next = empty_schema();
    for entity in entities {
        next.entities.insert(entity.name.clone(), entity);
    }
    let joined = migration_statements(&empty_schema(), &next, &[])
        .0
        .unwrap()
        .join("\n");
    assert!(joined.contains("CREATE TABLE IF NOT EXISTS `acme_blog_translation`"));
    assert!(joined.contains(
        "FOREIGN KEY (`acme_blog_id`, `acme_blog_version_id`) REFERENCES `acme_blog` (`id`, `version_id`)"
    ));
    assert!(joined.contains("FOREIGN KEY (`language_id`) REFERENCES `language` (`id`)"));
}

#[test]
fn translation_table_indexes_validate_persist_and_round_trip() {
    let mut spec = example_spec();
    spec.indexes.clear();
    spec.fields[1].translated = true;
    spec.fields.push(FieldSpec {
        id: s("version"),
        kind: s(FIELD_VERSION),
        editable: true,
        ..FieldSpec::default()
    });
    spec.translation = Some(TranslationSpec {
        enabled: true,
        association_required: true,
        ..TranslationSpec::default()
    });
    let mut spec = complete_spec(spec);
    let index = |name: &str, kind: &str, columns: &[&str], translation: bool| IndexSpec {
        name: s(name),
        kind: s(kind),
        columns: columns.iter().map(|column| s(column)).collect(),
        translation,
    };
    spec.indexes = vec![
        index("idx.same_name", INDEX_NORMAL, &["active"], false),
        index(
            "idx.same_name",
            INDEX_UNIQUE,
            &["name", "language_id"],
            true,
        ),
        index(
            "idx.translation_parent",
            INDEX_NORMAL,
            &["acme_catalog_item_id", "acme_catalog_item_version_id"],
            true,
        ),
    ];
    assert_eq!(validate_spec(&spec), vec![]);

    let entities = schema_entities_from_spec(&spec).unwrap();
    assert_eq!(entities.len(), 2);
    let mut schema = empty_schema();
    for entity in entities {
        schema.entities.insert(entity.name.clone(), entity);
    }
    let translation_name = spec.translation.as_ref().unwrap().entity_name.clone();
    assert!(!schema.entities[&spec.entity_name].indexes["idx.same_name"].unique);
    assert!(schema.entities[&translation_name].indexes["idx.same_name"].unique);
    assert_eq!(
        schema.entities[&translation_name].indexes["idx.same_name"].columns,
        vec!["name", "language_id"]
    );

    let joined = migration_statements(&empty_schema(), &schema, &[])
        .0
        .unwrap()
        .join("\n");
    assert!(joined.contains("UNIQUE KEY `idx.same_name` (`name`, `language_id`)"));
    assert!(joined.contains(
        "KEY `idx.translation_parent` (`acme_catalog_item_id`, `acme_catalog_item_version_id`)"
    ));

    let restored = index_specs_from_entities(&spec, &schema);
    assert_eq!(spec.indexes, restored);

    let mut disabled = spec.clone();
    for field in &mut disabled.fields {
        field.translated = false;
    }
    disabled.translation = None;
    require_issue_code(
        &validate_spec(&disabled),
        "entity.index.translation.missing",
    );
}

fn column(name: &str, sql_type: &str) -> Column {
    Column {
        name: s(name),
        sql_type: s(sql_type),
        ..Column::default()
    }
}

fn single_entity(name: &str, columns: Vec<Column>) -> Schema {
    let mut schema = empty_schema();
    schema.entities.insert(
        s(name),
        Entity {
            name: s(name),
            columns: columns
                .into_iter()
                .map(|column| (column.name.clone(), column))
                .collect(),
            ..Entity::default()
        },
    );
    schema
}

fn decision(kind: &str, entity: &str, from: &str, to: &str) -> Decision {
    Decision {
        kind: s(kind),
        entity: s(entity),
        from: s(from),
        to: s(to),
        ..Decision::default()
    }
}

#[test]
fn migration_column_rename_restates_new_definition_in_update() {
    let before = single_entity("example", vec![column("old", "VARCHAR(255)")]);
    let after = single_entity(
        "example",
        vec![Column {
            not_null: true,
            backfill_sql: s("'renamed'"),
            ..column("new", "VARCHAR(500)")
        }],
    );
    let joined = migration_statements(
        &before,
        &after,
        &[decision("columnRename", "example", "old", "new")],
    )
    .0
    .unwrap()
    .join("\n");
    assert!(joined.contains("CHANGE COLUMN `old` `new` VARCHAR(500) NULL"));
    assert!(joined.contains("UPDATE `example` SET `new` = 'renamed' WHERE `new` IS NULL"));
    assert!(joined.contains("MODIFY COLUMN `new` VARCHAR(500) NOT NULL"));
}

#[test]
fn migration_entity_rename_preserves_table_and_rebuilds_generated_objects() {
    let id = Column {
        not_null: true,
        primary_key: true,
        ..column("id", "BINARY(16)")
    };
    let mut before = empty_schema();
    before.entities.insert(
        s("old_table"),
        Entity {
            name: s("old_table"),
            columns: BTreeMap::from([
                (s("id"), id.clone()),
                (s("payload"), column("payload", "JSON")),
                (s("target_id"), column("target_id", "BINARY(16)")),
            ]),
            indexes: BTreeMap::from([(
                s("idx.old_table.target_id"),
                Index {
                    name: s("idx.old_table.target_id"),
                    unique: false,
                    columns: vec![s("target_id")],
                },
            )]),
            foreign_keys: BTreeMap::from([(
                s("fk.old_table.target_id"),
                ForeignKey {
                    name: s("fk.old_table.target_id"),
                    column: s("target_id"),
                    reference_entity: s("target"),
                    reference_column: s("id"),
                    ..ForeignKey::default()
                },
            )]),
            ..Entity::default()
        },
    );
    before.entities.insert(
        s("target"),
        Entity {
            name: s("target"),
            columns: BTreeMap::from([(s("id"), id)]),
            ..Entity::default()
        },
    );
    let mut after = before.clone();
    let mut entity = after.entities.remove("old_table").unwrap();
    entity.name = s("new_table");
    entity.indexes = BTreeMap::from([(
        s("idx.new_table.target_id"),
        Index {
            name: s("idx.new_table.target_id"),
            unique: false,
            columns: vec![s("target_id")],
        },
    )]);
    entity.foreign_keys = BTreeMap::from([(
        s("fk.new_table.target_id"),
        ForeignKey {
            name: s("fk.new_table.target_id"),
            column: s("target_id"),
            reference_entity: s("target"),
            reference_column: s("id"),
            ..ForeignKey::default()
        },
    )]);
    after.entities.insert(s("new_table"), entity);

    let (statements, diff) = migration_statements(
        &before,
        &after,
        &[decision(
            "entityRename",
            "new_table",
            "old_table",
            "new_table",
        )],
    );
    let joined = statements.unwrap().join("\n");
    assert!(joined.contains("RENAME TABLE `old_table` TO `new_table`;"));
    assert!(joined.contains("ALTER TABLE `new_table` DROP CHECK `json.old_table.payload`;"));
    assert!(
        joined.contains("ALTER TABLE `new_table` ADD CONSTRAINT `json.new_table.payload` CHECK")
    );
    assert!(joined.contains("ALTER TABLE `new_table` DROP INDEX `idx.old_table.target_id`;"));
    assert!(joined.contains("ALTER TABLE `new_table` ADD INDEX `idx.new_table.target_id`"));
    assert!(!joined.contains("CREATE TABLE IF NOT EXISTS `new_table`"));
    assert!(!joined.contains("DROP TABLE IF EXISTS `old_table`"));
    assert!(diff.created_entities.is_empty());
    assert!(diff.removed_entities.is_empty());
}

#[test]
fn migration_entity_and_json_column_rename_drops_original_constraint_once() {
    let id = Column {
        not_null: true,
        primary_key: true,
        ..column("id", "BINARY(16)")
    };
    let before = single_entity("old_table", vec![id.clone(), column("payload", "JSON")]);
    let after = single_entity("new_table", vec![id, column("content", "JSON")]);
    let decisions = [
        decision("entityRename", "new_table", "old_table", "new_table"),
        decision("columnRename", "new_table", "payload", "content"),
    ];
    let joined = migration_statements(&before, &after, &decisions)
        .0
        .unwrap()
        .join("\n");
    assert_eq!(
        joined
            .matches("DROP CHECK `json.old_table.payload`")
            .count(),
        1
    );
    assert!(!joined.contains("DROP CHECK `json.new_table.payload`"));
    assert!(joined.contains("CHANGE COLUMN `payload` `content` JSON NULL"));
    assert!(joined.contains("ADD CONSTRAINT `json.new_table.content` CHECK"));
}

#[test]
fn migration_required_column_uses_nullable_backfill_sequence() {
    let before = single_entity(
        "example",
        vec![Column {
            not_null: true,
            primary_key: true,
            ..column("id", "BINARY(16)")
        }],
    );
    let mut after = before.clone();
    after.entities.get_mut("example").unwrap().columns.insert(
        s("active"),
        Column {
            not_null: true,
            backfill_sql: s("0"),
            ..column("active", "TINYINT(1)")
        },
    );
    let (statements, diff) = migration_statements(&before, &after, &[]);
    assert!(validate_migration(&before, &after, &diff, &[]).is_empty());
    let joined = statements.unwrap().join("\n");
    assert!(joined.contains("ADD COLUMN `active` TINYINT(1) NULL"));
    assert!(joined.contains("UPDATE `example` SET `active` = 0 WHERE `active` IS NULL"));
    assert!(joined.contains("MODIFY COLUMN `active` TINYINT(1) NOT NULL"));
}

#[test]
fn migration_required_column_rejects_missing_backfill() {
    let before = single_entity("example", vec![]);
    let mut after = before.clone();
    after.entities.get_mut("example").unwrap().columns.insert(
        s("name"),
        Column {
            not_null: true,
            ..column("name", "VARCHAR(255)")
        },
    );
    let diff = diff_schemas(&before, &after);
    assert!(!validate_migration(&before, &after, &diff, &[]).is_empty());
    let error = migration_statements(&before, &after, &[]).0.unwrap_err();
    assert!(error.to_string().contains("backfill"));
}
