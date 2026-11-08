//! Port of Go `yaml_service_authoring_completion_test.go`.

use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_php_project::{Model, Package};

use super::*;
use crate::symfony::YamlServiceAuthoringCompletionProvider;

fn run(
    provider: &YamlServiceAuthoringCompletionProvider,
    path: &str,
    source: &str,
) -> (Arc<TextDocument>, Vec<protocol::CompletionItem>) {
    let (source, offset) = completion_caret(source);
    let (document, request) = bundle_request(path, &source, offset);
    let items = completions(provider, &request);
    (document, items)
}

#[test]
fn yaml_service_option_completion_block_and_flow_mappings() {
    let provider = YamlServiceAuthoringCompletionProvider::new(None);
    for (name, source, label, replaced, new_text, not_present) in [
        (
            "block partial key",
            "services:\n  app.consumer:\n    class: App\\Consumer\n    aut<caret>\n",
            "autowire",
            "aut",
            "autowire: ",
            "class",
        ),
        (
            "block complete key",
            "services:\n  app.consumer:\n    clas<caret>s:\n",
            "class",
            "class",
            "class",
            "",
        ),
        (
            "empty block key",
            "services:\n  app.consumer:\n    <caret>\n",
            "decoration_inner_name",
            "",
            "decoration_inner_name: ",
            "",
        ),
        (
            "flow mapping",
            "services:\n  app.consumer: { class: App\\Consumer, deco<caret> }\n",
            "decorates",
            "deco",
            "decorates: ",
            "class",
        ),
        (
            "instanceof options",
            "services:\n  _instanceof:\n    App\\Contract:\n      auto<caret>\n",
            "autoconfigure",
            "auto",
            "autoconfigure: ",
            "",
        ),
    ] {
        let (document, items) = run(&provider, "/tmp/project/config/services.yaml", source);
        let item = require_completion(&items, label);
        assert_eq!(
            item.kind,
            kind(protocol::CompletionItemKind::PROPERTY),
            "{name}"
        );
        let edit = edit(&item);
        assert_eq!(edit.new_text, new_text, "{name}");
        assert_eq!(range_text(&document, edit.range), replaced, "{name}");
        if !not_present.is_empty() {
            assert!(!labels(&items).contains(&not_present.to_string()), "{name}");
        }
    }
}

#[test]
fn yaml_service_option_completion_rejects_nested_mappings() {
    let provider = YamlServiceAuthoringCompletionProvider::new(None);
    for source in [
        "services:\n  app.consumer:\n    arguments:\n      $log<caret>: '@logger'\n",
        "other:\n  app.consumer:\n    aut<caret>\n",
        "services:\n  app.consumer: ~\n  aut<caret>\n",
    ] {
        let (_, items) = run(&provider, "/tmp/project/config/services.yaml", source);
        assert!(items.is_empty(), "{source}: {:?}", labels(&items));
    }
}

#[test]
fn yaml_value_tag_and_keyword_completion() {
    let provider = YamlServiceAuthoringCompletionProvider::new(None);
    let (document, items) = run(
        &provider,
        "/tmp/project/config/services.yaml",
        "services:\n  app.consumer:\n    arguments:\n      - !tag<caret>\n",
    );
    let tagged = require_completion(&items, "!tagged_iterator");
    let edit_value = edit(&tagged);
    assert_eq!(edit_value.new_text, "!tagged_iterator ");
    assert_eq!(range_text(&document, edit_value.range), "!tag");
    let all = labels(&items);
    for label in [
        "!tagged_locator",
        "!service_locator",
        "!php/const",
        "!php/enum",
    ] {
        assert!(all.contains(&label.to_string()), "{label}");
    }
    assert!(!all.contains(&"!tagged".to_string()));

    let (document, items) = run(
        &provider,
        "/tmp/project/settings.yaml",
        "root:\n  enabled: tr<caret>\n",
    );
    let true_item = require_completion(&items, "true");
    assert_eq!(range_text(&document, edit(&true_item).range), "tr");
    assert!(!labels(&items).contains(&"!php/const".to_string()));
}

fn model(name: &str, version: &str) -> Arc<Model> {
    Arc::new(Model {
        dependencies: vec![Package {
            name: name.to_string(),
            version: version.to_string(),
            ..Default::default()
        }],
        ..Default::default()
    })
}

#[test]
fn yaml_enum_tag_respects_installed_symfony_version() {
    for (version, present) in [("v6.1.12", false), ("v6.2.0", true), ("v7.4.0", true)] {
        let provider =
            YamlServiceAuthoringCompletionProvider::new(Some(model("symfony/yaml", version)));
        let (_, items) = run(
            &provider,
            "/tmp/project/config/packages.yaml",
            "root:\n  value: !php/<caret>\n",
        );
        assert_eq!(
            labels(&items).contains(&"!php/enum".to_string()),
            present,
            "{version}"
        );
    }
}

#[test]
fn yaml_service_tags_respect_installed_symfony_version() {
    for (version, present, not_present) in [
        (
            "v4.3.9",
            &["!tagged", "!tagged_locator", "!service_locator"],
            "!tagged_iterator",
        ),
        (
            "v4.4.0",
            &["!tagged_iterator", "!tagged_locator", "!service_locator"],
            "!tagged",
        ),
    ] {
        let provider = YamlServiceAuthoringCompletionProvider::new(Some(model(
            "symfony/dependency-injection",
            version,
        )));
        let (_, items) = run(
            &provider,
            "/tmp/project/config/services.yaml",
            "services:\n  app.consumer:\n    arguments: [<caret>]\n",
        );
        let all = labels(&items);
        for label in present {
            assert!(all.contains(&label.to_string()), "{version} {label}");
        }
        assert!(!all.contains(&not_present.to_string()), "{version}");
    }
}

#[test]
fn yaml_value_completion_rejects_strings_and_values_after_tags() {
    let provider = YamlServiceAuthoringCompletionProvider::new(None);
    for source in [
        "root:\n  value: '<caret>'\n",
        "root:\n  value: !php/const <caret>\n",
        "root:\n  <caret>\n",
    ] {
        let (_, items) = run(&provider, "/tmp/project/config/config.yaml", source);
        assert!(items.is_empty(), "{source}: {:?}", labels(&items));
    }
}
