//! Go-oracle and performance driver for the composition root.
//!
//! ```text
//! oracle registrations <root> [cache]   # JSON: registrations, catalog, indexers
//! oracle index <root> [cache]           # CLI-like index run; JSON timing + stats
//! ```
//!
//! The cache defaults to `SHOPWARE_LSP_CACHE_DIR` resolution. Both modes start
//! the real server over JSON-RPC pipes with the production factory, like the
//! Go CLI session.

#![allow(dead_code)]

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde_json::{Value, json};
#[allow(unused_imports)]
use swlsp_app::workspace_factory_in;
use swlsp_lsp::{BoxError, Context, Server, WorkspaceRuntime};

#[path = "../src/tests/support.rs"]
mod support;

use support::TestClient;

fn initialize_params(root: &Path) -> Value {
    let uri = swlsp_uriutil::file_uri(&root.to_string_lossy());
    json!({
        "processId": std::process::id(),
        "rootUri": uri,
        "initializationOptions": {"cliMode": true, "allowUnsupportedProject": false, "watchFiles": false},
        "workspaceFolders": [{"uri": uri, "name": root.file_name().map(|name| name.to_string_lossy().into_owned()).unwrap_or_default()}],
        "capabilities": {"textDocument": {"diagnostic": {}, "codeAction": {"dataSupport": true, "resolveSupport": {"properties": ["edit"]}}}},
    })
}

struct Captured {
    registrations: Vec<swlsp_app::Registration>,
    indexers: Vec<(String, String, bool)>,
}

fn server_with_capture(cache: Option<PathBuf>) -> (Server, Arc<Mutex<Option<Captured>>>) {
    let server = Server::new(None, "", "oracle");
    server.configure_project_detection(true, false);
    let captured: Arc<Mutex<Option<Captured>>> = Arc::default();
    let sink = captured.clone();
    server.set_workspace_factory(Arc::new(
        move |ctx: &Context, root: &str, server: &Server| {
            let workspace = match &cache {
                Some(cache) => swlsp_app::Workspace::new_in(ctx, root, server, cache),
                None => swlsp_app::Workspace::new(ctx, root, server),
            }
            .map_err(BoxError::from)?;
            let configuration = server.effective_configuration();
            *sink.lock().unwrap() = Some(Captured {
                registrations: workspace.registrations().to_vec(),
                indexers: workspace
                    .indexers()
                    .iter()
                    .map(|index| {
                        let domain = swlsp_app::domain_for_indexer(index.id());
                        (
                            index.id().to_owned(),
                            domain.to_owned(),
                            configuration.domain_enabled(domain),
                        )
                    })
                    .collect(),
            });
            Ok(Box::new(workspace) as Box<dyn WorkspaceRuntime>)
        },
    ));
    (server, captured)
}

fn registrations(root: &Path, cache: Option<PathBuf>) {
    let (server, captured) = server_with_capture(cache);
    let client = TestClient::start(&server);
    let initialized = client.call("initialize", initialize_params(root));
    let catalog = client.call("shopware/configuration/catalog", json!({}));
    let captured = captured
        .lock()
        .unwrap()
        .take()
        .expect("workspace was built");
    let registrations: Vec<Value> = captured
        .registrations
        .iter()
        .map(|registration| {
            json!({"method": registration.method, "name": registration.name, "languages": registration.languages})
        })
        .collect();
    let indexers: Vec<Value> = captured
        .indexers
        .iter()
        .map(|(id, domain, scanned)| json!({"id": id, "domain": domain, "scanned": scanned}))
        .collect();
    println!(
        "{}",
        serde_json::to_string_pretty(&json!({
            "registrations": registrations,
            "indexers": indexers,
            "catalog": catalog,
            "initialize": initialized,
        }))
        .unwrap()
    );
    client.shutdown().unwrap();
}

fn index(root: &Path, cache: Option<PathBuf>) {
    let started = Instant::now();
    let (server, _) = server_with_capture(cache);
    let client = TestClient::start(&server);
    client.call("initialize", initialize_params(root));
    let initialized = started.elapsed();
    client.notify("initialized", json!({}));
    let deadline = Instant::now() + Duration::from_secs(3600);
    let completed = loop {
        let found = client
            .notifications
            .lock()
            .unwrap()
            .iter()
            .find(|(method, _)| {
                method == "shopware/indexingCompleted" || method == "shopware/indexingFailed"
            })
            .cloned();
        if let Some((method, params)) = found {
            assert_eq!(method, "shopware/indexingCompleted", "{params}");
            break params;
        }
        assert!(Instant::now() < deadline, "indexing did not finish");
        std::thread::sleep(Duration::from_millis(20));
    };
    let total = started.elapsed();
    let stats = client.call("shopware/index/stats", json!({}));
    println!(
        "{}",
        json!({
            "initializeSeconds": initialized.as_secs_f64(),
            "indexSeconds": completed["timeInSeconds"],
            "totalSeconds": total.as_secs_f64(),
            "trackedFiles": stats["trackedFiles"],
            "indexers": stats["indexers"],
        })
    );
    client.shutdown().unwrap();
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let usage = "usage: oracle (registrations|index) <root> [cache]";
    let mode = args.get(1).expect(usage);
    let root = PathBuf::from(args.get(2).expect(usage));
    let root = std::fs::canonicalize(&root).unwrap_or(root);
    let cache = args.get(3).map(PathBuf::from);
    match mode.as_str() {
        "registrations" => registrations(&root, cache),
        "index" => index(&root, cache),
        _ => panic!("{usage}"),
    }
}
