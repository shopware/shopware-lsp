use std::collections::BTreeMap;
use std::fs;
use std::sync::Arc;

use swlsp_indexer::{CancelToken, Indexer, ParsedFile, Store, WorkspaceSymbolCatalog};
use swlsp_php::PhpIndex;

use crate::*;

fn path_string(path: &std::path::Path) -> String {
    path.to_string_lossy().into_owned()
}

#[test]
fn indexed_catalog_rejects_incomplete_workspace_generation() {
    let store_dir = tempfile::tempdir().unwrap();
    let store = Store::open(store_dir.path().join("indexes.db")).unwrap();
    let readiness = Arc::new(WorkspaceSymbolCatalog::new(&store).unwrap());
    let php_cache = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
    let catalog = IndexedCatalog::new(php_index.clone(), None, Some(readiness.clone()));

    let plugin = tempfile::tempdir().unwrap();
    let error = catalog.scan(&path_string(plugin.path()), None).unwrap_err();
    assert!(matches!(error, Error::IndexNotReady), "{error}");

    readiness.set_ready(&CancelToken::new(), true).unwrap();
    let (schema, definitions) = catalog.scan(&path_string(plugin.path()), None).unwrap();
    assert_eq!(schema, empty_schema());
    assert!(definitions.is_empty());
    php_index.close().unwrap();
    store.close().unwrap();
}

#[test]
fn indexed_catalog_discovers_custom_bases_without_walking_plugin() {
    let root_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path();
    let php_cache = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
    let source_cache = tempfile::tempdir().unwrap();
    let source_index = Arc::new(SourceIndex::new(source_cache.path(), None).unwrap());

    let sources = [
        (
            root.join("vendor/shopware/DAL.php"),
            r#"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityDefinition {}
abstract class MappingEntityDefinition extends EntityDefinition {}
abstract class EntityTranslationDefinition extends EntityDefinition {}
abstract class EntityExtension {}
abstract class BulkEntityExtension {}
"#,
        ),
        (
            root.join("src/Foundation.php"),
            r#"<?php
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
abstract class RecordBase extends EntityDefinition {}
abstract class EnricherBase extends EntityExtension {}
"#,
        ),
        (
            root.join("src/UnexpectedName.php"),
            r#"<?php
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
class CatalogModel extends RecordBase {
    public const ENTITY_NAME = 'acme_catalog';
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode'), new EnumField('status', 'status', Status::Active)]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}
"#,
        ),
        (
            root.join("src/Status.php"),
            r#"<?php
namespace Acme\Odd;
enum Status: string { case Active = 'active'; }
"#,
        ),
        (
            root.join("src/Extension.php"),
            r#"<?php
namespace Acme\Odd;
class CatalogExtension extends EnricherBase {
    public function getDefinitionClass(): string { return CatalogModel::class; }
}
"#,
        ),
        (
            root.join("src/Unrelated.php"),
            r#"<?php
namespace Acme\Odd;
class Unrelated {}
"#,
        ),
    ];
    for (path, source) in &sources {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, source).unwrap();
        let file = ParsedFile::new(path_string(path), source.as_bytes().to_vec());
        php_index.index(&file).unwrap();
        source_index.index(&file).unwrap();
    }
    assert_eq!(
        source_index
            .source(&path_string(&root.join("src/Unrelated.php")))
            .unwrap(),
        None
    );

    // A valid but deliberately unindexed definition proves discovery uses
    // the semantic catalog instead of walking src at request time.
    fs::write(
        root.join("src/NotIndexed.php"),
        r#"<?php
namespace Acme\Odd;
class NotIndexed extends RecordBase {
    public const ENTITY_NAME = 'must_not_appear';
    protected function defineFields(): \Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection { return new \Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection([]); }
}"#,
    )
    .unwrap();
    // Indexed DAL source stays available after the workspace file disappears;
    // the request path must not reopen any PHP file.
    for (path, _) in &sources {
        fs::remove_file(path).unwrap();
    }

    let catalog = IndexedCatalog::new(php_index.clone(), Some(source_index.clone()), None);
    let (schema, definitions) = catalog.scan(&path_string(root), None).unwrap();
    assert!(schema.entities.contains_key("acme_catalog"));
    assert_eq!(
        schema.entities["acme_catalog"].columns["status"].sql_type,
        "VARCHAR(255)"
    );
    assert_eq!(
        schema.entities["acme_catalog"].columns["base_code"].sql_type,
        "VARCHAR(255)"
    );
    assert!(!schema.entities.contains_key("must_not_appear"));
    assert_eq!(definitions.len(), 2);
    let mut kinds: BTreeMap<String, usize> = BTreeMap::new();
    for definition in &definitions {
        *kinds
            .entry(definition.spec.definition_kind.clone())
            .or_default() += 1;
    }
    let expected: BTreeMap<String, usize> = [
        (DEFINITION_ENTITY.to_owned(), 1),
        (DEFINITION_EXTENSION.to_owned(), 1),
    ]
    .into_iter()
    .collect();
    assert_eq!(kinds, expected);
    let entity = definitions
        .iter()
        .map(|definition| &definition.spec)
        .find(|spec| spec.definition_kind == DEFINITION_ENTITY)
        .unwrap();
    let behavior = entity.definition_behavior.as_ref().unwrap();
    let status = behavior
        .base_fields
        .iter()
        .find(|field| field.property_name == "status")
        .expect("field not found: status");
    assert_eq!(status.enum_backing_type, "string");
    source_index.close().unwrap();
    php_index.close().unwrap();
}
