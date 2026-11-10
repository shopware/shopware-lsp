//! Scaffold and entity-schema tools. Generators run
//! through the production LSP commands; their workspace edits are validated
//! against the root and previewed (or written when requested).

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use swlsp_ide_shopware::scaffold::{
    self as scaffold, EntitySchemaApplyRequest, EntitySchemaApplyResponse,
    EntitySchemaBootstrapRequest, EntitySchemaBootstrapResponse, EntitySchemaEditableTarget,
    EntitySchemaFieldType, EntitySchemaGraph, EntitySchemaLoadRequest, EntitySchemaPreviewRequest,
    EntitySchemaPreviewResponse, EntitySchemaReconcileRequest, EntitySchemaRelationTarget,
    EntitySchemaSearchRequest,
};
use swlsp_lsp_protocol::{
    CREATE_FILE_OPERATION, DocumentChange, OptionalVersionedTextDocumentIdentifier, Range,
    TextEdit, WorkspaceEdit,
};
use swlsp_shopware_entityschema::{
    self as es, Decision, EntityRenameQuestion, EntitySpec, FieldSpec, PluginContext,
    RenameQuestion, ValidationIssue,
};

use super::tools::{bounded_limit, decode};
use super::{McpRuntime, ToolCall, output};
use crate::CliError;
use crate::edit::{EditMode, apply_workspace_edit};
use crate::session::CliSession;

pub(crate) fn dispatch(
    runtime: &ToolCall<'_>,
    name: &str,
    arguments: Value,
) -> Result<Value, CliError> {
    match name {
        "shopware_scaffold_catalog" => output(&ScaffoldCatalogOutput {
            protocol_version: swlsp_integration::PROTOCOL_VERSION,
            scaffolds: swlsp_integration::scaffolds(),
            entity_schema: true,
        }),
        "shopware_scaffold" => output(&scaffold_tool(runtime, decode(arguments)?)?),
        "shopware_entity_schema_bootstrap" => {
            let input: BootstrapInput = decode(arguments)?;
            let response = bootstrap_response(runtime, &input.directory)?;
            output(&compact_bootstrap(response))
        }
        "shopware_entity_schema_field_types" => output(&field_types(runtime, decode(arguments)?)?),
        "shopware_entity_schema_search" => output(&search(runtime, decode(arguments)?)?),
        "shopware_entity_schema_load" => output(&load(runtime, decode(arguments)?)?),
        "shopware_entity_schema_preview" => output(&preview(runtime, decode(arguments)?)?),
        "shopware_entity_schema_apply" => output(&apply(runtime, decode(arguments)?)?),
        "shopware_entity_schema_reconcile" => output(&reconcile(runtime, decode(arguments)?)?),
        _ => Err(CliError::fail(format!("unknown tool {name}"))),
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ScaffoldCatalogOutput {
    protocol_version: i32,
    scaffolds: Vec<swlsp_integration::ScaffoldDefinition>,
    entity_schema: bool,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct ScaffoldInput {
    family: String,
    kind: String,
    directory: String,
    name: String,
    options: Map<String, Value>,
    write: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ScaffoldOutput {
    family: String,
    kind: String,
    applied: bool,
    primary_file: String,
    files: Vec<String>,
    diff: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    shopware_version: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct BootstrapInput {
    directory: String,
}

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
struct FieldTypeSummary {
    id: String,
    kind: String,
    label: String,
    stored: bool,
    #[serde(skip_serializing_if = "is_false")]
    specialized: bool,
    #[serde(skip_serializing_if = "is_false")]
    requires_default_fields_override: bool,
}

fn is_false(value: &bool) -> bool {
    !*value
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct BootstrapOutput {
    plugin: PluginContext,
    spec: EntitySpec,
    definition_kinds: Option<Vec<String>>,
    field_types: Vec<FieldTypeSummary>,
    graph: EntitySchemaGraph,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    editable: Vec<EntitySchemaEditableTarget>,
    next_action: String,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct FieldTypesInput {
    directory: String,
    id: String,
    query: String,
    definition_kind: String,
    limit: i64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct FieldTypeDetail {
    #[serde(flatten)]
    summary: FieldTypeSummary,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    definition_kinds: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    template: Option<Value>,
    #[serde(skip_serializing_if = "String::is_empty")]
    usage: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct FieldTypesOutput {
    field_types: Option<Vec<FieldTypeDetail>>,
    next_action: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct SearchInput {
    query: String,
    limit: i64,
}

#[derive(Serialize)]
struct SearchOutput {
    results: Option<Vec<EntitySchemaRelationTarget>>,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct LoadInput {
    definition_class: String,
    definition_kind: String,
    entity_name: String,
    path: String,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct PreviewInput {
    spec: EntitySpec,
    decisions: Vec<Decision>,
    drift_decision: String,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct ApplyInput {
    spec: EntitySpec,
    decisions: Vec<Decision>,
    drift_decision: String,
    revision: String,
    allow_destructive: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PreviewFile {
    path: String,
    action: String,
    language: String,
    size_bytes: usize,
}

#[derive(Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DiffSummary {
    #[serde(skip_serializing_if = "Vec::is_empty")]
    created_entities: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    removed_entities: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    added_columns: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    removed_columns: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    changed_columns: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    rename_questions: Vec<RenameQuestion>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub(crate) entity_rename_questions: Vec<EntityRenameQuestion>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    added_indexes: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    removed_indexes: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    added_foreign_keys: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    removed_foreign_keys: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    changed_primary_keys: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PreviewOutput {
    status: String,
    ready_to_apply: bool,
    next_action: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    revision: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    files: Vec<PreviewFile>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    issues: Vec<ValidationIssue>,
    diff: DiffSummary,
    destructive: bool,
    requires_destructive_confirmation: bool,
    drift: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    drift_message: String,
    #[serde(rename = "snapshotId", skip_serializing_if = "String::is_empty")]
    snapshot_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    primary_file: String,
    #[serde(skip_serializing_if = "is_zero")]
    migration_timestamp: i64,
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct ReconcileInput {
    directory: String,
    selected_leaf: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct WriteOutput {
    applied: bool,
    primary_file: String,
    #[serde(rename = "snapshotId")]
    snapshot_id: String,
    files: Vec<String>,
    diff: String,
}

/// Runs a server command; a `null` result becomes the default response and
/// errors read `command: method: cause`.
fn call_command<Request: Serialize, Response: serde::de::DeserializeOwned + Default>(
    session: &CliSession,
    command: &str,
    request: &Request,
) -> Result<Response, CliError> {
    let params = serde_json::to_value(request).unwrap_or(Value::Null);
    session
        .call::<Option<Response>>(command, params)
        .map(Option::unwrap_or_default)
        .map_err(|error| CliError::fail(format!("{command}: {}", error.message)))
}

fn scaffold_tool(runtime: &ToolCall<'_>, input: ScaffoldInput) -> Result<ScaffoldOutput, CliError> {
    let family = input.family.trim().to_lowercase();
    if family != "shopware" && family != "symfony" {
        return Err(CliError::fail("family must be shopware or symfony"));
    }
    let kind = input.kind.trim().to_lowercase();
    if kind.is_empty() {
        return Err(CliError::fail("kind is required"));
    }
    let directory = runtime.resolve_path(&input.directory)?;
    if input.name.trim().is_empty() {
        return Err(CliError::fail("name is required"));
    }
    runtime.with_session(|session| {
        let directory_uri = swlsp_uriutil::file_uri(&directory);
        let (edit, primary, version) = if family == "symfony" {
            let response: scaffold::Response = call_command(
                session,
                scaffold::CREATE_SYMFONY_SCAFFOLD_COMMAND,
                &scaffold::Request {
                    kind: kind.clone(),
                    directory_uri,
                    name: input.name.clone(),
                },
            )?;
            (
                Some(create_file_workspace_edit(
                    &response.file_uri,
                    &response.content,
                )),
                response.file_uri,
                String::new(),
            )
        } else {
            let response: scaffold::ShopwareResponse = call_command(
                session,
                scaffold::CREATE_SHOPWARE_SCAFFOLD_COMMAND,
                &scaffold::ShopwareRequest {
                    kind: kind.clone(),
                    directory_uri,
                    name: input.name.clone(),
                    options: input.options.clone(),
                },
            )?;
            (
                response.edit,
                response.primary_file_uri,
                response.shopware_version,
            )
        };
        let (files, diff) = apply_mcp_workspace_edit(runtime, edit.as_ref(), input.write)?;
        Ok(ScaffoldOutput {
            family: family.clone(),
            kind: kind.clone(),
            applied: input.write,
            primary_file: runtime.output_uri(&primary),
            files,
            diff,
            shopware_version: version,
        })
    })
}

fn create_file_workspace_edit(uri: &str, content: &str) -> WorkspaceEdit {
    WorkspaceEdit {
        document_changes: vec![
            DocumentChange {
                kind: CREATE_FILE_OPERATION.into(),
                uri: uri.to_owned(),
                ..DocumentChange::default()
            },
            DocumentChange {
                text_document: Some(OptionalVersionedTextDocumentIdentifier {
                    uri: uri.to_owned(),
                    version: None,
                }),
                edits: vec![TextEdit::new(Range::default(), content)],
                ..DocumentChange::default()
            },
        ],
        ..WorkspaceEdit::default()
    }
}

fn apply_mcp_workspace_edit(
    runtime: &McpRuntime,
    edit: Option<&WorkspaceEdit>,
    write: bool,
) -> Result<(Vec<String>, String), CliError> {
    let Some(edit) = edit else {
        return Err(CliError::fail("command returned no workspace edit"));
    };
    runtime.validate_workspace_edit(edit)?;
    let mut diff = Vec::new();
    apply_workspace_edit(&mut diff, Some(edit), EditMode { write, diff: true })?;
    Ok((
        runtime.workspace_edit_paths(edit),
        String::from_utf8_lossy(&diff).into_owned(),
    ))
}

fn bootstrap_response(
    runtime: &ToolCall<'_>,
    directory: &str,
) -> Result<EntitySchemaBootstrapResponse, CliError> {
    let directory_uri = runtime.input_uri(directory, true)?;
    let mut response: EntitySchemaBootstrapResponse = runtime.with_session(|session| {
        call_command(
            session,
            scaffold::ENTITY_SCHEMA_BOOTSTRAP_COMMAND,
            &EntitySchemaBootstrapRequest { directory_uri },
        )
    })?;
    runtime.normalize_bootstrap(&mut response);
    Ok(response)
}

fn field_type_id(field_type: &EntitySchemaFieldType) -> String {
    if field_type.id.trim().is_empty() {
        field_type.kind.clone()
    } else {
        field_type.id.clone()
    }
}

fn summarize(field_type: &EntitySchemaFieldType) -> FieldTypeSummary {
    FieldTypeSummary {
        id: field_type_id(field_type),
        kind: field_type.kind.clone(),
        label: field_type.label.clone(),
        stored: field_type.stored,
        specialized: field_type.template.is_some(),
        requires_default_fields_override: field_type.requires_default_fields_override,
    }
}

fn compact_bootstrap(input: EntitySchemaBootstrapResponse) -> BootstrapOutput {
    BootstrapOutput {
        field_types: input.field_types.iter().map(summarize).collect(),
        plugin: input.plugin,
        spec: input.spec,
        definition_kinds: Some(input.definition_kinds).filter(|kinds| !kinds.is_empty()),
        graph: input.graph,
        editable: input.editable,
        next_action: "Modify this exact spec. A normal scalar field is {id, kind, propertyName, storageName, editable:true}; add translated:true for translated storage. Call shopware_entity_schema_field_types with an exact id for a copyable template, or shopware_entity_schema_search for relation targets. Then preview the complete spec. Do not inspect content.json or temporary payload files.".into(),
    }
}

fn field_types(
    runtime: &ToolCall<'_>,
    input: FieldTypesInput,
) -> Result<FieldTypesOutput, CliError> {
    let limit = bounded_limit(input.limit, 100, 200, "limit")? as usize;
    let bootstrap = bootstrap_response(runtime, &input.directory)?;
    if !input.definition_kind.is_empty()
        && !bootstrap.definition_kinds.contains(&input.definition_kind)
    {
        return Err(CliError::fail(format!(
            "definition kind {:?} is unavailable for Shopware constraint {:?}",
            input.definition_kind, bootstrap.plugin.shopware_version
        )));
    }
    let exact_id = input.id.trim().to_owned();
    let query = input.query.trim().to_lowercase();
    let mut result: Option<Vec<FieldTypeDetail>> = None;
    for field_type in &bootstrap.field_types {
        let id = field_type_id(field_type);
        if !exact_id.is_empty()
            && !id.eq_ignore_ascii_case(&exact_id)
            && id.to_lowercase() != exact_id.to_lowercase()
        {
            continue;
        }
        if !input.definition_kind.is_empty()
            && !field_type.definition_kinds.contains(&input.definition_kind)
        {
            continue;
        }
        if !query.is_empty()
            && !format!("{id} {} {}", field_type.kind, field_type.label)
                .to_lowercase()
                .contains(&query)
        {
            continue;
        }
        let mut detail = FieldTypeDetail {
            summary: summarize(field_type),
            definition_kinds: Vec::new(),
            template: None,
            usage: String::new(),
        };
        if !exact_id.is_empty() {
            detail.definition_kinds = field_type.definition_kinds.clone();
            detail.template = field_template(field_type);
            detail.usage = field_type_usage(field_type).to_owned();
        }
        let list = result.get_or_insert_with(Vec::new);
        list.push(detail);
        if list.len() >= limit {
            break;
        }
    }
    if !exact_id.is_empty() && result.as_ref().is_none_or(Vec::is_empty) {
        return Err(CliError::fail(format!(
            "field type {:?} is unavailable; call shopware_entity_schema_field_types without id to list valid ids",
            exact_id
        )));
    }
    let next_action = if exact_id.is_empty() {
        "Call shopware_entity_schema_field_types again with one exact id to receive its copyable template; do not inspect content.json or temporary payload files."
    } else {
        "Copy template into the bootstrapped spec.fields, replace the example identifiers and requested options, then call shopware_entity_schema_preview with the complete spec."
    };
    Ok(FieldTypesOutput {
        field_types: result,
        next_action: next_action.into(),
    })
}

fn field_template(field_type: &EntitySchemaFieldType) -> Option<Value> {
    let field = match &field_type.template {
        Some(template) => {
            let mut field = template.clone();
            field.id = "replace-me".into();
            field.editable = true;
            field.raw = String::new();
            field
        }
        None => basic_field_template(&field_type.kind),
    };
    match serde_json::to_value(&field) {
        Ok(Value::Object(map)) => Some(Value::Object(map)),
        _ => None,
    }
}

fn basic_field_template(kind: &str) -> FieldSpec {
    const TARGET_DEFINITION: &str = "Vendor\\Plugin\\Content\\ReplaceMe\\ReplaceMeDefinition";
    const TARGET_ENTITY: &str = "Vendor\\Plugin\\Content\\ReplaceMe\\ReplaceMeEntity";
    const TARGET_COLLECTION: &str = "Vendor\\Plugin\\Content\\ReplaceMe\\ReplaceMeCollection";
    let mut field = FieldSpec {
        id: "replace-me".into(),
        kind: kind.to_owned(),
        property_name: "replaceMe".into(),
        storage_name: "replace_me".into(),
        editable: true,
        ..FieldSpec::default()
    };
    match kind {
        es::FIELD_ID => {
            field.id = "id".into();
            field.property_name = "id".into();
            field.storage_name = "id".into();
            field.required = true;
            field.primary = true;
        }
        es::FIELD_AUTO_INCREMENT => {
            field.property_name = "autoIncrement".into();
            field.storage_name = "auto_increment".into();
            field.required = true;
        }
        es::FIELD_VERSION => {
            field.property_name = "versionId".into();
            field.storage_name = "version_id".into();
            field.required = true;
            field.primary = true;
        }
        es::FIELD_CREATED_AT => {
            field.property_name = "createdAt".into();
            field.storage_name = "created_at".into();
            field.required = true;
        }
        es::FIELD_UPDATED_AT => {
            field.property_name = "updatedAt".into();
            field.storage_name = "updated_at".into();
        }
        es::FIELD_HIERARCHY => {
            field.property_name = "children".into();
            field.storage_name = String::new();
        }
        es::FIELD_REFERENCE_VERSION => {
            field.property_name = "replaceMeVersionId".into();
            field.storage_name = "replace_me_version_id".into();
            field.target_definition_class = TARGET_DEFINITION.into();
        }
        es::FIELD_FOREIGN_KEY => {
            field.property_name = "replaceMeId".into();
            field.storage_name = "replace_me_id".into();
            field.target_definition_class = TARGET_DEFINITION.into();
            field.reference_field = "id".into();
            field.reference_storage_name = "id".into();
        }
        es::FIELD_MANY_TO_ONE | es::FIELD_ONE_TO_ONE => {
            field.foreign_key_property_name = "replaceMeId".into();
            field.target_definition_class = TARGET_DEFINITION.into();
            field.target_entity_class = TARGET_ENTITY.into();
            field.target_collection_class = TARGET_COLLECTION.into();
            field.target_entity_name = "replace_me".into();
            field.reference_field = "id".into();
            field.reference_storage_name = "id".into();
        }
        es::FIELD_ONE_TO_MANY => {
            field.property_name = "replaceMes".into();
            field.storage_name = String::new();
            field.target_definition_class = TARGET_DEFINITION.into();
            field.target_entity_class = TARGET_ENTITY.into();
            field.target_collection_class = TARGET_COLLECTION.into();
            field.target_entity_name = "replace_me".into();
            field.reference_storage_name = "owner_id".into();
            field.source_column = "id".into();
        }
        es::FIELD_MANY_TO_MANY => {
            field.property_name = "replaceMes".into();
            field.storage_name = String::new();
            field.target_definition_class = TARGET_DEFINITION.into();
            field.target_entity_class = TARGET_ENTITY.into();
            field.target_collection_class = TARGET_COLLECTION.into();
            field.target_entity_name = "replace_me".into();
            field.mapping_definition_class =
                "Vendor\\Plugin\\Content\\ReplaceMe\\ReplaceMeMappingDefinition".into();
            field.mapping_local_column = "owner_id".into();
            field.mapping_reference_column = "replace_me_id".into();
            field.source_column = "id".into();
            field.reference_field = "id".into();
        }
        _ => {}
    }
    field
}

fn field_type_usage(field_type: &EntitySchemaFieldType) -> &'static str {
    if field_type.template.is_some() {
        return "Copy the complete template and retain implementation unchanged. Replace id and only the storage/property values that are not fixed by the returned implementation metadata.";
    }
    match field_type.kind.as_str() {
        es::FIELD_MANY_TO_ONE
        | es::FIELD_ONE_TO_ONE
        | es::FIELD_ONE_TO_MANY
        | es::FIELD_MANY_TO_MANY
        | es::FIELD_FOREIGN_KEY
        | es::FIELD_REFERENCE_VERSION => {
            "Call shopware_entity_schema_search, replace every ReplaceMe target value with one returned target, and adjust relation/mapping columns."
        }
        es::FIELD_ENUM => {
            "Replace the example identifiers and set enumClass, enumCase, and enumBackingType to string or int."
        }
        es::FIELD_CREATED_AT | es::FIELD_UPDATED_AT => {
            "Normal EntityDefinition classes inherit this field already. Add it only for a mapping or an explicit defaultFields override advertised by the catalog."
        }
        es::FIELD_HIERARCHY => {
            "Use exactly one hierarchy row. The server derives parent FK, associations, and version pairing; do not add those rows separately."
        }
        _ => {
            "Replace the example id, propertyName, and storageName. Set translated=true for translation storage; leave createdAt and updatedAt implicit on normal entities."
        }
    }
}

fn search(runtime: &ToolCall<'_>, input: SearchInput) -> Result<SearchOutput, CliError> {
    let limit = bounded_limit(input.limit, 50, 200, "limit")?;
    let mut results: Option<Vec<EntitySchemaRelationTarget>> = runtime.with_session(|session| {
        let params = serde_json::to_value(EntitySchemaSearchRequest {
            query: input.query.clone(),
            limit,
        })
        .unwrap_or(Value::Null);
        session
            .call::<Option<Vec<EntitySchemaRelationTarget>>>(
                scaffold::ENTITY_SCHEMA_SEARCH_COMMAND,
                params,
            )
            .map_err(|error| {
                CliError::fail(format!(
                    "{}: {}",
                    scaffold::ENTITY_SCHEMA_SEARCH_COMMAND,
                    error.message
                ))
            })
    })?;
    if let Some(results) = results.as_mut() {
        runtime.normalize_relation_targets(results);
    }
    Ok(SearchOutput { results })
}

fn load(runtime: &ToolCall<'_>, input: LoadInput) -> Result<EntitySpec, CliError> {
    let file_uri = runtime.input_uri(&input.path, false)?;
    if input.definition_class.trim().is_empty()
        && input.entity_name.trim().is_empty()
        && file_uri.is_empty()
    {
        return Err(CliError::fail(
            "definitionClass, entityName, or path is required",
        ));
    }
    let mut spec: EntitySpec = runtime.with_session(|session| {
        call_command(
            session,
            scaffold::ENTITY_SCHEMA_LOAD_COMMAND,
            &EntitySchemaLoadRequest {
                definition_class: input.definition_class.clone(),
                definition_kind: input.definition_kind.clone(),
                entity_name: input.entity_name.clone(),
                file_uri: file_uri.clone(),
                ..EntitySchemaLoadRequest::default()
            },
        )
    })?;
    runtime.normalize_spec(&mut spec);
    Ok(spec)
}

fn preview(runtime: &ToolCall<'_>, input: PreviewInput) -> Result<PreviewOutput, CliError> {
    let request = runtime.preview_request(input.spec, input.decisions, input.drift_decision)?;
    let response: EntitySchemaPreviewResponse = runtime.with_session(|session| {
        call_command(session, scaffold::ENTITY_SCHEMA_PREVIEW_COMMAND, &request)
    })?;
    Ok(runtime.preview_output(response))
}

fn apply(runtime: &ToolCall<'_>, input: ApplyInput) -> Result<WriteOutput, CliError> {
    if input.revision.trim().is_empty() {
        return Err(CliError::fail("revision is required"));
    }
    let preview = runtime.preview_request(input.spec, input.decisions, input.drift_decision)?;
    let request = EntitySchemaApplyRequest {
        preview,
        revision: input.revision.clone(),
        allow_destructive: input.allow_destructive,
    };
    runtime.with_session(|session| {
        let response: EntitySchemaApplyResponse =
            call_command(session, scaffold::ENTITY_SCHEMA_APPLY_COMMAND, &request)?;
        runtime.apply_entity_schema_response(response)
    })
}

fn reconcile(runtime: &ToolCall<'_>, input: ReconcileInput) -> Result<WriteOutput, CliError> {
    let directory_uri = runtime.input_uri(&input.directory, true)?;
    runtime.with_session(|session| {
        let response: EntitySchemaApplyResponse = call_command(
            session,
            scaffold::ENTITY_SCHEMA_RECONCILE_COMMAND,
            &EntitySchemaReconcileRequest {
                directory_uri: directory_uri.clone(),
                selected_leaf: input.selected_leaf.clone(),
            },
        )?;
        runtime.apply_entity_schema_response(response)
    })
}

pub(crate) fn compact_diff(diff: &es::SchemaDiff) -> DiffSummary {
    let mut result = DiffSummary {
        rename_questions: diff.rename_questions.clone(),
        entity_rename_questions: diff.entity_rename_questions.clone(),
        ..DiffSummary::default()
    };
    result.created_entities = diff
        .created_entities
        .iter()
        .map(|entity| entity.name.clone())
        .collect();
    result.removed_entities = diff
        .removed_entities
        .iter()
        .map(|entity| entity.name.clone())
        .collect();
    result.added_columns = diff
        .added_columns
        .iter()
        .filter_map(|change| {
            change
                .after
                .as_ref()
                .map(|after| format!("{}.{}", change.entity, after.name))
        })
        .collect();
    result.removed_columns = diff
        .removed_columns
        .iter()
        .filter_map(|change| {
            change
                .before
                .as_ref()
                .map(|before| format!("{}.{}", change.entity, before.name))
        })
        .collect();
    result.changed_columns = diff
        .changed_columns
        .iter()
        .map(|change| {
            let name = change
                .after
                .as_ref()
                .or(change.before.as_ref())
                .map(|column| column.name.clone())
                .unwrap_or_default();
            format!("{}.{name}", change.entity)
        })
        .collect();
    result.added_indexes = diff
        .added_indexes
        .iter()
        .map(|change| format!("{}.{}", change.entity, change.index.name))
        .collect();
    result.removed_indexes = diff
        .removed_indexes
        .iter()
        .map(|change| format!("{}.{}", change.entity, change.index.name))
        .collect();
    result.added_foreign_keys = diff
        .added_foreign_keys
        .iter()
        .map(|change| format!("{}.{}", change.entity, change.foreign_key.name))
        .collect();
    result.removed_foreign_keys = diff
        .removed_foreign_keys
        .iter()
        .map(|change| format!("{}.{}", change.entity, change.foreign_key.name))
        .collect();
    result.changed_primary_keys = diff
        .changed_primary_keys
        .iter()
        .map(|change| change.entity.clone())
        .collect();
    result
}

impl McpRuntime {
    /// A validated workspace path as a file URI (empty when optional and
    /// not given).
    fn input_uri(&self, value: &str, required: bool) -> Result<String, CliError> {
        if value.trim().is_empty() {
            if required {
                return Err(CliError::fail("path is required"));
            }
            return Ok(String::new());
        }
        let path = self.resolve_path(value)?;
        Ok(swlsp_uriutil::file_uri(&path))
    }

    fn preview_request(
        &self,
        mut spec: EntitySpec,
        decisions: Vec<Decision>,
        drift_decision: String,
    ) -> Result<EntitySchemaPreviewRequest, CliError> {
        for (name, target, required) in [
            ("spec.pluginRootUri", &mut spec.plugin_root_uri, true),
            ("spec.directoryUri", &mut spec.directory_uri, true),
            ("spec.definitionUri", &mut spec.definition_uri, false),
            ("spec.entityUri", &mut spec.entity_uri, false),
            ("spec.collectionUri", &mut spec.collection_uri, false),
            ("spec.serviceUri", &mut spec.service_uri, false),
        ] {
            *target = self
                .input_uri(target, required)
                .map_err(|error| CliError::fail(format!("{name}: {}", error.message)))?;
        }
        if let Some(translation) = spec.translation.as_mut() {
            for (name, target) in [
                (
                    "spec.translation.definitionUri",
                    &mut translation.definition_uri,
                ),
                ("spec.translation.entityUri", &mut translation.entity_uri),
                (
                    "spec.translation.collectionUri",
                    &mut translation.collection_uri,
                ),
            ] {
                *target = self
                    .input_uri(target, false)
                    .map_err(|error| CliError::fail(format!("{name}: {}", error.message)))?;
            }
        }
        Ok(EntitySchemaPreviewRequest {
            spec,
            decisions,
            drift_decision,
            ..EntitySchemaPreviewRequest::default()
        })
    }

    fn apply_entity_schema_response(
        &self,
        response: EntitySchemaApplyResponse,
    ) -> Result<WriteOutput, CliError> {
        let (files, diff) = apply_mcp_workspace_edit(self, response.edit.as_ref(), true)?;
        Ok(WriteOutput {
            applied: true,
            primary_file: self.output_uri(&response.primary_file_uri),
            snapshot_id: response.snapshot_id,
            files,
            diff,
        })
    }

    fn normalize_bootstrap(&self, output: &mut EntitySchemaBootstrapResponse) {
        output.plugin.root_uri = self.output_uri(&output.plugin.root_uri);
        output.plugin.source_root_uri = self.output_uri(&output.plugin.source_root_uri);
        for uri in &mut output.plugin.service_uris {
            *uri = self.output_uri(uri);
        }
        self.normalize_spec(&mut output.spec);
        self.normalize_relation_targets(&mut output.existing);
        for target in &mut output.editable {
            if !target.file_uri.is_empty() {
                target.file_uri = self.output_uri(&target.file_uri);
            }
        }
    }

    fn normalize_relation_targets(&self, targets: &mut [EntitySchemaRelationTarget]) {
        for target in targets {
            if !target.file_uri.is_empty() {
                target.file_uri = self.output_uri(&target.file_uri);
            }
        }
    }

    fn normalize_spec(&self, spec: &mut EntitySpec) {
        for target in [
            &mut spec.plugin_root_uri,
            &mut spec.directory_uri,
            &mut spec.definition_uri,
            &mut spec.entity_uri,
            &mut spec.collection_uri,
            &mut spec.service_uri,
        ] {
            if !target.is_empty() {
                *target = self.output_uri(target);
            }
        }
        if let Some(translation) = spec.translation.as_mut() {
            for target in [
                &mut translation.definition_uri,
                &mut translation.entity_uri,
                &mut translation.collection_uri,
            ] {
                if !target.is_empty() {
                    *target = self.output_uri(target);
                }
            }
        }
    }

    fn preview_output(&self, preview: EntitySchemaPreviewResponse) -> PreviewOutput {
        let mut output = PreviewOutput {
            status: String::new(),
            ready_to_apply: false,
            next_action: String::new(),
            revision: preview.revision.clone(),
            files: preview
                .files
                .iter()
                .map(|file| PreviewFile {
                    path: self.output_uri(&file.uri),
                    action: file.action.clone(),
                    language: file.language.clone(),
                    size_bytes: file.after.len(),
                })
                .collect(),
            issues: preview.issues.clone(),
            diff: compact_diff(&preview.diff),
            destructive: preview.destructive,
            requires_destructive_confirmation: preview.destructive,
            drift: preview.drift,
            drift_message: preview.drift_message.clone(),
            snapshot_id: preview.snapshot_id.clone(),
            primary_file: if preview.primary_file_uri.is_empty() {
                String::new()
            } else {
                self.output_uri(&preview.primary_file_uri)
            },
            migration_timestamp: preview.migration_timestamp,
        };
        let (status, ready, next) = if !preview.issues.is_empty() {
            (
                "needs-input",
                false,
                "Resolve every reported issue or rename question, then call shopware_entity_schema_preview again. Do not apply this result.",
            )
        } else if preview.drift {
            (
                "needs-input",
                false,
                "Choose driftDecision=adopt or driftDecision=migrate as described, then call shopware_entity_schema_preview again. Do not apply this result.",
            )
        } else if preview.revision.is_empty() {
            (
                "blocked",
                false,
                "The preview did not produce an applicable revision. Resolve the reported state and preview again.",
            )
        } else if preview.destructive {
            (
                "requires-destructive-confirmation",
                true,
                "Obtain explicit user confirmation, then call shopware_entity_schema_apply once with the unchanged spec, this exact revision, and allowDestructive=true. Do not preview again or inspect temporary payload files.",
            )
        } else {
            (
                "ready",
                true,
                "Call shopware_entity_schema_apply once with the unchanged spec and this exact revision. Do not preview again or inspect temporary payload files.",
            )
        };
        output.status = status.into();
        output.ready_to_apply = ready;
        output.next_action = next.into();
        output
    }
}
