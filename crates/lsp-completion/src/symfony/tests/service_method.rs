//! Tests for configured service method completion.

use swlsp_lsp_protocol as protocol;

use super::*;
use crate::symfony::ServiceCompletionProvider;

fn configured_method_php_index(dir: &Path) -> PhpIndex {
    php_index(
        &dir.join("php"),
        &[(
            &p(&dir.join("Configured.php")),
            r"<?php
namespace App;
class ParentConfigured
{
    public function inherited(): void {}
}
class Configured extends ParentConfigured
{
    public function __construct() {}
    public function setLogger(): void {}
    public function onKernelRequest(): void {}
    protected function hidden(): void {}
}
class Builder
{
    public function create(): Product { return new Product(); }
    public static function build(): Product { return new Product(); }
    private function secret(): void {}
}
class Product {}
",
        )],
    )
}

#[test]
fn configured_service_method_completion_across_yaml_and_xml() {
    let dir = tempfile::tempdir().unwrap();
    let provider =
        ServiceCompletionProvider::new(None, Some(configured_method_php_index(dir.path())));
    let fixtures: &[(&str, &str, &str, &str, &str)] = &[
        (
            "YAML call",
            "services.yaml",
            "services:\n  app.consumer:\n    class: App\\Configured\n    calls:\n      - [setLog<caret>, []]\n",
            "setLogger",
            "setLog",
        ),
        (
            "YAML empty call",
            "services.yaml",
            "services:\n  app.consumer:\n    class: App\\Configured\n    calls:\n      - ['<caret>', []]\n",
            "inherited",
            "",
        ),
        (
            "YAML unquoted empty call",
            "services.yaml",
            "services:\n  app.consumer:\n    class: App\\Configured\n    calls:\n      - [<caret>, []]\n",
            "inherited",
            "",
        ),
        (
            "YAML tag callback",
            "services.yaml",
            "services:\n  app.consumer:\n    class: App\\Configured\n    tags:\n      - { name: kernel.event_listener, method: onKernel<caret> }\n",
            "onKernelRequest",
            "onKernel",
        ),
        (
            "YAML service factory tuple",
            "services.yaml",
            "services:\n  app.builder:\n    class: App\\Builder\n  app.product:\n    class: App\\Product\n    factory: ['@app.builder', cre<caret>]\n",
            "create",
            "cre",
        ),
        (
            "YAML class factory string",
            "services.yaml",
            "services:\n  app.product:\n    class: App\\Product\n    factory: 'App\\Builder::bu<caret>'\n",
            "build",
            "bu",
        ),
        (
            "YAML legacy factory",
            "services.yaml",
            "services:\n  app.builder:\n    class: App\\Builder\n  app.product:\n    class: App\\Product\n    factory_service: app.builder\n    factory_method: cre<caret>\n",
            "create",
            "cre",
        ),
        (
            "XML call",
            "services.xml",
            "<container><services>\n  <service id=\"app.consumer\" class=\"App\\Configured\">\n    <call method=\"setLog<caret>\"/>\n  </service>\n</services></container>",
            "setLogger",
            "setLog",
        ),
        (
            "XML tag callback",
            "services.xml",
            "<container><services>\n  <service id=\"app.consumer\" class=\"App\\Configured\">\n    <tag name=\"kernel.event_listener\" method=\"onKernel<caret>\"/>\n  </service>\n</services></container>",
            "onKernelRequest",
            "onKernel",
        ),
        (
            "XML service factory",
            "services.xml",
            "<container><services>\n  <service id=\"app.builder\" class=\"App\\Builder\"/>\n  <service id=\"app.product\" class=\"App\\Product\">\n    <factory service=\"app.builder\" method=\"cre<caret>\"/>\n  </service>\n</services></container>",
            "create",
            "cre",
        ),
        (
            "XML class factory",
            "services.xml",
            "<container><services>\n  <service id=\"app.product\" class=\"App\\Product\">\n    <factory class=\"App\\Builder\" method=\"bu<caret>\"/>\n  </service>\n</services></container>",
            "build",
            "bu",
        ),
        (
            "XML legacy factory",
            "services.xml",
            "<container><services>\n  <service id=\"app.builder\" class=\"App\\Builder\"/>\n  <service id=\"app.product\" class=\"App\\Product\"\n           factory-service=\"app.builder\" factory-method=\"cre<caret>\"/>\n</services></container>",
            "create",
            "cre",
        ),
    ];
    for (name, file, source, label, replaced) in fixtures {
        let (source, offset) = completion_caret(source);
        let (document, request) = bundle_request(&p(&dir.path().join(file)), &source, offset);
        let items = completions(&provider, &request);
        let item = require_completion(&items, label);
        assert_eq!(
            item.kind,
            kind(protocol::CompletionItemKind::METHOD),
            "{name}"
        );
        let edit = edit(&item);
        assert_eq!(edit.new_text, *label, "{name}");
        assert_eq!(range_text(&document, edit.range), *replaced, "{name}");
    }
}

#[test]
fn configured_service_method_completion_filters_non_public_and_magic_methods() {
    let dir = tempfile::tempdir().unwrap();
    let provider =
        ServiceCompletionProvider::new(None, Some(configured_method_php_index(dir.path())));
    let (source, offset) = completion_caret(
        "services:\n  app.consumer:\n    class: App\\Configured\n    calls:\n      - ['<caret>', []]\n",
    );
    let (_, request) = bundle_request(&p(&dir.path().join("services.yaml")), &source, offset);
    let labels = labels(&completions(&provider, &request));
    assert!(labels.contains(&"setLogger".to_string()));
    assert!(labels.contains(&"inherited".to_string()));
    assert!(!labels.contains(&"hidden".to_string()));
    assert!(!labels.contains(&"__construct".to_string()));
}
