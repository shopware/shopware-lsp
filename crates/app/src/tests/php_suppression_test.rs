//! Suppression comments apply to every inspection's PHP problems, not only
//! to analyzers that read them.

use serde_json::{Value, json};

use swlsp_lsp::Server;

use super::support::{TestClient, file_uri, workspace_server, write_file};

fn codes(publication: &Value, code: &str) -> Vec<u64> {
    publication["diagnostics"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|item| item["code"] == json!(code))
        .map(|item| item["range"]["start"]["line"].as_u64().unwrap())
        .collect()
}

#[test]
fn php_suppression_comments_hide_framework_diagnostics() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    let server = workspace_server(root, cache.path());
    let client = TestClient::start(&server);
    client.call(
        "initialize",
        json!({"rootUri": file_uri(root), "initializationOptions": {}, "capabilities": {}}),
    );
    let uri = file_uri(&root.join("Superglobals.php"));
    let source = "<?php\n\
        $_GET['reported'];\n\
        // @phpstan-ignore-next-line shopware.php.superglobal\n\
        $_GET['by code'];\n\
        /** @phpstan-ignore-next-line */\n\
        $_GET['wildcard'];\n\
        // @phpstan-ignore-next-line php.unusedImport\n\
        $_GET['other code'];\n";
    client.open(&uri, 1, source);
    let publication = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
    assert_eq!(
        codes(&publication, "shopware.php.superglobal"),
        [1, 7],
        "{publication}"
    );
}

#[test]
fn noinspection_comments_hide_twig_diagnostics() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_file(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.6.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}}}"#,
    );
    let page = write_file(
        root,
        "src/Resources/views/storefront/page.html.twig",
        "{% import _self as ui %}\n\
        {{ ui.missing() }}\n\
        {# @noinspection twig.macro.missing #}\n\
        {{ ui.missing() }}\n",
    );
    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    server.set_workspace_factory(crate::workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    client.initialize(root, json!({}));
    client.wait_notification("shopware/indexingCompleted");
    let uri = file_uri(&page);
    client.open(&uri, 1, &std::fs::read_to_string(&page).unwrap());
    let publication = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
    assert_eq!(
        codes(&publication, "twig.macro.missing"),
        [1],
        "{publication}"
    );
}
