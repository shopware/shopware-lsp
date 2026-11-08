//! LSP wire types used by the Shopware language server (port of
//! `internal/lsp/protocol`).
//!
//! Standard value types (`Position`, `Range`, `MarkupContent`, the numeric
//! enumerations such as `SymbolKind`) come from the [`lsp_types`] crate,
//! which is re-exported. The message shapes are defined here because the Go
//! server deliberately deviates from the specification-complete
//! `lsp-types` structures: URIs are plain strings, `documentChanges` uses one
//! compact union record, completion items carry untyped edits, and custom
//! Shopware extensions (initialization options, input requests, errors) are
//! included. Every type serializes to exactly the Go JSON shape (`omitempty`
//! fields are skipped when empty) and decodes leniently like Go: missing and
//! `null` fields take their zero value and unknown fields are ignored.

use std::collections::BTreeMap;

use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

pub use lsp_types;
pub use lsp_types::{
    CodeActionKind, CompletionItemKind, CompletionTriggerKind, DiagnosticSeverity, DiagnosticTag,
    DocumentHighlightKind, FileChangeType, InlayHintKind, InsertTextFormat, MarkupContent,
    MarkupKind, Position, Range, SymbolKind,
};

/// Go JSON decoding leaves a field at its zero value when the input is null.
fn nullable<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

fn is_false(value: &bool) -> bool {
    !*value
}

fn is_zero(value: &i32) -> bool {
    *value == 0
}

// ---------------------------------------------------------------------------
// Shared identifiers (definition.go, hierarchy.go)

#[derive(Debug, Clone, PartialEq, Eq, Hash, Default, Serialize, Deserialize)]
pub struct TextDocumentIdentifier {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
}

impl TextDocumentIdentifier {
    pub fn new(uri: impl Into<String>) -> Self {
        Self { uri: uri.into() }
    }
}

/// A location in a document. The URI stays a plain string like Go.
#[derive(Debug, Clone, PartialEq, Eq, Hash, Default, Serialize, Deserialize)]
pub struct Location {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default)]
    pub range: Range,
}

/// Parameters of `textDocument/definition`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DefinitionParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
}

/// Identifies the declaration or reference whose concrete implementations
/// are requested.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ImplementationParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
}

pub type PrepareTypeHierarchyParams = ImplementationParams;
pub type CallHierarchyPrepareParams = ImplementationParams;
pub type LinkedEditingRangeParams = ImplementationParams;
pub type DocumentHighlightParams = ImplementationParams;

/// The LSP representation of a class-like declaration.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TypeHierarchyItem {
    #[serde(default, deserialize_with = "nullable")]
    pub name: String,
    pub kind: SymbolKind,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub detail: String,
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default)]
    pub range: Range,
    #[serde(default)]
    pub selection_range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct TypeHierarchySupertypesParams {
    pub item: TypeHierarchyItem,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct TypeHierarchySubtypesParams {
    pub item: TypeHierarchyItem,
}

// ---------------------------------------------------------------------------
// Call hierarchy (call_hierarchy.go)

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CallHierarchyItem {
    #[serde(default, deserialize_with = "nullable")]
    pub name: String,
    pub kind: SymbolKind,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub tags: Vec<i32>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub detail: String,
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default)]
    pub range: Range,
    #[serde(default)]
    pub selection_range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CallHierarchyIncomingCallsParams {
    pub item: CallHierarchyItem,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CallHierarchyOutgoingCallsParams {
    pub item: CallHierarchyItem,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CallHierarchyIncomingCall {
    pub from: CallHierarchyItem,
    #[serde(default, deserialize_with = "nullable")]
    pub from_ranges: Vec<Range>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CallHierarchyOutgoingCall {
    pub to: CallHierarchyItem,
    #[serde(default, deserialize_with = "nullable")]
    pub from_ranges: Vec<Range>,
}

// ---------------------------------------------------------------------------
// Code actions and workspace edits (codeaction.go)

/// Parameters of `textDocument/codeAction`.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeActionParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub range: Range,
    #[serde(default, deserialize_with = "nullable")]
    pub context: CodeActionContext,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct CodeActionContext {
    #[serde(default, deserialize_with = "nullable")]
    pub diagnostics: Vec<Diagnostic>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub only: Vec<String>,
}

/// Go `CodeActionSourceFixAll` and friends are the `lsp_types` constants
/// (`CodeActionKind::QUICKFIX`, `CodeActionKind::SOURCE_FIX_ALL`, ...).
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeAction {
    #[serde(default, deserialize_with = "nullable")]
    pub title: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<CodeActionKind>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub diagnostics: Vec<Diagnostic>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub is_preferred: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disabled: Option<CodeActionDisabled>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub edit: Option<WorkspaceEdit>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub command: Option<CommandAction>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct CodeActionDisabled {
    #[serde(default, deserialize_with = "nullable")]
    pub reason: String,
}

/// A command to be executed.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct CommandAction {
    #[serde(default, deserialize_with = "nullable")]
    pub title: String,
    #[serde(default, deserialize_with = "nullable")]
    pub command: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub arguments: Vec<Value>,
}

/// A command reference (code lenses, inlay hint label parts).
pub type Command = CommandAction;

/// A text edit. `insertTextFormat` is a Shopware extension used by snippet
/// edits.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TextEdit {
    #[serde(default)]
    pub range: Range,
    #[serde(default, deserialize_with = "nullable")]
    pub new_text: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub insert_text_format: Option<InsertTextFormat>,
}

impl TextEdit {
    pub fn new(range: Range, new_text: impl Into<String>) -> Self {
        Self {
            range,
            new_text: new_text.into(),
            insert_text_format: None,
        }
    }
}

/// A workspace edit. `changes` is ordered by URI like Go's map encoding.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkspaceEdit {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub changes: BTreeMap<String, Vec<TextEdit>>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub document_changes: Vec<DocumentChange>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub change_annotations: BTreeMap<String, ChangeAnnotation>,
}

pub const CREATE_FILE_OPERATION: &str = "create";
pub const DELETE_FILE_OPERATION: &str = "delete";

/// One `documentChanges` entry. LSP models documentChanges as a union; one
/// compact transport type keeps create operations and text edits ordered.
/// Text-document edits use `text_document`/`edits`/`annotation_id`; resource
/// operations use `kind`/`uri`/`options`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentChange {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text_document: Option<OptionalVersionedTextDocumentIdentifier>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub edits: Vec<TextEdit>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub annotation_id: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub kind: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub uri: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub options: Option<ResourceOperationOptions>,
}

/// The union of create/delete resource-operation options.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ResourceOperationOptions {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub overwrite: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub ignore_if_exists: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub recursive: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub ignore_if_not_exists: bool,
}

pub type CreateFileOptions = ResourceOperationOptions;
pub type DeleteFileOptions = ResourceOperationOptions;

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChangeAnnotation {
    #[serde(default, deserialize_with = "nullable")]
    pub label: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub needs_confirmation: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub description: String,
}

/// A text document identifier with an optional version (`null` when
/// unversioned).
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct OptionalVersionedTextDocumentIdentifier {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default)]
    pub version: Option<i32>,
}

// ---------------------------------------------------------------------------
// Code lenses (codelens.go)

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeLensParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub partial_result_token: Option<Value>,
}

/// A command shown along with source text.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct CodeLens {
    #[serde(default)]
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub command: Option<Command>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

// ---------------------------------------------------------------------------
// Colors (color.go). Components are `f64` like Go (`lsp_types` uses `f32`).

/// Normalized RGBA components in the inclusive range [0, 1].
#[derive(Debug, Clone, Copy, PartialEq, Default, Serialize, Deserialize)]
pub struct Color {
    #[serde(default, deserialize_with = "nullable")]
    pub red: f64,
    #[serde(default, deserialize_with = "nullable")]
    pub green: f64,
    #[serde(default, deserialize_with = "nullable")]
    pub blue: f64,
    #[serde(default, deserialize_with = "nullable")]
    pub alpha: f64,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentColorParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
}

#[derive(Debug, Clone, Copy, PartialEq, Default, Serialize, Deserialize)]
pub struct ColorInformation {
    #[serde(default)]
    pub range: Range,
    #[serde(default, deserialize_with = "nullable")]
    pub color: Color,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ColorPresentationParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, deserialize_with = "nullable")]
    pub color: Color,
    #[serde(default)]
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ColorPresentation {
    #[serde(default, deserialize_with = "nullable")]
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text_edit: Option<TextEdit>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub additional_text_edits: Vec<TextEdit>,
}

// ---------------------------------------------------------------------------
// Custom commands and input requests (commands.go)

/// A custom `workspace/executeCommand` style request.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct CommandRequest {
    #[serde(default, deserialize_with = "nullable")]
    pub command: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub arguments: Vec<Value>,
}

/// Parameters of the Shopware request-input request.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RequestInputParams {
    #[serde(default, deserialize_with = "nullable")]
    pub prompt: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub place_holder: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub default_value: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct RequestInputResponse {
    #[serde(default, deserialize_with = "nullable")]
    pub value: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct MultipleInputsParams {
    #[serde(default, deserialize_with = "nullable")]
    pub items: Vec<RequestInputParams>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct MultipleInputsResponse {
    #[serde(default, deserialize_with = "nullable")]
    pub values: Vec<String>,
}

// ---------------------------------------------------------------------------
// Initialization and completion (completion.go)

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CompletionList {
    #[serde(default, deserialize_with = "nullable")]
    pub is_incomplete: bool,
    #[serde(default, deserialize_with = "nullable")]
    pub items: Vec<CompletionItem>,
}

/// Parameters of the `initialize` request.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InitializeParams {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub root_path: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub root_uri: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub workspace_folders: Vec<WorkspaceFolder>,
    #[serde(default, deserialize_with = "nullable")]
    pub initialization_options: InitializationOptions,
    #[serde(default, deserialize_with = "nullable")]
    pub capabilities: ClientCapabilities,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ClientCapabilities {
    #[serde(default, deserialize_with = "nullable")]
    pub workspace: WorkspaceClientCapabilities,
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentClientCapabilities,
    #[serde(default, deserialize_with = "nullable")]
    pub window: WindowClientCapabilities,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct WorkspaceClientCapabilities {
    #[serde(default, deserialize_with = "nullable")]
    pub diagnostics: DiagnosticWorkspaceClientCapabilities,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiagnosticWorkspaceClientCapabilities {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub refresh_support: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WindowClientCapabilities {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub work_done_progress: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TextDocumentClientCapabilities {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub diagnostic: Option<DiagnosticClientCapabilities>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code_action: Option<CodeActionClientCapabilities>,
}

/// Records presence of pull diagnostic support. No optional document
/// diagnostic capabilities are needed by the server yet.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DiagnosticClientCapabilities {}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CodeActionClientCapabilities {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub data_support: bool,
    #[serde(default, deserialize_with = "nullable")]
    pub resolve_support: CodeActionResolveSupport,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct CodeActionResolveSupport {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub properties: Vec<String>,
}

/// Shopware-specific `initializationOptions`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InitializationOptions {
    #[serde(
        rename = "phpExtensions",
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub php_extensions: Vec<String>,
    #[serde(
        rename = "disabledPhpExtensions",
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub disabled_php_extensions: Vec<String>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub shopware_target_version: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub configuration: Option<swlsp_projectconfig::Partial>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub allow_unsupported_project: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub omit_execute_command_provider: bool,
    #[serde(
        rename = "cliMode",
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub cli_mode: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub watch_files: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shopware_client: Option<ShopwareClientOptions>,
}

/// Negotiates optional integration behavior without changing the default
/// presentation used by legacy LSP clients.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ShopwareClientOptions {
    #[serde(default, deserialize_with = "nullable")]
    pub protocol_version: i32,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub presentation_profile: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub supported_commands: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct WorkspaceFolder {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default, deserialize_with = "nullable")]
    pub name: String,
}

/// Parameters of `textDocument/completion`.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CompletionParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
    #[serde(default, deserialize_with = "nullable")]
    pub context: CompletionContext,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub partial_result_token: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CompletionContext {
    /// A [`CompletionTriggerKind`] value.
    #[serde(default, deserialize_with = "nullable")]
    pub trigger_kind: i32,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub trigger_character: String,
}

/// A completion item. `kind` and `insert_text_format` are omitted when
/// unset; `documentation` is omitted unless set (an empty documentation
/// object is not a valid `MarkupContent`).
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CompletionItem {
    #[serde(default, deserialize_with = "nullable")]
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<CompletionItemKind>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub tags: Vec<i32>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub detail: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub documentation: Option<MarkupContent>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub deprecated: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub preselect: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub sort_text: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub filter_text: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub insert_text: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub insert_text_format: Option<InsertTextFormat>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub insert_text_mode: i32,
    // Typed rather than `Value`: Go only ever stores `TextEdit` here, and a
    // JSON tree per edit cost ~2.5 KB vs ~80 B on large completion lists.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text_edit: Option<TextEdit>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub additional_text_edits: Vec<TextEdit>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub command: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub text_edit_text: String,
}

// ---------------------------------------------------------------------------
// Diagnostics (diagnostics.go)

/// Parameters of `textDocument/diagnostic`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiagnosticParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub previous_result_id: String,
}

/// A full report returned by `textDocument/diagnostic`.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiagnosticResult {
    #[serde(default, deserialize_with = "nullable")]
    pub kind: String,
    #[serde(default, deserialize_with = "nullable")]
    pub items: Vec<Diagnostic>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub result_id: String,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Diagnostic {
    #[serde(default)]
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub severity: Option<DiagnosticSeverity>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<Value>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub source: String,
    #[serde(default, deserialize_with = "nullable")]
    pub message: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub tags: Vec<DiagnosticTag>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub related_information: Vec<DiagnosticRelatedInformation>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DiagnosticRelatedInformation {
    #[serde(default, deserialize_with = "nullable")]
    pub location: Location,
    #[serde(default, deserialize_with = "nullable")]
    pub message: String,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct PublishDiagnosticsParams {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub version: i32,
    #[serde(default, deserialize_with = "nullable")]
    pub diagnostics: Vec<Diagnostic>,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentDiagnosticReport {
    #[serde(default, deserialize_with = "nullable")]
    pub kind: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub result_id: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub items: Vec<Diagnostic>,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct WorkspaceDiagnosticReport {
    #[serde(default, deserialize_with = "nullable")]
    pub items: Vec<WorkspaceDocumentDiagnosticReport>,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct WorkspaceDocumentDiagnosticReport {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(default, deserialize_with = "nullable")]
    pub version: i32,
    #[serde(default, deserialize_with = "nullable")]
    pub report: DocumentDiagnosticReport,
}

// ---------------------------------------------------------------------------
// Document highlights, links and symbols

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub struct DocumentHighlight {
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<DocumentHighlightKind>,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentLinkParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub partial_result_token: Option<Value>,
}

/// A clickable source range. `target` is omitted when a client must call
/// `documentLink/resolve`; Shopware LSP currently resolves eagerly.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct DocumentLink {
    #[serde(default)]
    pub range: Range,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub target: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub tooltip: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

pub type DocumentSymbolParams = DocumentLinkParams;

/// One hierarchical declaration. `range` spans the complete declaration
/// while `selection_range` identifies its name.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentSymbol {
    #[serde(default, deserialize_with = "nullable")]
    pub name: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub detail: String,
    pub kind: SymbolKind,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub deprecated: bool,
    #[serde(default)]
    pub range: Range,
    #[serde(default)]
    pub selection_range: Range,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub children: Vec<DocumentSymbol>,
}

// ---------------------------------------------------------------------------
// Errors (error.go)

/// A Shopware-specific error payload.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct ShopwareLspError {
    #[serde(default, deserialize_with = "nullable")]
    pub code: String,
    #[serde(default, deserialize_with = "nullable")]
    pub message: String,
}

/// Go `NewLspError` (note the message-first argument order).
pub fn new_lsp_error(message: impl Into<String>, code: impl Into<String>) -> ShopwareLspError {
    ShopwareLspError {
        code: code.into(),
        message: message.into(),
    }
}

// ---------------------------------------------------------------------------
// File synchronization and file operations (file_sync.go)

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileOperationRegistrationOptions {
    #[serde(default, deserialize_with = "nullable")]
    pub filters: Vec<FileOperationFilter>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileOperationFilter {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub scheme: String,
    #[serde(default, deserialize_with = "nullable")]
    pub pattern: FileOperationPattern,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileOperationPattern {
    #[serde(default, deserialize_with = "nullable")]
    pub glob: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub matches: String,
    #[serde(default, deserialize_with = "nullable")]
    pub options: FileOperationPatternOptions,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileOperationPatternOptions {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub ignore_case: bool,
}

/// A watched-file event. `kind` holds a [`FileChangeType`] value.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileEvent {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
    #[serde(rename = "type", default, deserialize_with = "nullable")]
    pub kind: i32,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DidChangeWatchedFilesParams {
    #[serde(default, deserialize_with = "nullable")]
    pub changes: Vec<FileEvent>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DidChangeWatchedFilesRegistrationOptions {
    #[serde(default, deserialize_with = "nullable")]
    pub watchers: Vec<FileSystemWatcher>,
}

/// A file system watcher. `kind` is a bit set of the `WATCH_*` constants.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileSystemWatcher {
    #[serde(default, deserialize_with = "nullable")]
    pub glob_pattern: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub kind: i32,
}

pub const WATCH_CREATE: i32 = 1;
pub const WATCH_CHANGE: i32 = 2;
pub const WATCH_DELETE: i32 = 4;

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileOperationOptions {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub did_create: Option<FileOperationRegistrationOptions>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub will_create: Option<FileOperationRegistrationOptions>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub did_rename: Option<FileOperationRegistrationOptions>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub will_rename: Option<FileOperationRegistrationOptions>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub did_delete: Option<FileOperationRegistrationOptions>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub will_delete: Option<FileOperationRegistrationOptions>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct CreateFilesParams {
    #[serde(default, deserialize_with = "nullable")]
    pub files: Vec<FileCreate>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileCreate {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct RenameFilesParams {
    #[serde(default, deserialize_with = "nullable")]
    pub files: Vec<FileRename>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileRename {
    #[serde(default, deserialize_with = "nullable")]
    pub old_uri: String,
    #[serde(default, deserialize_with = "nullable")]
    pub new_uri: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct DeleteFilesParams {
    #[serde(default, deserialize_with = "nullable")]
    pub files: Vec<FileDelete>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FileDelete {
    #[serde(default, deserialize_with = "nullable")]
    pub uri: String,
}

// ---------------------------------------------------------------------------
// Folding, formatting, hover, inlay hints

pub const FOLDING_RANGE_KIND_COMMENT: &str = "comment";
pub const FOLDING_RANGE_KIND_IMPORTS: &str = "imports";
pub const FOLDING_RANGE_KIND_REGION: &str = "region";

pub type FoldingRangeParams = DocumentColorParams;

/// Line based unless the optional character positions are set.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FoldingRange {
    #[serde(default, deserialize_with = "nullable")]
    pub start_line: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub start_character: Option<u32>,
    #[serde(default, deserialize_with = "nullable")]
    pub end_line: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub end_character: Option<u32>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub kind: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub collapsed_text: String,
}

/// Parameters of `textDocument/formatting`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DocumentFormattingParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, deserialize_with = "nullable")]
    pub options: FormattingOptions,
}

/// The standard LSP document formatting options.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FormattingOptions {
    #[serde(default, deserialize_with = "nullable")]
    pub tab_size: u32,
    #[serde(default, deserialize_with = "nullable")]
    pub insert_spaces: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub trim_trailing_whitespace: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub insert_final_newline: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub trim_final_newlines: bool,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HoverParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
}

/// The result of a hover request.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Hover {
    pub contents: MarkupContent,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub range: Option<Range>,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InlayHintParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
}

/// An inlay hint. `label` is a string or an array of
/// [`InlayHintLabelPart`] values.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InlayHint {
    #[serde(default)]
    pub position: Position,
    #[serde(default)]
    pub label: Value,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<InlayHintKind>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub tooltip: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub padding_left: bool,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub padding_right: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

/// A clickable segment of an inlay label.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
pub struct InlayHintLabelPart {
    #[serde(default, deserialize_with = "nullable")]
    pub value: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub tooltip: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub location: Option<Location>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub command: Option<Command>,
}

/// Document ranges an editor should update together.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LinkedEditingRanges {
    #[serde(default, deserialize_with = "nullable")]
    pub ranges: Vec<Range>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub word_pattern: String,
}

// ---------------------------------------------------------------------------
// References, rename, selection ranges

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReferenceParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
    #[serde(default, deserialize_with = "nullable")]
    pub context: ReferenceContext,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReferenceContext {
    #[serde(default, deserialize_with = "nullable")]
    pub include_declaration: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RenameParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
    #[serde(default, deserialize_with = "nullable")]
    pub new_name: String,
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SelectionRangeParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default, deserialize_with = "nullable")]
    pub positions: Vec<Position>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub work_done_token: Option<Value>,
}

/// One nested source selection. `parent` must contain `range`.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct SelectionRange {
    #[serde(default)]
    pub range: Range,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent: Option<Box<SelectionRange>>,
}

// ---------------------------------------------------------------------------
// Semantic tokens (semantic_tokens.go)

pub type SemanticTokensParams = DocumentLinkParams;

/// The relative, delta-encoded token stream defined by LSP.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SemanticTokens {
    #[serde(
        rename = "resultId",
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub result_id: String,
    #[serde(default, deserialize_with = "nullable")]
    pub data: Vec<u32>,
}

/// Maps numeric token types and modifier bit positions to their names.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SemanticTokensLegend {
    #[serde(default, deserialize_with = "nullable")]
    pub token_types: Vec<String>,
    #[serde(default, deserialize_with = "nullable")]
    pub token_modifiers: Vec<String>,
}

pub const SEMANTIC_TOKEN_KEYWORD: u32 = 0;
pub const SEMANTIC_TOKEN_PROPERTY: u32 = 1;
pub const SEMANTIC_TOKEN_TYPE: u32 = 2;
pub const SEMANTIC_TOKEN_VARIABLE: u32 = 3;
pub const SEMANTIC_TOKEN_STRING: u32 = 4;
pub const SEMANTIC_TOKEN_NUMBER: u32 = 5;
pub const SEMANTIC_TOKEN_CLASS: u32 = 6;
pub const SEMANTIC_TOKEN_FUNCTION: u32 = 7;
pub const SEMANTIC_TOKEN_OPERATOR: u32 = 8;

/// Token type names indexed by the `SEMANTIC_TOKEN_*` constants.
pub const SEMANTIC_TOKEN_TYPES: &[&str] = &[
    "keyword", "property", "type", "variable", "string", "number", "class", "function", "operator",
];

pub const SEMANTIC_TOKEN_MODIFIERS: &[&str] = &[];

impl SemanticTokensLegend {
    /// The server's legend.
    pub fn shopware() -> Self {
        Self {
            token_types: SEMANTIC_TOKEN_TYPES
                .iter()
                .map(|value| (*value).to_owned())
                .collect(),
            token_modifiers: SEMANTIC_TOKEN_MODIFIERS
                .iter()
                .map(|value| (*value).to_owned())
                .collect(),
        }
    }
}

// ---------------------------------------------------------------------------
// Signature help (signature_help.go)

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignatureHelpParams {
    #[serde(default, deserialize_with = "nullable")]
    pub text_document: TextDocumentIdentifier,
    #[serde(default)]
    pub position: Position,
    #[serde(default, deserialize_with = "nullable")]
    pub context: SignatureHelpContext,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignatureHelpContext {
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub trigger_kind: i32,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub trigger_character: String,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_false"
    )]
    pub is_retrigger: bool,
    #[serde(
        rename = "activeSignatureHelp",
        default,
        skip_serializing_if = "Option::is_none"
    )]
    pub active_help: Option<SignatureHelp>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignatureHelp {
    #[serde(default, deserialize_with = "nullable")]
    pub signatures: Vec<SignatureInformation>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub active_signature: i32,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub active_parameter: i32,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignatureInformation {
    #[serde(default, deserialize_with = "nullable")]
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub documentation: Option<MarkupContent>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub parameters: Vec<ParameterInformation>,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "is_zero"
    )]
    pub active_parameter: i32,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct ParameterInformation {
    #[serde(default, deserialize_with = "nullable")]
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub documentation: Option<MarkupContent>,
}

// ---------------------------------------------------------------------------
// Workspace symbols (symbol.go)

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct WorkspaceSymbolParams {
    #[serde(default, deserialize_with = "nullable")]
    pub query: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SymbolInformation {
    #[serde(default, deserialize_with = "nullable")]
    pub name: String,
    pub kind: SymbolKind,
    #[serde(default, deserialize_with = "nullable")]
    pub location: Location,
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "String::is_empty"
    )]
    pub container_name: String,
    /// An internal ranking hint that is intentionally not sent over LSP.
    /// Persisted catalogs use it to break equally relevant text matches.
    #[serde(skip)]
    pub priority: i32,
}

#[cfg(test)]
mod tests;

#[cfg(test)]
mod float_roundtrip_tests {
    /// Values such as color channels must survive a JSON round trip
    /// bit-for-bit (serde_json's default parser can be one ulp off).
    #[test]
    fn floats_round_trip_exactly() {
        for channel in 0..=255u32 {
            let value = f64::from(channel) / 255.0;
            let text = serde_json::to_string(&value).unwrap();
            let parsed: f64 = serde_json::from_str(&text).unwrap();
            assert_eq!(
                parsed.to_bits(),
                value.to_bits(),
                "{channel}/255 via {text}"
            );
        }
    }
}
