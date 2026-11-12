//! The stdio MCP server on the official Rust SDK (`rmcp`).
//!
//! `rmcp` owns the protocol: framing, version negotiation, the
//! initialization gate, cancellation and pagination. This module supplies
//! the tools: their definitions come from `tools.json`, filtered by the
//! configuration's MCP tool switches, so the handler lists and dispatches
//! them by hand instead of through `rmcp`'s macros. Arguments are validated
//! against the advertised input schemas before a tool runs; tools run on a
//! blocking thread against the shared, lazily indexed `CliSession`.

pub(crate) mod scaffold;
mod schema;
mod tools;

use std::io::Read;
use std::sync::{Arc, Mutex};

use rmcp::model::{
    CallToolRequestParams, CallToolResponse, CallToolResult, ContentBlock, Implementation,
    ListToolsResult, PaginatedRequestParams, ServerCapabilities, ServerConfig,
};
use rmcp::service::RequestContext;
use rmcp::{ErrorData, RoleServer, ServerHandler, ServiceExt};
use serde::Serialize;
use serde_json::{Value, json};
use swlsp_lsp::async_io::{self, FrameWriter, Input, Output as StreamOutput};
use swlsp_projectconfig::{Effective, Partial};

use crate::session::{CliSession, SessionOptions};
use crate::{Cancel, CliError, Output, Runner};

pub(crate) use schema::{ToolDefinition, tool_definitions};

pub(crate) const INSTRUCTIONS: &str = "Use Shopware MCP generators instead of manually creating supported files. \
For plugin/artifact requests always use shopware_scaffold with family=\"shopware\", kind=\"plugin\" for plugins. \
For DAL entity, definition, mapping, EntityExtension, or BulkEntityExtension requests, always run shopware_entity_schema_bootstrap, optionally field-types/search/load, then shopware_entity_schema_preview and shopware_entity_schema_apply; never handwrite entity PHP, services, migrations, or snapshots, and do not use shopware_scaffold for entities. \
Bootstrap is intentionally compact. Call shopware_entity_schema_field_types to list field ids or fetch one exact copyable template; never inspect content.json or temporary MCP payload files. \
Only choose definition kinds advertised by bootstrap.definitionKinds. Set spec.definitionKind=\"mapping\" for MappingEntityDefinition requests, spec.definitionKind=\"extension\" plus an indexed extendedDefinitionClass/entityName for EntityExtension requests, or spec.definitionKind=\"bulk-extension\" with one bulkExtensions entry per indexed target. \
Each bulk target has its own entityName, extendedDefinitionClass, fields, and indexes; leave top-level fields/indexes empty. A loaded collectMethodRaw is losslessly preserved and must not be changed or combined with structured targets. \
Extension indexes may combine target fields returned by search with new fields, but must include at least one column owned by that extension target. Copy the exact field-types template for specialized fields; do not invent implementation metadata. \
An ordinary scalar field needs id, kind, propertyName, storageName, and editable=true; set translated=true for translation storage. Normal entities inherit createdAt and updatedAt and must not add them. \
Use one kind=\"hierarchy\" field for native parent/children trees. For variant-style inheritance set spec.inheritanceAware=true and use typed inherited/associationInherited/translationInherited/reverseInheritedProperty values. \
Use spec.definitionBehavior for aggregate parents and awareness/default/base-field behavior, and spec.definitionMetadata for since/defaults/child defaults/hydrators. Preserve loaded *MethodRaw values exactly. \
An explicit create/edit request permits non-destructive apply after a clean preview; destructive changes require explicit confirmation. The plugin parent directory is usually \"custom/plugins\". Paths may be workspace-relative or absolute, and positions use one-based lines and columns. List a code action before applying it.";

const EDITOR_CONFIGURATION_ENV: &str = "SHOPWARE_LSP_EDITOR_CONFIGURATION";

/// The workspace-scoped MCP runtime. The production session is created on
/// the first tool call that needs it and serialized by `session`.
pub(crate) struct McpRuntime {
    pub root: String,
    pub editor_configuration: Option<Partial>,
    pub configuration: Effective,
    cancel: Cancel,
    version: String,
    err_out: Output,
    allow_unsupported_project: bool,
    /// Why the server offers no tools: the root is no Shopware or Symfony
    /// project, or its configuration is invalid. Clients start an MCP
    /// server for every project, so it has to start in every project.
    inactive: Option<String>,
    session: Mutex<Option<McpSession>>,
}

/// One tool call: the runtime and the call's cancellation, which every
/// session request of the call uses. Other runtime members are reached
/// through `Deref`.
pub(crate) struct ToolCall<'a> {
    runtime: &'a McpRuntime,
    cancel: &'a Cancel,
}

impl<'a> ToolCall<'a> {
    pub fn new(runtime: &'a McpRuntime, cancel: &'a Cancel) -> Self {
        Self { runtime, cancel }
    }

    /// [`McpRuntime::with_session`] with this call's cancellation.
    pub fn with_session<T>(
        &self,
        operation: impl FnOnce(&CliSession) -> Result<T, CliError>,
    ) -> Result<T, CliError> {
        self.runtime.with_session(self.cancel, operation)
    }
}

impl std::ops::Deref for ToolCall<'_> {
    type Target = McpRuntime;

    fn deref(&self) -> &McpRuntime {
        self.runtime
    }
}

/// The shared session and whether its first index completed.
struct McpSession {
    session: CliSession,
    indexed: bool,
}

impl McpRuntime {
    pub fn new(
        runner: &Runner,
        root: String,
        editor_configuration: Option<Partial>,
        configuration: Effective,
        inactive: Option<String>,
    ) -> Self {
        Self {
            inactive,
            root,
            editor_configuration,
            configuration,
            cancel: runner.cancel.clone(),
            version: runner.version().to_owned(),
            err_out: runner.err_out.clone(),
            allow_unsupported_project: runner.allow_unsupported_project,
            session: Mutex::new(None),
        }
    }

    pub fn tool_enabled(&self, name: &str) -> bool {
        self.inactive.is_none() && self.configuration.mcp_tool_enabled(name)
    }

    /// Runs `operation` on the shared, indexed session,
    /// with its requests cancelled by `cancel` (the tool call's token). A
    /// cancelled wait for the first index keeps the session, so the next
    /// call resumes waiting.
    pub fn with_session<T>(
        &self,
        cancel: &Cancel,
        operation: impl FnOnce(&CliSession) -> Result<T, CliError>,
    ) -> Result<T, CliError> {
        let mut guard = self.session.lock().unwrap_or_else(|p| p.into_inner());
        if guard.is_none() {
            let session = CliSession::new(
                &self.cancel,
                SessionOptions {
                    root: self.root.clone(),
                    version: self.version.clone(),
                    err_out: Some(self.err_out.clone()),
                    start_index: true,
                    allow_unsupported_project: self.allow_unsupported_project,
                    watch_files: true,
                    editor_configuration: self.editor_configuration.clone(),
                },
            )?;
            *guard = Some(McpSession {
                session,
                indexed: false,
            });
        }
        let Some(state) = guard.as_mut() else {
            return Err(CliError::fail("language server session is unavailable"));
        };
        if !state.indexed {
            state.session.initial_index = state
                .session
                .with_cancel(cancel, || state.session.wait_for_index())?;
            state.indexed = true;
        }
        let session = &state.session;
        session.with_cancel(cancel, || operation(session))
    }

    pub fn close(&self) -> Result<(), CliError> {
        let mut guard = self.session.lock().unwrap_or_else(|p| p.into_inner());
        match guard.take() {
            Some(state) => state.session.close(),
            None => Ok(()),
        }
    }
}

pub(crate) fn run_mcp(runner: &Runner) -> Result<(), CliError> {
    let root = runner.workspace_root()?;
    // A client registers the server for every project it opens and shows
    // an exit as a failed server. So what would stop a one-shot command
    // (no project, a configuration that cannot be read) only makes the
    // server inactive: it answers the handshake, says why, and offers no
    // tools. The root is then never indexed.
    let mut inactive = runner.unsupported_project(&root)?;
    let editor_configuration = mcp_editor_configuration().unwrap_or_else(|error| {
        inactive.get_or_insert(error.message);
        None
    });
    let configuration =
        mcp_configuration(&root, editor_configuration.as_ref()).unwrap_or_else(|error| {
            inactive.get_or_insert(format!("invalid project configuration: {}", error.message));
            swlsp_projectconfig::default_effective()
        });
    if let Some(reason) = &inactive {
        let _ = runner.write_err(&format!("Shopware MCP inactive: {reason}\n"));
    }
    let runtime = Arc::new(McpRuntime::new(
        runner,
        root,
        editor_configuration,
        configuration,
        inactive,
    ));
    let result = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .thread_name("mcp")
        .build()
        .map_err(|error| error.to_string())
        .and_then(|executor| {
            executor.block_on(serve(
                runtime.clone(),
                runner.take_input(),
                runner.out.clone(),
                runner.cancel.clone(),
            ))
        });
    if let Err(error) = runtime.close()
        && runner.verbose
    {
        let _ = runner.write_err(&format!("Close MCP workspace: {}\n", error.message));
    }
    result.map_err(|error| CliError::fail(format!("run stdio MCP server: {error}")))
}

fn mcp_configuration(root: &str, editor: Option<&Partial>) -> Result<Effective, CliError> {
    let project = swlsp_projectconfig::load(root)
        .map_err(|error| CliError::fail(error.to_string()))?
        .unwrap_or_default();
    let editor = editor.cloned().unwrap_or_default();
    Ok(swlsp_projectconfig::resolve(&project, &editor))
}

/// Why `SHOPWARE_LSP_EDITOR_CONFIGURATION` could not be decoded.
#[derive(Debug, thiserror::Error)]
enum EditorConfigurationError {
    #[error(transparent)]
    Json(#[from] serde_json::Error),
    #[error("no JSON value")]
    Empty,
    #[error("multiple JSON values")]
    MultipleValues,
    #[error("expected a configuration object, found {0}")]
    NotAnObject(&'static str),
    #[error("unknown field {0:?}")]
    UnknownField(String),
}

/// Decodes `SHOPWARE_LSP_EDITOR_CONFIGURATION` (one JSON object or `null`)
/// with unknown fields rejected, then validates it.
fn mcp_editor_configuration() -> Result<Option<Partial>, CliError> {
    let source = std::env::var(EDITOR_CONFIGURATION_ENV).unwrap_or_default();
    let source = source.trim();
    if source.is_empty() {
        return Ok(None);
    }
    let partial = decode_editor_configuration(source)
        .map_err(|error| CliError::fail(format!("decode MCP editor configuration: {error}")))?;
    swlsp_projectconfig::validate(&partial)
        .map_err(|error| CliError::fail(format!("validate MCP editor configuration: {error}")))?;
    Ok(Some(partial))
}

fn decode_editor_configuration(source: &str) -> Result<Partial, EditorConfigurationError> {
    let mut stream = serde_json::Deserializer::from_str(source).into_iter::<Value>();
    let value = stream.next().ok_or(EditorConfigurationError::Empty)??;
    match stream.next() {
        None => {}
        Some(Ok(_)) => return Err(EditorConfigurationError::MultipleValues),
        Some(Err(error)) => return Err(error.into()),
    }
    let kind = match &value {
        Value::Object(_) => None,
        Value::Null => return Ok(Partial::default()),
        Value::Array(_) => Some("array"),
        Value::String(_) => Some("string"),
        Value::Number(_) => Some("number"),
        Value::Bool(_) => Some("boolean"),
    };
    if let Some(kind) = kind {
        return Err(EditorConfigurationError::NotAnObject(kind));
    }
    reject_unknown_configuration_fields(&value)?;
    Ok(serde_json::from_value(value)?)
}

/// Rejects unknown members of the configuration objects, so a misspelled
/// setting fails instead of being ignored. Maps such as `features` accept
/// any key here; `swlsp_projectconfig::validate` rejects unknown IDs.
fn reject_unknown_configuration_fields(value: &Value) -> Result<(), EditorConfigurationError> {
    type Nested<'a> = &'a dyn Fn(&str, &Value) -> Result<(), EditorConfigurationError>;
    fn check(
        value: &Value,
        allowed: &[&str],
        nested: Nested<'_>,
    ) -> Result<(), EditorConfigurationError> {
        let Value::Object(map) = value else {
            return Ok(());
        };
        for (key, item) in map {
            if !allowed.contains(&key.as_str()) {
                return Err(EditorConfigurationError::UnknownField(key.clone()));
            }
            nested(key, item)?;
        }
        Ok(())
    }
    let none = |_: &str, _: &Value| Ok(());
    let overrides = |key: &str, item: &Value| -> Result<(), EditorConfigurationError> {
        if key == "overrides"
            && let Value::Array(items) = item
        {
            for item in items {
                check(item, &["files", "enabled", "inspections", "rules"], &none)?;
            }
        }
        Ok(())
    };
    check(
        value,
        &[
            "$schema",
            "version",
            "php",
            "shopware",
            "features",
            "indexing",
            "mcp",
            "domains",
            "diagnostics",
            "check",
        ],
        &|key, item| match key {
            "php" => check(item, &["extensions", "disabledExtensions"], &none),
            "shopware" => check(item, &["targetVersion"], &none),
            "indexing" => check(item, &["enabled", "exclude", "maxFileSizeMiB"], &none),
            "mcp" => check(item, &["tools"], &none),
            "diagnostics" => check(
                item,
                &["enabled", "inspections", "rules", "overrides"],
                &overrides,
            ),
            "check" => check(item, &["severity", "failOn"], &none),
            _ => Ok(()),
        },
    )
}

// ---------------------------------------------------------------------------
// Server

/// Serves MCP on `input` and `output` until the client disconnects or the
/// CLI is cancelled.
async fn serve(
    runtime: Arc<McpRuntime>,
    input: Box<dyn Read + Send>,
    output: Output,
    cancel: Cancel,
) -> Result<(), String> {
    let (reader, frames, output_done) = async_io::connect(
        Input::Blocking(input),
        StreamOutput::Blocking(Box::new(output)),
    )
    .map_err(|error| error.to_string())?;
    let stop = cancel.child();
    let result = match (McpServer { runtime })
        .serve_with_ct((reader, FrameWriter::new(frames)), stop.token().clone())
        .await
    {
        Ok(service) => service
            .waiting()
            .await
            .map(|_| ())
            .map_err(|error| error.to_string()),
        // The client went away before initializing.
        Err(error) => Err(error.to_string()),
    };
    stop.cancel();
    output_done.wait().await;
    result
}

/// The MCP request handler.
#[derive(Clone)]
struct McpServer {
    runtime: Arc<McpRuntime>,
}

impl McpServer {
    fn enabled_tools(&self) -> impl Iterator<Item = &'static ToolDefinition> + '_ {
        tool_definitions()
            .iter()
            .filter(|tool| self.runtime.tool_enabled(&tool.name))
    }
}

impl ServerHandler for McpServer {
    fn get_info(&self) -> ServerConfig {
        let capabilities = if self.enabled_tools().next().is_some() {
            ServerCapabilities::builder()
                .enable_tools()
                .enable_tool_list_changed()
                .build()
        } else {
            ServerCapabilities::default()
        };
        let instructions = match &self.runtime.inactive {
            Some(reason) => format!(
                "The Shopware MCP server is inactive in this workspace and offers no tools: {reason}"
            ),
            None => INSTRUCTIONS.to_owned(),
        };
        ServerConfig::new(capabilities)
            .with_server_info(Implementation::new(
                "shopware-lsp",
                self.runtime.version.clone(),
            ))
            .with_instructions(instructions)
    }

    async fn list_tools(
        &self,
        _request: Option<PaginatedRequestParams>,
        _context: RequestContext<RoleServer>,
    ) -> Result<ListToolsResult, ErrorData> {
        Ok(ListToolsResult::with_all_items(
            self.enabled_tools().map(|tool| tool.tool.clone()).collect(),
        ))
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        context: RequestContext<RoleServer>,
    ) -> Result<CallToolResponse, ErrorData> {
        let name = request.name.to_string();
        let Some(definition) = self.enabled_tools().find(|tool| tool.name == name) else {
            return Err(ErrorData::invalid_params(
                format!("unknown tool {name:?}"),
                None,
            ));
        };
        let arguments = Value::Object(request.arguments.unwrap_or_default());
        if let Err(error) = schema::validate(&arguments, &definition.input_schema) {
            return Ok(tool_error(format!("invalid arguments: {error}")));
        }
        let arguments = normalize_arguments(arguments);
        // Cancelled by the client (`notifications/cancelled`) or the CLI; the
        // running analysis is cancelled on the language server too.
        let cancel = self.runtime.cancel.child();
        let runtime = self.runtime.clone();
        let call_cancel = cancel.clone();
        let mut call = tokio::task::spawn_blocking(move || {
            tools::dispatch(&runtime, &name, arguments, &call_cancel)
        });
        let outcome = tokio::select! {
            outcome = &mut call => outcome,
            () = context.ct.cancelled() => {
                cancel.cancel();
                call.await
            }
        };
        Ok(match outcome {
            Ok(Ok(output)) => {
                let text = serde_json::to_string(&output).unwrap_or_else(|_| "null".into());
                let mut result = CallToolResult::success(vec![ContentBlock::text(text)]);
                result.structured_content = Some(output);
                result.into()
            }
            Ok(Err(error)) => tool_error(error.message),
            Err(error) => tool_error(format!("tool failed: {error}")),
        })
    }
}

/// A tool error the caller can read (protocol errors are often hidden from
/// the model).
fn tool_error(message: impl Into<String>) -> CallToolResponse {
    CallToolResult::error(vec![ContentBlock::text(message.into())]).into()
}

/// Prepares validated arguments for typed decoding. The schema accepts an
/// integral float such as `1.0` as an integer, so those become integers that
/// serde decodes into integer fields; `null` members are dropped so optional
/// fields fall back to their `#[serde(default)]` values instead of failing.
fn normalize_arguments(value: Value) -> Value {
    match value {
        Value::Number(number) if !number.is_i64() && !number.is_u64() => {
            let float = number.as_f64().unwrap_or(0.0);
            if float.fract() == 0.0 && float.abs() < 9.2e18 {
                json!(float as i64)
            } else {
                Value::Number(number)
            }
        }
        Value::Array(items) => Value::Array(items.into_iter().map(normalize_arguments).collect()),
        Value::Object(map) => Value::Object(
            map.into_iter()
                .filter(|(_, value)| !value.is_null())
                .map(|(key, value)| (key, normalize_arguments(value)))
                .collect(),
        ),
        other => other,
    }
}

/// Tool output: serialized through `serde_json::Value`, whose maps are
/// ordered, so object keys come out sorted and the output is deterministic.
pub(crate) fn output<T: Serialize>(value: &T) -> Result<Value, CliError> {
    serde_json::to_value(value)
        .map_err(|error| CliError::fail(format!("encode tool output: {error}")))
}
