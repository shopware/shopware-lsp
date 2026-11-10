//! Store-compliance diagnostics refreshed by extension configuration overlays.

use std::sync::Arc;

use serde_json::json;
use swlsp_extension::{CONFIG_LOCATIONS, ConfigIndex};
use swlsp_ide_shopware::diagnostics::{ShopwareStoreComposerAnalyzer, StoreComplianceResolver};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri};
use swlsp_ide::{Precedence, Registrar};
use swlsp_ide_shopware::register_extension_config_document_observer;

fn diagnostics_empty(params: &serde_json::Value) -> bool {
    params["diagnostics"].as_array().is_none_or(Vec::is_empty)
}

#[test]
fn store_compliance_overlay_refreshes_composer_diagnostics() {
    let handles: Vec<_> = CONFIG_LOCATIONS
        .iter()
        .map(|location| std::thread::spawn(move || extension_config_overlay(location)))
        .collect();
    for handle in handles {
        handle.join().unwrap();
    }
}

fn extension_config_overlay(location: &str) {
    let project = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = project.path();
    let compliance = Arc::new(ConfigIndex::new(cache.path(), None).unwrap());
    let server = Server::new(None, &root.to_string_lossy(), "test");
    let registrar = Registrar::new(&server);
    register_extension_config_document_observer(&registrar, &compliance);
    let resolver: Arc<dyn StoreComplianceResolver> = compliance.clone();
    registrar.inspection(swlsp_ide::inspections::analyzer_inspection(
        ShopwareStoreComposerAnalyzer::new(Some(resolver)),
    ));
    registrar.finish(Precedence(&[]));
    let client = TestClient::start(&server);
    // A standalone plugin at the workspace root must support the same opt-in.
    let composer_uri = file_uri(&root.join("composer.json"));
    let config_path = root.join(location);
    let config_path_text = config_path.to_string_lossy().into_owned();
    let config_uri = file_uri(&config_path);
    client.open(&composer_uri, 1, r#"{"type":"shopware-platform-plugin"}"#);
    let initial = client.wait_diagnostics(&[&composer_uri]);
    assert!(
        diagnostics_empty(&initial[&composer_uri]),
        "{location}: initial"
    );
    let config = "validation:\n  store_compliance: true\n";
    client.open(&config_uri, 1, config);
    let opened = client.wait_diagnostics(&[&composer_uri, &config_uri]);
    assert!(
        !diagnostics_empty(&opened[&composer_uri]),
        "{location}: opened"
    );
    client.change(&config_uri, 2, "validation:\n  store_compliance: false\n");
    let changed = client.wait_diagnostics(&[&composer_uri, &config_uri]);
    assert!(
        diagnostics_empty(&changed[&composer_uri]),
        "{location}: disabled"
    );
    client.change(&config_uri, 3, config);
    let changed = client.wait_diagnostics(&[&composer_uri, &config_uri]);
    assert!(
        !diagnostics_empty(&changed[&composer_uri]),
        "{location}: re-enabled"
    );

    compliance
        .index(&ParsedFile::new(
            config_path_text.clone(),
            b"validation: {}".to_vec(),
        ))
        .unwrap();
    client.notify(
        "textDocument/didClose",
        json!({"textDocument": {"uri": config_uri}}),
    );
    let closed = client.wait_diagnostics(&[&composer_uri, &config_uri]);
    assert!(
        diagnostics_empty(&closed[&composer_uri]),
        "{location}: closed"
    );
    compliance
        .index(&ParsedFile::new(
            config_path_text.clone(),
            config.as_bytes().to_vec(),
        ))
        .unwrap();
    let disk = client.wait_diagnostics(&[&composer_uri]);
    assert!(!diagnostics_empty(&disk[&composer_uri]), "{location}: disk");
    compliance.removed_files(&[config_path_text]).unwrap();
    let removed = client.wait_diagnostics(&[&composer_uri]);
    assert!(
        diagnostics_empty(&removed[&composer_uri]),
        "{location}: removed"
    );
    client.shutdown().unwrap();
    Indexer::close(compliance.as_ref()).unwrap();
}
