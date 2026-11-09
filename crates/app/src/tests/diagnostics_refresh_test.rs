//! Index changes refresh open documents through the production wiring,
//! including inspections whose results are reused while their declared
//! inputs are unchanged.

use serde_json::{Value, json};
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri, write_file};
use crate::workspace_factory_in;

const CONSUMER: &str = r#"<?php
namespace Acme;

class Consumer {
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(service: 'acme.greeter')]
    public function __construct(private object $greeter) {}
}
"#;

fn has_code(publication: &Value, code: &str) -> bool {
    publication["diagnostics"]
        .as_array()
        .into_iter()
        .flatten()
        .any(|item| item["code"] == json!(code))
}

#[test]
fn indexing_a_service_definition_refreshes_open_php_documents() {
    let project = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = project.path();
    write_file(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.6.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}}}"#,
    );
    let consumer = write_file(root, "src/Consumer.php", CONSUMER);
    write_file(
        root,
        "src/Resources/config/services.xml",
        "<container><services/></container>\n",
    );

    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    client.initialize(root, json!({}));
    client.wait_notification("shopware/indexingCompleted");

    let uri = file_uri(&consumer);
    client.open(&uri, 1, CONSUMER);
    let initial = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
    assert!(has_code(&initial, "symfony.service.missing"), "{initial}");

    // A watcher-driven reindex of another file changes the service index.
    write_file(
        root,
        "src/Resources/config/services.xml",
        "<container><services><service id=\"acme.greeter\" class=\"Acme\\Consumer\"/></services></container>\n",
    );
    let refreshed = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
    assert!(
        !has_code(&refreshed, "symfony.service.missing"),
        "{refreshed}"
    );
    client.shutdown().unwrap();
}
