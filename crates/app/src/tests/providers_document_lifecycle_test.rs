//! Port of Go `internal/app/providers_document_lifecycle_test.go`.

use std::sync::Arc;

use serde_json::json;
use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::Indexer;
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri};
use crate::providers::register_administration_document_observer;
use crate::registrar::Registrar;

#[test]
fn administration_document_close_removes_overlay_and_refreshes_dependents() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    let cache = root.join("cache");
    let admin_index = Arc::new(AdminComponentIndexer::new(&cache.to_string_lossy(), None).unwrap());
    let server = Server::new(None, &root.to_string_lossy(), "test");
    register_administration_document_observer(
        &Registrar::new(&server),
        Some(admin_index.clone()),
        None,
    );
    let client = TestClient::start(&server);
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let definition_uri = file_uri(&admin_root.join("component/live.js"));
    let consumer_uri = file_uri(&admin_root.join("component/consumer.html.twig"));

    client.open(&consumer_uri, 1, "{{ component.visible }}\n");
    client.wait_diagnostics(&[&consumer_uri]);

    let definition = "Shopware.Component.register('sw-live-only', {
    props: { title: { type: String, required: true } },
    data() { return { visible: true }; },
});";
    client.open(&definition_uri, 1, definition);
    client.wait_diagnostics(&[&definition_uri, &consumer_uri]);
    assert!(
        admin_index
            .get_effective_component("sw-live-only")
            .unwrap()
            .is_some()
    );

    client.close_document(&definition_uri);
    let closed = client.wait_diagnostics(&[&definition_uri, &consumer_uri]);
    assert_eq!(closed[&definition_uri]["diagnostics"], json!([]));
    assert!(
        closed[&definition_uri]["version"].as_i64().unwrap_or(0) == 0,
        "closed version: {}",
        closed[&definition_uri]
    );
    assert_eq!(closed[&consumer_uri]["version"], json!(1));
    assert!(
        admin_index
            .get_effective_component("sw-live-only")
            .unwrap()
            .is_none()
    );
    client.shutdown().unwrap();
    admin_index.close().unwrap();
}
