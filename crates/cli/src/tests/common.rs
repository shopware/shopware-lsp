//! Shared test helpers: the process-wide test cache and an MCP test client.

use std::io::Write;
use std::path::Path;
use std::sync::{Once, RwLock, RwLockReadGuard};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use serde_json::{Value, json};

use crate::{Buffer, Cancel, CliError, Options, Runner};

static INIT: Once = Once::new();

/// One cache parent for the whole process. Production cache
/// folders are keyed by workspace root, and every test owns a unique root.
pub fn init() {
    INIT.call_once(|| {
        let cache =
            std::env::temp_dir().join(format!("shopware-lsp-cli-tests-{}", std::process::id()));
        std::fs::create_dir_all(&cache).expect("create test cache");
        // SAFETY: set once before any test reads it.
        unsafe { std::env::set_var(swlsp_app::CACHE_DIR_ENV, &cache) };
    });
}

pub fn temp_dir() -> tempfile::TempDir {
    init();
    tempfile::Builder::new()
        .prefix("cli-test-")
        .tempdir()
        .expect("create temp dir")
}

pub fn path(root: &Path, relative: &str) -> String {
    root.join(relative).to_string_lossy().into_owned()
}

pub fn write(path: &str, content: &str) {
    std::fs::create_dir_all(Path::new(path).parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

pub fn args(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

pub struct Run {
    pub result: Result<(), CliError>,
    pub out: String,
    pub err: String,
}

pub fn run_with(options: Options, cancel: &Cancel, values: &[&str]) -> Run {
    let _guard = ENV_LOCK.read().unwrap_or_else(|p| p.into_inner());
    run_unlocked(options, cancel, values)
}

/// Callers must hold `ENV_LOCK`.
pub fn run_unlocked(options: Options, cancel: &Cancel, values: &[&str]) -> Run {
    init();
    let out = Buffer::new();
    let err = Buffer::new();
    let mut runner = Runner::new(options);
    let result = runner.run(
        cancel,
        &args(values),
        Box::new(std::io::empty()),
        out.output(),
        err.output(),
    );
    Run {
        result,
        out: out.contents(),
        err: err.contents(),
    }
}

pub fn test_options() -> Options {
    Options {
        version: "test".into(),
        ..Options::default()
    }
}

pub fn run(values: &[&str]) -> Run {
    run_with(
        Options {
            version: "test".into(),
            ..Options::default()
        },
        &Cancel::new(),
        values,
    )
}

/// Runs and requires success, returning stdout.
pub fn run_ok(values: &[&str]) -> String {
    let run = run(values);
    if let Err(error) = &run.result {
        panic!("{values:?} failed: {error}\nstderr: {}", run.err);
    }
    run.out
}

pub fn assert_err_contains(run: &Run, needle: &str) {
    match &run.result {
        Ok(()) => panic!("expected error containing {needle:?}, got success"),
        Err(error) => assert!(
            error.message.contains(needle),
            "error {:?} does not contain {needle:?}",
            error.message
        ),
    }
}

/// Every run and MCP request holds this for reading; tests that change
/// process-wide state (environment variables, the global log sink) hold it
/// for writing.
pub static ENV_LOCK: RwLock<()> = RwLock::new(());

pub struct ToolResult {
    pub is_error: bool,
    pub text: String,
    pub structured: Value,
}

/// An MCP client driving `shopware-lsp --root <root> mcp` in-process over a
/// pipe.
pub struct McpClient {
    writer: Option<std::io::PipeWriter>,
    out: Buffer,
    thread: Option<JoinHandle<Result<(), CliError>>>,
    next_id: i64,
    pub initialize: Value,
}

impl McpClient {
    pub fn connect(root: &Path) -> McpClient {
        let _guard: RwLockReadGuard<'_, ()> = ENV_LOCK.read().unwrap_or_else(|p| p.into_inner());
        Self::connect_unlocked(root)
    }

    /// Callers must hold `ENV_LOCK` for the startup.
    pub fn connect_unlocked(root: &Path) -> McpClient {
        init();
        let (reader, writer) = std::io::pipe().expect("pipe");
        let out = Buffer::new();
        let output = out.output();
        let root = root.to_string_lossy().into_owned();
        let thread = std::thread::spawn(move || {
            let mut runner = Runner::new(Options {
                version: "test".into(),
                ..Options::default()
            });
            runner.run(
                &Cancel::new(),
                &args(&["--root", &root, "--allow-unsupported-project", "mcp"]),
                Box::new(reader),
                output,
                Buffer::new().output(),
            )
        });
        let mut client = McpClient {
            writer: Some(writer),
            out,
            thread: Some(thread),
            next_id: 1,
            initialize: Value::Null,
        };
        let response = client.request_unlocked(
            "initialize",
            json!({"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "shopware-lsp-test", "version": "test"}}),
        );
        client.initialize = response["result"].clone();
        client.notify("notifications/initialized", json!({}));
        client
    }

    fn send(&mut self, message: &Value) {
        let writer = self.writer.as_mut().expect("client open");
        writeln!(writer, "{message}").expect("write request");
        writer.flush().unwrap();
    }

    pub fn notify(&mut self, method: &str, params: Value) {
        self.send(&json!({"jsonrpc": "2.0", "method": method, "params": params}));
    }

    pub fn request(&mut self, method: &str, params: Value) -> Value {
        let _guard = ENV_LOCK.read().unwrap_or_else(|p| p.into_inner());
        self.request_unlocked(method, params)
    }

    fn request_unlocked(&mut self, method: &str, params: Value) -> Value {
        let id = self.next_id;
        self.next_id += 1;
        self.send(&json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params}));
        let deadline = Instant::now() + Duration::from_secs(600);
        loop {
            for line in self.out.contents().lines() {
                if let Ok(message) = serde_json::from_str::<Value>(line)
                    && message["id"] == json!(id)
                {
                    return message;
                }
            }
            if let Some(thread) = &self.thread
                && thread.is_finished()
            {
                panic!("MCP server exited: {}", self.out.contents());
            }
            assert!(Instant::now() < deadline, "timed out waiting for {method}");
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    pub fn list_tools(&mut self) -> Vec<Value> {
        let response = self.request("tools/list", json!({}));
        response["result"]["tools"]
            .as_array()
            .cloned()
            .expect("tools")
    }

    pub fn tool_names(&mut self) -> Vec<String> {
        self.list_tools()
            .iter()
            .map(|tool| tool["name"].as_str().unwrap().to_owned())
            .collect()
    }

    pub fn call_tool(&mut self, name: &str, arguments: Value) -> ToolResult {
        let response = self.request("tools/call", json!({"name": name, "arguments": arguments}));
        let result = &response["result"];
        assert!(result.is_object(), "tools/call {name} failed: {response}");
        let text = result["content"]
            .as_array()
            .map(|items| {
                items
                    .iter()
                    .filter_map(|item| item["text"].as_str())
                    .collect::<String>()
            })
            .unwrap_or_default();
        ToolResult {
            is_error: result["isError"].as_bool().unwrap_or(false),
            text,
            structured: result["structuredContent"].clone(),
        }
    }

    /// Calls a tool and requires a successful result.
    pub fn call_ok(&mut self, name: &str, arguments: Value) -> Value {
        let result = self.call_tool(name, arguments);
        assert!(!result.is_error, "{name}: {}", result.text);
        result.structured
    }
}

impl Drop for McpClient {
    fn drop(&mut self) {
        self.writer.take();
        if let Some(thread) = self.thread.take() {
            let result = thread.join();
            if !std::thread::panicking() {
                let result = result.expect("MCP thread panicked");
                assert!(result.is_ok(), "MCP server failed: {result:?}");
            }
        }
    }
}

pub fn action_titles(output: &Value) -> Vec<String> {
    output["actions"]
        .as_array()
        .map(|actions| {
            actions
                .iter()
                .filter_map(|action| action["title"].as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}
