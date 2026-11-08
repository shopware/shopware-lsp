use std::fs;
use std::path::Path;

use serde_json::{Map, Value, json};
use swlsp_uriutil::file_uri;
use tempfile::TempDir;

use super::{ctx, edit_text, path_string, raw};
use crate::{Provider, ShopwareRequest, ShopwareResponse};

fn provider(root: &Path) -> Provider {
    Provider::new(&path_string(root), None, None, None)
}

fn create(
    provider: &Provider,
    kind: &str,
    directory: &Path,
    name: &str,
    options: Value,
) -> crate::Result<ShopwareResponse> {
    let options: Map<String, Value> = match options {
        Value::Object(map) => map,
        _ => Map::new(),
    };
    let request = ShopwareRequest {
        kind: kind.to_owned(),
        directory_uri: file_uri(&path_string(directory)),
        name: name.to_owned(),
        options,
    };
    provider.create_shopware(&ctx(), Some(&raw(&request)))
}

fn base_name(uri: &str) -> &str {
    uri.rsplit('/').next().unwrap_or(uri)
}

#[test]
fn kinds_produce_validated_atomic_edits() {
    let root = TempDir::new().unwrap();
    let provider = provider(root.path());
    for kind in [
        "plugin",
        "system-config",
        "scheduled-task",
        "migration",
        "app",
        "app-custom-entities",
        "app-cms",
        "app-script",
        "admin-component",
        "admin-module",
        "cms-block",
        "cms-element",
    ] {
        let directory = root.path().join(kind);
        fs::create_dir_all(&directory).unwrap();
        if kind == "admin-component" || kind == "admin-module" {
            fs::write(directory.join("main.js"), "// entry\n").unwrap();
        }
        let response = create(
            &provider,
            kind,
            &directory,
            "acme-example",
            json!({"namespace": "Acme\\Example", "timestamp": "1700000000", "hook": "product-page-loaded"}),
        )
        .unwrap_or_else(|error| panic!("{kind}: {error}"));
        let edit = response.edit.expect("edit");
        assert!(!edit.document_changes.is_empty(), "{kind}");
        assert!(!response.primary_file_uri.is_empty(), "{kind}");
    }
}

#[test]
fn plugin_scaffold_creates_yaml_service_configuration() {
    let root = TempDir::new().unwrap();
    let provider = provider(root.path());
    let response = create(
        &provider,
        "plugin",
        root.path(),
        "FroshTools",
        json!({"namespace": "Frosh\\Tools"}),
    )
    .unwrap();
    let mut service_path = String::new();
    let mut service_source = String::new();
    for change in &response.edit.unwrap().document_changes {
        let uri = change
            .text_document
            .as_ref()
            .map_or(change.uri.as_str(), |document| document.uri.as_str());
        if base_name(uri) != "services.yaml" {
            assert_ne!(base_name(uri), "services.xml");
            continue;
        }
        service_path = uri.to_owned();
        for edit in &change.edits {
            service_source.push_str(&edit.new_text);
        }
    }
    assert!(service_path.contains("FroshTools/src/Resources/config/services.yaml"));
    assert!(service_source.contains("Frosh\\Tools\\:"));
    assert!(service_source.contains("autoconfigure: true"));
}

#[test]
fn rejects_collisions_before_returning_edit() {
    let root = TempDir::new().unwrap();
    fs::write(root.path().join("manifest.xml"), "existing").unwrap();
    let provider = provider(root.path());
    // The custom entities target differs from manifest.xml and remains valid.
    create(
        &provider,
        "app-custom-entities",
        root.path(),
        "existing",
        Value::Null,
    )
    .unwrap();

    let config = root.path().join("Resources/config/config.xml");
    fs::create_dir_all(config.parent().unwrap()).unwrap();
    fs::write(&config, "existing").unwrap();
    assert!(
        create(
            &provider,
            "system-config",
            root.path(),
            "existing",
            Value::Null
        )
        .is_err()
    );
}

#[test]
fn creates_admin_override_and_entrypoint() {
    let root = TempDir::new().unwrap();
    let provider = provider(root.path());
    let response = create(
        &provider,
        "admin-component",
        root.path(),
        "sw-product-list-custom",
        json!({
            "mode": "extend",
            "target": "sw-product-list",
            "method": "loadProducts",
            "methodGroup": "methods",
            "parameters": "criteria, context",
        }),
    )
    .unwrap();
    let edit = response.edit.unwrap();
    let created_main = edit
        .document_changes
        .iter()
        .any(|change| base_name(&change.uri) == "main.js");
    let generated = edit_text(&edit);
    assert!(created_main);
    assert!(generated.contains("Component.extend('sw-product-list-custom', 'sw-product-list'"));
    assert!(generated.contains("loadProducts(criteria, context)"));
    assert!(generated.contains("this.$super('loadProducts', criteria, context)"));
}

#[test]
fn creates_event_listener() {
    let root = TempDir::new().unwrap();
    let provider = provider(root.path());
    let response = create(
        &provider,
        "event-listener",
        root.path(),
        "ProductLoadedEventListener",
        json!({
            "namespace": "Acme\\Example\\EventListener",
            "event": "Shopware\\Core\\Content\\Product\\ProductLoadedEvent",
        }),
    )
    .unwrap();
    assert!(
        response
            .primary_file_uri
            .contains("ProductLoadedEventListener.php")
    );
    let generated = edit_text(&response.edit.unwrap());
    assert!(generated.contains("#[AsEventListener]"));
    assert!(generated.contains("ProductLoadedEvent $event"));
}

#[test]
fn appends_import_to_existing_main_js() {
    let root = TempDir::new().unwrap();
    fs::write(root.path().join("main.js"), "import './existing';").unwrap();
    let provider = provider(root.path());
    let response = create(
        &provider,
        "admin-module",
        root.path(),
        "acme-module",
        Value::Null,
    )
    .unwrap();
    let edit = response.edit.unwrap();
    let main = edit
        .document_changes
        .iter()
        .find(|change| {
            change
                .text_document
                .as_ref()
                .is_some_and(|document| base_name(&document.uri) == "main.js")
        })
        .expect("main.js update");
    assert_eq!(main.edits[0].new_text, "\nimport './module/acme-module';\n");
}
