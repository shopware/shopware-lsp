//! Twig Extends and Block Overwrite generator tests.

use std::sync::Arc;

use serde_json::json;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, CommandProvider, Context};
use swlsp_twig::TwigIndexer;

use super::*;
use crate::test_support::request_at;

/// A caret at `needle`.
fn caret_request(uri: &str, source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap_or(0) as u32;
    request_at(uri, source, offset, offset)
}

fn index_with(files: &[(&str, &str)]) -> (tempfile::TempDir, Arc<TwigIndexer>) {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(dir.path(), None).unwrap());
    for (path, source) in files {
        index
            .index(&ParsedFile::new(
                path.to_string(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    (dir, index)
}

fn fixture() -> (tempfile::TempDir, TwigTemplateGeneratorProvider) {
    let (dir, index) = index_with(&[
        (
            "/project/templates/base.html.twig",
            "{% block header %}{% endblock %}
{% block content %}{% endblock %}
{% block footer %}{% endblock %}",
        ),
        (
            "/project/templates/layout.html.twig",
            "{% extends 'base.html.twig' %}
{% block header %}layout{% endblock %}
{% block sidebar %}{% endblock %}",
        ),
    ]);
    (dir, TwigTemplateGeneratorProvider::new(Some(index)))
}

const CHILD_SOURCE: &str = "{% extends 'layout.html.twig' %}
{% block sidebar %}custom{% endblock %}
";

#[test]
fn offers_contextual_actions() {
    let (_dir, provider) = fixture();
    let ctx = Context::background();
    let empty = caret_request(
        "file:///project/templates/child.html.twig",
        "<main></main>",
        "main",
    );
    let actions = provider.get_code_actions(&ctx, &empty);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Symfony: Add Twig extends");
    assert_eq!(
        actions[0].command.as_ref().unwrap().command,
        GENERATE_TWIG_EXTENDS_ACTION
    );

    let child = caret_request(
        "file:///project/templates/child.html.twig",
        CHILD_SOURCE,
        "sidebar",
    );
    let actions = provider.get_code_actions(&ctx, &child);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Symfony: Override Twig blocks");
    assert_eq!(
        actions[0].command.as_ref().unwrap().command,
        GENERATE_TWIG_BLOCKS_ACTION
    );
}

#[test]
fn defers_administration_templates_to_component_override() {
    let (_dir, provider) = fixture();
    let request = caret_request(
        "file:///project/src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
        CHILD_SOURCE,
        "sidebar",
    );
    assert!(
        provider
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );
}

#[test]
fn hides_extends_without_another_template() {
    let current_path = "/project/templates/child.html.twig";
    let (_dir, index) = index_with(&[(current_path, "<main></main>")]);
    let provider = TwigTemplateGeneratorProvider::new(Some(index.clone()));
    let request = caret_request(&format!("file://{current_path}"), "<main></main>", "main");
    assert!(
        provider
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );

    index
        .index(&ParsedFile::new(
            "/project/templates/base.html.twig".to_owned(),
            b"{% block content %}{% endblock %}".to_vec(),
        ))
        .unwrap();
    let actions = provider.get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Symfony: Add Twig extends");
}

#[test]
fn extends_generator_lists_and_validates_templates() {
    let (_dir, provider) = fixture();
    let ctx = Context::background();
    let raw =
        json!({"fileUri": "file:///project/templates/child.html.twig", "source": "<main></main>"});
    let candidates = provider
        .get_twig_extends_candidates(&ctx, Some(&raw))
        .unwrap();
    assert_eq!(
        candidates.templates,
        vec!["base.html.twig", "layout.html.twig"]
    );

    let raw = json!({
        "fileUri": "file:///project/templates/child.html.twig",
        "source": "<main></main>",
        "template": "layout.html.twig",
    });
    assert_eq!(
        provider
            .generate_twig_extends(&ctx, Some(&raw))
            .unwrap()
            .content,
        "{% extends 'layout.html.twig' %}\n"
    );

    let raw = json!({
        "fileUri": "file:///project/templates/child.html.twig",
        "source": "<main></main>",
        "template": "missing.html.twig",
    });
    assert_eq!(
        provider
            .generate_twig_extends(&ctx, Some(&raw))
            .unwrap_err()
            .to_string(),
        "twig template \"missing.html.twig\" is no longer available"
    );
}

#[test]
fn block_generator_uses_parent_chain_and_excludes_overrides() {
    let (_dir, provider) = fixture();
    let ctx = Context::background();
    let raw =
        json!({"fileUri": "file:///project/templates/child.html.twig", "source": CHILD_SOURCE});
    let candidates = provider
        .get_twig_block_candidates(&ctx, Some(&raw))
        .unwrap();
    assert_eq!(candidates.parent, "layout.html.twig");
    assert_eq!(candidates.blocks, vec!["content", "footer", "header"]);

    let raw = json!({
        "fileUri": "file:///project/templates/child.html.twig",
        "source": CHILD_SOURCE,
        "selectedBlocks": ["header", "content"],
    });
    assert_eq!(
        provider
            .generate_twig_blocks(&ctx, Some(&raw))
            .unwrap()
            .content,
        "{% block content %}\n    $0\n{% endblock %}\n\n{% block header %}\n    \n{% endblock %}\n"
    );
}

#[test]
fn commands_are_registered_and_decode_requests() {
    let (_dir, provider) = fixture();
    let ctx = Context::background();
    let commands = provider.commands(&ctx);
    assert_eq!(
        commands.keys().cloned().collect::<Vec<_>>(),
        vec![
            TWIG_BLOCK_CANDIDATES_COMMAND,
            GENERATE_TWIG_BLOCKS_COMMAND,
            TWIG_EXTENDS_CANDIDATES_COMMAND,
            GENERATE_TWIG_EXTENDS_COMMAND,
        ]
        .into_iter()
        .map(str::to_owned)
        .collect::<std::collections::BTreeSet<_>>()
        .into_iter()
        .collect::<Vec<_>>()
    );
    let error = (commands[TWIG_EXTENDS_CANDIDATES_COMMAND])(&ctx, None).unwrap_err();
    assert_eq!(error.to_string(), "missing generator request");
    let error = (commands[TWIG_EXTENDS_CANDIDATES_COMMAND])(&ctx, Some(&json!({}))).unwrap_err();
    assert_eq!(error.to_string(), "missing Twig file URI");
    let value = (commands[TWIG_EXTENDS_CANDIDATES_COMMAND])(
        &ctx,
        Some(&json!({"fileUri": "file:///project/templates/child.html.twig"})),
    )
    .unwrap();
    assert_eq!(
        value,
        json!({"templates": ["base.html.twig", "layout.html.twig"]})
    );
}
