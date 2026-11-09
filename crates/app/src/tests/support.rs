//! A minimal JSON-RPC client driving a real [`Server`] over pipes.

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use serde_json::{Value, json};
use swlsp_lsp::Server;

pub const TIMEOUT: Duration = Duration::from_secs(30);

type Pending = Arc<Mutex<HashMap<i64, Sender<Value>>>>;

pub struct TestClient {
    writer: Arc<Mutex<Option<std::io::PipeWriter>>>,
    pending: Pending,
    next_id: AtomicI64,
    pub published: Receiver<Value>,
    pub notifications: Arc<Mutex<Vec<(String, Value)>>>,
    server_thread: Option<JoinHandle<Result<(), String>>>,
    reader_thread: Option<JoinHandle<()>>,
}

fn write_message(writer: &mut impl Write, message: &Value) -> std::io::Result<()> {
    let body = serde_json::to_vec(message)?;
    write!(writer, "Content-Length: {}\r\n\r\n", body.len())?;
    writer.write_all(&body)?;
    writer.flush()
}

fn read_message(reader: &mut impl BufRead) -> Option<Value> {
    let mut length = None;
    loop {
        let mut line = String::new();
        if reader.read_line(&mut line).ok()? == 0 {
            return None;
        }
        let line = line.trim_end();
        if line.is_empty() {
            break;
        }
        if let Some(value) = line.strip_prefix("Content-Length:") {
            length = value.trim().parse::<usize>().ok();
        }
    }
    let mut body = vec![0; length?];
    reader.read_exact(&mut body).ok()?;
    serde_json::from_slice(&body).ok()
}

impl TestClient {
    pub fn start(server: &Server) -> TestClient {
        let (server_in, client_out) = std::io::pipe().expect("pipe");
        let (client_in, server_out) = std::io::pipe().expect("pipe");
        let running = server.clone();
        let server_thread = std::thread::spawn(move || {
            running
                .start(server_in, server_out)
                .map_err(|error| error.to_string())
        });
        let writer = Arc::new(Mutex::new(Some(client_out)));
        let pending: Pending = Arc::default();
        let notifications: Arc<Mutex<Vec<(String, Value)>>> = Arc::default();
        let (published_tx, published) = channel();
        let reader_thread = {
            let writer = writer.clone();
            let pending = pending.clone();
            let notifications = notifications.clone();
            std::thread::spawn(move || {
                let mut reader = BufReader::new(client_in);
                while let Some(message) = read_message(&mut reader) {
                    let method = message.get("method").and_then(Value::as_str);
                    match (method, message.get("id")) {
                        (Some(_), Some(id)) => {
                            // Server-to-client request: acknowledge it.
                            let response = json!({"jsonrpc": "2.0", "id": id, "result": null});
                            if let Some(writer) = writer.lock().unwrap().as_mut() {
                                let _ = write_message(writer, &response);
                            }
                        }
                        (Some(method), None) => {
                            let params = message.get("params").cloned().unwrap_or(Value::Null);
                            if method == "textDocument/publishDiagnostics" {
                                let _ = published_tx.send(params.clone());
                            }
                            notifications
                                .lock()
                                .unwrap()
                                .push((method.to_owned(), params));
                        }
                        (None, Some(id)) => {
                            if let Some(id) = id.as_i64()
                                && let Some(sender) = pending.lock().unwrap().remove(&id)
                            {
                                let _ = sender.send(message);
                            }
                        }
                        (None, None) => {}
                    }
                }
            })
        };
        TestClient {
            writer,
            pending,
            next_id: AtomicI64::new(1),
            published,
            notifications,
            server_thread: Some(server_thread),
            reader_thread: Some(reader_thread),
        }
    }

    fn send(&self, message: &Value) {
        let mut writer = self.writer.lock().unwrap();
        write_message(writer.as_mut().expect("client open"), message).expect("write message");
    }

    /// Sends a request and returns the full response (`result` or `error`).
    pub fn request(&self, method: &str, params: Value) -> Value {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let (sender, receiver) = channel();
        self.pending.lock().unwrap().insert(id, sender);
        self.send(&json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params}));
        receiver
            .recv_timeout(TIMEOUT)
            .unwrap_or_else(|_| panic!("timed out waiting for {method}"))
    }

    /// Sends a request and returns its result; panics on an error response.
    pub fn call(&self, method: &str, params: Value) -> Value {
        let response = self.request(method, params);
        if let Some(error) = response.get("error") {
            panic!("{method} failed: {error}");
        }
        response.get("result").cloned().unwrap_or(Value::Null)
    }

    pub fn notify(&self, method: &str, params: Value) {
        self.send(&json!({"jsonrpc": "2.0", "method": method, "params": params}));
    }

    pub fn initialize(&self, root: &Path, options: Value) -> Value {
        let result = self.call(
            "initialize",
            json!({
                "rootUri": swlsp_uriutil::file_uri(&root.to_string_lossy()),
                "initializationOptions": options,
                "capabilities": {},
            }),
        );
        self.notify("initialized", json!({}));
        result
    }

    pub fn open(&self, uri: &str, version: i32, text: &str) {
        self.notify(
            "textDocument/didOpen",
            json!({"textDocument": {"uri": uri, "version": version, "text": text}}),
        );
    }

    pub fn change(&self, uri: &str, version: i32, text: &str) {
        self.notify(
            "textDocument/didChange",
            json!({
                "textDocument": {"uri": uri, "version": version},
                "contentChanges": [{"text": text}],
            }),
        );
    }

    pub fn close_document(&self, uri: &str) {
        self.notify(
            "textDocument/didClose",
            json!({"textDocument": {"uri": uri}}),
        );
    }

    /// Waits until diagnostics were published for every URI and returns the
    /// last publication seen for each.
    pub fn wait_diagnostics(&self, uris: &[&str]) -> HashMap<String, Value> {
        let deadline = Instant::now() + TIMEOUT;
        let mut results = HashMap::new();
        while uris.iter().any(|uri| !results.contains_key(*uri)) {
            let remaining = deadline.saturating_duration_since(Instant::now());
            let params = self.published.recv_timeout(remaining).unwrap_or_else(|_| {
                panic!(
                    "timed out waiting for diagnostics: {:?}",
                    uris.iter()
                        .filter(|uri| !results.contains_key(**uri))
                        .collect::<Vec<_>>()
                )
            });
            let uri = params["uri"].as_str().unwrap_or_default().to_owned();
            if uris.contains(&uri.as_str()) {
                results.insert(uri, params);
            }
        }
        results
    }

    /// Waits for a notification with `method`.
    pub fn wait_notification(&self, method: &str) -> Value {
        let deadline = Instant::now() + TIMEOUT;
        loop {
            if let Some((_, params)) = self
                .notifications
                .lock()
                .unwrap()
                .iter()
                .find(|(name, _)| name == method)
            {
                return params.clone();
            }
            assert!(Instant::now() < deadline, "timed out waiting for {method}");
            std::thread::sleep(Duration::from_millis(10));
        }
    }

    /// Waits up to `timeout` for the first notification named in `methods`.
    #[cfg(feature = "integration")]
    pub fn wait_any_notification(&self, methods: &[&str], timeout: Duration) -> (String, Value) {
        let deadline = Instant::now() + timeout;
        loop {
            if let Some(found) = self
                .notifications
                .lock()
                .unwrap()
                .iter()
                .find(|(name, _)| methods.contains(&name.as_str()))
            {
                return found.clone();
            }
            assert!(
                Instant::now() < deadline,
                "timed out waiting for {methods:?}"
            );
            std::thread::sleep(Duration::from_millis(10));
        }
    }

    /// Disconnects and returns the server's close result.
    pub fn shutdown(mut self) -> Result<(), String> {
        self.disconnect()
    }

    fn disconnect(&mut self) -> Result<(), String> {
        self.writer.lock().unwrap().take();
        let result = match self.server_thread.take() {
            Some(thread) => thread
                .join()
                .unwrap_or_else(|_| Err("server panicked".into())),
            None => Ok(()),
        };
        if let Some(thread) = self.reader_thread.take() {
            let _ = thread.join();
        }
        result
    }
}

impl Drop for TestClient {
    fn drop(&mut self) {
        let _ = self.disconnect();
    }
}

pub fn write_file(root: &Path, relative: &str, content: &str) -> std::path::PathBuf {
    let path = root.join(relative);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(&path, content).unwrap();
    path
}

pub fn file_uri(path: &Path) -> String {
    swlsp_uriutil::file_uri(&path.to_string_lossy())
}

/// A test server for `root` with the
/// production workspace factory caching below `cache`.
pub fn workspace_server(root: &Path, cache: &Path) -> Server {
    let server = Server::new(None, &root.to_string_lossy(), "test");
    server.set_workspace_factory(crate::workspace_factory_in(cache.to_path_buf()));
    server
}

/// `shopwareClient` options selecting the framework presentation profile.
pub fn framework_client_options() -> Value {
    json!({
        "protocolVersion": swlsp_lsp::CLIENT_PROTOCOL_VERSION,
        "presentationProfile": "framework",
        "supportedCommands": [],
    })
}
