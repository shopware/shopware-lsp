//! Port of Go `internal/app/php_extract_method_test.go`.

use serde_json::{Value, json};

use super::support::{TestClient, file_uri, framework_client_options, workspace_server};

#[test]
fn php_extract_method_routing_and_configuration() {
    for mode in ["enabled", "domain-off", "framework"] {
        let cache = tempfile::tempdir().unwrap();
        let project = tempfile::tempdir().unwrap();
        let root = project.path();
        let server = workspace_server(root, cache.path());
        let client = TestClient::start(&server);
        let mut options = json!({});
        if mode == "domain-off" {
            options["configuration"] = json!({"domains": {"php": false}});
        }
        if mode == "framework" {
            options["shopwareClient"] = framework_client_options();
        }
        client.call(
            "initialize",
            json!({"rootUri": file_uri(root), "initializationOptions": options}),
        );
        let uri = file_uri(&root.join("Live.php"));
        client.open(
            &uri,
            1,
            "<?php class Demo { public function run($a) {\nreturn $a;\n} }",
        );
        client.wait_diagnostics(&[&uri]);
        let params = json!({
            "textDocument": {"uri": uri},
            "range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 0}},
            "context": {"only": ["refactor.extract"], "diagnostics": []},
        });
        let actions = client.call("textDocument/codeAction", params.clone());
        let actions = actions.as_array().cloned().unwrap_or_default();
        if mode != "enabled" {
            assert!(actions.is_empty(), "{mode}: {actions:?}");
            client.shutdown().unwrap();
            continue;
        }
        assert_eq!(actions.len(), 1);
        assert_eq!(
            actions[0]["title"],
            json!("Extract method 'extractedMethod'")
        );
        assert!(!actions[0]["edit"].is_null());
        assert_eq!(
            actions[0]["edit"]["documentChanges"][0]["textDocument"]["version"],
            json!(1)
        );
        client.change(
            &uri,
            2,
            "<?php class Demo { public function run($b) {\nreturn $b;\n} private function extractedMethod() {} }",
        );
        client.wait_diagnostics(&[&uri]);
        let actions: Vec<Value> = client
            .call("textDocument/codeAction", params)
            .as_array()
            .cloned()
            .unwrap_or_default();
        assert_eq!(actions.len(), 1);
        assert_eq!(
            actions[0]["title"],
            json!("Extract method 'extractedMethod2'")
        );
        assert_eq!(
            actions[0]["edit"]["documentChanges"][0]["textDocument"]["version"],
            json!(2)
        );
        client.shutdown().unwrap();
    }
}
