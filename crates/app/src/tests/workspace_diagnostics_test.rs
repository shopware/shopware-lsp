//! Project rules report problems in files nobody opened through
//! `workspace/diagnostic`.

use serde_json::{Value, json};
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri, write_file};
use crate::workspace_factory_in;

const CONTROLLER: &str = r"<?php
namespace Acme\Controller;
use Symfony\Component\Routing\Attribute\Route;
class PageController {
    #[Route('/page', name: 'acme.page')]
    public function page(): void {}
}
";

/// URI → codes of the full reports, and the URIs reported unchanged.
fn reports(report: &Value) -> (Vec<(String, Vec<String>)>, Vec<String>) {
    let mut full = Vec::new();
    let mut unchanged = Vec::new();
    for item in report["items"].as_array().unwrap() {
        let uri = item["uri"]
            .as_str()
            .unwrap()
            .rsplit('/')
            .next()
            .unwrap()
            .to_owned();
        if item["kind"] == json!("unchanged") {
            unchanged.push(uri);
            continue;
        }
        let codes = item["items"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|diagnostic| diagnostic["code"].as_str().map(str::to_owned))
            .filter(|code| code == "symfony.route.redefined")
            .collect();
        full.push((uri, codes));
    }
    full.sort();
    unchanged.sort();
    (full, unchanged)
}

fn previous(report: &Value) -> Value {
    Value::Array(
        report["items"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|item| item["resultId"].is_string())
            .map(|item| json!({"uri": item["uri"], "value": item["resultId"]}))
            .collect(),
    )
}

#[test]
fn project_rules_report_unopened_files_as_workspace_diagnostics() {
    let project = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = project.path();
    write_file(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.6.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}}}"#,
    );
    let controller = write_file(root, "src/Controller/PageController.php", CONTROLLER);
    write_file(
        root,
        "src/Resources/config/routes.yaml",
        "acme.page:\n  path: /other\n  controller: Acme\\Controller\\PageController::page\n",
    );
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    let initialized = client.call(
        "initialize",
        json!({
            "rootUri": file_uri(root),
            "initializationOptions": {},
            "capabilities": {"textDocument": {"diagnostic": {}}},
        }),
    );
    assert_eq!(
        initialized["capabilities"]["diagnosticProvider"]["workspaceDiagnostics"],
        json!(true)
    );
    client.notify("initialized", json!({}));
    client.wait_notification("shopware/indexingCompleted");

    let first = client.call("workspace/diagnostic", json!({"previousResultIds": []}));
    let route = vec!["symfony.route.redefined".to_owned()];
    assert_eq!(
        reports(&first).0,
        [
            ("PageController.php".to_owned(), route.clone()),
            ("routes.yaml".to_owned(), route.clone()),
        ],
        "{first}"
    );

    // Nothing changed: the client's result IDs are confirmed.
    let second = client.call(
        "workspace/diagnostic",
        json!({"previousResultIds": previous(&first)}),
    );
    assert_eq!(
        reports(&second),
        (
            Vec::new(),
            vec!["PageController.php".to_owned(), "routes.yaml".to_owned()]
        )
    );

    // Open documents are left to document pulls.
    let uri = file_uri(&controller);
    client.open(&uri, 1, CONTROLLER);
    let third = client.call(
        "workspace/diagnostic",
        json!({"previousResultIds": previous(&first)}),
    );
    assert_eq!(reports(&third).1, ["routes.yaml"]);

    // Fixing the collision on disk clears the unopened file's problems.
    write_file(
        root,
        "src/Resources/config/routes.yaml",
        "acme.other:\n  path: /other\n  controller: Acme\\Controller\\PageController::page\n",
    );
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(20);
    loop {
        let report = client.call(
            "workspace/diagnostic",
            json!({"previousResultIds": previous(&first)}),
        );
        if reports(&report).0 == [("routes.yaml".to_owned(), Vec::new())] {
            break;
        }
        assert!(
            std::time::Instant::now() < deadline,
            "not cleared: {report}"
        );
        std::thread::sleep(std::time::Duration::from_millis(50));
    }
    client.shutdown().unwrap();
}
