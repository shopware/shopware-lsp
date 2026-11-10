//! "Add Tags" service-tag action tests.

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, TextDocument};
use swlsp_lsp_protocol::CodeActionKind;
use swlsp_php::PhpIndex;

use super::ServiceTagCodeActionProvider;
use swlsp_ide::codeaction::testing::{apply_code_action_edit, request_at};

struct Fixture {
    _dir: tempfile::TempDir,
    provider: ServiceTagCodeActionProvider,
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(dir.path(), None).unwrap();
    for (path, source) in [
        (
            "/vendor/EventSubscriberInterface.php",
            "<?php\nnamespace Symfony\\Component\\EventDispatcher;\ninterface EventSubscriberInterface {}\n",
        ),
        (
            "/vendor/ExtensionInterface.php",
            "<?php\nnamespace Twig\\Extension;\ninterface ExtensionInterface {}\n",
        ),
        (
            "/project/src/Services.php",
            r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Twig\Extension\ExtensionInterface;
class Subscriber implements EventSubscriberInterface {}
class MultiExtension implements EventSubscriberInterface, ExtensionInterface {}
class PlainService {}
",
        ),
    ] {
        php_index
            .index(&ParsedFile::new(
                path.to_owned(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    Fixture {
        _dir: dir,
        provider: ServiceTagCodeActionProvider::new(Some(php_index)),
    }
}

fn request(source: &str, uri: &str, needle: &str) -> CodeActionRequest {
    match source.find(needle) {
        Some(offset) => request_at(uri, source, offset as u32, (offset + needle.len()) as u32),
        None => request_at(uri, source, 0, 0),
    }
}

fn assert_parses(uri: &str, source: &str) {
    assert!(
        TextDocument::new(uri, source, 2).parse_errors().is_empty(),
        "{source}"
    );
}

#[test]
fn expands_self_closing_xml_service() {
    let fixture = fixture();
    let source = "<container>\n  <services>\n    <service id=\"app.subscriber\" class=\"App\\Subscriber\"/>\n  </services>\n</container>";
    let uri = "file:///project/services.xml";
    let request = request(source, uri, "App\\Subscriber");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Symfony: Add service tag 'kernel.event_subscriber'"
    );
    assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    let updated = apply_code_action_edit(source, &actions[0], uri);
    assert!(updated.contains("<service id=\"app.subscriber\" class=\"App\\Subscriber\">"));
    assert!(updated.contains("      <tag name=\"kernel.event_subscriber\"/>"));
    assert!(updated.contains("    </service>"));
    assert_parses(uri, &updated);
}

#[test]
fn appends_xml_tag_and_preserves_child_indent() {
    let fixture = fixture();
    let source = "<container>\n\t<services>\n\t\t<service id=\"app.subscriber\" class=\"App\\Subscriber\">\n\t\t\t<argument type=\"service\" id=\"logger\"/>\n\t\t</service>\n\t</services>\n</container>";
    let uri = "file:///project/services.xml";
    let request = request(source, uri, "logger");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    let updated = apply_code_action_edit(source, &actions[0], uri);
    assert!(updated.contains("\t\t\t<tag name=\"kernel.event_subscriber\"/>\n\t\t</service>"));
}

#[test]
fn creates_yaml_tags() {
    let fixture = fixture();
    let source =
        "services:\n  app.subscriber:\n    class: App\\Subscriber\n    arguments: ['@logger']\n";
    let uri = "file:///project/services.yaml";
    let request = request(source, uri, "App\\Subscriber");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    let updated = apply_code_action_edit(source, &actions[0], uri);
    assert!(
        updated.contains("    tags:\n      - kernel.event_subscriber"),
        "{updated}"
    );
    assert_parses(uri, &updated);
}

#[test]
fn updates_yaml_flow_forms() {
    let fixture = fixture();
    for (name, source, expected) in [
        (
            "flow service",
            "services:\n  app.subscriber: { class: App\\Subscriber }\n",
            "{ class: App\\Subscriber, tags: [kernel.event_subscriber] }",
        ),
        (
            "flow tags",
            "services:\n  app.subscriber:\n    class: App\\Subscriber\n    tags: [app.custom]\n",
            "tags: [app.custom, kernel.event_subscriber]",
        ),
        (
            "scalar tag",
            "services:\n  app.subscriber:\n    class: App\\Subscriber\n    tags: app.custom\n",
            "tags: [app.custom, kernel.event_subscriber]",
        ),
    ] {
        let uri = "file:///project/services.yaml";
        let request = request(source, uri, "App\\Subscriber");
        let actions = fixture
            .provider
            .get_code_actions(&Context::background(), &request);
        assert_eq!(actions.len(), 1, "{name}");
        let updated = apply_code_action_edit(source, &actions[0], uri);
        assert!(updated.contains(expected), "{name}: {updated}");
        assert_parses(uri, &updated);
    }
}

#[test]
fn offers_every_missing_inferred_tag() {
    let fixture = fixture();
    let source = "services:\n  app.multi:\n    class: App\\MultiExtension\n    tags:\n      - kernel.event_subscriber\n";
    let uri = "file:///project/services.yaml";
    let request = request(source, uri, "App\\MultiExtension");
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Symfony: Add service tag 'twig.extension'"
    );
    let updated = apply_code_action_edit(source, &actions[0], uri);
    assert!(updated.contains("      - kernel.event_subscriber\n      - twig.extension"));
}

#[test]
fn rejects_unsupported_targets() {
    let fixture = fixture();
    for (name, uri, source, needle) in [
        (
            "already tagged",
            "file:///project/services.xml",
            "<service id=\"app.subscriber\" class=\"App\\Subscriber\"><tag name=\"kernel.event_subscriber\"/></service>",
            "App\\Subscriber",
        ),
        (
            "unrelated class",
            "file:///project/services.yaml",
            "services:\n  app.plain:\n    class: App\\PlainService\n",
            "App\\PlainService",
        ),
        (
            "alias",
            "file:///project/services.yaml",
            "services:\n  app.alias:\n    alias: app.subscriber\n",
            "app.subscriber",
        ),
        (
            "outside service",
            "file:///project/services.yaml",
            "value: App\\Subscriber\n",
            "App\\Subscriber",
        ),
    ] {
        let request = request(source, uri, needle);
        assert!(
            fixture
                .provider
                .get_code_actions(&Context::background(), &request)
                .is_empty(),
            "{name}"
        );
    }
}
