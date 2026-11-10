use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::sync::Arc;

use sha2::{Digest, Sha256};
use swlsp_shopware_entityschema::{self as es, EntitySpec, FieldSpec};
use swlsp_uriutil::file_uri;
use tempfile::TempDir;

use super::{ctx, index_source, path_string, php_index, raw, write};
use crate::scaffold::entity_schema::{
    History, entity_schema_field_types, entity_schema_revision, entity_schema_revision_timestamp,
    merge_previous_entity_spec, normalize_definition_transition, reconcile_entity_schema_drift,
    safe_plugin_target, validate_entity_schema_spec,
};
use crate::scaffold::*;

fn uri(path: impl AsRef<Path>) -> String {
    file_uri(&path_string(path))
}

fn composer(root: &Path, name: &str, namespace: &str, version: Option<&str>) {
    let require = version
        .map(|version| format!(r#""require":{{"shopware/core":"{version}"}},"#))
        .unwrap_or_default();
    let escaped = namespace.replace('\\', "\\\\");
    write(
        root.join("composer.json"),
        &format!(
            r#"{{
  "name":"{name}","type":"shopware-platform-plugin",{require}
  "autoload":{{"psr-4":{{"{escaped}\\":"src/"}}}},
  "extra":{{"shopware-plugin-class":"{escaped}\\Plugin"}}
}}"#
        ),
    );
}

fn field(id: &str, kind: &str) -> FieldSpec {
    FieldSpec {
        id: id.to_owned(),
        kind: kind.to_owned(),
        editable: true,
        ..FieldSpec::default()
    }
}

fn column(id: &str, kind: &str, property: &str, storage: &str) -> FieldSpec {
    FieldSpec {
        property_name: property.to_owned(),
        storage_name: storage.to_owned(),
        ..field(id, kind)
    }
}

fn new_spec(
    root: &Path,
    directory: &Path,
    namespace: &str,
    class: &str,
    entity: &str,
) -> EntitySpec {
    EntitySpec {
        mode: "new".to_owned(),
        plugin_root_uri: uri(root),
        directory_uri: uri(directory),
        namespace: namespace.to_owned(),
        class_name: class.to_owned(),
        entity_name: entity.to_owned(),
        create_migration: true,
        ..EntitySpec::default()
    }
}

fn preview(
    provider: &Provider,
    request: &EntitySchemaPreviewRequest,
) -> EntitySchemaPreviewResponse {
    provider
        .entity_schema_preview(&ctx(), Some(&raw(request)))
        .unwrap()
}

fn try_apply(
    provider: &Provider,
    request: &EntitySchemaPreviewRequest,
    revision: &str,
    allow_destructive: bool,
) -> crate::scaffold::Result<EntitySchemaApplyResponse> {
    let apply = EntitySchemaApplyRequest {
        preview: request.clone(),
        revision: revision.to_owned(),
        allow_destructive,
    };
    provider.entity_schema_apply(&ctx(), Some(&raw(&apply)))
}

fn apply(
    provider: &Provider,
    request: &EntitySchemaPreviewRequest,
    revision: &str,
    allow_destructive: bool,
) {
    let response = try_apply(provider, request, revision, allow_destructive).unwrap();
    apply_workspace_edit(response.edit.as_ref().unwrap());
}

fn load(provider: &Provider, request: &EntitySchemaLoadRequest) -> EntitySpec {
    provider
        .entity_schema_load(&ctx(), Some(&raw(request)))
        .unwrap()
}

fn load_path(provider: &Provider, path: &Path) -> EntitySpec {
    load(
        provider,
        &EntitySchemaLoadRequest {
            file_uri: uri(path),
            ..EntitySchemaLoadRequest::default()
        },
    )
}

fn bootstrap(provider: &Provider, directory: &Path) -> EntitySchemaBootstrapResponse {
    let request = EntitySchemaBootstrapRequest {
        directory_uri: uri(directory),
    };
    provider
        .entity_schema_bootstrap(&ctx(), Some(&raw(&request)))
        .unwrap()
}

fn issue_codes(issues: &[es::ValidationIssue]) -> Vec<String> {
    issues.iter().map(|issue| issue.code.clone()).collect()
}

fn file_after(files: &[EntitySchemaFilePreview], predicate: impl Fn(&str) -> bool) -> &str {
    files
        .iter()
        .find(|file| predicate(&file.uri))
        .map_or("", |file| file.after.as_str())
}

fn migration_after(files: &[EntitySchemaFilePreview]) -> &str {
    file_after(files, |uri| uri.contains("/Migration/Migration"))
}

fn apply_workspace_edit(edit: &swlsp_lsp_protocol::WorkspaceEdit) {
    for change in &edit.document_changes {
        match change.kind.as_str() {
            "create" => {
                let path = swlsp_uriutil::path(&change.uri).unwrap();
                fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
                fs::OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(&path)
                    .unwrap();
            }
            "delete" => {
                fs::remove_file(swlsp_uriutil::path(&change.uri).unwrap()).unwrap();
            }
            _ => {
                let document = change.text_document.as_ref().unwrap();
                assert_eq!(change.edits.len(), 1);
                let path = swlsp_uriutil::path(&document.uri).unwrap();
                fs::write(path, &change.edits[0].new_text).unwrap();
            }
        }
    }
}

fn dal_index(cache: &Path) -> Arc<swlsp_shopware_dal::Index> {
    Arc::new(swlsp_shopware_dal::Index::new(cache, None).unwrap())
}

fn index_dal_definition(
    dal: &swlsp_shopware_dal::Index,
    path: impl AsRef<Path>,
    namespace: &str,
    class: &str,
    entity: &str,
) {
    index_source(
        dal,
        path,
        &format!(
            "<?php declare(strict_types=1);
namespace {namespace};
use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;
use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\IdField;
use Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection;
class {class} extends EntityDefinition {{
    public const ENTITY_NAME = '{entity}';
    protected function defineFields(): FieldCollection {{ return new FieldCollection([new IdField('id', 'id')]); }}
}}"
        ),
    );
}

const PRODUCT_DEFINITION: &str = "Shopware\\Core\\Content\\Product\\ProductDefinition";
const CATEGORY_DEFINITION: &str = "Shopware\\Core\\Content\\Category\\CategoryDefinition";

fn index_product_and_category(dal: &swlsp_shopware_dal::Index, root: &Path) {
    index_dal_definition(
        dal,
        root.join("vendor/shopware/core/Content/Product/ProductDefinition.php"),
        "Shopware\\Core\\Content\\Product",
        "ProductDefinition",
        "product",
    );
    index_dal_definition(
        dal,
        root.join("vendor/shopware/core/Content/Category/CategoryDefinition.php"),
        "Shopware\\Core\\Content\\Category",
        "CategoryDefinition",
        "category",
    );
}

#[test]
fn create_preview_and_apply() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/Example");
    fs::create_dir_all(&directory).unwrap();
    write(
        root.join("composer.json"),
        r#"{
  "name": "acme/example",
  "type": "shopware-platform-plugin",
  "require": {"shopware/core": "~6.7"},
  "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Example\\AcmeExample"}
}"#,
    );
    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, &directory);
    assert_eq!(boot.plugin.namespace, "Acme\\Example\\Entity\\Example");
    assert!(
        boot.definition_kinds
            .contains(&es::DEFINITION_BULK_EXTENSION.to_owned())
    );
    assert_eq!(boot.spec.fields.len(), 1);
    assert_eq!(boot.spec.fields[0].kind, es::FIELD_ID);
    let mut timestamp_types = 0;
    for field_type in &boot.field_types {
        if field_type.kind == "created-at" || field_type.kind == "updated-at" {
            assert_eq!(field_type.definition_kinds, vec!["entity", "mapping"]);
            assert!(field_type.requires_default_fields_override);
            timestamp_types += 1;
        }
    }
    assert_eq!(timestamp_types, 2);

    let spec = es::complete_spec(EntitySpec {
        plugin_root_uri: boot.plugin.root_uri.clone(),
        definition_behavior: Some(es::DefinitionBehaviorSpec {
            parent_definition_class: PRODUCT_DEFINITION.to_owned(),
            version_aware: Some(false),
            override_default_fields: true,
            restrict_delete_meta_properties: vec!["id".to_owned()],
            ..es::DefinitionBehaviorSpec::default()
        }),
        definition_metadata: Some(es::DefinitionMetadataSpec {
            since: "6.7.1.0".to_owned(),
            defaults: vec![es::DefinitionDefaultSpec {
                property_name: "name".to_owned(),
                value_expression: "'default'".to_owned(),
            }],
            child_defaults: vec![es::DefinitionDefaultSpec {
                property_name: "name".to_owned(),
                value_expression: "'child'".to_owned(),
            }],
            hydrator_class: "Acme\\Example\\Entity\\Example\\ExampleHydrator".to_owned(),
            ..es::DefinitionMetadataSpec::default()
        }),
        fields: vec![
            field("id", es::FIELD_ID),
            FieldSpec {
                required: true,
                ..column("name", es::FIELD_STRING, "name", "name")
            },
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\Example\\Entity\\Example",
            "Example",
            "acme_example",
        )
    });
    let request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert!(response.revision.contains(':'));
    assert!(!response.snapshot_id.is_empty());
    assert!(response.migration_timestamp > 0);
    assert!(response.files.len() >= 7);
    let mut snapshot_files = 0;
    let mut service_file = String::new();
    for file in &response.files {
        assert!(!file.after.contains("updateDestructive"));
        if file.uri.ends_with("/Resources/config/services.yaml") {
            service_file = file.uri.clone();
            assert!(file.after.contains("shopware.entity.definition"));
        }
        assert!(!file.uri.ends_with("/Resources/config/services.xml"));
        if file.uri.ends_with(".json") {
            snapshot_files += 1;
        }
    }
    assert!(!service_file.is_empty());
    let definition = file_after(&response.files, |uri| {
        uri.ends_with("/ExampleDefinition.php")
    });
    for expected in [
        "return ProductDefinition::class",
        "public function isVersionAware(): bool",
        "protected function defaultFields(): array",
        "getRestrictDeleteMetaFields",
        "public function since(): ?string",
        "public function getDefaults(): array",
        "public function getChildDefaults(): array",
        "return ExampleHydrator::class",
    ] {
        assert!(definition.contains(expected), "{expected}");
    }
    assert_eq!(snapshot_files, 2);

    let applied = try_apply(&provider, &request, &response.revision, false).unwrap();
    assert!(!applied.edit.unwrap().document_changes.is_empty());
}

#[test]
fn technical_name_change_requires_table_rename_decision() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/Example");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/rename", "Acme\\Rename", Some("~6.7"));
    let provider = Provider::new(&path_string(root), None, None, None);
    let spec = es::complete_spec(EntitySpec {
        fields: vec![
            field("id", es::FIELD_ID),
            column("payload", es::FIELD_JSON, "payload", "payload"),
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\Rename\\Entity\\Example",
            "Example",
            "acme_old",
        )
    });
    let mut create_request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let created = preview(&provider, &create_request);
    assert!(created.issues.is_empty());
    create_request.spec.migration_timestamp = created.migration_timestamp;
    apply(&provider, &create_request, &created.revision, false);

    let mut loaded = load_path(&provider, &directory.join("ExampleDefinition.php"));
    loaded.entity_name = "acme_new".to_owned();
    loaded.migration_timestamp = 0;
    let mut rename_request = EntitySchemaPreviewRequest {
        spec: loaded,
        ..EntitySchemaPreviewRequest::default()
    };
    let unresolved = preview(&provider, &rename_request);
    assert!(unresolved.issues.contains(&es::ValidationIssue {
        code: "entity.table.rename.decision".to_owned(),
        message: "unresolved entity change for acme_new".to_owned(),
        field_id: String::new(),
        severity: "error".to_owned(),
    }));
    assert_eq!(unresolved.diff.entity_rename_questions.len(), 1);
    assert_eq!(
        unresolved.diff.entity_rename_questions[0].candidates[0].from,
        "acme_old"
    );

    rename_request.spec.migration_timestamp = unresolved.migration_timestamp;
    rename_request.decisions = vec![es::Decision {
        kind: "entityRename".to_owned(),
        entity: "acme_new".to_owned(),
        from: "acme_old".to_owned(),
        to: "acme_new".to_owned(),
        ..es::Decision::default()
    }];
    rename_request.spec.create_migration = false;
    assert!(
        preview(&provider, &rename_request)
            .issues
            .contains(&es::ValidationIssue {
                code: "entity.migration.required".to_owned(),
                message: "Database changes require a migration".to_owned(),
                field_id: String::new(),
                severity: "error".to_owned(),
            })
    );
    rename_request.spec.create_migration = true;
    let resolved = preview(&provider, &rename_request);
    assert!(resolved.issues.is_empty(), "{:?}", resolved.issues);
    assert!(!resolved.destructive);
    let migration = migration_after(&resolved.files);
    let definition = file_after(&resolved.files, |uri| {
        uri.ends_with("/ExampleDefinition.php")
    });
    let collection = file_after(&resolved.files, |uri| {
        uri.ends_with("/ExampleCollection.php")
    });
    assert!(migration.contains("RENAME TABLE `acme_old` TO `acme_new`;"));
    assert!(!migration.contains("DROP TABLE"));
    assert!(!migration.contains("CREATE TABLE"));
    assert!(definition.contains("const ENTITY_NAME = 'acme_new';"));
    assert!(!definition.contains("const ENTITY_NAME = 'acme_old';"));
    assert!(collection.contains("return 'acme_new_collection';"));
    assert!(!collection.contains("return 'acme_old_collection';"));
}

#[test]
fn mapping_definition_preview_creates_only_definition_and_table() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Content/ProductTag");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/mapping", "Acme\\Mapping", Some("~6.7"));
    let provider = Provider::new(&path_string(root), None, None, None);
    let foreign = |id: &str, property: &str, storage: &str, class: &str, entity: &str| FieldSpec {
        target_definition_class: class.to_owned(),
        target_entity_name: entity.to_owned(),
        required: true,
        primary: true,
        ..column(id, es::FIELD_FOREIGN_KEY, property, storage)
    };
    let spec = es::complete_spec(EntitySpec {
        definition_kind: es::DEFINITION_MAPPING.to_owned(),
        fields: vec![
            foreign(
                "product",
                "productId",
                "product_id",
                PRODUCT_DEFINITION,
                "product",
            ),
            foreign(
                "tag",
                "tagId",
                "tag_id",
                "Shopware\\Core\\System\\Tag\\TagDefinition",
                "tag",
            ),
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\Mapping\\Content\\ProductTag",
            "ProductTag",
            "acme_product_tag",
        )
    });
    let request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert_eq!(response.files.len(), 5);
    for file in &response.files {
        assert!(!file.uri.contains("ProductTagEntity.php"));
        assert!(!file.uri.contains("ProductTagCollection.php"));
    }
    let definition = file_after(&response.files, |uri| {
        uri.ends_with("/ProductTagDefinition.php")
    });
    let migration = migration_after(&response.files);
    assert!(definition.contains("extends MappingEntityDefinition"));
    assert!(!definition.contains("getEntityClass"));
    assert!(migration.contains("PRIMARY KEY (`product_id`, `tag_id`)"));
    assert!(!migration.contains("`created_at`"));
    assert!(!migration.contains("`updated_at`"));
    let applied = try_apply(&provider, &request, &response.revision, false).unwrap();
    assert!(!applied.edit.unwrap().document_changes.is_empty());
}

#[test]
fn version_aware_hierarchy_preview() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Content/Node");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/hierarchy", "Acme\\Hierarchy", Some("~6.7"));
    let provider = Provider::new(&path_string(root), None, None, None);
    let spec = es::complete_spec(EntitySpec {
        inheritance_aware: true,
        fields: vec![
            field("id", es::FIELD_ID),
            field("version", es::FIELD_VERSION),
            FieldSpec {
                property_name: "children".to_owned(),
                ..field("hierarchy", es::FIELD_HIERARCHY)
            },
            FieldSpec {
                inherited: true,
                ..column("label", es::FIELD_STRING, "label", "label")
            },
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\Hierarchy\\Content\\Node",
            "Node",
            "acme_node",
        )
    });
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    let definition = file_after(&response.files, |uri| uri.ends_with("/NodeDefinition.php"));
    let entity = file_after(&response.files, |uri| uri.ends_with("/NodeEntity.php"));
    let migration = migration_after(&response.files);
    for expected in [
        "new ParentFkField(NodeDefinition::class)",
        "new ReferenceVersionField(NodeDefinition::class, 'parent_version_id')",
        "new ParentAssociationField(NodeDefinition::class, 'id')",
        "new ChildrenAssociationField(NodeDefinition::class)",
        "public function isInheritanceAware(): bool",
        "new StringField('label', 'label'))->addFlags(new Inherited())",
    ] {
        assert!(definition.contains(expected), "{expected}");
    }
    assert!(entity.contains("protected ?NodeEntity $parent = null;"));
    assert!(entity.contains("protected ?NodeCollection $children = null;"));
    assert!(migration.contains("`parent_id` BINARY(16) NULL"));
    assert!(migration.contains("`parent_version_id` BINARY(16) NULL"));
    assert!(migration.contains(
        "FOREIGN KEY (`parent_id`, `parent_version_id`) REFERENCES `acme_node` (`id`, `version_id`) ON DELETE CASCADE"
    ));
}

fn many_to_one(
    id: &str,
    property: &str,
    storage: &str,
    target_class: &str,
    target_entity: &str,
) -> FieldSpec {
    let base = target_class
        .strip_suffix("Definition")
        .unwrap_or(target_class);
    FieldSpec {
        foreign_key_property_name: format!("{property}Id"),
        target_definition_class: target_class.to_owned(),
        target_entity_class: format!("{base}Entity"),
        target_collection_class: format!("{base}Collection"),
        target_entity_name: target_entity.to_owned(),
        reference_field: "id".to_owned(),
        reference_storage_name: "id".to_owned(),
        delete_behavior: es::DELETE_SET_NULL.to_owned(),
        ..column(id, es::FIELD_MANY_TO_ONE, property, storage)
    }
}

fn unique(name: &str, columns: &[&str]) -> es::IndexSpec {
    es::IndexSpec {
        name: name.to_owned(),
        kind: es::INDEX_UNIQUE.to_owned(),
        columns: columns.iter().map(|column| (*column).to_owned()).collect(),
        translation: false,
    }
}

#[test]
fn extension_preview_apply_and_load() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Extension/Content/Product");
    fs::create_dir_all(&directory).unwrap();
    composer(
        root,
        "acme/product-extension",
        "Acme\\ProductExtension",
        Some("~6.7"),
    );
    let cache = TempDir::new().unwrap();
    let dal = dal_index(cache.path());
    index_product_and_category(&dal, root);
    let provider = Provider::new(&path_string(root), None, None, Some(dal));

    let spec = es::complete_spec(EntitySpec {
        definition_kind: es::DEFINITION_EXTENSION.to_owned(),
        extended_definition_class: PRODUCT_DEFINITION.to_owned(),
        read_protected: true,
        read_protection_scopes: vec!["system".to_owned()],
        field_modifications: vec![es::FieldModificationSpec {
            id: "modify-id".to_owned(),
            property_name: "id".to_owned(),
            add_flags: vec![es::FieldFlagSpec {
                kind: es::FLAG_API_AWARE.to_owned(),
                ..es::FieldFlagSpec::default()
            }],
            remove_flags: vec![es::FLAG_REQUIRED.to_owned()],
        }],
        fields: vec![
            FieldSpec {
                foreign_key_property_name: "categoryId".to_owned(),
                ..many_to_one(
                    "category",
                    "category",
                    "acme_category_id",
                    CATEGORY_DEFINITION,
                    "category",
                )
            },
            FieldSpec {
                reference_storage_name: "parent_id".to_owned(),
                source_column: "id".to_owned(),
                target_definition_class: PRODUCT_DEFINITION.to_owned(),
                target_entity_class: "Shopware\\Core\\Content\\Product\\ProductEntity".to_owned(),
                target_collection_class: "Shopware\\Core\\Content\\Product\\ProductCollection"
                    .to_owned(),
                target_entity_name: "product".to_owned(),
                property_name: "children".to_owned(),
                ..field("children", es::FIELD_ONE_TO_MANY)
            },
        ],
        indexes: vec![unique(
            "uniq.product.acme_category_id_id",
            &["acme_category_id", "id"],
        )],
        ..new_spec(
            root,
            &directory,
            "Acme\\ProductExtension\\Extension\\Content\\Product",
            "ProductCustom",
            "product",
        )
    });
    let mut request = EntitySchemaPreviewRequest {
        spec: spec.clone(),
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert_eq!(response.files.len(), 5);
    for file in &response.files {
        assert!(!file.uri.contains("ProductCustomEntity.php"));
        assert!(!file.uri.contains("ProductCustomCollection.php"));
    }
    let extension = file_after(&response.files, |uri| {
        uri.ends_with("/ProductCustomExtension.php")
    });
    let service = file_after(&response.files, |uri| uri.ends_with("/services.yaml"));
    let migration = migration_after(&response.files);
    for expected in [
        "extends EntityExtension",
        "return ProductDefinition::ENTITY_NAME",
        "function extendProtections(EntityProtectionCollection $protections): void",
        "$protections->add(new ReadProtection('system'))",
        "$collection->get('id')?->addFlags(new ApiAware())",
        "$collection->get('id')?->removeFlag(Required::class)",
    ] {
        assert!(extension.contains(expected), "{expected}");
    }
    assert!(!extension.contains("getDefinitionClass"));
    assert!(service.contains("shopware.entity.extension"));
    assert!(
        migration.contains("ALTER TABLE `product` ADD COLUMN `acme_category_id` BINARY(16) NULL")
    );
    assert!(migration.contains(
        "ADD UNIQUE INDEX `uniq.product.acme_category_id_id` (`acme_category_id`, `id`)"
    ));
    assert!(migration.contains("REFERENCES `category` (`id`) ON DELETE SET NULL"));
    assert!(!migration.contains("CREATE TABLE"));
    assert!(response.diff.created_entities.is_empty());
    assert_eq!(response.diff.added_columns.len(), 1);
    assert_eq!(
        response.diff.added_indexes.len(),
        2,
        "the DAL relation owns its foreign-key index in addition to the custom index"
    );

    request.spec.migration_timestamp = response.migration_timestamp;
    apply(&provider, &request, &response.revision, false);

    let extension_path = directory.join("ProductCustomExtension.php");
    let mut loaded = load_path(&provider, &extension_path);
    assert_eq!(loaded.definition_kind, es::DEFINITION_EXTENSION);
    assert_eq!(loaded.extended_definition_class, PRODUCT_DEFINITION);
    assert_eq!(loaded.entity_name, "product");
    assert_eq!(loaded.fields.len(), 2);
    assert!(loaded.extended_fields.contains(&es::RelationTargetField {
        property_name: "id".to_owned(),
        storage_name: "id".to_owned(),
        primary: true,
    }));
    assert!(loaded.extended_fields.contains(&es::RelationTargetField {
        property_name: "createdAt".to_owned(),
        storage_name: "created_at".to_owned(),
        primary: false,
    }));
    assert_eq!(loaded.indexes, spec.indexes);
    assert!(loaded.read_protected);
    assert_eq!(loaded.read_protection_scopes, vec!["system"]);
    assert_eq!(loaded.field_modifications.len(), 1);
    assert_eq!(loaded.field_modifications[0].property_name, "id");
    assert_eq!(
        loaded.field_modifications[0].remove_flags,
        vec![es::FLAG_REQUIRED]
    );

    loaded.entity_name = "category".to_owned();
    loaded.extended_definition_class = CATEGORY_DEFINITION.to_owned();
    loaded.migration_timestamp = 0;
    let mut retarget_request = EntitySchemaPreviewRequest {
        spec: loaded,
        ..EntitySchemaPreviewRequest::default()
    };
    let retarget = preview(&provider, &retarget_request);
    assert!(retarget.issues.is_empty(), "{:?}", retarget.issues);
    assert!(retarget.destructive);
    assert_eq!(retarget.diff.removed_columns.len(), 1);
    assert_eq!(retarget.diff.removed_columns[0].entity, "product");
    assert_eq!(retarget.diff.added_columns.len(), 1);
    assert_eq!(retarget.diff.added_columns[0].entity, "category");
    assert_eq!(retarget.diff.removed_indexes.len(), 2);
    assert!(
        retarget
            .diff
            .removed_indexes
            .iter()
            .all(|index| index.entity == "product")
    );
    assert_eq!(retarget.diff.added_indexes.len(), 2);
    assert!(
        retarget
            .diff
            .added_indexes
            .iter()
            .all(|index| index.entity == "category")
    );
    let retarget_definition = file_after(&retarget.files, |uri| {
        uri.ends_with("/ProductCustomExtension.php")
    });
    let retarget_migration = migration_after(&retarget.files);
    assert!(retarget_definition.contains("return CategoryDefinition::ENTITY_NAME"));
    for expected in [
        "ALTER TABLE `product` DROP COLUMN `acme_category_id`",
        "ALTER TABLE `product` DROP INDEX `uniq.product.acme_category_id_id`",
        "ALTER TABLE `category` ADD COLUMN `acme_category_id`",
        "ALTER TABLE `category` ADD UNIQUE INDEX `uniq.product.acme_category_id_id` (`acme_category_id`, `id`)",
    ] {
        assert!(retarget_migration.contains(expected), "{expected}");
    }
    retarget_request.spec.migration_timestamp = retarget.migration_timestamp;
    apply(&provider, &retarget_request, &retarget.revision, true);
    let retargeted = load_path(&provider, &extension_path);
    assert_eq!(retargeted.extended_definition_class, CATEGORY_DEFINITION);
    assert_eq!(retargeted.entity_name, "category");
}

#[test]
fn extension_target_can_change_during_edit() {
    let previous = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: es::DEFINITION_EXTENSION.to_owned(),
        entity_name: "product".to_owned(),
        extended_definition_class: PRODUCT_DEFINITION.to_owned(),
        ..EntitySpec::default()
    };
    let mut requested = previous.clone();
    requested.entity_name = "category".to_owned();
    requested.extended_definition_class = CATEGORY_DEFINITION.to_owned();
    let issues =
        merge_previous_entity_spec(&requested, Some(&previous), &[], &mut es::Schema::default())
            .unwrap();
    assert!(issues.is_empty());
}

#[test]
fn bulk_extension_preview_apply_and_load() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Extension/Catalog");
    fs::create_dir_all(&directory).unwrap();
    composer(
        root,
        "acme/bulk-extension",
        "Acme\\BulkExtension",
        Some("~6.7"),
    );
    let cache = TempDir::new().unwrap();
    let dal = dal_index(cache.path());
    index_product_and_category(&dal, root);
    let provider = Provider::new(&path_string(root), None, None, Some(dal));
    let spec = es::complete_spec(EntitySpec {
        definition_kind: es::DEFINITION_BULK_EXTENSION.to_owned(),
        bulk_extensions: vec![
            es::BulkExtensionTargetSpec {
                id: "product".to_owned(),
                entity_name: "product".to_owned(),
                extended_definition_class: PRODUCT_DEFINITION.to_owned(),
                fields: vec![many_to_one(
                    "category",
                    "acmeCategory",
                    "acme_category_id",
                    CATEGORY_DEFINITION,
                    "category",
                )],
                indexes: vec![unique(
                    "uniq.product.acme_category_id_id",
                    &["acme_category_id", "id"],
                )],
                ..es::BulkExtensionTargetSpec::default()
            },
            es::BulkExtensionTargetSpec {
                id: "category".to_owned(),
                entity_name: "category".to_owned(),
                extended_definition_class: CATEGORY_DEFINITION.to_owned(),
                fields: vec![many_to_one(
                    "product",
                    "acmeProduct",
                    "acme_product_id",
                    PRODUCT_DEFINITION,
                    "product",
                )],
                indexes: vec![unique(
                    "uniq.category.acme_product_id_id",
                    &["acme_product_id", "id"],
                )],
                ..es::BulkExtensionTargetSpec::default()
            },
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\BulkExtension\\Extension\\Catalog",
            "Catalog",
            "",
        )
    });
    let mut request = EntitySchemaPreviewRequest {
        spec: spec.clone(),
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert_eq!(response.diff.added_columns.len(), 2);
    for file in &response.files {
        assert!(!file.uri.contains("CatalogEntity.php"));
        assert!(!file.uri.contains("CatalogCollection.php"));
    }
    let definition = file_after(&response.files, |uri| {
        uri.ends_with("/CatalogBulkEntityExtension.php")
    });
    let service = file_after(&response.files, |uri| uri.ends_with("/services.yaml"));
    let migration = migration_after(&response.files);
    assert!(definition.contains("extends BulkEntityExtension"));
    assert!(definition.contains("yield ProductDefinition::ENTITY_NAME"));
    assert!(definition.contains("yield CategoryDefinition::ENTITY_NAME"));
    assert!(service.contains("shopware.bulk.entity.extension"));
    assert!(!service.contains("shopware.entity.definition"));
    assert!(migration.contains("ALTER TABLE `product` ADD COLUMN `acme_category_id`"));
    assert!(migration.contains("ALTER TABLE `category` ADD COLUMN `acme_product_id`"));

    request.spec.migration_timestamp = response.migration_timestamp;
    apply(&provider, &request, &response.revision, false);
    let loaded = load_path(&provider, &directory.join("CatalogBulkEntityExtension.php"));
    assert_eq!(loaded.definition_kind, es::DEFINITION_BULK_EXTENSION);
    assert_eq!(loaded.class_name, "Catalog");
    assert_eq!(loaded.bulk_extensions.len(), 2);
    assert_eq!(
        loaded.bulk_extensions[0].indexes,
        spec.bulk_extensions[0].indexes
    );
    assert_eq!(
        loaded.bulk_extensions[1].indexes,
        spec.bulk_extensions[1].indexes
    );
    assert!(
        loaded.bulk_extensions[0]
            .extended_fields
            .contains(&es::RelationTargetField {
                property_name: "id".to_owned(),
                storage_name: "id".to_owned(),
                primary: true,
            })
    );
}

#[test]
fn preserves_custom_bulk_collect_and_rejects_structured_edits() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Extension");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/dynamic", "Acme\\Dynamic", Some("~6.7"));
    let definition_path = directory.join("DynamicBulkEntityExtension.php");
    let source = r"<?php declare(strict_types=1);

namespace Acme\Dynamic\Extension;

use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;

class DynamicBulkEntityExtension extends BulkEntityExtension
{
    public function collect(): \Generator
    {
        yield from $this->computedTargets();
    }

    private function computedTargets(): \Generator
    {
        yield 'product' => [];
    }
}
";
    write(&definition_path, source);
    let provider = Provider::new(&path_string(root), None, None, None);
    let loaded = load_path(&provider, &definition_path);
    assert!(!loaded.collect_method_raw.is_empty());
    assert!(loaded.bulk_extensions.is_empty());

    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: loaded.clone(),
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    for file in &response.files {
        if file.uri == uri(&definition_path) {
            assert_eq!(file.after, source);
        }
    }

    let mut mutated = loaded;
    mutated.collect_method_raw.push_str("\n// changed");
    mutated.bulk_extensions = vec![es::BulkExtensionTargetSpec {
        id: "product".to_owned(),
        entity_name: "product".to_owned(),
        ..es::BulkExtensionTargetSpec::default()
    }];
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: mutated,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(
        issue_codes(&response.issues)
            .contains(&"entity.bulkExtension.collectRaw.locked".to_owned())
    );
}

#[test]
fn preserves_and_locks_custom_definition_behavior() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Content/Catalog");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/behavior", "Acme\\Behavior", Some("~6.7"));
    let definition_path = directory.join("CatalogDefinition.php");
    write(
        &definition_path,
        r"<?php declare(strict_types=1);
namespace Acme\Behavior\Content\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { return $this->configuredSince(); }
    public function isVersionAware(): bool { return $this->configuredVersioning(); }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}",
    );
    let provider = Provider::new(&path_string(root), None, None, None);
    let loaded = load(
        &provider,
        &EntitySchemaLoadRequest {
            file_uri: uri(&definition_path),
            definition_class: "Acme\\Behavior\\Content\\Catalog\\CatalogDefinition".to_owned(),
            definition_kind: es::DEFINITION_ENTITY.to_owned(),
            ..EntitySchemaLoadRequest::default()
        },
    );
    assert!(
        !loaded
            .definition_metadata
            .as_ref()
            .unwrap()
            .since_method_raw
            .is_empty()
    );
    assert!(
        !loaded
            .definition_behavior
            .as_ref()
            .unwrap()
            .version_aware_method_raw
            .is_empty()
    );
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: loaded.clone(),
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(response.issues.is_empty(), "{:?}", response.issues);

    let mut mutated = loaded;
    mutated
        .definition_metadata
        .as_mut()
        .unwrap()
        .since_method_raw
        .push_str("\n// changed");
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: mutated,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(issue_codes(&response.issues).contains(&"entity.definition.raw.locked".to_owned()));
}

#[test]
fn applies_every_class_based_definition_kind_transition() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Content/Transition");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/transition", "Acme\\Transition", Some("~6.7"));
    let cache = TempDir::new().unwrap();
    let dal = dal_index(cache.path());
    index_dal_definition(
        &dal,
        root.join("vendor/shopware/core/Content/Product/ProductDefinition.php"),
        "Shopware\\Core\\Content\\Product",
        "ProductDefinition",
        "product",
    );
    let provider = Provider::new(&path_string(root), None, None, Some(dal));
    let definition_path = directory.join("TransitionDefinition.php");
    let entity_path = directory.join("TransitionEntity.php");
    let collection_path = directory.join("TransitionCollection.php");
    let service_path = root.join("src/Resources/config/services.yaml");

    let run = |mut spec: EntitySpec, allow_destructive: bool| -> EntitySchemaPreviewResponse {
        spec.migration_timestamp = 0;
        let mut request = EntitySchemaPreviewRequest {
            spec,
            ..EntitySchemaPreviewRequest::default()
        };
        let response = preview(&provider, &request);
        assert!(
            response.issues.is_empty(),
            "diff: {:?} {:?}",
            response.diff,
            response.issues
        );
        request.spec.migration_timestamp = response.migration_timestamp;
        assert_eq!(preview(&provider, &request).revision, response.revision);
        apply(&provider, &request, &response.revision, allow_destructive);
        response
    };
    let base_fields = || {
        vec![
            FieldSpec {
                required: true,
                primary: true,
                ..column("id", es::FIELD_ID, "id", "id")
            },
            column("note", es::FIELD_STRING, "note", "note"),
        ]
    };
    let convert = |kind: &str| -> EntitySchemaPreviewResponse {
        let mut spec = load_path(&provider, &definition_path);
        spec.definition_kind = kind.to_owned();
        spec.translation = None;
        spec.inheritance_aware = false;
        spec.read_protected = false;
        spec.write_protected = false;
        spec.preserved_protections = Vec::new();
        spec.protection_method_raw.clear();
        spec.field_modifications = Vec::new();
        spec.modify_fields_method_raw.clear();
        spec.extended_definition_class.clear();
        spec.extended_fields = Vec::new();
        spec.bulk_extensions = Vec::new();
        spec.entity_name = "acme_transition".to_owned();
        spec.fields = base_fields();
        match kind {
            es::DEFINITION_EXTENSION => {
                spec.entity_name = "product".to_owned();
                spec.extended_definition_class = PRODUCT_DEFINITION.to_owned();
                spec.fields.remove(0);
                spec.fields[0].behavior = Some(es::FieldBehavior {
                    runtime: true,
                    ..es::FieldBehavior::default()
                });
            }
            es::DEFINITION_BULK_EXTENSION => {
                spec.entity_name.clear();
                spec.fields = Vec::new();
                spec.indexes = Vec::new();
                spec.bulk_extensions = vec![es::BulkExtensionTargetSpec {
                    id: "product".to_owned(),
                    entity_name: "product".to_owned(),
                    extended_definition_class: PRODUCT_DEFINITION.to_owned(),
                    fields: vec![FieldSpec {
                        behavior: Some(es::FieldBehavior {
                            runtime: true,
                            ..es::FieldBehavior::default()
                        }),
                        ..column("note", es::FIELD_STRING, "note", "note")
                    }],
                    ..es::BulkExtensionTargetSpec::default()
                }];
            }
            _ => {}
        }
        run(spec, true)
    };

    run(
        es::complete_spec(EntitySpec {
            fields: base_fields(),
            ..new_spec(
                root,
                &directory,
                "Acme\\Transition\\Content\\Transition",
                "Transition",
                "acme_transition",
            )
        }),
        false,
    );
    assert!(entity_path.exists());
    assert!(collection_path.exists());

    for (kind, destructive) in [
        (es::DEFINITION_MAPPING, true),
        (es::DEFINITION_ENTITY, false),
        (es::DEFINITION_EXTENSION, true),
        (es::DEFINITION_BULK_EXTENSION, false),
        (es::DEFINITION_MAPPING, false),
        (es::DEFINITION_BULK_EXTENSION, true),
        (es::DEFINITION_EXTENSION, false),
        (es::DEFINITION_ENTITY, false),
    ] {
        let response = convert(kind);
        assert_eq!(response.destructive, destructive, "{kind}");
        let definition = fs::read_to_string(&definition_path).unwrap();
        let service = fs::read_to_string(&service_path).unwrap();
        match kind {
            es::DEFINITION_ENTITY => {
                assert!(definition.contains("extends EntityDefinition"));
                assert!(!definition.contains("new Extension()"));
                assert!(entity_path.exists());
                assert!(collection_path.exists());
                assert!(service.contains("shopware.entity.definition"));
                assert!(!service.contains("shopware.entity.extension"));
            }
            es::DEFINITION_MAPPING => {
                assert!(definition.contains("extends MappingEntityDefinition"));
                assert!(!definition.contains("function getEntityClass"));
                assert!(!definition.contains("function getCollectionClass"));
                assert!(!definition.contains("new Extension()"));
                assert!(!entity_path.exists());
                assert!(!collection_path.exists());
                assert!(service.contains("shopware.entity.definition"));
                assert!(!service.contains("shopware.entity.extension"));
            }
            es::DEFINITION_EXTENSION => {
                assert!(definition.contains("extends EntityExtension"));
                assert!(definition.contains("return ProductDefinition::ENTITY_NAME"));
                assert!(!entity_path.exists());
                assert!(!collection_path.exists());
                assert!(service.contains("shopware.entity.extension"));
                assert!(!service.contains("shopware.entity.definition"));
            }
            _ => {
                assert!(definition.contains("extends BulkEntityExtension"));
                assert!(definition.contains("yield ProductDefinition::ENTITY_NAME"));
                assert!(!entity_path.exists());
                assert!(!collection_path.exists());
                assert!(service.contains("shopware.bulk.entity.extension"));
                assert!(!service.contains("shopware.entity.extension"));
                assert!(!service.contains("shopware.entity.definition"));
            }
        }
        assert_eq!(load_path(&provider, &definition_path).definition_kind, kind);
    }
}

#[test]
fn normalize_definition_transition_preserves_only_current_mapping_companions() {
    let previous_entity = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: es::DEFINITION_ENTITY.to_owned(),
        entity_class: "Acme\\Example\\ExampleEntity".to_owned(),
        collection_class: "Acme\\Example\\ExampleCollection".to_owned(),
        entity_uri: "file:///plugin/ExampleEntity.php".to_owned(),
        collection_uri: "file:///plugin/ExampleCollection.php".to_owned(),
        ..EntitySpec::default()
    };
    let mut next = previous_entity.clone();
    next.definition_kind = es::DEFINITION_MAPPING.to_owned();
    let normalized = normalize_definition_transition(next.clone(), Some(&previous_entity));
    assert!(normalized.entity_class.is_empty());
    assert!(normalized.collection_class.is_empty());
    assert_eq!(normalized.entity_uri, previous_entity.entity_uri);
    assert_eq!(normalized.collection_uri, previous_entity.collection_uri);

    let mut previous_mapping = next;
    previous_mapping.entity_class = "Acme\\Example\\CustomMappingEntity".to_owned();
    previous_mapping.collection_class = "Acme\\Example\\CustomMappingCollection".to_owned();
    let normalized =
        normalize_definition_transition(previous_mapping.clone(), Some(&previous_mapping));
    assert_eq!(normalized.entity_class, previous_mapping.entity_class);
    assert_eq!(
        normalized.collection_class,
        previous_mapping.collection_class
    );

    let previous_extension = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: es::DEFINITION_EXTENSION.to_owned(),
        ..EntitySpec::default()
    };
    let mut to_entity = previous_extension.clone();
    to_entity.definition_kind = es::DEFINITION_ENTITY.to_owned();
    to_entity.fields = vec![FieldSpec {
        metadata: Some(es::FieldMetadata {
            extension: true,
            ..es::FieldMetadata::default()
        }),
        association_metadata: Some(es::FieldMetadata {
            extension: true,
            immutable: true,
            ..es::FieldMetadata::default()
        }),
        ..column("note", es::FIELD_STRING, "note", "note")
    }];
    let normalized = normalize_definition_transition(to_entity, Some(&previous_extension));
    assert!(normalized.fields[0].metadata.is_none());
    let association = normalized.fields[0].association_metadata.as_ref().unwrap();
    assert!(!association.extension);
    assert!(association.immutable);
}

#[test]
fn translation_bundle_preview_apply_and_load() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Content/Blog");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/blog", "Acme\\Blog", Some("~6.7"));
    let provider = Provider::new(&path_string(root), None, None, None);
    let translation_index = es::IndexSpec {
        translation: true,
        ..unique(
            "uniq.acme_blog_translation.name_language",
            &["name", "language_id"],
        )
    };
    let spec = es::complete_spec(EntitySpec {
        fields: vec![
            field("id", es::FIELD_ID),
            field("version", es::FIELD_VERSION),
            FieldSpec {
                required: true,
                api_aware: true,
                translated: true,
                ..column("name", es::FIELD_STRING, "name", "name")
            },
        ],
        indexes: vec![translation_index.clone()],
        ..new_spec(
            root,
            &directory,
            "Acme\\Blog\\Content\\Blog",
            "Blog",
            "acme_blog",
        )
    });
    let mut request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert_eq!(response.files.len(), 10);
    let files: BTreeMap<String, String> = response
        .files
        .iter()
        .map(|file| (file.uri.clone(), file.after.clone()))
        .collect();
    let translation_directory = directory.join("Aggregate/BlogTranslation");
    assert!(
        files[&uri(directory.join("BlogDefinition.php"))].contains("new TranslatedField('name')")
    );
    assert!(
        files[&uri(translation_directory.join("BlogTranslationDefinition.php"))]
            .contains("extends EntityTranslationDefinition")
    );
    assert!(
        files[&uri(translation_directory.join("BlogTranslationEntity.php"))]
            .contains("protected string $acmeBlogVersionId;")
    );
    let service = &files[&uri(root.join("src/Resources/config/services.yaml"))];
    assert!(service.contains("Acme\\Blog\\Content\\Blog\\BlogDefinition"));
    assert!(service.contains(
        "Acme\\Blog\\Content\\Blog\\Aggregate\\BlogTranslation\\BlogTranslationDefinition"
    ));
    let migration = migration_after(&response.files);
    assert!(migration.contains("CREATE TABLE IF NOT EXISTS `acme_blog_translation`"));
    assert!(migration.contains("FOREIGN KEY (`acme_blog_id`, `acme_blog_version_id`)"));
    assert!(
        migration.contains(
            "UNIQUE KEY `uniq.acme_blog_translation.name_language` (`name`, `language_id`)"
        )
    );

    request.spec.migration_timestamp = response.migration_timestamp;
    apply(&provider, &request, &response.revision, false);

    let mut loaded = load_path(&provider, &directory.join("BlogDefinition.php"));
    assert_eq!(
        loaded.translation.as_ref().unwrap().definition_uri,
        uri(translation_directory.join("BlogTranslationDefinition.php"))
    );
    assert_eq!(loaded.indexes, vec![translation_index]);
    let translated = loaded
        .fields
        .iter()
        .find(|field| field.property_name == "name")
        .unwrap();
    assert!(translated.translated);
    assert_eq!(translated.kind, es::FIELD_STRING);
    loaded.fields.push(FieldSpec {
        translated: true,
        ..column(
            "description",
            es::FIELD_LONG_TEXT,
            "description",
            "description",
        )
    });
    let edit = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: loaded.clone(),
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(edit.issues.is_empty(), "{:?}", edit.issues);
    assert!(
        file_after(&edit.files, |uri| uri
            .ends_with("/BlogTranslationDefinition.php"))
        .contains("new LongTextField('description', 'description')")
    );

    loaded.fields.pop();
    for field in &mut loaded.fields {
        if field.property_name == "name" {
            field.translated = false;
            field.migration_default = "'migrated'".to_owned();
        }
    }
    loaded.indexes = Vec::new();
    loaded.translation.as_mut().unwrap().enabled = false;
    let remove_request = EntitySchemaPreviewRequest {
        spec: loaded,
        ..EntitySchemaPreviewRequest::default()
    };
    let removal = preview(&provider, &remove_request);
    assert!(removal.issues.is_empty(), "{:?}", removal.issues);
    assert!(removal.destructive);
    let mut deleted = Vec::new();
    for file in &removal.files {
        if file.action == "delete" {
            deleted.push(file.uri.clone());
        }
        if file.uri.ends_with("/BlogDefinition.php") {
            assert!(file.after.contains("new StringField('name', 'name')"));
            assert!(!file.after.contains("TranslationsAssociationField"));
        }
        if file.uri.ends_with("/services.yaml") {
            assert!(
                !file
                    .after
                    .contains("BlogTranslation\\BlogTranslationDefinition")
            );
        }
    }
    for name in [
        "BlogTranslationDefinition.php",
        "BlogTranslationEntity.php",
        "BlogTranslationCollection.php",
    ] {
        assert!(
            deleted.contains(&uri(translation_directory.join(name))),
            "{name}"
        );
    }
    apply(&provider, &remove_request, &removal.revision, true);
    for name in [
        "BlogTranslationDefinition.php",
        "BlogTranslationEntity.php",
        "BlogTranslationCollection.php",
    ] {
        assert!(!translation_directory.join(name).exists(), "{name}");
    }
}

fn example_composer(root: &Path) {
    write(
        root.join("composer.json"),
        r#"{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}"#,
    );
}

#[test]
fn bootstrap_uses_composer_source_root_for_plugin_root() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let source_root = root.join("src");
    fs::create_dir_all(&source_root).unwrap();
    example_composer(root);
    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, root);
    assert_eq!(boot.spec.directory_uri, uri(&source_root));
    assert_eq!(boot.spec.namespace, "Acme\\Example");
    assert_eq!(boot.spec.class_name, "Example");
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: boot.spec,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    for file in &response.files {
        let path = swlsp_uriutil::path(&file.uri).unwrap();
        if path.ends_with("Definition.php")
            || path.ends_with("Entity.php")
            || path.ends_with("Collection.php")
        {
            assert!(
                safe_plugin_target(&path_string(&source_root), &path),
                "{path}"
            );
        }
    }
}

#[test]
fn preview_rejects_directory_outside_composer_source_root() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    fs::create_dir_all(root.join("src")).unwrap();
    example_composer(root);
    let spec = es::complete_spec(EntitySpec {
        create_migration: false,
        fields: vec![field("id", es::FIELD_ID)],
        ..new_spec(root, root, "Acme\\Example", "Example", "acme_example")
    });
    let request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let error = Provider::new(&path_string(root), None, None, None)
        .entity_schema_preview(&ctx(), Some(&raw(&request)))
        .unwrap_err();
    assert!(
        error
            .to_string()
            .contains("outside the Composer PSR-4 source root"),
        "{error}"
    );
}

#[test]
fn revision_carries_generated_timestamp() {
    let hash: [u8; 32] = Sha256::digest(b"entity preview").into();
    let revision = entity_schema_revision(1_700_000_000, &hash);
    assert_eq!(
        entity_schema_revision_timestamp(&revision),
        Some(1_700_000_000)
    );
    assert_eq!(entity_schema_revision_timestamp("stale"), None);
}

#[test]
fn drift_returns_concrete_diff_and_recovery_choices() {
    let spec = es::complete_spec(EntitySpec {
        mode: "new".to_owned(),
        namespace: "Acme\\Example".to_owned(),
        class_name: "Example".to_owned(),
        entity_name: "acme_example".to_owned(),
        fields: vec![field("id", es::FIELD_ID)],
        ..EntitySpec::default()
    });
    let entity = es::schema_from_spec(&spec).unwrap();
    let mut committed = es::empty_schema();
    committed.entities.insert(entity.name.clone(), entity);
    let mut response = EntitySchemaPreviewResponse::default();
    let leaf = es::Snapshot {
        id: "leaf".to_owned(),
        schema: committed,
        ..es::Snapshot::default()
    };
    let history = reconcile_entity_schema_drift(
        &es::PluginContext::default(),
        &leaf,
        &spec,
        "",
        &mut response,
        History {
            scanned: es::empty_schema(),
            ..History::default()
        },
    )
    .unwrap();
    assert!(history.stop);
    assert!(response.drift);
    assert_eq!(response.diff.removed_entities.len(), 1);
    assert!(response.drift_message.contains("driftDecision=adopt"));
    assert!(response.drift_message.contains("driftDecision=migrate"));
}

#[test]
fn production_round_trip() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/CatalogItem");
    fs::create_dir_all(&directory).unwrap();
    write(
        root.join("composer.json"),
        r#"{
  "name":"acme/catalog","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Catalog\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Catalog\\AcmeCatalog"}
}"#,
    );
    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, &directory);
    let spec = es::complete_spec(EntitySpec {
        plugin_root_uri: boot.plugin.root_uri.clone(),
        fields: vec![
            field("id", es::FIELD_ID),
            FieldSpec {
                required: true,
                max_length: 500,
                api_aware: true,
                ..column("name", es::FIELD_STRING, "name", "name")
            },
            column("position", es::FIELD_INT, "position", "position"),
            column("config", es::FIELD_JSON, "config", "config"),
        ],
        indexes: vec![unique("uniq.acme_catalog_item.name", &["name"])],
        ..new_spec(
            root,
            &directory,
            "Acme\\Catalog\\Entity\\CatalogItem",
            "CatalogItem",
            "acme_catalog_item",
        )
    });
    let mut request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let created = preview(&provider, &request);
    assert!(created.issues.is_empty(), "{:?}", created.issues);
    request.spec.migration_timestamp = created.migration_timestamp;
    apply(&provider, &request, &created.revision, false);

    let definition_path = directory.join("CatalogItemDefinition.php");
    let entity_path = directory.join("CatalogItemEntity.php");
    let entity_source = fs::read_to_string(&entity_path).unwrap();
    let entity_source = format!(
        "{}    public function customLabel(): string {{ return 'custom'; }}\n}}\n",
        entity_source.strip_suffix("}\n").unwrap_or(&entity_source)
    );
    fs::write(&entity_path, entity_source).unwrap();

    let mut loaded = load_path(&provider, &definition_path);
    assert_eq!(loaded.mode, "edit");
    assert!(!loaded.base_snapshot_ids.is_empty());
    assert_eq!(
        loaded.indexes,
        vec![unique("uniq.acme_catalog_item.name", &["name"])]
    );
    for field in &mut loaded.fields {
        if field.id == "field-2" || field.property_name == "name" {
            field.property_name = "displayName".to_owned();
            field.storage_name = "display_name".to_owned();
        }
    }
    loaded.indexes[0].name = "uniq.acme_catalog_item.display_name".to_owned();
    loaded.indexes[0].columns = vec!["display_name".to_owned()];
    let mut edit_request = EntitySchemaPreviewRequest {
        spec: loaded,
        ..EntitySchemaPreviewRequest::default()
    };
    let rename = preview(&provider, &edit_request);
    assert!(issue_codes(&rename.issues).contains(&"entity.column.rename.decision".to_owned()));
    assert_eq!(rename.diff.rename_questions.len(), 1);
    edit_request.spec.migration_timestamp = rename.migration_timestamp;
    edit_request.decisions = vec![es::Decision {
        kind: "columnRename".to_owned(),
        entity: "acme_catalog_item".to_owned(),
        from: "name".to_owned(),
        to: "display_name".to_owned(),
        ..es::Decision::default()
    }];
    let edit = preview(&provider, &edit_request);
    assert!(edit.issues.is_empty(), "{:?}", edit.issues);
    assert!(edit.destructive);
    apply(&provider, &edit_request, &edit.revision, true);

    let definition_source = fs::read_to_string(&definition_path).unwrap();
    let entity_source = fs::read_to_string(&entity_path).unwrap();
    assert!(definition_source.contains("'display_name', 'displayName', 500"));
    assert!(!definition_source.contains("'name', 'name'"));
    assert!(entity_source.contains("function customLabel()"));
    assert!(entity_source.contains("$displayName"));
    assert!(!entity_source.contains("$name"));
    assert!(
        swlsp_parser_php::parse(&definition_source)
            .errors
            .is_empty()
    );
    assert!(swlsp_parser_php::parse(&entity_source).errors.is_empty());
    assert_eq!(es::read_snapshots(&path_string(root)).unwrap().len(), 3);
    let migrations = fs::read_dir(root.join("src/Migration"))
        .unwrap()
        .filter(|entry| {
            entry
                .as_ref()
                .unwrap()
                .file_name()
                .to_string_lossy()
                .ends_with(".php")
        })
        .count();
    assert_eq!(migrations, 2);
}

#[test]
fn apply_rejects_stale_preview() {
    let dir = TempDir::new().unwrap();
    let provider = Provider::new(&path_string(dir.path()), None, None, None);
    assert!(
        try_apply(
            &provider,
            &EntitySchemaPreviewRequest::default(),
            "stale",
            false
        )
        .is_err()
    );
}

#[test]
fn load_uses_open_definition_document() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/Example");
    fs::create_dir_all(&directory).unwrap();
    example_composer(root);
    let disk_spec = es::complete_spec(EntitySpec {
        mode: "new".to_owned(),
        namespace: "Acme\\Example\\Entity\\Example".to_owned(),
        class_name: "Example".to_owned(),
        entity_name: "acme_example".to_owned(),
        fields: vec![
            field("id", es::FIELD_ID),
            column("name", es::FIELD_STRING, "name", "name"),
        ],
        ..EntitySpec::default()
    });
    let definition_path = directory.join("ExampleDefinition.php");
    write(
        &definition_path,
        &es::render_definition(&disk_spec).unwrap(),
    );
    let mut overlay_spec = disk_spec.clone();
    overlay_spec.fields[1].property_name = "displayName".to_owned();
    overlay_spec.fields[1].storage_name = "display_name".to_owned();
    let overlay = es::render_definition(&overlay_spec).unwrap();
    let definition_uri = uri(&definition_path);
    let request = EntitySchemaLoadRequest {
        file_uri: definition_uri.clone(),
        documents: BTreeMap::from([(
            definition_uri,
            EntitySchemaDocument {
                text: overlay,
                version: Some(7),
            },
        )]),
        ..EntitySchemaLoadRequest::default()
    };
    let loaded = load(
        &Provider::new(&path_string(root), None, None, None),
        &request,
    );
    assert_eq!(loaded.fields[1].property_name, "displayName");
    assert_eq!(loaded.fields[1].storage_name, "display_name");
}

fn seal(snapshot: es::Snapshot) -> (es::Snapshot, Vec<u8>) {
    let sealed = snapshot.seal().unwrap();
    let content = es::marshal_snapshot(&sealed).unwrap();
    (sealed, content)
}

#[test]
fn preview_rejects_changed_snapshot_head() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/Example");
    fs::create_dir_all(&directory).unwrap();
    example_composer(root);
    let snapshot_directory = root.join(es::SNAPSHOT_RELATIVE_DIRECTORY);
    fs::create_dir_all(&snapshot_directory).unwrap();
    let (baseline, content) = seal(es::Snapshot {
        kind: es::SNAPSHOT_BASELINE.to_owned(),
        schema: es::empty_schema(),
        ..es::Snapshot::default()
    });
    fs::write(snapshot_directory.join("baseline.snapshot.json"), content).unwrap();

    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, &directory);
    assert_eq!(boot.spec.base_snapshot_ids, vec![baseline.id.clone()]);

    let (_, content) = seal(es::Snapshot {
        parents: vec![baseline.id],
        kind: es::SNAPSHOT_MIGRATION.to_owned(),
        schema: es::empty_schema(),
        ..es::Snapshot::default()
    });
    fs::write(snapshot_directory.join("new-head.snapshot.json"), content).unwrap();
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: boot.spec,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(issue_codes(&response.issues).contains(&"entity.snapshot.stale".to_owned()));
    assert!(response.revision.is_empty());
}

#[test]
fn restore_snapshot_only_indexes_does_not_restore_obsolete_relation_index() {
    let index = |name: &str, unique: bool, column: &str| es::Index {
        name: name.to_owned(),
        unique,
        columns: vec![column.to_owned()],
    };
    let foreign_key = |name: &str, column: &str| es::ForeignKey {
        name: name.to_owned(),
        column: column.to_owned(),
        columns: vec![column.to_owned()],
        ..es::ForeignKey::default()
    };
    let mut snapshot = es::empty_schema();
    snapshot.entities.insert(
        "example".to_owned(),
        es::Entity {
            name: "example".to_owned(),
            indexes: BTreeMap::from([
                (
                    "idx.example.old_id".to_owned(),
                    index("idx.example.old_id", false, "old_id"),
                ),
                (
                    "uniq.example.name".to_owned(),
                    index("uniq.example.name", true, "name"),
                ),
            ]),
            foreign_keys: BTreeMap::from([(
                "fk.example.old_id".to_owned(),
                foreign_key("fk.example.old_id", "old_id"),
            )]),
            ..es::Entity::default()
        },
    );
    let mut scanned = es::empty_schema();
    scanned.entities.insert(
        "example".to_owned(),
        es::Entity {
            name: "example".to_owned(),
            indexes: BTreeMap::from([(
                "idx.example.new_id".to_owned(),
                index("idx.example.new_id", false, "new_id"),
            )]),
            foreign_keys: BTreeMap::from([(
                "fk.example.new_id".to_owned(),
                foreign_key("fk.example.new_id", "new_id"),
            )]),
            ..es::Entity::default()
        },
    );
    let restored = es::restore_snapshot_only_indexes(&scanned, &snapshot);
    let indexes = &restored.entities["example"].indexes;
    assert!(indexes.contains_key("uniq.example.name"));
    assert!(indexes.contains_key("idx.example.new_id"));
    assert!(!indexes.contains_key("idx.example.old_id"));
}

#[test]
fn namespace_move_does_not_cause_drift() {
    let before_spec = es::complete_spec(EntitySpec {
        mode: "edit".to_owned(),
        namespace: "Acme\\Old\\Content\\Example".to_owned(),
        class_name: "Example".to_owned(),
        entity_name: "acme_example".to_owned(),
        fields: vec![
            field("id", es::FIELD_ID),
            column("name", es::FIELD_STRING, "name", "name"),
        ],
        ..EntitySpec::default()
    });
    let mut after_spec = before_spec.clone();
    after_spec.namespace = "Acme\\New\\Domain\\Example".to_owned();
    after_spec.definition_class.clear();
    after_spec.entity_class.clear();
    after_spec.collection_class.clear();
    let after_spec = es::complete_spec(after_spec);
    let before_entity = es::schema_from_spec(&before_spec).unwrap();
    let after_entity = es::schema_from_spec(&after_spec).unwrap();
    let mut before = es::empty_schema();
    before
        .entities
        .insert(before_entity.name.clone(), before_entity);
    let mut after = es::empty_schema();
    after
        .entities
        .insert(after_entity.name.clone(), after_entity);
    let leaf = es::Snapshot {
        kind: es::SNAPSHOT_BASELINE.to_owned(),
        schema: before.clone(),
        ..es::Snapshot::default()
    }
    .seal()
    .unwrap();
    let mut response = EntitySchemaPreviewResponse::default();
    let history = reconcile_entity_schema_drift(
        &es::PluginContext::default(),
        &leaf,
        &after_spec,
        "",
        &mut response,
        History {
            scanned: after,
            parents: vec![leaf.id.clone()],
            ..History::default()
        },
    )
    .unwrap();
    assert!(!response.drift);
    assert!(!history.stop);
    assert_eq!(history.previous, before);
}

#[test]
fn edit_preserves_custom_code_and_confirms_destructive_ddl() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Entity/Example");
    fs::create_dir_all(&directory).unwrap();
    example_composer(root);
    let mut spec = es::complete_spec(EntitySpec {
        mode: "edit".to_owned(),
        migration_timestamp: 1_700_000_001,
        fields: vec![
            field("id", es::FIELD_ID),
            FieldSpec {
                required: true,
                ..column("name", es::FIELD_STRING, "name", "name")
            },
        ],
        ..new_spec(
            root,
            &directory,
            "Acme\\Example\\Entity\\Example",
            "Example",
            "acme_example",
        )
    });
    let preserve = |source: String| {
        format!(
            "{}\n    public function custom(): string {{ return 'preserved'; }}\n}}\n",
            &source[..source.len() - 2]
        )
    };
    let definition = preserve(es::render_definition(&spec).unwrap());
    let entity = preserve(es::render_entity(&spec).unwrap());
    let collection = es::render_collection(&spec).unwrap();
    write(directory.join("ExampleDefinition.php"), &definition);
    write(directory.join("ExampleEntity.php"), &entity);
    write(directory.join("ExampleCollection.php"), &collection);
    let services_path = root.join("src/Resources/config/services.xml");
    let services =
        es::patch_service_configuration(&path_string(&services_path), "", &spec.definition_class)
            .unwrap();
    write(&services_path, &services);
    let current_entity = es::schema_from_spec(&spec).unwrap();
    let mut current_schema = es::empty_schema();
    current_schema
        .entities
        .insert(spec.entity_name.clone(), current_entity);
    let (_, content) = seal(es::Snapshot {
        kind: es::SNAPSHOT_BASELINE.to_owned(),
        schema: current_schema,
        ..es::Snapshot::default()
    });
    write(
        root.join(es::SNAPSHOT_RELATIVE_DIRECTORY)
            .join("baseline.snapshot.json"),
        &String::from_utf8(content).unwrap(),
    );

    spec.fields.truncate(1);
    spec.service_uri = uri(&services_path);
    let provider = Provider::new(&path_string(root), None, None, None);
    let request = EntitySchemaPreviewRequest {
        spec,
        ..EntitySchemaPreviewRequest::default()
    };
    let response = preview(&provider, &request);
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    assert!(response.destructive);
    let entity_uri = uri(directory.join("ExampleEntity.php"));
    let entity_file = response
        .files
        .iter()
        .find(|file| file.uri == entity_uri)
        .expect("entity preserved");
    assert!(entity_file.after.contains("function custom"));
    assert!(!entity_file.after.contains("$name"));
    let error = try_apply(&provider, &request, &response.revision, false).unwrap_err();
    assert!(error.to_string().contains("destructive"), "{error}");
}

#[test]
fn safe_plugin_target_resolves_existing_symlink_ancestors() {
    let plugin = TempDir::new().unwrap();
    let outside = TempDir::new().unwrap();
    fs::create_dir_all(plugin.path().join("src")).unwrap();
    std::os::unix::fs::symlink(outside.path(), plugin.path().join("src/Migration")).unwrap();
    let plugin_path = path_string(plugin.path());
    assert!(!safe_plugin_target(
        &plugin_path,
        &path_string(plugin.path().join("src/Migration/Nested/Migration1.php"))
    ));
    assert!(safe_plugin_target(
        &plugin_path,
        &path_string(plugin.path().join("src/Entity/Example.php"))
    ));
}

#[test]
fn field_catalog_includes_specialized_creation_templates() {
    let types = entity_schema_field_types("", None);
    let custom_fields = types
        .iter()
        .find(|field_type| field_type.id == "specialized:CustomFields")
        .unwrap();
    assert_eq!(
        custom_fields.template.as_ref().unwrap().storage_name,
        "custom_fields"
    );
    let state_machine = types
        .iter()
        .find(|field_type| field_type.id == "specialized:StateMachineStateField")
        .unwrap();
    assert_eq!(
        state_machine
            .template
            .as_ref()
            .unwrap()
            .implementation
            .as_ref()
            .unwrap()
            .minimum_additional_arguments,
        1
    );
}

#[test]
fn enum_validation_uses_php_enum_semantics() {
    let cache = TempDir::new().unwrap();
    let php = php_index(cache.path());
    let sources = TempDir::new().unwrap();
    index_source(
        &php,
        sources.path().join("Status.php"),
        "<?php\nnamespace Acme\\Example;\nenum Status: int { case Active = 1; }\n",
    );
    let workspace = TempDir::new().unwrap();
    let provider = Provider::new(&path_string(workspace.path()), Some(php), None, None);
    let mut spec = es::complete_spec(EntitySpec {
        mode: "new".to_owned(),
        namespace: "Acme\\Example".to_owned(),
        class_name: "Example".to_owned(),
        entity_name: "acme_example".to_owned(),
        shopware_version: "6.7.0".to_owned(),
        fields: vec![
            field("id", es::FIELD_ID),
            FieldSpec {
                enum_class: "Acme\\Example\\Status".to_owned(),
                enum_case: "Active".to_owned(),
                ..column("status", es::FIELD_ENUM, "status", "status")
            },
        ],
        ..EntitySpec::default()
    });
    provider.entities.as_ref().unwrap().enrich_spec(&mut spec);
    assert_eq!(spec.fields[1].enum_backing_type, "int");
    assert!(validate_entity_schema_spec(&provider, &spec).is_empty());

    spec.fields[1].enum_case = "Missing".to_owned();
    assert!(
        validate_entity_schema_spec(&provider, &spec).contains(&es::ValidationIssue {
            code: "entity.field.enum.case.unavailable".to_owned(),
            message: "Enum case Acme\\Example\\Status::Missing was not found".to_owned(),
            field_id: "status".to_owned(),
            severity: "error".to_owned(),
        })
    );
    spec.fields[1].enum_case = "Active".to_owned();
    spec.fields[1].enum_backing_type = "string".to_owned();
    assert!(
        validate_entity_schema_spec(&provider, &spec).contains(&es::ValidationIssue {
            code: "entity.field.enum.backing.mismatch".to_owned(),
            message: "Enum Acme\\Example\\Status is int-backed, not string-backed".to_owned(),
            field_id: "status".to_owned(),
            severity: "error".to_owned(),
        })
    );
}

#[test]
fn field_catalog_filters_target_version_and_installed_classes() {
    let classes = [
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CustomFields",
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\EnumField",
        "Shopware\\Core\\Content\\MeasurementSystem\\Field\\MeasurementUnitsField",
    ];
    let available = |class: &str| classes.contains(&class);
    let types = entity_schema_field_types("6.7.0", Some(&available));
    let ids: Vec<&str> = types
        .iter()
        .map(|field_type| field_type.id.as_str())
        .collect();
    assert!(ids.contains(&"specialized:CustomFields"));
    assert!(types.iter().any(|field_type| field_type.kind == "enum"));
    assert!(
        !ids.contains(&"specialized:MeasurementUnitsField"),
        "field was introduced in 6.7.1"
    );
    assert!(
        !ids.contains(&"specialized:PriceField"),
        "installed-class filter must be honored"
    );
    let types = entity_schema_field_types("6.7.1", Some(&available));
    assert!(
        types
            .iter()
            .any(|field_type| field_type.id == "specialized:MeasurementUnitsField")
    );
    let types = entity_schema_field_types("6.6.9", Some(&available));
    assert!(types.iter().all(|field_type| field_type.kind != "enum"));
}

#[test]
fn bootstrap_hides_bulk_extension_before_shopware_6610() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    fs::create_dir_all(root.join("src/Entity")).unwrap();
    write(
        root.join("composer.json"),
        r#"{
  "name": "acme/legacy",
  "type": "shopware-platform-plugin",
  "require": {"shopware/core": "~6.6.9"},
  "autoload": {"psr-4": {"Acme\\Legacy\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Legacy\\AcmeLegacy"}
}"#,
    );
    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, &root.join("src/Entity"));
    let bulk = es::DEFINITION_BULK_EXTENSION.to_owned();
    assert!(!boot.definition_kinds.contains(&bulk));
    for field_type in &boot.field_types {
        assert!(!field_type.definition_kinds.contains(&bulk));
    }
}

#[test]
fn loads_and_rewrites_definition_using_custom_abstract_base() {
    let dir = TempDir::new().unwrap();
    let root = dir.path();
    let directory = root.join("src/Catalog");
    fs::create_dir_all(&directory).unwrap();
    composer(root, "acme/custom-base", "Acme\\CustomBase", Some("~6.7"));
    write(
        root.join("src/AbstractRecord.php"),
        r"<?php declare(strict_types=1);
namespace Acme\CustomBase;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
abstract class AbstractRecord extends EntityDefinition {}
",
    );
    write(
        directory.join("CatalogModel.php"),
        r"<?php declare(strict_types=1);
namespace Acme\CustomBase\Catalog;
use Acme\CustomBase\AbstractRecord;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogModel extends AbstractRecord {
    public const ENTITY_NAME = 'acme_catalog';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}
",
    );
    let provider = Provider::new(&path_string(root), None, None, None);
    let boot = bootstrap(&provider, &directory);
    let target = boot
        .editable
        .iter()
        .find(|target| target.definition_class == "Acme\\CustomBase\\Catalog\\CatalogModel")
        .expect("editable custom-base definition")
        .clone();
    assert_eq!(target.definition_kind, es::DEFINITION_ENTITY);

    let mut loaded = load(
        &provider,
        &EntitySchemaLoadRequest {
            file_uri: target.file_uri.clone(),
            definition_class: target.definition_class.clone(),
            definition_kind: target.definition_kind.clone(),
            ..EntitySchemaLoadRequest::default()
        },
    );
    assert_eq!(loaded.entity_name, "acme_catalog");
    loaded
        .fields
        .push(column("label", es::FIELD_STRING, "label", "label"));
    let response = preview(
        &provider,
        &EntitySchemaPreviewRequest {
            spec: loaded,
            ..EntitySchemaPreviewRequest::default()
        },
    );
    assert!(response.issues.is_empty(), "{:?}", response.issues);
    let file = response
        .files
        .iter()
        .find(|file| file.uri == target.file_uri)
        .expect("rewritten custom-base definition was not previewed");
    assert!(file.after.contains("extends AbstractRecord"));
    assert!(file.after.contains("new StringField('label', 'label')"));
}

#[test]
fn search_resolves_indexed_custom_dal_base_semantically() {
    let dal_cache = TempDir::new().unwrap();
    let dal = dal_index(dal_cache.path());
    let php_cache = TempDir::new().unwrap();
    let php = php_index(php_cache.path());
    for (path, source) in [
        (
            "/project/vendor/shopware/EntityDefinition.php",
            "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityDefinition {}\n",
        ),
        (
            "/project/src/AbstractRecord.php",
            "<?php\nnamespace Acme;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;\nabstract class AbstractRecord extends EntityDefinition {}\n",
        ),
        (
            "/project/src/CatalogModel.php",
            "<?php\nnamespace Acme;\nclass CatalogModel extends AbstractRecord {\n    public const ENTITY_NAME = 'acme_catalog';\n    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }\n}",
        ),
    ] {
        index_source(&php, path, source);
        index_source(&*dal, path, source);
    }
    let provider = Provider::new("/project", Some(php), None, Some(dal));
    let results = provider.relation_targets("acme_catalog").unwrap();
    assert_eq!(results.len(), 1);
    assert_eq!(results[0].definition_kind, es::DEFINITION_ENTITY);
    assert_eq!(results[0].definition_class, "Acme\\CatalogModel");
    assert!(results[0].fields.contains(&es::RelationTargetField {
        property_name: "id".to_owned(),
        storage_name: "id".to_owned(),
        primary: true,
    }));
}
