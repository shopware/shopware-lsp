//! The stdio MCP server (Go `mcp.go` on `github.com/modelcontextprotocol/
//! go-sdk`). The JSON-RPC layer is hand-rolled because the tool names,
//! schemas, results and error texts must be identical to the Go SDK's:
//! newline-delimited JSON, `initialize` version negotiation, the
//! initialization gate, input validation against the advertised schemas and
//! tool results with sorted structured content plus a text fallback.

pub(crate) mod scaffold;
mod schema;
mod tools;

use std::io::{BufReader, Read, Write};
use std::sync::Mutex;

use serde::Serialize;
use serde_json::value::RawValue;
use serde_json::{Map, Value, json};
use swlsp_projectconfig::{Effective, Partial};

use crate::session::{CliSession, SessionOptions};
use crate::{CliError, Output, Runner};

pub(crate) use schema::tool_definitions;

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

/// The workspace-scoped MCP runtime (Go `mcpRuntime`). The production
/// session is created on the first tool call that needs it and serialized
/// by `session`.
pub(crate) struct McpRuntime<'a> {
    pub runner: &'a Runner,
    pub root: String,
    pub editor_configuration: Option<Partial>,
    pub configuration: Effective,
    session: Mutex<Option<CliSession>>,
}

impl<'a> McpRuntime<'a> {
    pub fn new(
        runner: &'a Runner,
        root: String,
        editor_configuration: Option<Partial>,
        configuration: Effective,
    ) -> Self {
        Self {
            runner,
            root,
            editor_configuration,
            configuration,
            session: Mutex::new(None),
        }
    }

    pub fn tool_enabled(&self, name: &str) -> bool {
        self.configuration.mcp_tool_enabled(name)
    }

    /// Runs `operation` on the shared, indexed session (Go `withMCPSession`).
    pub fn with_session<T>(
        &self,
        operation: impl FnOnce(&CliSession) -> Result<T, CliError>,
    ) -> Result<T, CliError> {
        let mut guard = self.session.lock().unwrap_or_else(|p| p.into_inner());
        if guard.is_none() {
            let mut session = CliSession::new(
                &self.runner.cancel,
                SessionOptions {
                    root: self.root.clone(),
                    version: self.runner.version().to_owned(),
                    err_out: Some(self.runner.err_out.clone()),
                    start_index: true,
                    allow_unsupported_project: self.runner.allow_unsupported_project,
                    watch_files: true,
                    editor_configuration: self.editor_configuration.clone(),
                },
            )?;
            let result = session.wait_for_index()?;
            session.initial_index = result;
            *guard = Some(session);
        }
        operation(guard.as_ref().expect("session was created"))
    }

    pub fn close(&self) -> Result<(), CliError> {
        let mut guard = self.session.lock().unwrap_or_else(|p| p.into_inner());
        match guard.take() {
            Some(session) => session.close(),
            None => Ok(()),
        }
    }
}

pub(crate) fn run_mcp(runner: &Runner) -> Result<(), CliError> {
    let root = runner.workspace_root()?;
    runner.require_supported_project(&root)?;
    let editor_configuration = mcp_editor_configuration()?;
    let configuration = mcp_configuration(&root, editor_configuration.as_ref())?;
    let runtime = McpRuntime::new(runner, root, editor_configuration, configuration);
    let result = serve(&runtime, runner.take_input(), runner.out.clone());
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

/// Decodes `SHOPWARE_LSP_EDITOR_CONFIGURATION` with unknown fields rejected
/// (Go `mcpEditorConfiguration`).
fn mcp_editor_configuration() -> Result<Option<Partial>, CliError> {
    let source = std::env::var(EDITOR_CONFIGURATION_ENV).unwrap_or_default();
    let source = source.trim();
    if source.is_empty() {
        return Ok(None);
    }
    let decode_error =
        |message: String| CliError::fail(format!("decode MCP editor configuration: {message}"));
    let mut stream = serde_json::Deserializer::from_str(source).into_iter::<Value>();
    let value = match stream.next() {
        Some(Ok(value)) => value,
        Some(Err(error)) => return Err(decode_error(error.to_string())),
        None => return Err(decode_error("EOF".into())),
    };
    match stream.next() {
        None => {}
        Some(Ok(_)) => return Err(decode_error("multiple JSON values".into())),
        Some(Err(error)) => return Err(decode_error(error.to_string())),
    }
    let kind = match &value {
        Value::Object(_) | Value::Null => None,
        Value::Array(_) => Some("array"),
        Value::String(_) => Some("string"),
        Value::Number(_) => Some("number"),
        Value::Bool(_) => Some("bool"),
    };
    if let Some(kind) = kind {
        return Err(decode_error(format!(
            "json: cannot unmarshal {kind} into Go value of type projectconfig.Partial"
        )));
    }
    reject_unknown_configuration_fields(&value).map_err(decode_error)?;
    let partial: Partial = if value.is_null() {
        Partial::default()
    } else {
        serde_json::from_value(value).map_err(|error| decode_error(error.to_string()))?
    };
    swlsp_projectconfig::validate(&partial)
        .map_err(|error| CliError::fail(format!("validate MCP editor configuration: {error}")))?;
    Ok(Some(partial))
}

/// Go's `DisallowUnknownFields` for the configuration structs (maps such as
/// `features` accept any key; `Validate` rejects unknown IDs).
fn reject_unknown_configuration_fields(value: &Value) -> Result<(), String> {
    fn check(
        value: &Value,
        allowed: &[&str],
        nested: &dyn Fn(&str, &Value) -> Result<(), String>,
    ) -> Result<(), String> {
        let Value::Object(map) = value else {
            return Ok(());
        };
        for (key, item) in map {
            if !allowed.contains(&key.as_str()) {
                return Err(format!("json: unknown field {:?}", key));
            }
            nested(key, item)?;
        }
        Ok(())
    }
    let none = |_: &str, _: &Value| Ok(());
    let overrides = |key: &str, item: &Value| -> Result<(), String> {
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
// JSON-RPC

#[derive(Clone)]
struct Writer(Output);

impl Writer {
    fn respond(&self, id: &Value, result: Result<Value, RpcError>) {
        #[derive(Serialize)]
        struct Response<'a> {
            jsonrpc: &'static str,
            id: &'a Value,
            #[serde(skip_serializing_if = "Option::is_none")]
            result: Option<Box<RawValue>>,
            #[serde(skip_serializing_if = "Option::is_none")]
            error: Option<RpcError>,
        }
        let (result, error) = match result {
            Ok(result) => (
                Some(
                    RawValue::from_string(encode_result(&result))
                        .unwrap_or_else(|_| RawValue::NULL.to_owned()),
                ),
                None,
            ),
            Err(error) => (None, Some(error)),
        };
        let mut line = to_json(&Response {
            jsonrpc: "2.0",
            id,
            result,
            error,
        });
        line.push('\n');
        let mut output = self.0.clone();
        let _ = output.write_all(line.as_bytes());
    }
}

/// Compact JSON text of `value`.
fn to_json<T: Serialize + ?Sized>(value: &T) -> String {
    serde_json::to_string(value).unwrap_or_else(|_| "null".into())
}

/// Results are pre-encoded where Go's struct field order matters; such
/// values travel as `{"$raw": "<json>"}`.
fn encode_result(result: &Value) -> String {
    if let Some(raw) = result.get(RAW_KEY).and_then(Value::as_str)
        && result.as_object().is_some_and(|map| map.len() == 1)
    {
        return raw.to_owned();
    }
    to_json(result)
}

const RAW_KEY: &str = "\u{0}raw";

fn raw_result(text: String) -> Value {
    json!({ RAW_KEY: text })
}

#[derive(Debug, Clone, Serialize)]
pub(crate) struct RpcError {
    code: i64,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<Value>,
}

impl RpcError {
    fn new(code: i64, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            data: None,
        }
    }
}

const INVALID_REQUEST: i64 = -32600;
const METHOD_NOT_FOUND: i64 = -32601;
const INVALID_PARAMS: i64 = -32602;

struct State {
    initialized: bool,
    initialized_notification: bool,
    client_version: String,
}

/// Serves MCP until end of input (Go `server.Run` on an `IOTransport`).
fn serve(
    runtime: &McpRuntime<'_>,
    input: Box<dyn Read + Send>,
    output: Output,
) -> Result<(), String> {
    let writer = Writer(output);
    let state = Mutex::new(State {
        initialized: false,
        initialized_notification: false,
        client_version: String::new(),
    });
    let reader = BufReader::new(input);
    let stream = serde_json::Deserializer::from_reader(reader).into_iter::<Value>();
    std::thread::scope(|scope| {
        for message in stream {
            let message = message.map_err(|error| error.to_string())?;
            let Value::Object(message) = message else {
                if message.is_array() {
                    let version = state
                        .lock()
                        .unwrap_or_else(|p| p.into_inner())
                        .client_version
                        .clone();
                    return Err(format!(
                        "JSON-RPC batching is not supported in 2025-06-18 and later (request version: {version})"
                    ));
                }
                return Err("invalid message".into());
            };
            let tag = message.get("jsonrpc").and_then(Value::as_str).unwrap_or("");
            if tag != "2.0" {
                return Err(format!(
                    "invalid message version tag {:?}; expected \"2.0\"",
                    tag
                ));
            }
            let Some(method) = message
                .get("method")
                .and_then(Value::as_str)
                .map(str::to_owned)
            else {
                // Responses to server-initiated calls: none are sent.
                continue;
            };
            let id = message.get("id").and_then(normalize_id);
            let params = message.get("params").cloned();
            let call = id.is_some();
            // Initialization and notifications are synchronous; other calls
            // run concurrently like Go's async handlers.
            if method == "initialize" || !call {
                let result = handle(runtime, &state, &method, params, call);
                if let Some(id) = &id {
                    writer.respond(id, result);
                }
                continue;
            }
            let writer = writer.clone();
            let state = &state;
            scope.spawn(move || {
                let result = handle(runtime, state, &method, params, call);
                if let Some(id) = &id {
                    writer.respond(id, result);
                }
            });
        }
        Ok(())
    })
}

/// Go jsonrpc2 IDs are int64 or string; fractional numbers truncate.
fn normalize_id(id: &Value) -> Option<Value> {
    match id {
        Value::Number(number) => {
            if let Some(value) = number.as_i64() {
                Some(json!(value))
            } else {
                number.as_f64().map(|value| json!(value as i64))
            }
        }
        Value::String(value) => Some(json!(value)),
        _ => None,
    }
}

/// Method flags of Go's `serverMethodInfos`.
struct MethodInfo {
    notification: bool,
    missing_params_ok: bool,
    params_type: &'static str,
}

fn method_info(method: &str) -> Option<MethodInfo> {
    let info = |notification, missing_params_ok, params_type| MethodInfo {
        notification,
        missing_params_ok,
        params_type,
    };
    Some(match method {
        "completion/complete" => info(false, false, "CompleteParams"),
        "initialize" => info(false, false, "InitializeParams"),
        "ping" => info(false, true, "PingParams"),
        "prompts/list" => info(false, true, "ListPromptsParams"),
        "prompts/get" => info(false, false, "GetPromptParams"),
        "tools/list" => info(false, true, "ListToolsParams"),
        "tools/call" => info(false, false, "CallToolParamsRaw"),
        "resources/list" => info(false, true, "ListResourcesParams"),
        "resources/templates/list" => info(false, true, "ListResourceTemplatesParams"),
        "resources/read" => info(false, false, "ReadResourceParams"),
        "logging/setLevel" => info(false, false, "SetLoggingLevelParams"),
        "notifications/cancelled" => info(true, true, "CancelledParams"),
        "notifications/initialized" => info(true, true, "InitializedParams"),
        "notifications/roots/list_changed" => info(true, true, "RootsListChangedParams"),
        "notifications/progress" => info(true, false, "ProgressNotificationParams"),
        _ => return None,
    })
}

fn handle(
    runtime: &McpRuntime<'_>,
    state: &Mutex<State>,
    method: &str,
    params: Option<Value>,
    call: bool,
) -> Result<Value, RpcError> {
    let initialized = state.lock().unwrap_or_else(|p| p.into_inner()).initialized;
    let gated = !matches!(
        method,
        "initialize"
            | "ping"
            | "notifications/initialized"
            | "notifications/roots/list_changed"
            | "logging/setLevel"
            | "resources/subscribe"
            | "resources/unsubscribe"
    );
    if gated && !initialized {
        return Err(RpcError::new(
            0,
            format!(
                "method {:?} is invalid during session initialization",
                method
            ),
        ));
    }
    let Some(info) = method_info(method) else {
        return Err(RpcError::new(
            METHOD_NOT_FOUND,
            format!("method not found: {:?}", method),
        ));
    };
    if info.notification && call {
        return Err(RpcError::new(
            INVALID_REQUEST,
            format!("invalid request: unexpected id for {:?}", method),
        ));
    }
    if !info.missing_params_ok && params.is_none() {
        return Err(RpcError::new(
            INVALID_REQUEST,
            "invalid request: missing required \"params\"",
        ));
    }
    let params = match params {
        None | Some(Value::Null) => None,
        Some(Value::Object(map)) => Some(map),
        Some(other) => {
            let raw = to_json(&other);
            return Err(RpcError::new(
                INVALID_PARAMS,
                format!(
                    "handling '{method}': invalid params: unmarshaling {:?} into a *mcp.{}: json: cannot unmarshal {:?} into Go value of type mcp.{}",
                    raw, info.params_type, raw, info.params_type
                ),
            ));
        }
    };
    if !info.missing_params_ok && params.is_none() {
        let message = if method == "initialize" {
            format!("handling '{method}': missing required \"params\"")
        } else {
            format!("handling '{method}': invalid request: missing required \"params\"")
        };
        let code = if method == "initialize" {
            0
        } else {
            INVALID_REQUEST
        };
        return Err(RpcError::new(code, message));
    }
    let params = params.unwrap_or_default();
    match method {
        "initialize" => {
            let mut state = state.lock().unwrap_or_else(|p| p.into_inner());
            if state.initialized {
                return Err(RpcError::new(0, "duplicate \"initialize\" received"));
            }
            state.initialized = true;
            let requested = params
                .get("protocolVersion")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned();
            state.client_version = requested.clone();
            Ok(raw_result(initialize_result(runtime, &requested)))
        }
        "notifications/initialized" => {
            let mut state = state.lock().unwrap_or_else(|p| p.into_inner());
            state.initialized_notification = true;
            Ok(Value::Null)
        }
        "ping" | "logging/setLevel" => Ok(json!({})),
        "tools/list" => {
            if params
                .get("cursor")
                .and_then(Value::as_str)
                .is_some_and(|cursor| !cursor.is_empty())
            {
                return Err(RpcError::new(INVALID_PARAMS, "invalid params"));
            }
            Ok(raw_result(tools_list(runtime)))
        }
        "resources/list" => Ok(raw_result(
            "{\"ttlMs\":0,\"cacheScope\":\"public\",\"resources\":[]}".into(),
        )),
        "resources/templates/list" => Ok(raw_result(
            "{\"ttlMs\":0,\"cacheScope\":\"public\",\"resourceTemplates\":[]}".into(),
        )),
        "prompts/list" => Ok(raw_result(
            "{\"ttlMs\":0,\"cacheScope\":\"public\",\"prompts\":[]}".into(),
        )),
        "prompts/get" => Err(RpcError::new(
            INVALID_PARAMS,
            format!(
                "unknown prompt {:?}",
                params.get("name").and_then(Value::as_str).unwrap_or("")
            ),
        )),
        "resources/read" => Err(RpcError {
            code: INVALID_PARAMS,
            message: "Resource not found".into(),
            data: Some(json!({"uri": params.get("uri").and_then(Value::as_str).unwrap_or("")})),
        }),
        "completion/complete" => {
            if !params.contains_key("ref") {
                return Err(RpcError::new(
                    INVALID_PARAMS,
                    "invalid params: missing required 'ref' field",
                ));
            }
            Err(RpcError::new(
                METHOD_NOT_FOUND,
                format!("method not found: {:?}", method),
            ))
        }
        "tools/call" => {
            let client_version = state
                .lock()
                .unwrap_or_else(|p| p.into_inner())
                .client_version
                .clone();
            call_tool(runtime, &params, &client_version)
        }
        _ => Ok(Value::Null),
    }
}

fn initialize_result(runtime: &McpRuntime<'_>, requested: &str) -> String {
    const SUPPORTED: [&str; 4] = ["2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"];
    let version = if SUPPORTED.contains(&requested) {
        requested
    } else {
        "2025-11-25"
    };
    let has_tools = tool_definitions()
        .iter()
        .any(|tool| runtime.tool_enabled(&tool.name));
    let capabilities = if has_tools {
        "{\"tools\":{\"listChanged\":true}}"
    } else {
        "{}"
    };
    format!(
        "{{\"capabilities\":{capabilities},\"instructions\":{},\"protocolVersion\":{},\"serverInfo\":{{\"name\":\"shopware-lsp\",\"version\":{}}}}}",
        to_json(INSTRUCTIONS),
        to_json(version),
        to_json(runtime.runner.version())
    )
}

fn tools_list(runtime: &McpRuntime<'_>) -> String {
    let tools: Vec<&str> = tool_definitions()
        .iter()
        .filter(|tool| runtime.tool_enabled(&tool.name))
        .map(|tool| tool.raw.get())
        .collect();
    format!(
        "{{\"ttlMs\":0,\"cacheScope\":\"public\",\"tools\":[{}]}}",
        tools.join(",")
    )
}

fn call_tool(
    runtime: &McpRuntime<'_>,
    params: &Map<String, Value>,
    client_version: &str,
) -> Result<Value, RpcError> {
    let name = params.get("name").and_then(Value::as_str).unwrap_or("");
    let Some(definition) = tool_definitions()
        .iter()
        .find(|tool| tool.name == name && runtime.tool_enabled(&tool.name))
    else {
        return Err(RpcError::new(
            INVALID_PARAMS,
            format!("unknown tool {:?}", name),
        ));
    };
    let result_type = if client_version >= "2026-07-28" {
        ",\"resultType\":\"complete\""
    } else {
        ""
    };
    let error_result = |message: &str| {
        raw_result(format!(
            "{{\"content\":[{{\"type\":\"text\",\"text\":{}}}],\"isError\":true{result_type}}}",
            to_json(message)
        ))
    };
    // Validate input and apply defaults (Go `applySchema`).
    let arguments = match params.get("arguments") {
        None | Some(Value::Null) => Value::Object(Map::new()),
        Some(Value::Object(map)) => Value::Object(map.clone()),
        Some(other) => {
            let raw = to_json(other);
            return Ok(error_result(&format!(
                "validating \"arguments\": unmarshaling arguments: json: cannot unmarshal {:?} into Go value of type map[string]interface {{}}",
                raw
            )));
        }
    };
    if let Err(error) = schema::validate(&arguments, &definition.input_schema) {
        return Ok(error_result(&format!("validating \"arguments\": {error}")));
    }
    let arguments = normalize_arguments(arguments);
    match tools::dispatch(runtime, name, arguments) {
        Ok(output) => {
            let structured = to_json(&output);
            Ok(raw_result(format!(
                "{{\"content\":[{{\"type\":\"text\",\"text\":{}}}],\"structuredContent\":{structured}{result_type}}}",
                to_json(&structured)
            )))
        }
        Err(error) => Ok(error_result(&error.message)),
    }
}

/// Prepares validated arguments for typed decoding the way Go's decoder
/// sees them: integral floats become integers, and `null` members (which Go
/// leaves at their zero value) are dropped.
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

/// Tool output: serialized through `serde_json::Value`, so object keys come
/// out sorted like Go's re-marshaled structured content.
pub(crate) fn output<T: Serialize>(value: &T) -> Result<Value, CliError> {
    serde_json::to_value(value)
        .map_err(|error| CliError::fail(format!("marshaling output: {error}")))
}
