use std::collections::{BTreeMap, HashSet};
use std::fs;

use crate::*;

fn path_string(path: std::path::PathBuf) -> String {
    path.to_string_lossy().into_owned()
}

#[test]
fn find_plugin_context_prefers_yaml_service_configuration() {
    let root = tempfile::tempdir().unwrap();
    let config_directory = root.path().join("src").join("Resources").join("config");
    fs::create_dir_all(&config_directory).unwrap();
    fs::write(
        root.path().join("composer.json"),
        r#"{
  "name": "acme/example",
  "type": "shopware-platform-plugin",
  "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
}"#,
    )
    .unwrap();
    for name in ["services.xml", "services.php", "services.yaml"] {
        fs::write(config_directory.join(name), "services:\n").unwrap();
    }
    let root_path = path_string(root.path().to_path_buf());
    let context = find_plugin_context(&root_path, &path_string(root.path().join("src"))).unwrap();
    assert_eq!(context.service_uris.len(), 3);
    assert_eq!(
        context.service_uris[0],
        path_string(config_directory.join("services.yaml"))
    );
    assert_eq!(
        context.service_uris[2],
        path_string(config_directory.join("services.xml"))
    );
}

#[test]
fn scan_plugin_schema_discovers_arbitrary_files_and_custom_dal_bases() {
    let root = tempfile::tempdir().unwrap();
    let source_root = root.path().join("src");
    fs::create_dir_all(&source_root).unwrap();
    let write = |name: &str, source: &str| fs::write(source_root.join(name), source).unwrap();
    write(
        "Foundation.php",
        r#"<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;
abstract class RecordBase extends EntityDefinition {}
abstract class LinkBase extends MappingEntityDefinition {}
abstract class EnricherBase extends EntityExtension {}
abstract class BulkBase extends BulkEntityExtension {}
"#,
    );
    write(
        "CatalogModel.php",
        r#"<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogModel extends RecordBase {
    public const ENTITY_NAME = 'acme_catalog';
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode')]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([(new IdField('id', 'id'))]); }
}
"#,
    );
    write(
        "CatalogLinks.php",
        r#"<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogLinks extends LinkBase {
    public const ENTITY_NAME = 'acme_catalog_link';
    protected function defineFields(): FieldCollection { return new FieldCollection([(new IdField('id', 'id'))]); }
}
"#,
    );
    write(
        "CatalogEnricher.php",
        r#"<?php declare(strict_types=1);
namespace Acme\Odd;
class CatalogEnricher extends EnricherBase {
    public function getDefinitionClass(): string { return CatalogModel::class; }
}
"#,
    );
    write(
        "CatalogBatch.php",
        r#"<?php declare(strict_types=1);
namespace Acme\Odd;
class CatalogBatch extends BulkBase {
    public function collect(): \Generator { yield CatalogModel::ENTITY_NAME => []; }
}
"#,
    );
    let (schema, definitions) =
        scan_plugin_schema(&path_string(root.path().to_path_buf())).unwrap();
    assert!(schema.entities.contains_key("acme_catalog"));
    assert!(
        schema.entities["acme_catalog"]
            .columns
            .contains_key("base_code")
    );
    assert!(schema.entities.contains_key("acme_catalog_link"));
    let mut kinds: BTreeMap<String, usize> = BTreeMap::new();
    let mut files = HashSet::new();
    for definition in &definitions {
        *kinds
            .entry(definition.spec.definition_kind.clone())
            .or_default() += 1;
        files.insert(definition.path.rsplit('/').next().unwrap().to_owned());
    }
    let expected: BTreeMap<String, usize> = [
        (DEFINITION_ENTITY, 1),
        (DEFINITION_MAPPING, 1),
        (DEFINITION_EXTENSION, 1),
        (DEFINITION_BULK_EXTENSION, 1),
    ]
    .into_iter()
    .map(|(kind, count)| (kind.to_owned(), count))
    .collect();
    assert_eq!(kinds, expected);
    for name in [
        "CatalogModel.php",
        "CatalogLinks.php",
        "CatalogEnricher.php",
        "CatalogBatch.php",
    ] {
        assert!(files.contains(name), "{name}");
    }
}

fn memo_declaration(path: &str, class: &str, source: &str) -> IndexedClassDeclaration {
    IndexedClassDeclaration {
        path: path.to_owned(),
        class: class.to_owned(),
        parents: vec![
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition".to_owned(),
        ],
        abstract_class: false,
        source: source.to_owned(),
    }
}

fn memo_definition(entity: &str, reference: &str) -> String {
    format!(
        r#"<?php
namespace Acme\Entity;

use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ReferenceVersionField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Content\Product\ProductDefinition;

class {entity}Definition extends EntityDefinition
{{
    public const ENTITY_NAME = '{reference}';

    public function getEntityName(): string
    {{
        return self::ENTITY_NAME;
    }}

    protected function defineFields(): FieldCollection
    {{
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new PrimaryKey(), new Required()),
            new FkField('product_id', 'productId', ProductDefinition::class),
            new ReferenceVersionField(ProductDefinition::class),
            new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class),
        ]);
    }}
}}
"#
    )
}

/// Memoized scans must match fresh scans when sources and lookup answers
/// change between scans.
#[test]
fn memoized_scans_match_fresh_scans() {
    let memo = crate::context::ImportMemo::default();
    let root = "/plugin";
    let mut declarations = vec![
        memo_declaration(
            "/plugin/src/A/ADefinition.php",
            "Acme\\Entity\\ADefinition",
            &memo_definition("A", "acme_a"),
        ),
        memo_declaration(
            "/plugin/src/B/BDefinition.php",
            "Acme\\Entity\\BDefinition",
            &memo_definition("B", "acme_b"),
        ),
    ];
    let product = |entity: &str| RelationTarget {
        definition_class: "Shopware\\Core\\Content\\Product\\ProductDefinition".to_owned(),
        entity_class: "Shopware\\Core\\Content\\Product\\ProductEntity".to_owned(),
        collection_class: "Shopware\\Core\\Content\\Product\\ProductCollection".to_owned(),
        entity_name: entity.to_owned(),
        fields: vec![RelationTargetField {
            property_name: "id".to_owned(),
            storage_name: "pid".to_owned(),
            primary: true,
        }],
        ..RelationTarget::default()
    };
    let first = |class: &str| {
        (class == "Shopware\\Core\\Content\\Product\\ProductDefinition").then(|| product("product"))
    };
    let second = |class: &str| {
        (class == "Shopware\\Core\\Content\\Product\\ProductDefinition").then(|| product("item"))
    };
    let scan = |declarations: &[IndexedClassDeclaration], lookup: RelationLookup, memo| {
        crate::context::scan_indexed_plugin_schema_with_enricher(
            root,
            declarations,
            Some(lookup),
            false,
            None,
            memo,
        )
        .unwrap()
    };
    for (lookup, change) in [
        (&first as RelationLookup, false),
        (&first, false),
        (&second, false),
        (&second, true),
        (&first, false),
    ] {
        if change {
            declarations[1].source = memo_definition("B", "acme_b_changed");
        }
        assert_eq!(
            scan(&declarations, lookup, Some(&memo)),
            scan(&declarations, lookup, None)
        );
    }
}
