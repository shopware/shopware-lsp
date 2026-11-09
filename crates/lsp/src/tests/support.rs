//! Shared fixtures: the YAML test inspection, its quick fix and an
//! in-process JSON-RPC client.

use std::collections::HashMap;
use std::io::{BufReader, PipeWriter, Write};
use std::path::Path;
use std::sync::atomic::{AtomicI32, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use crossbeam_channel::{Receiver, Sender, bounded, unbounded};
use lsp_server::{Message, Notification, Request, RequestId, Response};
use serde_json::{Value, json};
use swlsp_language::LanguageId;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};

use crate::context::lock;
use crate::server::write;
use crate::*;

pub const TEST_FIX_ID: &str = "replace-bad";
pub const TIMEOUT: Duration = Duration::from_secs(5);

pub fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

pub fn position(line: u32, character: u32) -> protocol::Position {
    protocol::Position { line, character }
}

pub fn protocol_range_of(start: (u32, u32), end: (u32, u32)) -> protocol::Range {
    protocol::Range {
        start: position(start.0, start.1),
        end: position(end.0, end.1),
    }
}

pub fn file_uri(path: &Path) -> String {
    swlsp_uriutil::file_uri(&path.to_string_lossy())
}

pub fn test_definition(id: &str, disabled_by_default: bool) -> InspectionDefinition {
    InspectionDefinition {
        id: id.into(),
        description: "Test inspection.",
        languages: vec![LanguageId::Yaml],
        problems: vec![ProblemDefinition {
            id: id.into(),
            source: "test".into(),
            default_severity: protocol::DiagnosticSeverity::WARNING,
            disabled_by_default,
        }],
        ..InspectionDefinition::default()
    }
}

pub fn element_at(
    document: &TextDocument,
    start: u32,
    end: u32,
) -> Option<swlsp_syntax::SyntaxElement> {
    document.root()?.descendant_for_range(range(start, end))
}

/// Reports `bad` in `value: bad` with one bound replacement fix.
#[derive(Default)]
pub struct TestInspection;

impl Inspection for TestInspection {
    fn definition(&self) -> InspectionDefinition {
        test_definition("test.invalid-value", false)
    }

    fn inspect(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        report_invalid_value(
            document,
            reporter,
            vec![bind_fix(TEST_FIX_ID, json!({"replacement": "good"}))],
        )
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        vec![Arc::new(TestInspectionFix)]
    }
}

pub fn report_invalid_value(
    document: &Arc<TextDocument>,
    reporter: &mut dyn ProblemReporter,
    fixes: Vec<BoundFix>,
) -> Result<(), BoxError> {
    reporter.report(Problem {
        id: "test.invalid-value".into(),
        range: range(7, 10),
        element: element_at(document, 7, 10),
        message: "Invalid value".into(),
        payload: Some(json!({"replacement": "good"})),
        fixes,
        ..Problem::default()
    })
}

pub struct TestInspectionFix;

impl QuickFix for TestInspectionFix {
    fn id(&self) -> FixId {
        TEST_FIX_ID.into()
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: HashMap<String, String> = fix.fix_payload()?;
        let replacement = payload.get("replacement").cloned().unwrap_or_default();
        if replacement.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Replace with {replacement}"),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for TestInspectionFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: HashMap<String, String> = fix.fix_payload()?;
        let target = text_range(fix.document.line_index(), fix.diagnostic.range);
        let mut builder = Builder::new(fix.document.source());
        builder.replace_range(
            target,
            payload.get("replacement").cloned().unwrap_or_default(),
        )?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![DocumentPlan::new(
                fix.document.uri.clone(),
                Some(fix.document.version),
                fix.document.source_arc().clone(),
                edits,
            )],
            ..WorkspacePlan::default()
        })
    }
}

pub fn enable_resolve_support(server: &Server) {
    write(&server.session).code_action_resolve_support = true;
}

pub fn register_command(
    server: &Server,
    name: &str,
    command: impl Fn(&Context, Option<&Value>) -> Result<Value, BoxError> + Send + Sync + 'static,
) {
    server.register_command(name, Arc::new(command));
}

pub fn write_project_configuration(root: &Path, source: &str) {
    let path = swlsp_projectconfig::path(&root.to_string_lossy());
    std::fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

pub fn temp_dir() -> tempfile::TempDir {
    let base = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../target/agent-lsp/tmp");
    std::fs::create_dir_all(&base).unwrap();
    let base = std::fs::canonicalize(base).unwrap();
    tempfile::Builder::new()
        .prefix("lsp-test")
        .tempdir_in(base)
        .unwrap()
}

pub fn root_string(dir: &tempfile::TempDir) -> String {
    dir.path().to_string_lossy().into_owned()
}

pub fn initialize_params(root: &Path) -> protocol::InitializeParams {
    protocol::InitializeParams {
        root_uri: file_uri(root),
        ..protocol::InitializeParams::default()
    }
}

// -- in-process JSON-RPC client ---------------------------------------------

type RequestHook = Arc<dyn Fn(&str, &Value) + Send + Sync>;

/// A client connected to `Server::start` through OS pipes. Server requests
/// are answered with `null`; every server message is passed to the hook.
pub struct TestClient {
    writer: Arc<Mutex<Option<PipeWriter>>>,
    pending: Arc<Mutex<HashMap<RequestId, Sender<Response>>>>,
    next_id: AtomicI32,
    server_done: Receiver<Result<(), String>>,
    pub published: Receiver<protocol::PublishDiagnosticsParams>,
}

pub fn start_client(
    server: &Server,
    hook: impl Fn(&str, &Value) + Send + Sync + 'static,
) -> TestClient {
    let hook: RequestHook = Arc::new(hook);
    let (server_in, client_out) = std::io::pipe().unwrap();
    let (client_in, server_out) = std::io::pipe().unwrap();
    let (done_sender, server_done) = bounded(1);
    let serving = server.clone();
    thread::spawn(move || {
        let _ = done_sender.send(
            serving
                .start(server_in, server_out)
                .map_err(|error| error.to_string()),
        );
    });
    let pending: Arc<Mutex<HashMap<RequestId, Sender<Response>>>> = Arc::default();
    let (published_sender, published) = unbounded();
    let writer = Arc::new(Mutex::new(Some(client_out)));
    let client = TestClient {
        writer: writer.clone(),
        pending: pending.clone(),
        next_id: AtomicI32::new(1000),
        server_done,
        published,
    };
    thread::spawn(move || {
        let mut reader = BufReader::new(client_in);
        while let Ok(Some(message)) = Message::read(&mut reader) {
            match message {
                Message::Response(response) => {
                    if let Some(sender) = lock(&pending).remove(&response.id) {
                        let _ = sender.send(response);
                    }
                }
                Message::Request(request) => {
                    hook(&request.method, &request.params);
                    if let Some(out) = lock(&writer).as_mut() {
                        let _ = Message::Response(Response {
                            id: request.id,
                            result: Some(Value::Null),
                            error: None,
                        })
                        .write(out);
                        let _ = out.flush();
                    }
                }
                Message::Notification(notification) => {
                    hook(&notification.method, &notification.params);
                    if notification.method == "textDocument/publishDiagnostics"
                        && let Ok(params) = serde_json::from_value(notification.params)
                    {
                        let _ = published_sender.send(params);
                    }
                }
            }
        }
        lock(&pending).clear();
    });
    client
}

impl TestClient {
    fn send(&self, message: Message) {
        let mut writer = lock(&self.writer);
        if let Some(out) = writer.as_mut() {
            message.write(out).unwrap();
            out.flush().unwrap();
        }
    }

    pub fn notify(&self, method: &str, params: Value) {
        self.send(Message::Notification(Notification {
            method: method.into(),
            params,
        }));
    }

    pub fn call_with_id(
        &self,
        id: i32,
        method: &str,
        params: Value,
    ) -> Result<Value, (i32, String)> {
        let (sender, receiver) = bounded(1);
        let id = RequestId::from(id);
        lock(&self.pending).insert(id.clone(), sender);
        self.send(Message::Request(Request {
            id,
            method: method.into(),
            params,
        }));
        match receiver.recv_timeout(TIMEOUT * 2) {
            Ok(response) => match response.error {
                Some(error) => Err((error.code, error.message)),
                None => Ok(response.result.unwrap_or(Value::Null)),
            },
            Err(_) => Err((0, "connection closed or timed out".into())),
        }
    }

    pub fn call(&self, method: &str, params: Value) -> Result<Value, (i32, String)> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        self.call_with_id(id, method, params)
    }

    /// Starts a call on another thread.
    pub fn call_async(
        self: &Arc<Self>,
        id: i32,
        method: &str,
        params: Value,
    ) -> Receiver<Result<Value, (i32, String)>> {
        let (sender, receiver) = bounded(1);
        let client = self.clone();
        let method = method.to_owned();
        thread::spawn(move || {
            let _ = sender.send(client.call_with_id(id, &method, params));
        });
        receiver
    }

    /// Closes the client side; pending calls fail.
    pub fn close(&self) {
        *lock(&self.writer) = None;
        for (_, sender) in lock(&self.pending).drain() {
            drop(sender);
        }
    }

    pub fn wait_published(&self) -> protocol::PublishDiagnosticsParams {
        self.published
            .recv_timeout(TIMEOUT)
            .expect("timed out waiting for published diagnostics")
    }

    /// Closes the connection and waits for `Server::start` to return.
    pub fn shutdown(&self) -> Result<(), String> {
        self.close();
        self.server_done
            .recv_timeout(TIMEOUT)
            .expect("server did not stop")
    }
}

pub fn did_open(client: &TestClient, uri: &str, version: i32, text: &str) {
    client.notify(
        "textDocument/didOpen",
        json!({"textDocument": {"uri": uri, "version": version, "text": text}}),
    );
}

pub fn did_change(client: &TestClient, uri: &str, version: i32, text: &str) {
    client.notify(
        "textDocument/didChange",
        json!({
            "textDocument": {"uri": uri, "version": version},
            "contentChanges": [{"text": text}],
        }),
    );
}

pub fn recv<T>(receiver: &Receiver<T>, message: &str) -> T {
    receiver
        .recv_timeout(TIMEOUT)
        .unwrap_or_else(|_| panic!("{message}"))
}
