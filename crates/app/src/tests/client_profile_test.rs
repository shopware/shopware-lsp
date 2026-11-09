//! Capabilities advertised per client profile.

use serde_json::{Map, Value, json};
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri};
use crate::workspace_factory_in;

fn initialize_client_profile(profile: &str) -> Map<String, Value> {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let server = Server::new(None, "", "test");
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    let result = client.call(
        "initialize",
        json!({
            "rootUri": file_uri(root.path()),
            "capabilities": {"textDocument": {"diagnostic": {}}},
            "initializationOptions": {
                "shopwareClient": {
                    "protocolVersion": swlsp_lsp::CLIENT_PROTOCOL_VERSION,
                    "presentationProfile": profile,
                    "supportedCommands": [],
                },
            },
        }),
    );
    client.call("shutdown", json!({}));
    client.notify("exit", json!({}));
    client.shutdown().unwrap();
    result["capabilities"].as_object().unwrap().clone()
}

#[test]
fn framework_client_profile_advertises_only_framework_presentation() {
    let full = initialize_client_profile("full");
    let framework = initialize_client_profile("framework");

    for capability in [
        "implementationProvider",
        "typeHierarchyProvider",
        "colorProvider",
    ] {
        assert!(full.contains_key(capability), "full lacks {capability}");
        assert!(
            !framework.contains_key(capability),
            "framework has {capability}"
        );
    }
    for capability in [
        "completionProvider",
        "definitionProvider",
        "referencesProvider",
        "hoverProvider",
        "signatureHelpProvider",
        "diagnosticProvider",
        "codeActionProvider",
        "workspaceSymbolProvider",
        "semanticTokensProvider",
    ] {
        assert!(
            framework.contains_key(capability),
            "framework lacks {capability}"
        );
    }
    let commands = framework["executeCommandProvider"]["commands"]
        .as_array()
        .unwrap();
    assert!(commands.contains(&json!("shopware/integration/catalog")));
}
