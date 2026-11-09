//! The framework client profile against a real Shopware checkout.

use std::time::Duration;

use serde_json::{Value, json};
use swlsp_lsp::Server;

use crate::tests::support::{TestClient, framework_client_options};
use crate::workspace_factory_in;

#[test]
fn shopware_trunk_framework_client_profile() {
    let root = crate::real_world_root_or_skip!();
    let cache = tempfile::tempdir().unwrap();
    let server = Server::new(None, "", "integration-test");
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);

    let initialized = client.call(
        "initialize",
        json!({
            "rootUri": swlsp_uriutil::file_uri(&root),
            "capabilities": {"textDocument": {"diagnostic": {}}},
            "initializationOptions": {"shopwareClient": framework_client_options()},
        }),
    );
    let capabilities = initialized["capabilities"]
        .as_object()
        .expect("capabilities");
    assert!(!capabilities.contains_key("implementationProvider"));
    assert!(!capabilities.contains_key("typeHierarchyProvider"));
    assert!(capabilities.contains_key("completionProvider"));
    assert!(capabilities.contains_key("definitionProvider"));
    assert!(capabilities.contains_key("diagnosticProvider"));
    client.notify("initialized", json!({}));
    let (method, params) = client.wait_any_notification(
        &["shopware/indexingCompleted", "shopware/indexingFailed"],
        Duration::from_secs(15 * 60),
    );
    assert_eq!(
        method, "shopware/indexingCompleted",
        "framework integration indexing failed: {params}"
    );

    let symbols = client.call("workspace/symbol", json!({"query": "SystemConfigService"}));
    assert!(
        symbols
            .as_array()
            .is_some_and(|symbols| !symbols.is_empty()),
        "{symbols}"
    );

    let path = format!("{root}/src/Core/Framework/Demodata/DemodataContext.php");
    let source = std::fs::read_to_string(&path).unwrap();
    let uri = swlsp_uriutil::file_uri(&path);
    client.notify(
        "textDocument/didOpen",
        json!({"textDocument": {"uri": uri, "languageId": "php", "version": 1, "text": source}}),
    );
    let diagnostics = client.call(
        "textDocument/diagnostic",
        json!({"textDocument": {"uri": uri}}),
    );
    for diagnostic in diagnostics["items"].as_array().cloned().unwrap_or_default() {
        let source = diagnostic["source"].as_str().unwrap_or_default();
        assert!(!source.starts_with("shopware-php"), "{diagnostic}");
        let code = match &diagnostic["code"] {
            Value::String(code) => code.as_str(),
            _ => "",
        };
        assert!(!code.starts_with("php."), "{diagnostic}");
    }

    client.call("shutdown", json!(null));
    client.notify("exit", json!(null));
    client.shutdown().unwrap();
}
