use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;
use swlsp_shopware_entityschema as entityschema;
use swlsp_uriutil::file_uri;

use crate::shopware::*;
use crate::test_support::check_problems;

fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

#[test]
fn entity_snapshot_analyzer_reports_changed_migration() {
    let root = tempfile::tempdir().unwrap();
    let directory = root.path().join(entityschema::SNAPSHOT_RELATIVE_DIRECTORY);
    std::fs::create_dir_all(&directory).unwrap();
    let migration_path = root.path().join("src/Migration/Migration1.php");
    std::fs::create_dir_all(migration_path.parent().unwrap()).unwrap();
    std::fs::write(&migration_path, "changed").unwrap();
    let snapshot = entityschema::Snapshot {
        kind: entityschema::SNAPSHOT_MIGRATION.to_owned(),
        schema: entityschema::empty_schema(),
        migrations: vec![entityschema::MigrationReference {
            path: "src/Migration/Migration1.php".to_owned(),
            sha256: entityschema::file_sha256(b"original"),
            ..Default::default()
        }],
        ..Default::default()
    }
    .seal()
    .unwrap();
    let content = entityschema::marshal_snapshot(&snapshot).unwrap();
    let path = directory.join("1.snapshot.json");
    std::fs::write(&path, &content).unwrap();
    // The analyzer reports snapshot problems on the whole document.
    check_problems(
        &EntitySnapshotAnalyzer::new(None),
        &file_uri(&path_string(&path)),
        &format!(
            "{{|shopware.entity_snapshot.migration_changed:{}|}}",
            std::str::from_utf8(&content).unwrap()
        ),
    );
}

#[test]
fn entity_snapshot_analyzer_accepts_external_relations_and_snapshot_only_indexes() {
    let root = tempfile::tempdir().unwrap();
    let definition_directory = root.path().join("src/Entity/Example");
    std::fs::create_dir_all(&definition_directory).unwrap();
    let spec = entityschema::complete_spec(entityschema::EntitySpec {
        mode: "new".to_owned(),
        namespace: r"Acme\Example\Entity\Example".to_owned(),
        class_name: "Example".to_owned(),
        entity_name: "acme_example".to_owned(),
        fields: vec![
            entityschema::FieldSpec {
                id: "id".to_owned(),
                kind: entityschema::FIELD_ID.to_owned(),
                editable: true,
                ..Default::default()
            },
            entityschema::FieldSpec {
                id: "language".to_owned(),
                kind: entityschema::FIELD_MANY_TO_ONE.to_owned(),
                property_name: "language".to_owned(),
                foreign_key_property_name: "languageId".to_owned(),
                storage_name: "language_id".to_owned(),
                target_definition_class: r"Shopware\Core\System\Language\LanguageDefinition"
                    .to_owned(),
                target_entity_class: r"Shopware\Core\System\Language\LanguageEntity".to_owned(),
                target_collection_class: r"Shopware\Core\System\Language\LanguageCollection"
                    .to_owned(),
                target_entity_name: "language".to_owned(),
                reference_field: "id".to_owned(),
                reference_storage_name: "id".to_owned(),
                editable: true,
                ..Default::default()
            },
        ],
        indexes: vec![entityschema::IndexSpec {
            name: "uniq.acme_example.language_id".to_owned(),
            kind: entityschema::INDEX_UNIQUE.to_owned(),
            columns: vec!["language_id".to_owned()],
            ..Default::default()
        }],
        ..Default::default()
    });
    let definition = entityschema::render_definition(&spec).unwrap();
    let definition_path = definition_directory.join("ExampleDefinition.php");
    std::fs::write(&definition_path, &definition).unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
    let source_cache = tempfile::tempdir().unwrap();
    let source_index = Arc::new(entityschema::SourceIndex::new(source_cache.path(), None).unwrap());
    let framework_path = root.path().join("vendor/shopware/EntityDefinition.php");
    let framework = "<?php
namespace Shopware\\Core\\Framework\\DataAbstractionLayer;
abstract class EntityDefinition {}
";
    for (indexed_path, indexed_source) in [
        (&framework_path, framework.to_owned()),
        (&definition_path, definition.clone()),
    ] {
        std::fs::create_dir_all(indexed_path.parent().unwrap()).unwrap();
        std::fs::write(indexed_path, &indexed_source).unwrap();
        let file = ParsedFile::new(
            path_string(indexed_path).as_str(),
            indexed_source.into_bytes(),
        );
        php_index.index(&file).unwrap();
        source_index.index(&file).unwrap();
    }
    // A valid but unindexed source must not affect request-time drift checks.
    std::fs::write(
        definition_directory.join("UnindexedDefinition.php"),
        r"<?php
namespace Acme\Example\Entity\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection;
final class UnindexedDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'unindexed';
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}",
    )
    .unwrap();
    std::fs::remove_file(&definition_path).unwrap();
    std::fs::remove_file(&framework_path).unwrap();
    let entity = entityschema::schema_from_spec(&spec).unwrap();
    let mut schema = entityschema::empty_schema();
    schema.entities.insert(entity.name.clone(), entity);
    let snapshot = entityschema::Snapshot {
        kind: entityschema::SNAPSHOT_MIGRATION.to_owned(),
        schema,
        ..Default::default()
    }
    .seal()
    .unwrap();
    let content = entityschema::marshal_snapshot(&snapshot).unwrap();
    let directory = root.path().join(entityschema::SNAPSHOT_RELATIVE_DIRECTORY);
    std::fs::create_dir_all(&directory).unwrap();
    let path = directory.join("1.snapshot.json");
    std::fs::write(&path, &content).unwrap();

    let catalog = Arc::new(entityschema::IndexedCatalog::new(
        php_index,
        Some(source_index),
        None,
    ));
    check_problems(
        &EntitySnapshotAnalyzer::new(Some(catalog)),
        &file_uri(&path_string(&path)),
        std::str::from_utf8(&content).unwrap(),
    );
}
