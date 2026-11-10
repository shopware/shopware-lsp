//! The CLI's production LSP session: an in-process
//! [`swlsp_app::Application`] served over an in-memory stream on a small
//! Tokio runtime of the session, and driven through a JSON-RPC client,
//! exactly as an editor would drive it. Client calls block the calling
//! thread; a cancelled call sends `$/cancelRequest`, so the server stops
//! the work too.

use std::collections::HashMap;
use std::io::{self, Write};
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, bounded, unbounded};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use serde_json::{Value, json};
use swlsp_lsp::async_io::{FrameSender, Input, Output as StreamOutput};
use swlsp_lsp_protocol::{Diagnostic, DiagnosticRelatedInformation, DiagnosticResult, Position};
use tokio::io::{AsyncBufRead, AsyncBufReadExt, AsyncReadExt, BufReader};

use crate::autofix::FixMode;
use crate::{Cancel, CliError, Output};

const POLL: Duration = Duration::from_millis(25);

/// JSON-RPC `InternalError`, for server requests the client cannot handle.
const INTERNAL_ERROR: i64 = -32603;

/// Buffer of the in-memory stream between the client and the server.
const STREAM_CAPACITY: usize = 1024 * 1024;

#[derive(Debug, Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
pub struct IndexingResult {
    #[serde(rename = "timeInSeconds", default)]
    pub time_in_seconds: f64,
}

enum IndexEvent {
    /// The server's progress message, such as `1,200/40,000 files`.
    Progress(String),
    Done(IndexingResult),
    Failed(String),
}

/// The subset of the configuration catalog the CLI reads.
#[derive(Debug, Clone, Default)]
pub struct Configuration {
    /// The catalog exactly as the server sent it.
    pub raw: Value,
    pub exclude: Vec<String>,
    pub severity: String,
    pub fail_on: String,
}

impl Configuration {
    fn from_raw(text: &str) -> Result<Self, CliError> {
        let raw: Value = serde_json::from_str(text)
            .map_err(|error| CliError::fail(format!("decode configuration catalog: {error}")))?;
        let effective = &raw["effective"];
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

/// Error of a request to the in-process server.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum ClientError {
    #[error("connection closed")]
    Closed,
    #[error("cancelled")]
    Cancelled,
    #[error("timed out")]
    Timeout,
    #[error("{message} (code {code})")]
    Remote { code: i64, message: String },
    #[error("encode a request: {0}")]
    Encode(String),
}

/// The raw JSON result of a request.
type Reply = Result<String, ClientError>;

/// A JSON-RPC client over one framed byte stream.
pub(crate) struct RpcClient {
    writer: Mutex<Option<FrameSender>>,
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
    /// A client writing its messages to `frames`.
    pub(crate) fn new(frames: FrameSender) -> Self {
        Self {
            writer: Mutex::new(Some(frames)),
            next_id: AtomicI64::new(1),
            pending: Mutex::new(HashMap::new()),
            closed: AtomicBool::new(false),
        }
    }

    fn send(&self, message: &Value) -> Result<(), ClientError> {
        let body =
            serde_json::to_vec(message).map_err(|error| ClientError::Encode(error.to_string()))?;
        let mut frame = format!("Content-Length: {}\r\n\r\n", body.len()).into_bytes();
        frame.extend_from_slice(&body);
        let writer = lock(&self.writer);
        match writer.as_ref() {
            Some(writer) if writer.send(frame) => Ok(()),
            _ => Err(ClientError::Closed),
        }
    }

    fn notify(&self, method: &str, params: Value) -> Result<(), ClientError> {
        self.send(&json!({"jsonrpc": "2.0", "method": method, "params": params}))
    }

    pub(crate) fn call(
        &self,
        method: &str,
        params: Value,
        cancel: &Cancel,
        timeout: Option<Duration>,
    ) -> Reply {
        if self.closed.load(Ordering::Acquire) {
            return Err(ClientError::Closed);
        }
        if cancel.is_cancelled() {
            return Err(ClientError::Cancelled);
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
                // Stops the work on the server; its answer is ignored.
                let _ = self.notify("$/cancelRequest", json!({"id": id}));
                return Err(ClientError::Cancelled);
            }
            if let Some(deadline) = deadline
                && Instant::now() >= deadline
            {
                lock(&self.pending).remove(&id);
                return Err(ClientError::Timeout);
            }
            match receiver.recv_timeout(POLL) {
                Ok(reply) => return reply,
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(crossbeam_channel::RecvTimeoutError::Disconnected) => {
                    return Err(ClientError::Closed);
                }
            }
        }
    }

    pub(crate) fn close(&self) {
        self.closed.store(true, Ordering::Release);
        *lock(&self.writer) = None;
        lock(&self.pending).clear();
    }
}

/// Reads one `Content-Length` framed message; `None` at end of stream.
async fn read_message(reader: &mut (impl AsyncBufRead + Unpin)) -> io::Result<Option<String>> {
    let mut length: Option<usize> = None;
    let mut line = String::new();
    loop {
        line.clear();
        if reader.read_line(&mut line).await? == 0 {
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
    reader.read_exact(&mut body).await?;
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
    server_done: Receiver<Result<(), String>>,
    server_error: Mutex<Option<Result<(), String>>>,
    /// Ends serving when the server does not stop on `exit`.
    stop: Cancel,
    index_events: Receiver<IndexEvent>,
    /// Cancels requests; replaced for the duration of [`CliSession::with_cancel`].
    cancel: Mutex<Cancel>,
    /// Runs the server and the client's reader and writer. Declared last, so
    /// it is dropped after everything that uses it.
    executor: Option<tokio::runtime::Runtime>,
    pub root: String,
    pub capabilities: Value,
    pub configuration: Configuration,
    pub initial_index: IndexingResult,
    close_result: Mutex<Option<Result<(), String>>>,
    /// Notices of the server that no command has printed yet.
    notices: Arc<Mutex<Vec<String>>>,
    err_out: Option<Output>,
}

impl CliSession {
    pub fn new(cancel: &Cancel, options: SessionOptions) -> Result<CliSession, CliError> {
        let executor = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .thread_name("cli-lsp")
            .enable_all()
            .build()
            .map_err(|error| CliError::fail(format!("start LSP session runtime: {error}")))?;
        let _entered = executor.enter();
        let (client_stream, server_stream) = tokio::io::duplex(STREAM_CAPACITY);
        let (server_reader, server_writer) = tokio::io::split(server_stream);
        let (client_reader, client_writer) = tokio::io::split(client_stream);
        let application = Arc::new(swlsp_app::Application::with_options(
            &options.version,
            swlsp_app::Options {
                allow_unsupported_project: options.allow_unsupported_project,
            },
        ));
        let (done_sender, server_done) = bounded(1);
        let stop = Cancel::new();
        {
            let application = application.clone();
            let stop = stop.clone();
            executor.spawn(async move {
                let result = application
                    .serve(
                        Input::Async(Box::new(server_reader)),
                        StreamOutput::Async(Box::new(server_writer)),
                        stop.cancelled(),
                    )
                    .await;
                let _ = done_sender.send(result.map_err(|error| error.to_string()));
            });
        }
        let (frames, _written) =
            swlsp_lsp::async_io::spawn_output(StreamOutput::Async(Box::new(client_writer)))
                .map_err(|error| CliError::fail(format!("start LSP client: {error}")))?;
        let client = Arc::new(RpcClient::new(frames));
        let (index_sender, index_events) = unbounded();
        let notices = Arc::new(Mutex::new(Vec::new()));
        executor.spawn(read_server_messages(
            client.clone(),
            client_reader,
            index_sender,
            options.err_out.clone(),
            notices.clone(),
        ));
        drop(_entered);

        let mut session = CliSession {
            application,
            client,
            server_done,
            server_error: Mutex::new(None),
            stop,
            index_events,
            cancel: Mutex::new(cancel.clone()),
            root: options.root.clone(),
            capabilities: Value::Null,
            configuration: Configuration::default(),
            initial_index: IndexingResult::default(),
            close_result: Mutex::new(None),
            notices,
            err_out: options.err_out.clone(),
            executor: Some(executor),
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
                "window": {"workDoneProgress": true},
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
            Ok(text) => session.capabilities = serde_json::from_str(&text).unwrap_or(Value::Null),
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

    /// The cancellation of this session's requests.
    pub fn cancel(&self) -> Cancel {
        lock(&self.cancel).clone()
    }

    /// Runs `operation` with requests cancelled by `cancel` instead of the
    /// session's own token (one MCP tool call). A cancelled request is
    /// cancelled on the server too.
    pub fn with_cancel<T>(&self, cancel: &Cancel, operation: impl FnOnce() -> T) -> T {
        struct Restore<'a>(&'a Mutex<Cancel>, Option<Cancel>);
        impl Drop for Restore<'_> {
            fn drop(&mut self) {
                if let Some(previous) = self.1.take() {
                    *lock(self.0) = previous;
                }
            }
        }
        let previous = std::mem::replace(&mut *lock(&self.cancel), cancel.clone());
        let _restore = Restore(&self.cancel, Some(previous));
        operation()
    }

    /// Waits for `shopware/indexingCompleted`.
    pub fn wait_for_index(&self) -> Result<IndexingResult, CliError> {
        self.wait_for_index_with(|_| {})
    }

    /// [`Self::wait_for_index`], passing the progress the server reports
    /// (such as `1200/40000 files`) to `on_progress`.
    pub fn wait_for_index_with(
        &self,
        on_progress: impl Fn(&str),
    ) -> Result<IndexingResult, CliError> {
        loop {
            crossbeam_channel::select! {
                recv(self.index_events) -> event => match event {
                    Ok(IndexEvent::Progress(message)) => on_progress(&message),
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
                    if self.cancel().is_cancelled() {
                        return Err(CliError::cancelled());
                    }
                }
            }
        }
    }

    /// Sends a request; errors read `method: cause`.
    pub fn call_raw(&self, method: &str, params: Value) -> Result<String, CliError> {
        self.client
            .call(method, params, &self.cancel(), None)
            .map_err(|error| CliError::fail(format!("{method}: {error}")))
    }

    /// Sends a request and decodes the result. `null` decodes as the
    /// target's `Default` through `Option`.
    pub fn call<T: DeserializeOwned>(&self, method: &str, params: Value) -> Result<T, CliError> {
        let text = self.call_raw(method, params)?;
        serde_json::from_str(&text)
            .map_err(|error| CliError::fail(format!("{method}: decode result: {error}")))
    }

    pub fn notify(&self, method: &str, params: Value) -> Result<(), ClientError> {
        self.client.notify(method, params)
    }

    pub fn open_document(&self, path: &str) -> Result<CliDocument, CliError> {
        let mut path = path.to_owned();
        if path.starts_with("file://") {
            path = swlsp_uriutil::path(&path).map_err(|error| CliError::fail(error.to_string()))?;
        }
        let absolute = swlsp_uriutil::filepath::abs(&path)
            .map_err(|error| CliError::fail(format!("resolve document path: {error}")))?;
        let content = std::fs::read(&absolute)
            .map_err(|error| CliError::fail(format!("read document {absolute}: {error}")))?;
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
        Ok(self.check_file(path, cutoff, FixMode::Ignore)?.findings)
    }

    /// Opens `path`, runs `inspect` on it and closes it again.
    pub(crate) fn with_document<T>(
        &self,
        path: &str,
        inspect: impl FnOnce(&CliDocument) -> Result<T, CliError>,
    ) -> Result<T, CliError> {
        let document = self.open_document(path)?;
        let result = inspect(&document);
        match (result, self.close_document(&document)) {
            (Ok(result), Ok(())) => Ok(result),
            (Err(error), Ok(())) | (Ok(_), Err(error)) => Err(error),
            (Err(first), Err(second)) => Err(CliError::fail(format!(
                "{}\n{}",
                first.message, second.message
            ))),
        }
    }

    /// The diagnostics of an open document at or above `cutoff`.
    pub(crate) fn diagnostics(
        &self,
        document: &CliDocument,
        cutoff: i32,
    ) -> Result<Vec<Diagnostic>, CliError> {
        let result: Option<DiagnosticResult> = self.call(
            "textDocument/diagnostic",
            text_document_params(&document.uri),
        )?;
        Ok(result
            .unwrap_or_default()
            .items
            .into_iter()
            .filter(|diagnostic| diagnostic_severity_value(diagnostic) <= cutoff)
            .collect())
    }

    /// The server's notices about the project (a prerequisite an
    /// inspection is missing) that were not printed yet.
    pub fn take_notices(&self) -> Vec<String> {
        std::mem::take(&mut *lock(&self.notices))
    }

    /// Shuts the server down. Idempotent.
    pub fn close(&self) -> Result<(), CliError> {
        let mut result = lock(&self.close_result);
        if let Some(result) = result.as_ref() {
            return result.clone().map_err(CliError::fail);
        }
        // Whatever the command did not print itself.
        if let Some(mut err_out) = self.err_out.clone() {
            for notice in self.take_notices() {
                let _ = writeln!(err_out, "note: {notice}");
            }
        }
        let mut errors: Vec<String> = Vec::new();
        let shutdown_deadline = Instant::now() + Duration::from_secs(10);
        let never = Cancel::new();
        if !self.client.closed.load(Ordering::Acquire) {
            if let Err(error) =
                self.client
                    .call("shutdown", json!({}), &never, Some(Duration::from_secs(10)))
                && error != ClientError::Closed
            {
                errors.push(error.to_string());
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
            None => {
                self.stop.cancel();
                errors.push("language server did not stop".into());
            }
        }
        self.client.close();
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
        // Tasks still blocked on the stream end with the runtime.
        if let Some(executor) = self.executor.take() {
            executor.shutdown_background();
        }
    }
}

fn language_for(path: &str) -> Option<swlsp_language::LanguageId> {
    let extension = swlsp_uriutil::filepath::ext(path);
    let extension = extension.strip_prefix('.')?;
    swlsp_language::LanguageId::from_path(&format!("x.{extension}"))
}

/// Reads the server's messages: answers waiting calls, records indexing
/// events and answers server requests.
async fn read_server_messages(
    client: Arc<RpcClient>,
    reader: tokio::io::ReadHalf<tokio::io::DuplexStream>,
    index_events: Sender<IndexEvent>,
    err_out: Option<Output>,
    notices: Arc<Mutex<Vec<String>>>,
) {
    let mut reader = BufReader::new(reader);
    while let Ok(Some(text)) = read_message(&mut reader).await {
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
                    Some(error) => Err(ClientError::Remote {
                        code: error.code,
                        message: error.message,
                    }),
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
                    &notices,
                );
                if let Some(id) = id {
                    let response = match result {
                        Ok(result) => json!({"jsonrpc": "2.0", "id": id, "result": result}),
                        Err(error) => {
                            json!({"jsonrpc": "2.0", "id": id, "error": {"code": INTERNAL_ERROR, "message": error}})
                        }
                    };
                    let _ = client.send(&response);
                }
            }
            (None, None) => {}
        }
    }
    client.closed.store(true, Ordering::Release);
    lock(&client.pending).clear();
}

fn handle_server_message(
    method: &str,
    params: Option<&RawValue>,
    index_events: &Sender<IndexEvent>,
    err_out: Option<&Output>,
    notices: &Mutex<Vec<String>>,
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
        // Reports of the indexing progress; begin and end carry nothing the
        // completion notification does not.
        "$/progress" if params["value"]["kind"] == "report" => {
            if let Some(message) = params["value"]["message"].as_str() {
                let _ = index_events.send(IndexEvent::Progress(message.to_owned()));
            }
        }
        "shopware/indexingFailed" => {
            let message = params["message"].as_str().unwrap_or("").to_owned();
            let _ = index_events.send(IndexEvent::Failed(message));
        }
        // Notices about the project (`Server::notice`): kept until the
        // command has printed its results, so they do not interleave.
        "window/logMessage" => {
            if let Some(message) = params["message"].as_str()
                && !message.is_empty()
            {
                let mut notices = lock(notices);
                if !notices.iter().any(|known| known == message) {
                    notices.push(message.to_owned());
                }
            }
        }
        "window/showMessage" => {
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

/// One finding of `check`.
#[derive(Debug, Clone, Serialize)]
pub struct DiagnosticOutput {
    pub uri: String,
    pub diagnostic: Diagnostic,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub related: Option<Vec<DiagnosticRelatedInformation>>,
    /// Whether `check --fix` fixes the finding. Absent where it was not
    /// determined (the MCP diagnostics tool lists fixes itself).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fixable: Option<bool>,
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
