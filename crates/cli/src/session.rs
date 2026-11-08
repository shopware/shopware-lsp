//! The CLI's production LSP session (Go `session.go`): an in-process
//! [`swlsp_app::Application`] served over OS pipes and driven through a
//! small JSON-RPC client, exactly as an editor would drive it.

use std::collections::HashMap;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, bounded, unbounded};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use serde_json::{Value, json};
use swlsp_lsp_protocol::{Diagnostic, DiagnosticRelatedInformation, DiagnosticResult, Position};

use crate::ordered::Node;
use crate::{Cancel, CliError, Output};

const POLL: Duration = Duration::from_millis(25);

#[derive(Debug, Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
pub struct IndexingResult {
    #[serde(rename = "timeInSeconds", default)]
    pub time_in_seconds: f64,
}

enum IndexEvent {
    Done(IndexingResult),
    Failed(String),
}

/// The subset of the configuration catalog the CLI reads.
#[derive(Debug, Clone, Default)]
pub struct Configuration {
    /// The catalog exactly as the server sent it.
    pub raw: Node,
    pub path: Node,
    pub effective: Node,
    pub scopes: Node,
    pub exclude: Vec<String>,
    pub severity: String,
    pub fail_on: String,
}

impl Configuration {
    fn from_raw(text: &str) -> Result<Self, CliError> {
        let raw =
            crate::config_order::reorder_catalog(Node::parse(text).map_err(|error| {
                CliError::fail(format!("decode configuration catalog: {error}"))
            })?);
        let value: Value = serde_json::from_str(text).unwrap_or(Value::Null);
        let effective = &value["effective"];
        let exclude = effective["indexing"]["exclude"]
            .as_array()
            .map(|items| {
                items
                    .iter()
                    .filter_map(|item| item.as_str().map(str::to_owned))
                    .collect()
            })
            .unwrap_or_default();
        Ok(Self {
            path: raw
                .get("path")
                .cloned()
                .unwrap_or(Node::String(String::new())),
            effective: raw.get("effective").cloned().unwrap_or(Node::Null),
            scopes: raw.get("scopes").cloned().unwrap_or(Node::Null),
            exclude,
            severity: effective["check"]["severity"]
                .as_str()
                .unwrap_or("")
                .to_owned(),
            fail_on: effective["check"]["failOn"]
                .as_str()
                .unwrap_or("")
                .to_owned(),
            raw,
        })
    }
}

#[derive(Deserialize)]
struct Incoming {
    #[serde(default)]
    id: Option<Value>,
    #[serde(default)]
    method: Option<String>,
    #[serde(default)]
    params: Option<Box<RawValue>>,
    #[serde(default)]
    result: Option<Box<RawValue>>,
    #[serde(default)]
    error: Option<RemoteError>,
}

#[derive(Deserialize, Debug, Clone)]
struct RemoteError {
    code: i64,
    #[serde(default)]
    message: String,
}

type Reply = Result<String, String>;

/// A JSON-RPC client over one framed byte stream.
struct RpcClient {
    writer: Mutex<Option<Box<dyn Write + Send>>>,
    next_id: AtomicI64,
    pending: Mutex<HashMap<i64, Sender<Reply>>>,
    closed: AtomicBool,
}

fn lock<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

impl RpcClient {
    fn send(&self, message: &Value) -> Result<(), String> {
        let body = serde_json::to_vec(message).map_err(|error| error.to_string())?;
        let mut writer = lock(&self.writer);
        let Some(writer) = writer.as_mut() else {
            return Err("jsonrpc2: connection is closed".into());
        };
        let result = write!(writer, "Content-Length: {}\r\n\r\n", body.len())
            .and_then(|()| writer.write_all(&body))
            .and_then(|()| writer.flush());
        result.map_err(|_| "jsonrpc2: connection is closed".to_owned())
    }

    fn notify(&self, method: &str, params: Value) -> Result<(), String> {
        self.send(&json!({"jsonrpc": "2.0", "method": method, "params": params}))
    }

    fn call(
        &self,
        method: &str,
        params: Value,
        cancel: &Cancel,
        timeout: Option<Duration>,
    ) -> Reply {
        if self.closed.load(Ordering::Acquire) {
            return Err("jsonrpc2: connection is closed".into());
        }
        if cancel.is_cancelled() {
            return Err("context canceled".into());
        }
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let (sender, receiver) = bounded(1);
        lock(&self.pending).insert(id, sender);
        if let Err(error) =
            self.send(&json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params}))
        {
            lock(&self.pending).remove(&id);
            return Err(error);
        }
        let deadline = timeout.map(|timeout| Instant::now() + timeout);
        loop {
            if cancel.is_cancelled() {
                lock(&self.pending).remove(&id);
                return Err("context canceled".into());
            }
            if let Some(deadline) = deadline
                && Instant::now() >= deadline
            {
                lock(&self.pending).remove(&id);
                return Err("context deadline exceeded".into());
            }
            match receiver.recv_timeout(POLL) {
                Ok(reply) => return reply,
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(crossbeam_channel::RecvTimeoutError::Disconnected) => {
                    return Err("jsonrpc2: connection is closed".into());
                }
            }
        }
    }

    fn close(&self) {
        self.closed.store(true, Ordering::Release);
        *lock(&self.writer) = None;
        lock(&self.pending).clear();
    }
}

/// Reads one `Content-Length` framed message; `None` at end of stream.
fn read_message(reader: &mut impl BufRead) -> io::Result<Option<String>> {
    let mut length: Option<usize> = None;
    let mut line = String::new();
    loop {
        line.clear();
        if reader.read_line(&mut line)? == 0 {
            return Ok(None);
        }
        let trimmed = line.trim_end_matches(['\r', '\n']);
        if trimmed.is_empty() {
            if length.is_some() {
                break;
            }
            continue;
        }
        if let Some((name, value)) = trimmed.split_once(':')
            && name.trim().eq_ignore_ascii_case("content-length")
        {
            length = value.trim().parse().ok();
        }
    }
    let mut body = vec![0; length.unwrap_or(0)];
    reader.read_exact(&mut body)?;
    Ok(Some(String::from_utf8_lossy(&body).into_owned()))
}

pub struct SessionOptions {
    pub root: String,
    pub version: String,
    pub err_out: Option<Output>,
    pub start_index: bool,
    pub allow_unsupported_project: bool,
    pub watch_files: bool,
    pub editor_configuration: Option<swlsp_projectconfig::Partial>,
}

pub struct CliDocument {
    pub path: String,
    pub uri: String,
    pub source: String,
    pub version: i32,
}

pub struct CliSession {
    application: Arc<swlsp_app::Application>,
    client: Arc<RpcClient>,
    server: Mutex<Option<JoinHandle<Result<(), String>>>>,
    server_done: Receiver<Result<(), String>>,
    server_error: Mutex<Option<Result<(), String>>>,
    index_events: Receiver<IndexEvent>,
    pub cancel: Cancel,
    pub root: String,
    pub capabilities: Node,
    pub configuration: Configuration,
    pub initial_index: IndexingResult,
    close_result: Mutex<Option<Result<(), String>>>,
}

impl CliSession {
    pub fn new(cancel: &Cancel, options: SessionOptions) -> Result<CliSession, CliError> {
        let (server_reader, client_writer) =
            io::pipe().map_err(|error| CliError::fail(format!("create LSP pipe: {error}")))?;
        let (client_reader, server_writer) =
            io::pipe().map_err(|error| CliError::fail(format!("create LSP pipe: {error}")))?;
        let application = Arc::new(swlsp_app::Application::with_options(
            &options.version,
            swlsp_app::Options {
                allow_unsupported_project: options.allow_unsupported_project,
            },
        ));
        let (done_sender, server_done) = bounded(1);
        let server_application = application.clone();
        let server = thread::Builder::new()
            .name("cli-lsp-server".into())
            .spawn(move || {
                let result = server_application.run(server_reader, server_writer);
                let _ = done_sender.send(result.clone());
                result
            })
            .map_err(|error| CliError::fail(format!("start language server: {error}")))?;
        let client = Arc::new(RpcClient {
            writer: Mutex::new(Some(Box::new(client_writer))),
            next_id: AtomicI64::new(1),
            pending: Mutex::new(HashMap::new()),
            closed: AtomicBool::new(false),
        });
        let (index_sender, index_events) = unbounded();
        spawn_reader(
            client.clone(),
            client_reader,
            index_sender,
            options.err_out.clone(),
        );

        let mut session = CliSession {
            application,
            client,
            server: Mutex::new(Some(server)),
            server_done,
            server_error: Mutex::new(None),
            index_events,
            cancel: cancel.clone(),
            root: options.root.clone(),
            capabilities: Node::Null,
            configuration: Configuration::default(),
            initial_index: IndexingResult::default(),
            close_result: Mutex::new(None),
        };
        let root_uri = swlsp_uriutil::file_uri(&options.root);
        let mut initialization_options = json!({
            "cliMode": true,
            "allowUnsupportedProject": options.allow_unsupported_project,
            "watchFiles": options.watch_files,
        });
        if let Some(configuration) = &options.editor_configuration {
            initialization_options["configuration"] =
                serde_json::to_value(configuration).unwrap_or(Value::Null);
        }
        let initialize = json!({
            "processId": std::process::id(),
            "rootUri": root_uri,
            "initializationOptions": initialization_options,
            "workspaceFolders": [{
                "uri": root_uri,
                "name": swlsp_uriutil::filepath::base(&options.root),
            }],
            "capabilities": {
                "textDocument": {
                    "diagnostic": {},
                    "codeAction": {
                        "dataSupport": true,
                        "resolveSupport": {"properties": ["edit"]},
                    },
                },
            },
        });
        match session.client.call("initialize", initialize, cancel, None) {
            Ok(text) => session.capabilities = Node::parse(&text).unwrap_or(Node::Null),
            Err(error) => {
                let _ = session.close();
                return Err(CliError::fail(format!("initialize LSP session: {error}")));
            }
        }
        match session
            .client
            .call("shopware/configuration/catalog", json!({}), cancel, None)
        {
            Ok(text) => match Configuration::from_raw(&text) {
                Ok(configuration) => session.configuration = configuration,
                Err(error) => {
                    let _ = session.close();
                    return Err(error);
                }
            },
            Err(error) => {
                let _ = session.close();
                return Err(CliError::fail(format!(
                    "read effective configuration: {error}"
                )));
            }
        }
        if !options.start_index {
            return Ok(session);
        }
        if let Err(error) = session.client.notify("initialized", json!({})) {
            let _ = session.close();
            return Err(CliError::fail(format!("notify initialized: {error}")));
        }
        Ok(session)
    }

    /// Waits for `shopware/indexingCompleted`.
    pub fn wait_for_index(&self) -> Result<IndexingResult, CliError> {
        loop {
            crossbeam_channel::select! {
                recv(self.index_events) -> event => match event {
                    Ok(IndexEvent::Done(result)) => return Ok(result),
                    Ok(IndexEvent::Failed(message)) => {
                        return Err(CliError::fail(format!("index workspace: {message}")));
                    }
                    Err(_) => return Err(CliError::fail("language server stopped during indexing")),
                },
                recv(self.server_done) -> result => {
                    let result = result.unwrap_or(Ok(()));
                    *lock(&self.server_error) = Some(result.clone());
                    return Err(match result {
                        Err(error) => CliError::fail(error),
                        Ok(()) => CliError::fail("language server stopped during indexing"),
                    });
                },
                default(POLL) => {
                    if self.cancel.is_cancelled() {
                        return Err(CliError::canceled());
                    }
                }
            }
        }
    }

    /// Sends a request; errors are `method: cause` like Go `session.call`.
    pub fn call_raw(&self, method: &str, params: Value) -> Result<String, CliError> {
        self.client
            .call(method, params, &self.cancel, None)
            .map_err(|error| CliError::fail(format!("{method}: {error}")))
    }

    /// Sends a request and decodes the result. `null` decodes as the
    /// target's `Default` through `Option`.
    pub fn call<T: DeserializeOwned>(&self, method: &str, params: Value) -> Result<T, CliError> {
        let text = self.call_raw(method, params)?;
        serde_json::from_str(&text)
            .map_err(|error| CliError::fail(format!("{method}: decode result: {error}")))
    }

    pub fn notify(&self, method: &str, params: Value) -> Result<(), String> {
        self.client.notify(method, params)
    }

    pub fn open_document(&self, path: &str) -> Result<CliDocument, CliError> {
        let mut path = path.to_owned();
        if path.starts_with("file://") {
            path = swlsp_uriutil::path(&path).map_err(|error| CliError::fail(error.to_string()))?;
        }
        let absolute = swlsp_uriutil::filepath::abs(&path)
            .map_err(|error| CliError::fail(format!("resolve document path: {error}")))?;
        let content = std::fs::read(&absolute).map_err(|error| {
            CliError::fail(format!(
                "read document: {}",
                crate::gofmt::path_error("open", &absolute, &error)
            ))
        })?;
        let extension = swlsp_uriutil::filepath::ext(&absolute).to_lowercase();
        let mut language_id = extension.trim_start_matches('.').to_owned();
        if let Some(language) = language_for(&absolute) {
            language_id = language.as_str().to_owned();
        }
        let document = CliDocument {
            uri: swlsp_uriutil::file_uri(&absolute),
            path: absolute,
            source: String::from_utf8_lossy(&content).into_owned(),
            version: 1,
        };
        self.client
            .notify(
                "textDocument/didOpen",
                json!({"textDocument": {
                    "uri": document.uri,
                    "version": document.version,
                    "languageId": language_id,
                    "text": document.source,
                }}),
            )
            .map_err(|error| CliError::fail(format!("open document: {error}")))?;
        Ok(document)
    }

    pub fn close_document(&self, document: &CliDocument) -> Result<(), CliError> {
        self.client
            .notify(
                "textDocument/didClose",
                json!({"textDocument": {"uri": document.uri}}),
            )
            .map_err(|error| CliError::fail(format!("close document: {error}")))
    }

    /// Opens, diagnoses and closes one file, keeping findings at or above
    /// `cutoff` (lower numbers are more severe).
    pub fn check_document(
        &self,
        path: &str,
        cutoff: i32,
    ) -> Result<Vec<DiagnosticOutput>, CliError> {
        let document = self.open_document(path)?;
        let result: Result<Option<DiagnosticResult>, CliError> = self.call(
            "textDocument/diagnostic",
            text_document_params(&document.uri),
        );
        let closed = self.close_document(&document);
        let result = match (result, closed) {
            (Ok(result), Ok(())) => result.unwrap_or_default(),
            (Err(error), Ok(())) | (Ok(_), Err(error)) => return Err(error),
            (Err(first), Err(second)) => {
                return Err(CliError::fail(format!(
                    "{}\n{}",
                    first.message, second.message
                )));
            }
        };
        let mut findings = Vec::new();
        for diagnostic in result.items {
            let severity = diagnostic_severity_value(&diagnostic);
            if severity > cutoff {
                continue;
            }
            findings.push(DiagnosticOutput {
                uri: document.uri.clone(),
                related: Some(diagnostic.related_information.clone())
                    .filter(|items| !items.is_empty()),
                diagnostic,
            });
        }
        Ok(findings)
    }

    /// Shuts the server down (Go `Close`). Idempotent.
    pub fn close(&self) -> Result<(), CliError> {
        let mut result = lock(&self.close_result);
        if let Some(result) = result.as_ref() {
            return result.clone().map_err(CliError::fail);
        }
        let mut errors: Vec<String> = Vec::new();
        let shutdown_deadline = Instant::now() + Duration::from_secs(10);
        let never = Cancel::new();
        if !self.client.closed.load(Ordering::Acquire) {
            if let Err(error) =
                self.client
                    .call("shutdown", json!({}), &never, Some(Duration::from_secs(10)))
                && error != "jsonrpc2: connection is closed"
            {
                errors.push(error);
            }
            let _ = self.client.notify("exit", json!({}));
        }
        let server_result = {
            let known = lock(&self.server_error).clone();
            match known {
                Some(result) => Some(result),
                None => {
                    let remaining = shutdown_deadline.saturating_duration_since(Instant::now());
                    self.server_done.recv_timeout(remaining).ok()
                }
            }
        };
        match server_result {
            Some(Err(error)) => errors.push(error),
            Some(Ok(())) => {}
            None => errors.push("language server did not stop".into()),
        }
        self.client.close();
        if let Some(handle) = lock(&self.server).take()
            && handle.is_finished()
        {
            let _ = handle.join();
        }
        let _ = &self.application;
        let outcome = if errors.is_empty() {
            Ok(())
        } else {
            Err(errors.join("\n"))
        };
        *result = Some(outcome.clone());
        outcome.map_err(CliError::fail)
    }
}

impl Drop for CliSession {
    fn drop(&mut self) {
        let _ = self.close();
    }
}

fn language_for(path: &str) -> Option<swlsp_language::LanguageId> {
    let extension = swlsp_uriutil::filepath::ext(path);
    let extension = extension.strip_prefix('.')?;
    swlsp_language::LanguageId::from_path(&format!("x.{extension}"))
}

fn spawn_reader(
    client: Arc<RpcClient>,
    reader: io::PipeReader,
    index_events: Sender<IndexEvent>,
    err_out: Option<Output>,
) {
    let _ = thread::Builder::new()
        .name("cli-lsp-client".into())
        .spawn(move || {
            let mut reader = BufReader::new(reader);
            while let Ok(Some(text)) = read_message(&mut reader) {
                let Ok(message) = serde_json::from_str::<Incoming>(&text) else {
                    continue;
                };
                match (&message.method, &message.id) {
                    (None, Some(id)) => {
                        let Some(id) = id.as_i64() else { continue };
                        let Some(sender) = lock(&client.pending).remove(&id) else {
                            continue;
                        };
                        let reply = match message.error {
                            Some(error) => Err(format!(
                                "jsonrpc2: code {} message: {}",
                                error.code, error.message
                            )),
                            None => Ok(message
                                .result
                                .map(|raw| raw.get().to_owned())
                                .unwrap_or_else(|| "null".into())),
                        };
                        let _ = sender.send(reply);
                    }
                    (Some(method), id) => {
                        let result = handle_server_message(
                            method,
                            message.params.as_deref(),
                            &index_events,
                            err_out.as_ref(),
                        );
                        if let Some(id) = id {
                            let response = match result {
                                Ok(result) => json!({"jsonrpc": "2.0", "id": id, "result": result}),
                                Err(error) => json!({"jsonrpc": "2.0", "id": id, "error": {"code": 0, "message": error}}),
                            };
                            let _ = client.send(&response);
                        }
                    }
                    (None, None) => {}
                }
            }
            client.closed.store(true, Ordering::Release);
            lock(&client.pending).clear();
        });
}

fn handle_server_message(
    method: &str,
    params: Option<&RawValue>,
    index_events: &Sender<IndexEvent>,
    err_out: Option<&Output>,
) -> Result<Value, String> {
    let params: Value = params
        .and_then(|raw| serde_json::from_str(raw.get()).ok())
        .unwrap_or(Value::Null);
    match method {
        "shopware/indexingCompleted" => {
            let result = IndexingResult {
                time_in_seconds: params["timeInSeconds"].as_f64().unwrap_or(0.0),
            };
            let _ = index_events.send(IndexEvent::Done(result));
        }
        "shopware/indexingFailed" => {
            let message = params["message"].as_str().unwrap_or("").to_owned();
            let _ = index_events.send(IndexEvent::Failed(message));
        }
        "window/logMessage" | "window/showMessage" => {
            if let Some(err_out) = err_out
                && let Some(message) = params["message"].as_str()
                && !message.is_empty()
            {
                let mut err_out = err_out.clone();
                let _ = writeln!(err_out, "{message}");
            }
        }
        "workspace/applyEdit" => {
            return Ok(json!({
                "applied": false,
                "failureReason": "CLI commands apply returned edits explicitly",
            }));
        }
        _ => {}
    }
    Ok(Value::Null)
}

/// One finding of `check` (Go `diagnosticOutput`).
#[derive(Debug, Clone, Serialize)]
pub struct DiagnosticOutput {
    pub uri: String,
    pub diagnostic: Diagnostic,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub related: Option<Vec<DiagnosticRelatedInformation>>,
}

/// The effective numeric severity (missing means warning).
pub fn diagnostic_severity_value(diagnostic: &Diagnostic) -> i32 {
    diagnostic
        .severity
        .map(severity_number)
        .filter(|severity| *severity != 0)
        .unwrap_or(2)
}

pub fn severity_number(severity: swlsp_lsp_protocol::DiagnosticSeverity) -> i32 {
    serde_json::to_value(severity)
        .ok()
        .and_then(|value| value.as_i64())
        .unwrap_or(0) as i32
}

pub fn text_document_params(uri: &str) -> Value {
    json!({"textDocument": {"uri": uri}})
}

pub fn position_params(uri: &str, position: Position) -> Value {
    json!({"textDocument": {"uri": uri}, "position": position})
}

#[allow(dead_code)]
fn _assert_read(_: &dyn Read) {}
