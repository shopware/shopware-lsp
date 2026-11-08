//! Server-level smoke test: the production workspace factory on a small
//! Shopware plugin, driven over JSON-RPC like an editor.

use serde_json::{Value, json};
use swlsp_lsp::Server;

use super::support::{TestClient, file_uri, write_file};
use crate::workspace_factory_in;

const GREETER: &str = r#"<?php
namespace Acme\Service;

class Greeter
{
    /** Greets somebody by name. */
    public function greet(string $name): string
    {
        return 'Hello ' . $name;
    }
}
"#;

const CONTROLLER: &str = r#"<?php
namespace Acme\Controller;

use Acme\Service\Greeter;

class HelloController
{
    public function __construct(private Greeter $greeter)
    {
    }

    public function show(): string
    {
        return $this->greeter->greet('world');
    }

    public function partial(): void
    {
        $this->greeter->g
    }
}
"#;

const SERVICES: &str = r#"<?xml version="1.0" ?>
<container xmlns="http://symfony.com/schema/dic/services">
    <services>
        <service id="Acme\Service\Greeter"/>
    </services>
</container>
"#;

const PAGE: &str = "{% macro badge(label) %}<span>{{ label }}</span>{% endmacro %}\n{% import _self as ui %}\n{{ ui.badge('x') }}\n{{ 'abc'|upper }}\n{{ 'abc'|up }}\n";

const ADMIN_COMPONENT: &str = r#"Shopware.Component.register('acme-greeting', {
    template: '<div>{{ title }}</div>',
    props: { title: { type: String, required: true } },
    methods: {
        greet() {
            return this.title;
        },
    },
});
"#;

fn position(line: u32, character: u32) -> Value {
    json!({"line": line, "character": character})
}

fn text_position(uri: &str, line: u32, character: u32) -> Value {
    json!({"textDocument": {"uri": uri}, "position": position(line, character)})
}

fn completion_labels(result: &Value) -> Vec<String> {
    let items = result
        .get("items")
        .and_then(Value::as_array)
        .or_else(|| result.as_array())
        .cloned()
        .unwrap_or_default();
    items
        .iter()
        .filter_map(|item| item["label"].as_str().map(str::to_owned))
        .collect()
}

#[test]
fn production_workspace_serves_php_twig_and_administration_documents() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_file(
        root,
        "composer.json",
        r#"{"name":"acme/plugin","type":"shopware-platform-plugin","require":{"shopware/core":"6.6.0.0"},"autoload":{"psr-4":{"Acme\\":"src/"}},"extra":{"shopware-plugin-class":"Acme\\AcmePlugin"}}"#,
    );
    write_file(
        root,
        "src/AcmePlugin.php",
        "<?php\nnamespace Acme;\nclass AcmePlugin {}\n",
    );
    let greeter = write_file(root, "src/Service/Greeter.php", GREETER);
    let controller = write_file(root, "src/Controller/HelloController.php", CONTROLLER);
    write_file(root, "src/Resources/config/services.xml", SERVICES);
    let page = write_file(root, "src/Resources/views/storefront/page.html.twig", PAGE);
    let admin = write_file(
        root,
        "src/Resources/app/administration/src/component/acme-greeting/index.js",
        ADMIN_COMPONENT,
    );

    let server = Server::new(None, "", "test");
    server.configure_project_detection(true, false);
    server.set_workspace_factory(workspace_factory_in(cache.path().to_path_buf()));
    let client = TestClient::start(&server);
    let initialized = client.initialize(root, json!({}));
    assert!(
        initialized["capabilities"]["hoverProvider"]
            .as_bool()
            .unwrap_or(true)
    );
    client.wait_notification("shopware/indexingCompleted");

    let controller_uri = file_uri(&controller);
    let page_uri = file_uri(&page);
    let admin_uri = file_uri(&admin);
    client.open(&controller_uri, 1, CONTROLLER);
    client.open(&page_uri, 1, PAGE);
    client.open(&admin_uri, 1, ADMIN_COMPONENT);
    let published = client.wait_diagnostics(&[&controller_uri, &page_uri, &admin_uri]);
    for uri in [&controller_uri, &page_uri, &admin_uri] {
        assert!(published[uri.as_str()]["diagnostics"].is_array(), "{uri}");
    }

    // PHP: hover, definition and member completion through phpsemantic.
    let hover = client.call("textDocument/hover", text_position(&controller_uri, 13, 33));
    let hover_text = hover.to_string();
    assert!(hover_text.contains("greet"), "PHP hover: {hover_text}");
    let definition = client.call(
        "textDocument/definition",
        text_position(&controller_uri, 13, 33),
    );
    let definition_text = definition.to_string();
    assert!(
        definition_text.contains(&file_uri(&greeter)),
        "PHP definition: {definition_text}"
    );
    let completion = client.call(
        "textDocument/completion",
        text_position(&controller_uri, 18, 25),
    );
    assert!(
        completion_labels(&completion)
            .iter()
            .any(|label| label == "greet"),
        "PHP completion: {completion}"
    );

    // Twig: macro definition and hover, filter completion.
    let definition = client.call("textDocument/definition", text_position(&page_uri, 2, 7));
    assert!(
        definition.to_string().contains(&page_uri),
        "Twig macro definition: {definition}"
    );
    let hover = client.call("textDocument/hover", text_position(&page_uri, 2, 7));
    assert!(
        hover.to_string().contains("badge(label)"),
        "Twig macro hover: {hover}"
    );
    let completion = client.call("textDocument/completion", text_position(&page_uri, 4, 11));
    assert!(
        completion_labels(&completion)
            .iter()
            .any(|label| label == "raw()"),
        "Twig filter completion: {completion}"
    );

    // Administration: hover, definition, completion and symbols.
    let hover = client.call("textDocument/hover", text_position(&admin_uri, 0, 32));
    assert!(
        hover.to_string().contains("acme-greeting"),
        "Administration hover: {hover}"
    );
    let definition = client.call("textDocument/definition", text_position(&admin_uri, 5, 25));
    assert!(
        definition.to_string().contains(&admin_uri),
        "Administration definition: {definition}"
    );
    let completion = client.call("textDocument/completion", text_position(&admin_uri, 5, 24));
    let labels = completion_labels(&completion);
    assert!(
        labels.iter().any(|label| label == "title") && labels.iter().any(|label| label == "greet"),
        "Administration completion: {labels:?}"
    );
    let symbols = client.call(
        "textDocument/documentSymbol",
        json!({"textDocument": {"uri": admin_uri}}),
    );
    assert!(
        symbols.to_string().contains("acme-greeting"),
        "Administration document symbols: {symbols}"
    );

    client.close_document(&admin_uri);
    let closed = client.wait_diagnostics(&[&admin_uri]);
    assert_eq!(closed[admin_uri.as_str()]["diagnostics"], json!([]));
    client.shutdown().unwrap();
    // Providers and document observers only hold weak handles, so the
    // server is freed once its owner lets go.
    let weak = server.downgrade();
    drop(server);
    assert!(
        weak.upgrade().is_none(),
        "server kept alive by a reference cycle"
    );
}
