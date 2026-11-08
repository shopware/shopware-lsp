//! Entity designer commands (port of Go `entity_schema.go`): bootstrap,
//! relation search, load, preview, apply and snapshot reconciliation.

use std::collections::{BTreeMap, HashMap, HashSet};

use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use swlsp_lsp::Context;
use swlsp_lsp_protocol as protocol;
use swlsp_php_semantic::SymbolKind;
use swlsp_rewrite::{CreateFilePlan, DeleteFilePlan, DocumentPlan, Edit, WorkspacePlan};
use swlsp_shopware_entityschema::{self as es, EntitySpec, FieldSpec, RelationTarget};
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::file_uri;

use crate::paths;
use crate::{Provider, Result, errorf, pascal_name, snake_case};

pub const ENTITY_SCHEMA_BOOTSTRAP_COMMAND: &str = "shopware/entity-schema/bootstrap";
pub const ENTITY_SCHEMA_SEARCH_COMMAND: &str = "shopware/entity-schema/search";
pub const ENTITY_SCHEMA_LOAD_COMMAND: &str = "shopware/entity-schema/load";
pub const ENTITY_SCHEMA_PREVIEW_COMMAND: &str = "shopware/entity-schema/preview";
pub const ENTITY_SCHEMA_APPLY_COMMAND: &str = "shopware/entity-schema/apply";
pub const ENTITY_SCHEMA_RECONCILE_COMMAND: &str = "shopware/entity-schema/reconcile";

fn is_false(value: &bool) -> bool {
    !*value
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct EntitySchemaDocument {
    pub text: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<i32>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct EntitySchemaBootstrapRequest {
    #[serde(rename = "directoryUri")]
    pub directory_uri: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaGraph {
    pub snapshot_count: usize,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub leaves: Vec<String>,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub missing: BTreeMap<String, Vec<String>>,
    pub needs_reconciliation: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaFieldType {
    pub kind: String,
    pub label: String,
    pub stored: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub definition_kinds: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub requires_default_fields_override: bool,
    #[serde(rename = "id", skip_serializing_if = "String::is_empty")]
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub template: Option<FieldSpec>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaBootstrapResponse {
    pub plugin: es::PluginContext,
    pub spec: EntitySpec,
    pub definition_kinds: Vec<String>,
    pub field_types: Vec<EntitySchemaFieldType>,
    pub graph: EntitySchemaGraph,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub existing: Vec<EntitySchemaRelationTarget>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub editable: Vec<EntitySchemaEditableTarget>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaEditableTarget {
    pub entity_name: String,
    pub definition_class: String,
    pub definition_kind: String,
    #[serde(rename = "fileUri")]
    pub file_uri: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaRelationTarget {
    pub entity_name: String,
    pub definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub collection_class: String,
    #[serde(rename = "fileUri", skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub fields: Vec<es::RelationTargetField>,
    #[serde(skip_serializing_if = "is_false")]
    pub version_aware: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub inheritance_aware: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct EntitySchemaSearchRequest {
    pub query: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub limit: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaLoadRequest {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_name: String,
    #[serde(rename = "fileUri", skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub documents: BTreeMap<String, EntitySchemaDocument>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaPreviewRequest {
    pub spec: EntitySpec,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub decisions: Vec<es::Decision>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub drift_decision: String,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub documents: BTreeMap<String, EntitySchemaDocument>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct EntitySchemaFilePreview {
    pub uri: String,
    pub action: String,
    pub language: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub before: String,
    pub after: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaPreviewResponse {
    pub revision: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub files: Vec<EntitySchemaFilePreview>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub issues: Vec<es::ValidationIssue>,
    pub diff: es::SchemaDiff,
    pub destructive: bool,
    pub drift: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub drift_message: String,
    #[serde(rename = "snapshotId", skip_serializing_if = "String::is_empty")]
    pub snapshot_id: String,
    #[serde(rename = "primaryFileUri", skip_serializing_if = "String::is_empty")]
    pub primary_file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub migration_timestamp: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaApplyRequest {
    #[serde(flatten)]
    pub preview: EntitySchemaPreviewRequest,
    pub revision: String,
    pub allow_destructive: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct EntitySchemaApplyResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    #[serde(rename = "primaryFileUri")]
    pub primary_file_uri: String,
    #[serde(rename = "snapshotId")]
    pub snapshot_id: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySchemaReconcileRequest {
    #[serde(rename = "directoryUri")]
    pub directory_uri: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub selected_leaf: String,
}

struct Prepared {
    response: EntitySchemaPreviewResponse,
    files: Vec<PreparedFile>,
}

impl Prepared {
    fn response_only(response: EntitySchemaPreviewResponse) -> Self {
        Prepared {
            response,
            files: Vec::new(),
        }
    }
}

#[derive(Clone, Debug, Default)]
pub(crate) struct PreparedFile {
    path: String,
    before: String,
    after: String,
    version: Option<i32>,
    exists: bool,
    delete: bool,
}

#[derive(Clone, Debug, Default)]
struct Source {
    path: String,
    content: String,
    version: Option<i32>,
    exists: bool,
}

#[derive(Clone, Debug, Default)]
struct Sources {
    definition: Source,
    entity: Source,
    collection: Source,
    translation_definition: Source,
    translation_entity: Source,
    translation_collection: Source,
}

#[derive(Clone, Debug, Default)]
pub(crate) struct History {
    pub(crate) scanned: es::Schema,
    pub(crate) previous: es::Schema,
    pub(crate) parents: Vec<String>,
    pub(crate) pending: Vec<PreparedFile>,
    pub(crate) stop: bool,
}

/// Relation targets by definition class and technical entity name.
pub(crate) struct Lookup(HashMap<String, RelationTarget>);

impl Lookup {
    fn get(&self, class: &str) -> Option<RelationTarget> {
        self.0.get(class).cloned()
    }
}

fn now_unix() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .unwrap_or_default()
}

fn decode<T: serde::de::DeserializeOwned + Default>(args: Option<&Value>) -> Result<T> {
    crate::decode_args(
        args,
        "missing entity schema request",
        "invalid entity schema request",
    )
}

fn uri_path(uri: &str) -> Result<String> {
    swlsp_uriutil::path(uri).map_err(|error| errorf!("{error}"))
}

impl Provider {
    pub fn entity_schema_bootstrap(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<EntitySchemaBootstrapResponse> {
        let request: EntitySchemaBootstrapRequest = decode(args)?;
        let (mut directory, mut plugin) = self.entity_plugin(&request.directory_uri)?;
        if !safe_plugin_target(&plugin.source_root, &directory) {
            directory = plugin.source_root.clone();
            plugin = es::find_plugin_context(&self.root, &directory)?;
        }
        ctx.check()?;
        plugin.root_uri = file_uri(&plugin.root);
        plugin.source_root_uri = file_uri(&plugin.source_root);
        for uri in &mut plugin.service_uris {
            *uri = file_uri(uri);
        }
        let snapshots = es::read_snapshots(&plugin.root)?;
        let graph = es::build_snapshot_graph(&snapshots)?;
        let leaves: Vec<String> = graph
            .leaves
            .iter()
            .map(|leaf| leaf.snapshot.id.clone())
            .collect();
        let mut name = pascal_name(&paths::base(&directory));
        if name.is_empty() || name == "Src" || name == "Entity" {
            name = "Example".to_owned();
        }
        let mut spec = es::complete_spec(EntitySpec {
            mode: "new".to_owned(),
            plugin_root_uri: plugin.root_uri.clone(),
            directory_uri: file_uri(&directory),
            namespace: plugin.namespace.clone(),
            entity_name: snake_case(&name),
            class_name: name,
            shopware_version: plugin.shopware_version.clone(),
            create_migration: true,
            fields: vec![FieldSpec {
                id: "id".to_owned(),
                kind: es::FIELD_ID.to_owned(),
                editable: true,
                ..FieldSpec::default()
            }],
            ..EntitySpec::default()
        });
        if let Some(first) = plugin.service_uris.first() {
            spec.service_uri = first.clone();
        }
        spec.base_snapshot_ids = leaves.clone();
        let mut existing: Vec<EntitySchemaRelationTarget> = self
            .relation_targets("")
            .unwrap_or_default()
            .into_iter()
            .filter(|target| {
                uri_path(&target.file_uri)
                    .ok()
                    .is_some_and(|path| paths::rel_within(&plugin.root, &path).is_some())
            })
            .collect();
        existing.truncate(100);
        let mut editable = Vec::new();
        if let Ok(lookup) = self.entity_relation_lookup()
            && let Ok((_, definitions)) = self.scan_entity_schema(&plugin.root, &lookup)
        {
            for definition in definitions {
                editable.push(EntitySchemaEditableTarget {
                    entity_name: definition.spec.entity_name,
                    definition_class: definition.spec.definition_class,
                    definition_kind: definition.spec.definition_kind,
                    file_uri: file_uri(&definition.path),
                });
            }
        }
        let available = |class: &str| self.specialized_field_class_available(class);
        Ok(EntitySchemaBootstrapResponse {
            graph: EntitySchemaGraph {
                snapshot_count: snapshots.len(),
                needs_reconciliation: graph.leaves.len() > 1 || !graph.missing.is_empty(),
                leaves,
                missing: graph.missing,
            },
            definition_kinds: es::definition_kinds_for_version(&plugin.shopware_version),
            field_types: entity_schema_field_types(&plugin.shopware_version, Some(&available)),
            plugin,
            spec,
            existing,
            editable,
        })
    }

    pub fn entity_schema_search(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<Option<Vec<EntitySchemaRelationTarget>>> {
        let request: EntitySchemaSearchRequest = decode(args)?;
        ctx.check()?;
        let mut results = self.relation_targets(&request.query)?;
        let mut limit = request.limit;
        if limit <= 0 || limit > 200 {
            limit = 50;
        }
        results.truncate(limit as usize);
        // Go returns a nil slice (JSON null) when nothing matches.
        Ok((!results.is_empty()).then_some(results))
    }

    pub(crate) fn relation_targets(&self, query: &str) -> Result<Vec<EntitySchemaRelationTarget>> {
        let Some(dal) = &self.dal else {
            return Ok(Vec::new());
        };
        let mut definitions = dal.definitions().map_err(|error| errorf!("{error}"))?;
        if let Some(php) = &self.php {
            let candidates = dal
                .unresolved_definitions()
                .map_err(|error| errorf!("{error}"))?;
            let snapshot = php.semantic_snapshot();
            for mut candidate in candidates {
                let class = candidate.fully_qualified_class.as_str();
                candidate.kind = if snapshot.is_subtype_of(
                    class,
                    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityTranslationDefinition",
                ) {
                    "translation"
                } else if snapshot.is_subtype_of(
                    class,
                    "Shopware\\Core\\Framework\\DataAbstractionLayer\\MappingEntityDefinition",
                ) {
                    "mapping"
                } else if snapshot.is_subtype_of(
                    class,
                    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition",
                ) {
                    "entity"
                } else {
                    continue;
                }
                .to_owned();
                definitions.push(candidate);
            }
        }
        let query = query.trim().to_lowercase();
        let mut results = Vec::new();
        for definition in definitions {
            let haystack = format!(
                "{} {} {}",
                definition.name, definition.fully_qualified_class, definition.class
            )
            .to_lowercase();
            if !query.is_empty() && !haystack.contains(&query) {
                continue;
            }
            let mut target = EntitySchemaRelationTarget {
                entity_name: definition.name,
                definition_class: definition.fully_qualified_class,
                definition_kind: definition.kind,
                entity_class: definition.entity_class,
                collection_class: definition.collection_class,
                file_uri: file_uri(&definition.file),
                fields: Vec::new(),
                version_aware: definition.version_aware,
                inheritance_aware: definition.inheritance_aware,
            };
            for field in definition.fields {
                if !field.stored {
                    continue;
                }
                target.fields.push(es::RelationTargetField {
                    property_name: field.name,
                    storage_name: field.storage_name,
                    primary: field.primary,
                });
            }
            if target.definition_kind == es::DEFINITION_ENTITY {
                append_missing_relation_target_fields(
                    &mut target.fields,
                    [("createdAt", "created_at"), ("updatedAt", "updated_at")],
                );
            }
            results.push(target);
        }
        // An entity name defined twice is ordered by definition class, then file.
        results.sort_unstable_by(|left, right| {
            (&left.entity_name, &left.definition_class, &left.file_uri).cmp(&(
                &right.entity_name,
                &right.definition_class,
                &right.file_uri,
            ))
        });
        Ok(results)
    }

    pub fn entity_schema_load(&self, ctx: &Context, args: Option<&Value>) -> Result<EntitySpec> {
        let request: EntitySchemaLoadRequest = decode(args)?;
        ctx.check()?;
        let mut path = String::new();
        if !request.file_uri.is_empty() {
            path = uri_path(&request.file_uri)?;
        }
        if path.is_empty()
            && let Some(dal) = &self.dal
        {
            for definition in dal.definitions().map_err(|error| errorf!("{error}"))? {
                if (!request.entity_name.is_empty() && definition.name == request.entity_name)
                    || (!request.definition_class.is_empty()
                        && same_php_class(
                            &definition.fully_qualified_class,
                            &request.definition_class,
                        ))
                {
                    path = definition.file;
                    break;
                }
            }
        }
        if path.is_empty() {
            return Err(errorf!("entity definition was not found in the index"));
        }
        let (content, _, exists) = source_for_path(&path, &request.documents)?;
        if !exists {
            return Err(errorf!("entity definition no longer exists"));
        }
        let mut spec =
            if !request.definition_kind.is_empty() && !request.definition_class.is_empty() {
                let lookup = self.entity_relation_lookup()?;
                es::import_class_based_definition(
                    &content,
                    &request.definition_class,
                    &request.definition_kind,
                    Some(&|class: &str| lookup.get(class)),
                )?
            } else {
                self.import_entity_definition(&content)?
            };
        let directory = paths::dir(&path);
        let plugin = es::find_plugin_context(&self.root, &directory)?;
        spec.mode = "edit".to_owned();
        spec.plugin_root_uri = file_uri(&plugin.root);
        spec.directory_uri = file_uri(&directory);
        spec.definition_uri = file_uri(&path);
        if spec.definition_kind == es::DEFINITION_ENTITY {
            spec.entity_uri = file_uri(&paths::join(&[
                &directory,
                &format!("{}.php", es::short_class(&spec.entity_class)),
            ]));
            spec.collection_uri = file_uri(&paths::join(&[
                &directory,
                &format!("{}.php", es::short_class(&spec.collection_class)),
            ]));
        } else {
            spec.entity_uri.clear();
            spec.collection_uri.clear();
        }
        spec.shopware_version = plugin.shopware_version.clone();
        if let Some(translation) = spec.translation.clone()
            && translation.enabled
        {
            let mut translation_path = String::new();
            if let Some(dal) = &self.dal {
                for definition in dal.definitions().map_err(|error| errorf!("{error}"))? {
                    if same_php_class(
                        &definition.fully_qualified_class,
                        &translation.definition_class,
                    ) {
                        translation_path = definition.file;
                        break;
                    }
                }
            }
            if translation_path.is_empty() {
                translation_path = paths::join(&[
                    &default_translation_directory(&directory, &spec),
                    &format!("{}.php", es::short_class(&translation.definition_class)),
                ]);
            }
            let (translation_source, _, translation_exists) =
                source_for_path(&translation_path, &request.documents)?;
            if !translation_exists {
                return Err(errorf!(
                    "translation definition {} was not found",
                    translation.definition_class
                ));
            }
            let lookup = self.entity_relation_lookup()?;
            let mut imported = es::import_translation_definition(
                &translation_source,
                Some(&|class: &str| lookup.get(class)),
            )?;
            if !same_php_class(
                &translation.definition_class,
                &imported.spec.definition_class,
            ) || !same_php_class(
                &spec.definition_class,
                &imported.spec.parent_definition_class,
            ) {
                return Err(errorf!(
                    "translation definition does not match its parent association"
                ));
            }
            let translation_directory = paths::dir(&translation_path);
            imported.spec.definition_uri = file_uri(&translation_path);
            imported.spec.entity_uri = file_uri(&paths::join(&[
                &translation_directory,
                &format!("{}.php", es::short_class(&imported.spec.entity_class)),
            ]));
            imported.spec.collection_uri = file_uri(&paths::join(&[
                &translation_directory,
                &format!("{}.php", es::short_class(&imported.spec.collection_class)),
            ]));
            spec = es::attach_translation(spec, imported);
        }
        if let Some(entities) = &self.entities {
            entities.enrich_spec(&mut spec);
        }
        if let Some(first) = plugin.service_uris.first() {
            spec.service_uri = file_uri(first);
        }
        let snapshots = es::read_snapshots(&plugin.root)?;
        let graph = es::build_snapshot_graph(&snapshots)?;
        if graph.leaves.len() == 1 {
            es::restore_spec_indexes_from_entities(&mut spec, &graph.leaves[0].snapshot.schema);
        }
        for leaf in &graph.leaves {
            spec.base_snapshot_ids.push(leaf.snapshot.id.clone());
        }
        Ok(spec)
    }

    fn import_entity_definition(&self, content: &str) -> Result<EntitySpec> {
        let lookup = self.entity_relation_lookup()?;
        let resolve = |class: &str| lookup.get(class);
        if let Ok(spec) = es::import_definition(content, Some(&resolve)) {
            return Ok(spec);
        }
        if let Ok(spec) = es::import_extension(content, Some(&resolve)) {
            return Ok(spec);
        }
        Ok(es::import_bulk_extension(content, Some(&resolve))?)
    }

    pub(crate) fn entity_relation_lookup(&self) -> Result<Lookup> {
        let targets = self.relation_targets("")?;
        let mut map = HashMap::with_capacity(targets.len());
        for target in targets {
            let relation = RelationTarget {
                definition_class: target.definition_class.clone(),
                definition_kind: target.definition_kind,
                entity_class: target.entity_class,
                collection_class: target.collection_class,
                entity_name: target.entity_name.clone(),
                file_uri: target.file_uri,
                fields: target.fields,
                version_aware: target.version_aware,
                inheritance_aware: target.inheritance_aware,
            };
            map.insert(target.definition_class, relation.clone());
            if !target.entity_name.is_empty() {
                map.insert(target.entity_name, relation);
            }
        }
        Ok(Lookup(map))
    }

    pub fn entity_schema_preview(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<EntitySchemaPreviewResponse> {
        let request: EntitySchemaPreviewRequest = decode(args)?;
        Ok(self.prepare_entity_schema(ctx, request)?.response)
    }

    pub fn entity_schema_apply(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<EntitySchemaApplyResponse> {
        let request: EntitySchemaApplyRequest = decode(args)?;
        let mut preview_request = request.preview;
        if preview_request.spec.migration_timestamp <= 0
            && let Some(timestamp) = entity_schema_revision_timestamp(&request.revision)
        {
            preview_request.spec.migration_timestamp = timestamp;
        }
        let prepared = self.prepare_entity_schema(ctx, preview_request)?;
        if request.revision.is_empty() || request.revision != prepared.response.revision {
            return Err(errorf!(
                "entity preview is stale; preview again before applying"
            ));
        }
        if !prepared.response.issues.is_empty() {
            return Err(errorf!(
                "entity schema has unresolved validation, drift, or rename questions"
            ));
        }
        if prepared.response.destructive && !request.allow_destructive {
            return Err(errorf!(
                "destructive entity schema changes require explicit confirmation"
            ));
        }
        let mut plan = WorkspacePlan::default();
        for file in prepared.files {
            let uri = file_uri(&file.path);
            if file.delete {
                plan.deletes.push(DeleteFilePlan {
                    uri,
                    version: file.version,
                    source: file.before,
                });
            } else if !file.exists {
                plan.creates.push(CreateFilePlan {
                    uri,
                    content: file.after,
                });
            } else {
                let range =
                    TextRange::new(TextSize::new(0), TextSize::new(file.before.len() as u32));
                plan.documents.push(DocumentPlan::new(
                    uri,
                    file.version,
                    file.before,
                    vec![Edit::new(range, file.after)],
                ));
            }
        }
        let edit = plan.workspace_edit()?;
        Ok(EntitySchemaApplyResponse {
            edit: Some(edit),
            primary_file_uri: prepared.response.primary_file_uri,
            snapshot_id: prepared.response.snapshot_id,
        })
    }

    fn prepare_entity_schema(
        &self,
        ctx: &Context,
        request: EntitySchemaPreviewRequest,
    ) -> Result<Prepared> {
        ctx.check()?;
        let mut spec = es::complete_spec(request.spec.clone());
        let generated_timestamp = spec.migration_timestamp <= 0;
        if generated_timestamp {
            spec.migration_timestamp = now_unix();
        }
        let mut response = EntitySchemaPreviewResponse {
            migration_timestamp: spec.migration_timestamp,
            ..EntitySchemaPreviewResponse::default()
        };
        let directory = swlsp_uriutil::path(&spec.directory_uri)
            .map_err(|error| errorf!("resolve entity directory: {error}"))?;
        let directory = self.validated_directory(&directory)?;
        let plugin = es::find_plugin_context(&self.root, &directory)?;
        if !safe_plugin_target(&plugin.source_root, &directory) {
            return Err(errorf!(
                "entity directory {directory} is outside the Composer PSR-4 source root {}",
                plugin.source_root
            ));
        }
        if !spec.plugin_root_uri.is_empty() && spec.plugin_root_uri != file_uri(&plugin.root) {
            return Err(errorf!(
                "entity plugin root changed since the designer was opened"
            ));
        }
        if spec.namespace.trim_matches('\\') != plugin.namespace.trim_matches('\\') {
            return Err(errorf!(
                "entity namespace {} does not match the Composer PSR-4 directory namespace {}",
                spec.namespace,
                plugin.namespace
            ));
        }
        spec.shopware_version = plugin.shopware_version.clone();
        if generated_timestamp {
            spec.migration_timestamp =
                available_migration_timestamp(&plugin.source_root, spec.migration_timestamp);
            response.migration_timestamp = spec.migration_timestamp;
        }
        let sources = entity_schema_sources_for(&directory, &spec, &request.documents)?;
        let relation_lookup = self.entity_relation_lookup()?;
        let resolve = |class: &str| relation_lookup.get(class);
        let mut previous_spec = import_previous_entity_spec(&spec, &sources, &resolve)?;
        if let Some(entities) = &self.entities {
            entities.enrich_spec(&mut spec);
            if let Some(previous) = previous_spec.as_mut() {
                entities.enrich_spec(previous);
            }
        }
        if let Some(previous) = &previous_spec
            && !previous.collect_method_raw.trim().is_empty()
            && (spec.definition_kind != es::DEFINITION_BULK_EXTENSION
                || spec.collect_method_raw != previous.collect_method_raw
                || !spec.bulk_extensions.is_empty()
                || !spec.fields.is_empty()
                || !spec.indexes.is_empty())
        {
            response.issues.push(entity_issue(
                "entity.bulkExtension.collectRaw.locked",
                "This custom BulkEntityExtension collect method is preserved losslessly and cannot be changed in the entity designer",
            ));
            return Ok(Prepared::response_only(response));
        }
        if let Some(previous) = &previous_spec
            && !preserved_definition_raw_methods_equal(previous, &spec)
        {
            response.issues.push(entity_issue(
                "entity.definition.raw.locked",
                "Custom definition behavior is preserved losslessly and cannot be changed in the entity designer",
            ));
            return Ok(Prepared::response_only(response));
        }
        spec = normalize_definition_transition(spec, previous_spec.as_ref());
        if spec.definition_kind == es::DEFINITION_EXTENSION {
            spec.extended_fields = Vec::new();
            match relation_lookup.get(&spec.extended_definition_class) {
                Some(target) if !target.entity_name.is_empty() => {
                    spec.extended_fields = target.fields.clone();
                    if spec.entity_name != target.entity_name {
                        response.issues.push(entity_issue(
                            "entity.extension.target.mismatch",
                            "Extension technical entity name does not match the selected indexed definition",
                        ));
                    }
                }
                _ => response.issues.push(entity_issue(
                    "entity.extension.target.missing",
                    "Select an indexed entity definition to extend",
                )),
            }
        }
        if spec.definition_kind == es::DEFINITION_BULK_EXTENSION {
            for target_spec in &mut spec.bulk_extensions {
                target_spec.extended_fields = Vec::new();
                if target_spec.extended_definition_class.is_empty() {
                    continue;
                }
                match relation_lookup.get(&target_spec.extended_definition_class) {
                    Some(target) if !target.entity_name.is_empty() => {
                        target_spec.extended_fields = target.fields.clone();
                        if target_spec.entity_name != target.entity_name {
                            let mut issue = entity_issue(
                                "entity.bulkExtension.target.mismatch",
                                "Bulk extension target entity name does not match its indexed definition",
                            );
                            issue.field_id = target_spec.id.clone();
                            response.issues.push(issue);
                        }
                    }
                    _ => {
                        let mut issue = entity_issue(
                            "entity.bulkExtension.target.missing",
                            "Select an indexed entity definition for every bulk extension target",
                        );
                        issue.field_id = target_spec.id.clone();
                        response.issues.push(issue);
                    }
                }
            }
        }
        response
            .issues
            .extend(validate_entity_schema_spec(self, &spec));
        let (mut scanned, scanned_specs) =
            self.scan_entity_schema(&plugin.root, &relation_lookup)?;
        merge_previous_entity_spec(&spec, previous_spec.as_ref(), &scanned_specs, &mut scanned)?;
        let history =
            prepare_entity_schema_history(&plugin, scanned, &spec, &request, &mut response)?;
        if history.stop {
            return Ok(Prepared::response_only(response));
        }
        let History {
            scanned,
            previous,
            parents,
            pending,
            ..
        } = history;
        if let Some(previous_spec) = previous_spec.as_mut() {
            es::restore_spec_indexes_from_entities(previous_spec, &previous);
        }
        let mut next = scanned.clone();
        if let Err(error) = es::replace_spec_schema(&mut next, previous_spec.as_ref(), &spec)
            && response.issues.is_empty()
        {
            return Err(error.into());
        }
        let (resolved_previous, resolved_diff, decision_result) =
            es::resolve_schema_diff(&previous, &next, &request.decisions);
        response.diff = resolved_diff;
        response.destructive = response.diff.destructive();
        if let Err(error) = decision_result {
            let message = error.to_string();
            let code = if message.contains("column") {
                "entity.column.rename.decision"
            } else {
                "entity.table.rename.decision"
            };
            response.issues.push(entity_issue(code, &message));
        }
        response.issues.extend(es::validate_migration(
            &resolved_previous,
            &next,
            &response.diff,
            &request.decisions,
        ));
        if !response.issues.is_empty() {
            return Ok(Prepared::response_only(response));
        }
        let (statements, _) = es::migration_statements(&previous, &next, &request.decisions);
        let statements = statements?;
        if response.diff.database_changed() && !spec.create_migration {
            response.issues.push(entity_issue(
                "entity.migration.required",
                "Database changes require a migration",
            ));
            return Ok(Prepared::response_only(response));
        }
        finalize_entity_schema_preview(
            spec,
            previous_spec.as_ref(),
            &sources,
            &plugin,
            &request,
            pending,
            &statements,
            parents,
            next,
            response,
        )
    }

    fn scan_entity_schema(
        &self,
        plugin_root: &str,
        lookup: &Lookup,
    ) -> Result<(es::Schema, Vec<es::ScannedDefinition>)> {
        let resolve = |class: &str| lookup.get(class);
        Ok(match &self.entities {
            Some(entities) => entities.scan(plugin_root, Some(&resolve))?,
            // Parser/importer unit tests intentionally construct a provider
            // without a workspace index. Production providers always use the
            // indexed catalog.
            None => es::scan_plugin_schema_with_lookup(plugin_root, Some(&resolve))?,
        })
    }

    pub fn entity_schema_reconcile(
        &self,
        _ctx: &Context,
        args: Option<&Value>,
    ) -> Result<EntitySchemaApplyResponse> {
        let request: EntitySchemaReconcileRequest = decode(args)?;
        let (_, plugin) = self.entity_plugin(&request.directory_uri)?;
        let files = es::read_snapshots(&plugin.root)?;
        let graph = es::build_snapshot_graph(&files)?;
        if graph.leaves.len() < 2 {
            return Err(errorf!("snapshot history does not require reconciliation"));
        }
        let mut selected = request.selected_leaf;
        if selected.is_empty() {
            let first = &graph.leaves[0].snapshot.schema;
            for leaf in &graph.leaves[1..] {
                if !same_entity_schema(first, &leaf.snapshot.schema) {
                    return Err(errorf!(
                        "snapshot leaves differ; select the leaf whose schema is authoritative"
                    ));
                }
            }
            selected = graph.leaves[0].snapshot.id.clone();
        }
        let Some(selected_file) = graph.files.get(&selected) else {
            return Err(errorf!("selected snapshot leaf was not found"));
        };
        if !graph.leaves.iter().any(|leaf| leaf.snapshot.id == selected) {
            return Err(errorf!("selected snapshot is not a graph leaf"));
        }
        let parents = graph
            .leaves
            .iter()
            .map(|leaf| leaf.snapshot.id.clone())
            .collect();
        let merged = es::Snapshot {
            parents,
            kind: es::SNAPSHOT_MERGE.to_owned(),
            plugin: es::PluginIdentity {
                composer_name: plugin.composer_name.clone(),
            },
            shopware_version: plugin.shopware_version.clone(),
            schema: selected_file.snapshot.schema.clone(),
            decisions: vec![es::Decision {
                kind: "branchReconcile".to_owned(),
                reason: format!("selected {selected}"),
                ..es::Decision::default()
            }],
            ..es::Snapshot::default()
        }
        .seal()?;
        let content = es::marshal_snapshot(&merged).unwrap_or_default();
        let path = paths::join(&[
            &plugin.snapshot_directory,
            &format!("{}-merge-{}.snapshot.json", now_unix(), &merged.id[..12]),
        ]);
        let plan = WorkspacePlan {
            creates: vec![CreateFilePlan {
                uri: file_uri(&path),
                content: String::from_utf8_lossy(&content).into_owned(),
            }],
            ..WorkspacePlan::default()
        };
        let edit = plan.workspace_edit()?;
        Ok(EntitySchemaApplyResponse {
            edit: Some(edit),
            primary_file_uri: file_uri(&path),
            snapshot_id: merged.id,
        })
    }

    fn entity_plugin(&self, directory_uri: &str) -> Result<(String, es::PluginContext)> {
        let directory = uri_path(directory_uri)?;
        let directory = self.validated_directory(&directory)?;
        let plugin = es::find_plugin_context(&self.root, &directory)?;
        Ok((directory, plugin))
    }

    pub(crate) fn specialized_field_class_available(&self, class_name: &str) -> bool {
        let Some(php) = &self.php else {
            return true;
        };
        if php
            .find_class("Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition")
            .is_none()
        {
            return true;
        }
        php.find_class(class_name).is_some()
    }
}

fn append_missing_relation_target_fields<const N: usize>(
    fields: &mut Vec<es::RelationTargetField>,
    additional: [(&str, &str); N],
) {
    let mut seen: HashSet<String> = fields
        .iter()
        .map(|field| field.storage_name.clone())
        .collect();
    for (property, storage) in additional {
        if !seen.insert(storage.to_owned()) {
            continue;
        }
        fields.push(es::RelationTargetField {
            property_name: property.to_owned(),
            storage_name: storage.to_owned(),
            primary: false,
        });
    }
}

#[allow(clippy::too_many_arguments)]
fn finalize_entity_schema_preview(
    spec: EntitySpec,
    previous_spec: Option<&EntitySpec>,
    sources: &Sources,
    plugin: &es::PluginContext,
    request: &EntitySchemaPreviewRequest,
    mut pending: Vec<PreparedFile>,
    statements: &[String],
    parents: Vec<String>,
    next: es::Schema,
    mut response: EntitySchemaPreviewResponse,
) -> Result<Prepared> {
    pending.extend(render_entity_schema_files(&spec, previous_spec, sources)?);
    let mut service_path = String::new();
    if !spec.service_uri.is_empty() {
        service_path = swlsp_uriutil::path(&spec.service_uri).unwrap_or_default();
    }
    if service_path.is_empty()
        && let Some(first) = plugin.service_uris.first()
    {
        service_path = first.clone();
    }
    if service_path.is_empty() {
        service_path = paths::join(&[&plugin.source_root, "Resources", "config", "services.yaml"]);
    }
    if !safe_plugin_target(&plugin.root, &service_path) {
        return Err(errorf!(
            "service configuration is outside plugin {}",
            plugin.root
        ));
    }
    let (service_source, service_version, service_exists) =
        source_for_path(&service_path, &request.documents)?;
    let service_tag = entity_schema_service_tag(&spec.definition_kind);
    let mut service_after = service_source.clone();
    if let Some(previous) = previous_spec {
        let previous_tag = entity_schema_service_tag(&previous.definition_kind);
        if previous_tag != service_tag
            || !same_php_class(&previous.definition_class, &spec.definition_class)
        {
            service_after = es::remove_service_configuration(
                &service_path,
                &service_after,
                &previous.definition_class,
            )?;
        }
    }
    service_after = es::patch_tagged_service_configuration(
        &service_path,
        &service_after,
        &spec.definition_class,
        service_tag,
    )?;
    let translation_enabled = spec
        .translation
        .as_ref()
        .is_some_and(|translation| translation.enabled);
    if let Some(previous_translation) =
        previous_spec.and_then(|previous| previous.translation.as_ref())
        && previous_translation.enabled
        && !translation_enabled
    {
        service_after = es::remove_service_configuration(
            &service_path,
            &service_after,
            &previous_translation.definition_class,
        )?;
    }
    if let Some(translation) = &spec.translation
        && translation.enabled
    {
        service_after = es::patch_service_configuration(
            &service_path,
            &service_after,
            &translation.definition_class,
        )?;
    }
    append_changed_file(
        &mut pending,
        &service_path,
        &service_source,
        &service_after,
        service_version,
        service_exists,
    );
    let timestamp = spec.migration_timestamp;
    let mut migrations = Vec::new();
    if !statements.is_empty() {
        let mut name = pascal_name(&spec.migration_name);
        if name.is_empty() {
            name = format!("Update{}", pascal_name(&spec.class_name));
        }
        let class_name = format!("Migration{timestamp}{name}");
        let migration_path = paths::join(&[
            &plugin.source_root,
            "Migration",
            &format!("{class_name}.php"),
        ]);
        let (_, _, migration_exists) = source_for_path(&migration_path, &request.documents)?;
        if migration_exists {
            return Err(errorf!("migration target already exists: {migration_path}"));
        }
        let migration =
            es::render_migration(&plugin.base_namespace, &class_name, timestamp, statements);
        append_changed_file(&mut pending, &migration_path, "", &migration, None, false);
        let relative = paths::rel(&plugin.root, &migration_path).unwrap_or_default();
        migrations.push(es::MigrationReference {
            path: paths::to_slash(&relative),
            timestamp,
            sha256: es::file_sha256(migration.as_bytes()),
        });
    }
    let snapshot = es::Snapshot {
        parents,
        kind: es::SNAPSHOT_MIGRATION.to_owned(),
        plugin: es::PluginIdentity {
            composer_name: plugin.composer_name.clone(),
        },
        shopware_version: plugin.shopware_version.clone(),
        migrations,
        schema: next,
        decisions: request.decisions.clone(),
        ..es::Snapshot::default()
    }
    .seal()?;
    let snapshot_content = es::marshal_snapshot(&snapshot)?;
    append_changed_file(
        &mut pending,
        &paths::join(&[
            &plugin.snapshot_directory,
            &format!("{timestamp}-{}.snapshot.json", &snapshot.id[..12]),
        ]),
        "",
        &String::from_utf8_lossy(&snapshot_content),
        None,
        false,
    );
    response.snapshot_id = snapshot.id;
    response.primary_file_uri = file_uri(&sources.definition.path);
    for file in &pending {
        if !safe_plugin_target(&plugin.root, &file.path) {
            return Err(errorf!(
                "entity output is outside plugin {}: {}",
                plugin.root,
                file.path
            ));
        }
        let action = if file.delete {
            "delete"
        } else if file.exists {
            "update"
        } else {
            "create"
        };
        response.files.push(EntitySchemaFilePreview {
            uri: file_uri(&file.path),
            action: action.to_owned(),
            language: language_for_path(&file.path).to_owned(),
            before: file.before.clone(),
            after: file.after.clone(),
        });
    }
    let mut revision_request = request.clone();
    revision_request.spec = spec;
    #[derive(Serialize)]
    struct RevisionContent<'a> {
        #[serde(rename = "Request")]
        request: &'a EntitySchemaPreviewRequest,
        #[serde(rename = "Files")]
        files: Option<&'a [EntitySchemaFilePreview]>,
    }
    let revision_content = es::gojson::to_vec(&RevisionContent {
        request: &revision_request,
        files: (!response.files.is_empty()).then_some(response.files.as_slice()),
    })
    .unwrap_or_default();
    let hash: [u8; 32] = Sha256::digest(&revision_content).into();
    response.revision = entity_schema_revision(revision_request.spec.migration_timestamp, &hash);
    Ok(Prepared {
        response,
        files: pending,
    })
}

fn preserved_definition_raw_methods_equal(previous: &EntitySpec, next: &EntitySpec) -> bool {
    fn behavior_raw(value: Option<&es::DefinitionBehaviorSpec>) -> Option<[&str; 6]> {
        value.map(|value| {
            [
                value.parent_definition_method_raw.as_str(),
                &value.version_aware_method_raw,
                &value.inheritance_aware_method_raw,
                &value.default_fields_method_raw,
                &value.base_fields_method_raw,
                &value.restrict_delete_meta_method_raw,
            ]
        })
    }
    fn metadata_raw(value: Option<&es::DefinitionMetadataSpec>) -> Option<[&str; 4]> {
        value.map(|value| {
            [
                value.since_method_raw.as_str(),
                &value.defaults_method_raw,
                &value.child_defaults_method_raw,
                &value.hydrator_method_raw,
            ]
        })
    }
    if behavior_raw(previous.definition_behavior.as_ref())
        != behavior_raw(next.definition_behavior.as_ref())
        || metadata_raw(previous.definition_metadata.as_ref())
            != metadata_raw(next.definition_metadata.as_ref())
    {
        return false;
    }
    let translation_behavior = |spec: &EntitySpec| {
        spec.translation
            .as_ref()
            .and_then(|translation| translation.definition_behavior.clone())
    };
    let translation_metadata = |spec: &EntitySpec| {
        spec.translation
            .as_ref()
            .and_then(|translation| translation.definition_metadata.clone())
    };
    let (previous_behavior, next_behavior) =
        (translation_behavior(previous), translation_behavior(next));
    let (previous_metadata, next_metadata) =
        (translation_metadata(previous), translation_metadata(next));
    behavior_raw(previous_behavior.as_ref()) == behavior_raw(next_behavior.as_ref())
        && metadata_raw(previous_metadata.as_ref()) == metadata_raw(next_metadata.as_ref())
}

pub(crate) fn normalize_definition_transition(
    mut next: EntitySpec,
    previous: Option<&EntitySpec>,
) -> EntitySpec {
    let Some(previous) = previous else {
        return next;
    };
    if previous.definition_kind == next.definition_kind {
        return next;
    }
    if next.definition_kind == es::DEFINITION_MAPPING {
        // The old URIs remain on the edit request so entity/collection files
        // can be deleted, while the desired mapping definition must not
        // reference those now-obsolete companion classes.
        next.entity_class.clear();
        next.collection_class.clear();
        if let Some(metadata) = next.definition_metadata.as_mut() {
            metadata.child_defaults = Vec::new();
            metadata.child_defaults_method_raw.clear();
            metadata.hydrator_class.clear();
            metadata.hydrator_method_raw.clear();
        }
        if let Some(behavior) = next.definition_behavior.as_mut() {
            behavior.override_default_fields = false;
            behavior.default_fields = Vec::new();
            behavior.default_fields_method_raw.clear();
            behavior.inheritance_aware_method_raw.clear();
        }
    }
    let is_extension =
        |kind: &str| kind == es::DEFINITION_EXTENSION || kind == es::DEFINITION_BULK_EXTENSION;
    if is_extension(&next.definition_kind) {
        next.definition_behavior = None;
        next.definition_metadata = None;
    }
    if is_extension(&previous.definition_kind) && !is_extension(&next.definition_kind) {
        for field in &mut next.fields {
            clear_entity_extension_flag(&mut field.metadata);
            clear_entity_extension_flag(&mut field.association_metadata);
        }
    }
    es::complete_spec(next)
}

fn clear_entity_extension_flag(metadata: &mut Option<es::FieldMetadata>) {
    let Some(value) = metadata.as_mut() else {
        return;
    };
    value.extension = false;
    if *value == es::FieldMetadata::default() {
        *metadata = None;
    }
}

fn entity_schema_service_tag(kind: &str) -> &'static str {
    match kind {
        es::DEFINITION_EXTENSION => "shopware.entity.extension",
        es::DEFINITION_BULK_EXTENSION => "shopware.bulk.entity.extension",
        _ => "shopware.entity.definition",
    }
}

pub(crate) fn validate_entity_schema_spec(
    provider: &Provider,
    spec: &EntitySpec,
) -> Vec<es::ValidationIssue> {
    let mut issues = es::validate_spec(spec);
    let mut append_field = |field: &FieldSpec| {
        if let Some(implementation) = &field.implementation
            && !provider.specialized_field_class_available(&implementation.class)
        {
            issues.push(es::ValidationIssue {
                code: "entity.field.implementation.unavailable".to_owned(),
                message: format!(
                    "Specialized field class {} is not installed in this Shopware project",
                    implementation.class
                ),
                field_id: field.id.clone(),
                severity: "error".to_owned(),
            });
        }
        append_enum_issue(provider, field, &mut issues);
    };
    for field in &spec.fields {
        append_field(field);
    }
    if let Some(behavior) = &spec.definition_behavior {
        behavior.default_fields.iter().for_each(&mut append_field);
        behavior.base_fields.iter().for_each(&mut append_field);
    }
    if let Some(behavior) = spec
        .translation
        .as_ref()
        .and_then(|translation| translation.definition_behavior.as_ref())
    {
        behavior.default_fields.iter().for_each(&mut append_field);
        behavior.base_fields.iter().for_each(&mut append_field);
    }
    for target in &spec.bulk_extensions {
        target.fields.iter().for_each(&mut append_field);
    }
    issues
}

fn append_enum_issue(
    provider: &Provider,
    field: &FieldSpec,
    issues: &mut Vec<es::ValidationIssue>,
) {
    let Some(php) = &provider.php else {
        return;
    };
    if field.kind != es::FIELD_ENUM || field.enum_class.is_empty() {
        return;
    }
    let issue = |code: &str, message: String| es::ValidationIssue {
        code: code.to_owned(),
        message,
        field_id: field.id.clone(),
        severity: "error".to_owned(),
    };
    let enumeration = match php.find_class(&field.enum_class) {
        Some(symbol) if symbol.kind == SymbolKind::Enum => symbol,
        _ => {
            issues.push(issue(
                "entity.field.enum.class.unavailable",
                format!(
                    "Enum class {} was not found in the PHP index",
                    field.enum_class
                ),
            ));
            return;
        }
    };
    let snapshot = php.semantic_snapshot();
    let case_found = snapshot
        .members(enumeration.id.as_str(), &field.enum_case)
        .iter()
        .any(|member| member.kind == SymbolKind::EnumCase);
    if !case_found {
        issues.push(issue(
            "entity.field.enum.case.unavailable",
            format!(
                "Enum case {}::{} was not found",
                field.enum_class, field.enum_case
            ),
        ));
    }
    let mut actual_backing = "";
    for member in snapshot.members(enumeration.id.as_str(), "value") {
        if member.kind != SymbolKind::Property {
            continue;
        }
        match member.ty.to_string().as_str() {
            "string" => actual_backing = "string",
            "int" => actual_backing = "int",
            _ => {}
        }
    }
    if actual_backing.is_empty() {
        issues.push(issue(
            "entity.field.enum.backing.unavailable",
            format!("Enum {} is not a backed enum", field.enum_class),
        ));
    } else if !field.enum_backing_type.is_empty() && field.enum_backing_type != actual_backing {
        issues.push(issue(
            "entity.field.enum.backing.mismatch",
            format!(
                "Enum {} is {actual_backing}-backed, not {}-backed",
                field.enum_class, field.enum_backing_type
            ),
        ));
    }
}

pub(crate) fn merge_previous_entity_spec(
    _requested: &EntitySpec,
    previous: Option<&EntitySpec>,
    scanned_specs: &[es::ScannedDefinition],
    scanned: &mut es::Schema,
) -> Result<Vec<es::ValidationIssue>> {
    let Some(previous) = previous else {
        return Ok(Vec::new());
    };
    let disk_spec = scanned_specs
        .iter()
        .find(|definition| {
            same_php_class(
                &definition.spec.definition_class,
                &previous.definition_class,
            )
        })
        .map(|definition| &definition.spec);
    es::replace_spec_schema(scanned, disk_spec, previous)
        .map_err(|error| errorf!("normalize current entity definition: {error}"))?;
    Ok(Vec::new())
}

fn import_previous_entity_spec(
    spec: &EntitySpec,
    sources: &Sources,
    lookup: es::RelationLookup,
) -> Result<Option<EntitySpec>> {
    if spec.mode != "edit" || sources.definition.content.is_empty() {
        return Ok(None);
    }
    let content = &sources.definition.content;
    let mut imported = es::import_definition(content, Some(lookup));
    if imported.is_err() {
        imported = es::import_extension(content, Some(lookup));
    }
    if imported.is_err() {
        imported = es::import_bulk_extension(content, Some(lookup));
    }
    if imported.is_err() && !spec.definition_class.is_empty() && !spec.definition_kind.is_empty() {
        imported = es::import_class_based_definition(
            content,
            &spec.definition_class,
            &spec.definition_kind,
            Some(lookup),
        );
    }
    let imported =
        imported.map_err(|error| errorf!("import current entity definition: {error}"))?;
    let Some(translation_spec) = imported
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    else {
        return Ok(Some(imported));
    };
    if sources.translation_definition.content.is_empty() {
        return Err(errorf!(
            "current entity references a translation definition that could not be loaded"
        ));
    }
    let mut translation =
        es::import_translation_definition(&sources.translation_definition.content, Some(lookup))
            .map_err(|error| errorf!("import current translation definition: {error}"))?;
    if !same_php_class(
        &translation_spec.definition_class,
        &translation.spec.definition_class,
    ) || !same_php_class(
        &imported.definition_class,
        &translation.spec.parent_definition_class,
    ) {
        return Err(errorf!(
            "current translation definition does not match its parent association"
        ));
    }
    translation.spec.definition_uri = file_uri(&sources.translation_definition.path);
    translation.spec.entity_uri = file_uri(&sources.translation_entity.path);
    translation.spec.collection_uri = file_uri(&sources.translation_collection.path);
    Ok(Some(es::attach_translation(imported, translation)))
}

fn render_entity_schema_files(
    spec: &EntitySpec,
    previous: Option<&EntitySpec>,
    sources: &Sources,
) -> Result<Vec<PreparedFile>> {
    let definition = if spec.mode == "edit" && !sources.definition.content.is_empty() {
        let Some(previous) = previous else {
            return Err(errorf!(
                "cannot safely edit an {} that was not imported from the plugin",
                spec.definition_kind
            ));
        };
        es::rewrite_definition_from(&sources.definition.content, previous, spec)?
    } else {
        es::render_definition(spec)?
    };
    let mut files = Vec::new();
    append_source(&mut files, &sources.definition, &definition);
    let previous_entity =
        previous.filter(|previous| previous.definition_kind == es::DEFINITION_ENTITY);
    if spec.definition_kind == es::DEFINITION_ENTITY {
        let mut collection = String::new();
        let entity = if let Some(previous) = previous_entity
            && !sources.entity.content.is_empty()
        {
            let entity = es::rewrite_entity(&sources.entity.content, previous, spec)?;
            if !sources.collection.content.is_empty() {
                collection = es::rewrite_collection(&sources.collection.content, previous, spec)?;
            }
            entity
        } else {
            es::render_entity(spec)?
        };
        if collection.is_empty() {
            collection = es::render_collection(spec)?;
        }
        append_source(&mut files, &sources.entity, &entity);
        append_source(&mut files, &sources.collection, &collection);
    } else if previous_entity.is_some() {
        append_deleted_source(&mut files, &sources.entity);
        append_deleted_source(&mut files, &sources.collection);
    }
    files.extend(render_translation_schema_files(spec, previous, sources)?);
    Ok(files)
}

fn render_translation_schema_files(
    spec: &EntitySpec,
    previous: Option<&EntitySpec>,
    sources: &Sources,
) -> Result<Vec<PreparedFile>> {
    let enabled = spec
        .translation
        .as_ref()
        .is_some_and(|translation| translation.enabled);
    if !enabled {
        let mut files = Vec::new();
        if previous
            .and_then(|previous| previous.translation.as_ref())
            .is_some_and(|translation| translation.enabled)
        {
            append_deleted_source(&mut files, &sources.translation_definition);
            append_deleted_source(&mut files, &sources.translation_entity);
            append_deleted_source(&mut files, &sources.translation_collection);
        }
        return Ok(files);
    }
    let (definition, entity, collection);
    if spec.mode == "edit" && !sources.translation_definition.content.is_empty() {
        let Some(previous) = previous.filter(|previous| previous.translation.is_some()) else {
            return Err(errorf!(
                "cannot safely edit a translation bundle that was not imported from the plugin"
            ));
        };
        definition = es::rewrite_translation_definition_from(
            &sources.translation_definition.content,
            previous,
            spec,
        )?;
        entity =
            es::rewrite_translation_entity(&sources.translation_entity.content, previous, spec)?;
        let mut rewritten = String::new();
        if !sources.translation_collection.content.is_empty() {
            rewritten = es::rewrite_translation_collection(
                &sources.translation_collection.content,
                previous,
                spec,
            )?;
        }
        collection = if rewritten.is_empty() {
            es::render_translation_collection(spec)?
        } else {
            rewritten
        };
    } else {
        definition = es::render_translation_definition(spec)?;
        entity = es::render_translation_entity(spec)?;
        collection = es::render_translation_collection(spec)?;
    }
    let mut files = Vec::new();
    append_source(&mut files, &sources.translation_definition, &definition);
    append_source(&mut files, &sources.translation_entity, &entity);
    append_source(&mut files, &sources.translation_collection, &collection);
    Ok(files)
}

fn append_source(files: &mut Vec<PreparedFile>, source: &Source, after: &str) {
    append_changed_file(
        files,
        &source.path,
        &source.content,
        after,
        source.version,
        source.exists,
    );
}

fn append_deleted_source(files: &mut Vec<PreparedFile>, source: &Source) {
    if !source.exists {
        return;
    }
    files.push(PreparedFile {
        path: paths::clean(&source.path),
        before: source.content.clone(),
        after: String::new(),
        version: source.version,
        exists: true,
        delete: true,
    });
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

pub(crate) fn entity_schema_revision(timestamp: i64, hash: &[u8; 32]) -> String {
    format!("{timestamp}:{}", hex(hash))
}

pub(crate) fn entity_schema_revision_timestamp(revision: &str) -> Option<i64> {
    let (timestamp_text, digest) = revision.split_once(':')?;
    if digest.len() != 64 || !digest.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return None;
    }
    go_parse_int(timestamp_text).filter(|timestamp| *timestamp > 0)
}

/// Go `strconv.ParseInt(text, 10, 64)`.
fn go_parse_int(text: &str) -> Option<i64> {
    let digits = text
        .strip_prefix('+')
        .or_else(|| text.strip_prefix('-'))
        .unwrap_or(text);
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    text.parse().ok()
}

fn prepare_entity_schema_history(
    plugin: &es::PluginContext,
    scanned: es::Schema,
    spec: &EntitySpec,
    request: &EntitySchemaPreviewRequest,
    response: &mut EntitySchemaPreviewResponse,
) -> Result<History> {
    let mut history = History {
        scanned,
        ..History::default()
    };
    let snapshots = es::read_snapshots(&plugin.root)?;
    let graph = es::build_snapshot_graph(&snapshots)?;
    if !graph.missing.is_empty() {
        response.issues.push(entity_issue(
            "entity.snapshot.parent.missing",
            "Snapshot history references missing parents",
        ));
        history.stop = true;
        return Ok(history);
    }
    if graph.leaves.len() > 1 {
        response.issues.push(entity_issue(
            "entity.snapshot.reconcile.required",
            "Snapshot history has multiple leaves; reconcile branches before generating a migration",
        ));
        history.stop = true;
        return Ok(history);
    }
    let current_leaves: Vec<String> = graph
        .leaves
        .iter()
        .map(|leaf| leaf.snapshot.id.clone())
        .collect();
    if !spec.base_snapshot_ids.is_empty()
        && !same_string_set(&spec.base_snapshot_ids, &current_leaves)
    {
        response.issues.push(entity_issue(
            "entity.snapshot.stale",
            "Snapshot history changed since the entity was opened; reload the designer before applying another migration",
        ));
        history.stop = true;
        return Ok(history);
    }
    let Some(leaf) = graph.leaves.into_iter().next() else {
        return new_entity_schema_baseline(plugin, history);
    };
    reconcile_entity_schema_drift(
        plugin,
        &leaf.snapshot,
        spec,
        &request.drift_decision,
        response,
        history,
    )
}

fn new_entity_schema_baseline(plugin: &es::PluginContext, mut history: History) -> Result<History> {
    let baseline = es::Snapshot {
        kind: es::SNAPSHOT_BASELINE.to_owned(),
        plugin: es::PluginIdentity {
            composer_name: plugin.composer_name.clone(),
        },
        shopware_version: plugin.shopware_version.clone(),
        schema: history.scanned.clone(),
        ..es::Snapshot::default()
    }
    .seal()?;
    let encoded = es::marshal_snapshot(&baseline)?;
    history.pending.push(PreparedFile {
        path: paths::join(&[
            &plugin.snapshot_directory,
            &format!("0000000000-baseline-{}.snapshot.json", &baseline.id[..12]),
        ]),
        after: String::from_utf8_lossy(&encoded).into_owned(),
        ..PreparedFile::default()
    });
    history.parents = vec![baseline.id];
    history.previous = history.scanned.clone();
    Ok(history)
}

pub(crate) fn reconcile_entity_schema_drift(
    plugin: &es::PluginContext,
    leaf: &es::Snapshot,
    spec: &EntitySpec,
    drift_decision: &str,
    response: &mut EntitySchemaPreviewResponse,
    mut history: History,
) -> Result<History> {
    history.parents = vec![leaf.id.clone()];
    history.previous = leaf.schema.clone();
    history.scanned = es::restore_snapshot_only_indexes(&history.scanned, &history.previous);
    if same_entity_schema(&history.previous, &history.scanned) {
        return Ok(history);
    }
    response.drift = true;
    response.diff = es::diff_schemas(&history.previous, &history.scanned);
    response.drift_message = "Entity definitions differ from the latest committed schema snapshot. Review the returned diff, then preview again with driftDecision=adopt when the current PHP definitions are authoritative, or driftDecision=migrate when the committed snapshot is authoritative and the code changes need migration SQL.".to_owned();
    match drift_decision {
        "adopt" => adopt_entity_schema_drift(plugin, spec, history),
        "migrate" => Ok(history),
        _ => {
            response.issues.push(entity_issue(
                "entity.snapshot.drift.decision",
                "Choose Adopt current schema or Generate migration before applying",
            ));
            history.stop = true;
            Ok(history)
        }
    }
}

fn adopt_entity_schema_drift(
    plugin: &es::PluginContext,
    spec: &EntitySpec,
    mut history: History,
) -> Result<History> {
    let adopted = es::Snapshot {
        parents: history.parents.clone(),
        kind: es::SNAPSHOT_BASELINE.to_owned(),
        plugin: es::PluginIdentity {
            composer_name: plugin.composer_name.clone(),
        },
        shopware_version: plugin.shopware_version.clone(),
        schema: history.scanned.clone(),
        decisions: vec![es::Decision {
            kind: "driftAdopt".to_owned(),
            reason: "adopt current plugin entity definitions".to_owned(),
            ..es::Decision::default()
        }],
        ..es::Snapshot::default()
    }
    .seal()?;
    let encoded = es::marshal_snapshot(&adopted).unwrap_or_default();
    history.pending.push(PreparedFile {
        path: paths::join(&[
            &plugin.snapshot_directory,
            &format!(
                "{}-adopt-{}.snapshot.json",
                spec.migration_timestamp,
                &adopted.id[..12]
            ),
        ]),
        after: String::from_utf8_lossy(&encoded).into_owned(),
        ..PreparedFile::default()
    });
    history.parents = vec![adopted.id];
    history.previous = history.scanned.clone();
    Ok(history)
}

pub(crate) fn entity_schema_field_types(
    version_constraint: &str,
    available: Option<&dyn Fn(&str) -> bool>,
) -> Vec<EntitySchemaFieldType> {
    let all = es::definition_kinds_for_version(version_constraint);
    let kinds = |values: &[&str]| {
        values
            .iter()
            .map(|value| (*value).to_owned())
            .collect::<Vec<_>>()
    };
    let entity_mapping = kinds(&[es::DEFINITION_ENTITY, es::DEFINITION_MAPPING]);
    let entity_only = kinds(&[es::DEFINITION_ENTITY]);
    let mut entity_extension = kinds(&[es::DEFINITION_ENTITY, es::DEFINITION_EXTENSION]);
    if es::bulk_entity_extension_supported(version_constraint) {
        entity_extension.push(es::DEFINITION_BULK_EXTENSION.to_owned());
    }
    let base = |kind: &str, label: &str, stored: bool, definition_kinds: &Vec<String>| {
        EntitySchemaFieldType {
            kind: kind.to_owned(),
            label: label.to_owned(),
            stored,
            definition_kinds: definition_kinds.clone(),
            ..EntitySchemaFieldType::default()
        }
    };
    let timestamps = |kind: &str, label: &str| EntitySchemaFieldType {
        requires_default_fields_override: true,
        ..base(kind, label, true, &entity_mapping)
    };
    let mut result = vec![
        base("id", "Primary ID", true, &entity_mapping),
        base("binary-id", "Binary ID", true, &all),
        base("auto-increment", "Auto increment", true, &entity_mapping),
        base("version", "Version ID", true, &entity_only),
        base("reference-version", "Reference version", true, &all),
        base("foreign-key", "Foreign key", true, &entity_mapping),
        base("string", "String", true, &all),
        base("long-text", "Long text", true, &all),
        base("blob", "Blob", true, &all),
        base("int", "Integer", true, &all),
        base("float", "Float", true, &all),
        base("bool", "Boolean", true, &all),
        base("date", "Date", true, &all),
        base("datetime", "Date/time", true, &all),
        base("json", "JSON", true, &all),
        base("list", "List (JSON)", true, &all),
        base("object", "Object (JSON)", true, &all),
        timestamps("created-at", "Created at"),
        timestamps("updated-at", "Updated at"),
        base("many-to-one", "Many to one", true, &all),
        base("one-to-one", "One to one", true, &entity_extension),
        base("one-to-many", "One to many", false, &entity_extension),
        base("many-to-many", "Many to many", false, &entity_extension),
        base(
            "hierarchy",
            "Parent / children hierarchy",
            true,
            &entity_only,
        ),
    ];
    let is_available = |class: &str| available.is_none_or(|available| available(class));
    if es::enum_field_supported(version_constraint)
        && is_available("Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\EnumField")
    {
        result.push(base("enum", "Backed enum", true, &all));
    }
    for template in es::specialized_field_templates() {
        let field = template.field;
        let class = field
            .implementation
            .as_ref()
            .map(|implementation| implementation.class.clone())
            .unwrap_or_default();
        if !es::specialized_field_supported(&class, version_constraint) || !is_available(&class) {
            continue;
        }
        result.push(EntitySchemaFieldType {
            kind: field.kind.clone(),
            label: template.label,
            stored: true,
            definition_kinds: all.clone(),
            requires_default_fields_override: false,
            id: template.id,
            template: Some(field),
        });
    }
    result
}

fn entity_issue(code: &str, message: &str) -> es::ValidationIssue {
    es::ValidationIssue {
        code: code.to_owned(),
        message: message.to_owned(),
        field_id: String::new(),
        severity: "error".to_owned(),
    }
}

fn same_string_set(left: &[String], right: &[String]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    let mut left = left.to_vec();
    let mut right = right.to_vec();
    left.sort();
    right.sort();
    left == right
}

fn same_php_class(left: &str, right: &str) -> bool {
    go_equal_fold(left.trim_matches('\\'), right.trim_matches('\\'))
}

/// Go `strings.EqualFold` (simple Unicode folding).
fn go_equal_fold(left: &str, right: &str) -> bool {
    let mut left = left.chars();
    let mut right = right.chars();
    loop {
        match (left.next(), right.next()) {
            (None, None) => return true,
            (Some(a), Some(b)) => {
                if a != b && crate::to_lower(a) != crate::to_lower(b) {
                    return false;
                }
            }
            _ => return false,
        }
    }
}

fn entity_schema_sources_for(
    directory: &str,
    spec: &EntitySpec,
    documents: &BTreeMap<String, EntitySchemaDocument>,
) -> Result<Sources> {
    let class_path = |base_directory: &str, class: &str| {
        paths::join(&[base_directory, &format!("{}.php", es::short_class(class))])
    };
    let read_source = |path: String| -> Result<Source> {
        let (content, version, exists) = source_for_path(&path, documents)?;
        Ok(Source {
            path,
            content,
            version,
            exists,
        })
    };
    let definition_path = entity_schema_path(
        &spec.definition_uri,
        class_path(directory, &spec.definition_class),
    )?;
    let definition = read_source(definition_path)?;
    let current = if definition.content.is_empty() {
        None
    } else {
        es::import_definition(&definition.content, None).ok()
    };
    let mut sources = Sources {
        definition,
        ..Sources::default()
    };
    let current_is_entity = current
        .as_ref()
        .is_some_and(|current| current.definition_kind == es::DEFINITION_ENTITY);
    let (mut entity_class, mut collection_class) = (&spec.entity_class, &spec.collection_class);
    if let Some(current) = &current
        && current_is_entity
        && spec.definition_kind != es::DEFINITION_ENTITY
    {
        entity_class = &current.entity_class;
        collection_class = &current.collection_class;
    }
    if spec.definition_kind == es::DEFINITION_ENTITY || current_is_entity {
        let entity_path =
            entity_schema_path(&spec.entity_uri, class_path(directory, entity_class))?;
        let collection_path = entity_schema_path(
            &spec.collection_uri,
            class_path(directory, collection_class),
        )?;
        sources.entity = read_source(entity_path)?;
        sources.collection = read_source(collection_path)?;
    }
    let mut translation = spec.translation.as_ref();
    let mut translation_owner = spec;
    if !translation.is_some_and(|translation| translation.enabled)
        && let Some(current) = &current
        && let Some(current_translation) = &current.translation
        && current_translation.enabled
    {
        translation = Some(current_translation);
        translation_owner = current;
    }
    if let Some(translation) = translation {
        let translation_directory = default_translation_directory(directory, translation_owner);
        let definition_path = entity_schema_path(
            &translation.definition_uri,
            class_path(&translation_directory, &translation.definition_class),
        )?;
        let entity_path = entity_schema_path(
            &translation.entity_uri,
            class_path(&translation_directory, &translation.entity_class),
        )?;
        let collection_path = entity_schema_path(
            &translation.collection_uri,
            class_path(&translation_directory, &translation.collection_class),
        )?;
        sources.translation_definition = read_source(definition_path)?;
        sources.translation_entity = read_source(entity_path)?;
        sources.translation_collection = read_source(collection_path)?;
    }
    Ok(sources)
}

fn entity_schema_path(uri: &str, fallback: String) -> Result<String> {
    if uri.is_empty() {
        return Ok(fallback);
    }
    swlsp_uriutil::path(uri).map_err(|error| errorf!("resolve entity source URI: {error}"))
}

fn default_translation_directory(parent_directory: &str, spec: &EntitySpec) -> String {
    paths::join(&[
        parent_directory,
        "Aggregate",
        &format!("{}Translation", spec.class_name),
    ])
}

fn source_for_path(
    path: &str,
    documents: &BTreeMap<String, EntitySchemaDocument>,
) -> Result<(String, Option<i32>, bool)> {
    if let Some(document) = documents.get(&file_uri(path)) {
        return Ok((document.text.clone(), document.version, true));
    }
    match paths::read_file(path).map_err(|error| errorf!("{error}"))? {
        Some(content) => Ok((content, None, true)),
        None => Ok((String::new(), None, false)),
    }
}

fn append_changed_file(
    files: &mut Vec<PreparedFile>,
    path: &str,
    before: &str,
    after: &str,
    version: Option<i32>,
    exists: bool,
) {
    if before == after {
        return;
    }
    files.push(PreparedFile {
        path: paths::clean(path),
        before: before.to_owned(),
        after: after.to_owned(),
        version,
        exists,
        delete: false,
    });
}

fn available_migration_timestamp(source_root: &str, mut timestamp: i64) -> i64 {
    let directory = paths::join(&[source_root, "Migration"]);
    let names: Vec<String> = std::fs::read_dir(&directory)
        .map(|entries| {
            entries
                .filter_map(|entry| entry.ok())
                .map(|entry| entry.file_name().to_string_lossy().into_owned())
                .collect()
        })
        .unwrap_or_default();
    loop {
        let prefix = format!("Migration{timestamp}");
        if !names.iter().any(|name| {
            name.starts_with(&prefix) && name.ends_with(".php") && name.len() >= prefix.len() + 4
        }) {
            return timestamp;
        }
        timestamp += 1;
    }
}

pub(crate) fn safe_plugin_target(plugin_root: &str, target: &str) -> bool {
    let canonical_root = paths::resolved_directory_path(plugin_root);
    let canonical_target = resolved_path_through_existing_ancestor(target);
    paths::rel_within(&canonical_root, &canonical_target).is_some()
}

fn resolved_path_through_existing_ancestor(path: &str) -> String {
    let path = paths::clean(path);
    let mut probe = path.clone();
    let mut suffix: Vec<String> = Vec::new();
    loop {
        if std::fs::symlink_metadata(&probe).is_ok() {
            if let Some(mut resolved) = paths::eval_symlinks(&probe) {
                for part in suffix.iter().rev() {
                    resolved = paths::join(&[&resolved, part]);
                }
                return paths::clean(&resolved);
            }
            break;
        }
        let parent = paths::dir(&probe);
        if parent == probe {
            break;
        }
        suffix.push(paths::base(&probe));
        probe = parent;
    }
    path
}

fn same_entity_schema(left: &es::Schema, right: &es::Schema) -> bool {
    es::gojson::to_vec(&left.normalize()).ok() == es::gojson::to_vec(&right.normalize()).ok()
}

fn language_for_path(path: &str) -> &'static str {
    match paths::ext(path).to_lowercase().as_str() {
        ".php" => "php",
        ".xml" => "xml",
        ".yaml" | ".yml" => "yaml",
        ".json" => "json",
        _ => "plaintext",
    }
}
