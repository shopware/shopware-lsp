//! Port of Go `internal/app/php_imports_test.go`.

use serde_json::{Value, json};

use super::support::{TestClient, file_uri, framework_client_options, workspace_server};

const SEVERITY_WARNING: i64 = 2;
const SEVERITY_HINT: i64 = 4;

#[test]
fn php_import_diagnostics_actions_and_configuration() {
    for mode in ["enabled", "rule-off", "domain-off", "framework", "severity"] {
        let cache = tempfile::tempdir().unwrap();
        let project = tempfile::tempdir().unwrap();
        let root = project.path();
        let server = workspace_server(root, cache.path());
        let client = TestClient::start(&server);
        let mut options = json!({});
        match mode {
            "rule-off" => {
                options["configuration"] =
                    json!({"diagnostics": {"rules": {"php.unusedImport": "off"}}});
            }
            "severity" => {
                options["configuration"] =
                    json!({"diagnostics": {"rules": {"php.unusedImport": "warning"}}});
            }
            "domain-off" => options["configuration"] = json!({"domains": {"php": false}}),
            "framework" => options["shopwareClient"] = framework_client_options(),
            _ => {}
        }
        client.call(
            "initialize",
            json!({
                "rootUri": file_uri(root),
                "initializationOptions": options,
                "capabilities": {"textDocument": {"codeAction": {
                    "dataSupport": true,
                    "resolveSupport": {"properties": ["edit"]},
                }}},
            }),
        );
        let uri = file_uri(&root.join("Live.php"));
        let source = "<?php\nuse Vendor\\Unused;\nclass Live {}\n";
        client.open(&uri, 1, source);
        let publication = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
        let diagnostic = publication["diagnostics"]
            .as_array()
            .cloned()
            .unwrap_or_default()
            .into_iter()
            .rfind(|item| item["code"] == json!("php.unusedImport"));
        if mode == "enabled" || mode == "severity" {
            let diagnostic = diagnostic
                .as_ref()
                .unwrap_or_else(|| panic!("{mode}: no diagnostic"));
            let severity = if mode == "severity" {
                SEVERITY_WARNING
            } else {
                SEVERITY_HINT
            };
            assert_eq!(diagnostic["severity"], json!(severity), "{mode}");
        } else {
            assert!(diagnostic.is_none(), "{mode}: {diagnostic:?}");
        }
        let mut params = json!({
            "textDocument": {"uri": uri},
            "range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 18}},
            "context": {"only": ["source.organizeImports"], "diagnostics": []},
        });
        let actions: Vec<Value> = client
            .call("textDocument/codeAction", params.clone())
            .as_array()
            .cloned()
            .unwrap_or_default();
        if mode == "domain-off" || mode == "framework" {
            assert!(actions.is_empty(), "{mode}: {actions:?}");
            client.shutdown().unwrap();
            continue;
        }
        assert_eq!(actions.len(), 1, "{mode}");
        assert_eq!(actions[0]["title"], json!("Organize Imports"));
        assert!(!actions[0]["edit"].is_null());
        let Some(diagnostic) = diagnostic else {
            client.shutdown().unwrap();
            continue;
        };
        params["context"] = json!({"only": ["quickfix"], "diagnostics": [diagnostic]});
        let actions: Vec<Value> = client
            .call("textDocument/codeAction", params)
            .as_array()
            .cloned()
            .unwrap_or_default();
        assert_eq!(actions.len(), 1, "{mode}");
        assert_eq!(actions[0]["title"], json!("Remove unused import 'Unused'"));
        assert!(actions[0]["edit"].is_null());
        assert!(!actions[0]["data"].is_null());
        let resolved = client.call("codeAction/resolve", actions[0].clone());
        assert!(!resolved["edit"].is_null(), "{mode}: {resolved}");
        client.change(&uri, 2, &format!("{source}new Unused;\n"));
        let changed = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
        for item in changed["diagnostics"]
            .as_array()
            .cloned()
            .unwrap_or_default()
        {
            assert_ne!(item["code"], json!("php.unusedImport"));
        }
        let resolved = client.call("codeAction/resolve", actions[0].clone());
        assert!(!resolved["disabled"].is_null(), "{mode}: {resolved}");
        client.shutdown().unwrap();
    }
}
