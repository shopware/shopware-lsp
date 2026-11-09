//! PHP editor basics through the server: lifecycle and configuration.

use serde_json::{Value, json};

use super::support::{TestClient, file_uri, framework_client_options, workspace_server};

fn array(value: &Value) -> Vec<Value> {
    value.as_array().cloned().unwrap_or_default()
}

// Exercise production registration and routing with source that exists only
// in the editor. Close must remove it; re-opening must use the new source
// snapshot.
#[test]
fn php_editor_basics_lifecycle_and_configuration() {
    for mode in ["enabled", "disabled", "framework"] {
        let cache = tempfile::tempdir().unwrap();
        let project = tempfile::tempdir().unwrap();
        let root = project.path();
        let server = workspace_server(root, cache.path());
        let client = TestClient::start(&server);
        let mut options = json!({});
        if mode == "framework" {
            options["shopwareClient"] = framework_client_options();
        }
        if mode == "disabled" {
            options["configuration"] = json!({"features": {
                "documentSymbols": false, "documentHighlights": false,
                "foldingRanges": false, "selectionRanges": false,
            }});
        }
        client.call(
            "initialize",
            json!({"rootUri": file_uri(root), "initializationOptions": options}),
        );
        let uri = file_uri(&root.join("Live.php"));
        let source = "<?php\nclass First {\n function run($value) {\n  return $value;\n }\n}\n";
        client.open(&uri, 1, source);
        client.wait_diagnostics(&[&uri]);
        let check = |expected_name: &str, active: bool| {
            let mut base = json!({"textDocument": {"uri": uri}});
            let symbols = array(&client.call("textDocument/documentSymbol", base.clone()));
            let folds = array(&client.call("textDocument/foldingRange", base.clone()));
            base["position"] = json!({"line": 3, "character": 10});
            let highlights = array(&client.call("textDocument/documentHighlight", base.clone()));
            base["positions"] = json!([{"line": 3, "character": 10}]);
            let selections = array(&client.call("textDocument/selectionRange", base));
            if active {
                assert_eq!(symbols.len(), 1, "{mode}: symbols");
                assert_eq!(symbols[0]["name"], json!(expected_name), "{mode}");
                assert_eq!(highlights.len(), 2, "{mode}: highlights");
                assert!(!folds.is_empty(), "{mode}: folds");
                assert_eq!(selections.len(), 1, "{mode}: selections");
                assert!(!selections[0]["parent"].is_null(), "{mode}: parent");
            } else {
                assert!(symbols.is_empty(), "{mode}: symbols {symbols:?}");
                assert!(highlights.is_empty(), "{mode}: highlights");
                assert!(folds.is_empty(), "{mode}: folds");
                assert!(selections.is_empty(), "{mode}: selections");
            }
        };
        check("First", mode == "enabled");
        let changed = "<?php\nclass Changed {\n function run($other) {\n  return $other;\n }\n}\n";
        client.change(&uri, 2, changed);
        client.wait_diagnostics(&[&uri]);
        check("Changed", mode == "enabled");
        client.close_document(&uri);
        client.wait_diagnostics(&[&uri]);
        check("", false);
        client.open(&uri, 3, source);
        client.wait_diagnostics(&[&uri]);
        check("First", mode == "enabled");
        client.shutdown().unwrap();
    }
}
