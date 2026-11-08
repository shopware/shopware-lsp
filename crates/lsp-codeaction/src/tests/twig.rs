//! Go `twig_versioning_codeaction_test.go` and
//! `TestStorefrontTwigActionsDoNotLeakIntoAdministrationTemplates`.

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{SyntaxContext, TextDocument};
use swlsp_syntax::TextSize;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::*;

fn path_of(root: &Path, parts: &[&str]) -> String {
    let mut path = root.to_path_buf();
    for part in parts {
        path.push(part);
    }
    path.to_str().unwrap().to_owned()
}

fn twig_index(root: &Path, cache: &str, files: &[(&str, &str)]) -> Arc<TwigIndexer> {
    let index = Arc::new(TwigIndexer::new(root.join(cache), None).unwrap());
    for (path, source) in files {
        index
            .index(&ParsedFile::new(
                (*path).to_owned(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    index
}

/// Go `twigVersioningActionFixture`.
fn fixture() -> (tempfile::TempDir, Arc<VersioningService>, String) {
    let dir = tempfile::tempdir().unwrap();
    let upstream = path_of(
        dir.path(),
        &[
            "src",
            "Storefront",
            "Resources",
            "views",
            "storefront",
            "page",
            "example.html.twig",
        ],
    );
    let index = twig_index(
        dir.path(),
        "cache",
        &[(&upstream, "{% block content %}upstream{% endblock %}")],
    );
    let current = path_of(
        dir.path(),
        &[
            "custom",
            "plugins",
            "Example",
            "src",
            "Resources",
            "views",
            "storefront",
            "page",
            "example.html.twig",
        ],
    );
    let service = Arc::new(VersioningService::new(
        dir.path().to_str().unwrap(),
        Some(index),
        "6.7.2",
    ));
    (dir, service, current)
}

/// A request whose node and token are taken at `offset` (Go
/// `NodeAtOffset`/`TokenAtOffset`), with an empty range at that offset.
fn offset_request(
    uri: &str,
    source: &str,
    offset: u32,
    diagnostics: Vec<protocol::Diagnostic>,
    with_range: bool,
) -> CodeActionRequest {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let root = document.root().unwrap();
    let position = if with_range {
        let (line, character) = document.line_index().position_utf16(offset);
        protocol::Position { line, character }
    } else {
        protocol::Position::default()
    };
    CodeActionRequest {
        params: protocol::CodeActionParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            range: protocol::Range {
                start: position,
                end: position,
            },
            context: protocol::CodeActionContext {
                diagnostics,
                ..Default::default()
            },
        },
        syntax: SyntaxContext {
            language: document.language,
            token: root.token_at(TextSize::from(offset)),
            node: root.node_at(TextSize::from(offset)),
            root: Some(root),
            document: Some(document),
        },
    }
}

/// Go `twigVersioningActionRequest`.
fn versioning_request(
    path: &str,
    source: &str,
    block: &str,
    diagnostics: Vec<protocol::Diagnostic>,
) -> CodeActionRequest {
    let needle = format!("{{% block {block}");
    let offset = source.rfind(&needle).unwrap() + "{% block ".len() + 1;
    offset_request(&file_uri(path), source, offset as u32, diagnostics, true)
}

fn action_by_title<'a>(
    actions: &'a [protocol::CodeAction],
    title: &str,
) -> Option<&'a protocol::CodeAction> {
    actions.iter().find(|action| action.title == title)
}

#[test]
fn versioning_context_action_adds_comment_without_diagnostic() {
    let (_dir, service, current) = fixture();
    let source = "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}local{% endblock %}";
    let request = versioning_request(&current, source, "content", Vec::new());
    let actions = TwigCodeActionProvider::new(Some(service))
        .get_code_actions(&Context::background(), &request);
    let action =
        action_by_title(&actions, "Shopware: Add Twig block version comment").expect("add action");
    let changes = &action.edit.as_ref().unwrap().changes[&request.params.text_document.uri];
    assert_eq!(changes.len(), 1);
    assert!(changes[0].new_text.contains("shopware-block:"));
}

#[test]
fn versioning_context_actions_do_not_duplicate_diagnostic_fixes() {
    let (_dir, service, current) = fixture();
    let source = "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{# shopware-block: deadbeef@6.6.0.0 #}\n{% block content %}local{% endblock %}";
    let diagnostic = protocol::Diagnostic {
        code: Some(serde_json::Value::from("twig.versioning.outdated")),
        ..Default::default()
    };
    let request = versioning_request(&current, source, "content", vec![diagnostic]);
    let actions = TwigCodeActionProvider::new(Some(service))
        .get_code_actions(&Context::background(), &request);
    assert!(action_by_title(&actions, "Shopware: Update Twig block version comment").is_none());
    assert!(action_by_title(&actions, "Shopware: Show Twig block difference").is_none());
}

#[test]
fn versioning_diff_is_not_offered_for_third_party_upstream() {
    let dir = tempfile::tempdir().unwrap();
    let upstream = path_of(
        dir.path(),
        &[
            "custom",
            "plugins",
            "Theme",
            "src",
            "Resources",
            "views",
            "storefront",
            "card.html.twig",
        ],
    );
    let index = twig_index(
        dir.path(),
        "cache",
        &[(&upstream, "{% block card %}upstream{% endblock %}")],
    );
    let current = path_of(
        dir.path(),
        &[
            "custom",
            "plugins",
            "Plugin",
            "src",
            "Resources",
            "views",
            "storefront",
            "card.html.twig",
        ],
    );
    let source = "{% sw_extends '@Theme/storefront/card.html.twig' %}\n{# shopware-block: deadbeef@1.0.0 #}\n{% block card %}local{% endblock %}";
    let service = Arc::new(VersioningService::new(
        dir.path().to_str().unwrap(),
        Some(index),
        "",
    ));
    let actions = TwigCodeActionProvider::new(Some(service)).get_code_actions(
        &Context::background(),
        &versioning_request(&current, source, "card", Vec::new()),
    );
    assert!(action_by_title(&actions, "Shopware: Update Twig block version comment").is_some());
    assert!(action_by_title(&actions, "Shopware: Show Twig block difference").is_none());
}

#[test]
fn storefront_twig_actions_do_not_leak_into_administration_templates() {
    let dir = tempfile::tempdir().unwrap();
    let storefront = path_of(
        dir.path(),
        &["src/Storefront/Resources/views/storefront/component/card.html.twig"],
    );
    let index = twig_index(
        dir.path(),
        "twig-cache",
        &[(&storefront, "{% block sw_card_header %}{% endblock %}")],
    );
    let source = "{% block sw_card_header %}{% endblock %}";
    let uri = file_uri(&path_of(
        dir.path(),
        &[
            "src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
        ],
    ));
    let offset = source.find("sw_card_header").unwrap() as u32 + 1;
    let request = offset_request(&uri, source, offset, Vec::new(), false);
    let service = Arc::new(VersioningService::new(
        dir.path().to_str().unwrap(),
        Some(index),
        "",
    ));
    assert!(
        TwigCodeActionProvider::new(Some(service))
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );
}

#[test]
fn overwrite_block_action_in_storefront_template() {
    let source = "{% block page_content %}x{% endblock %}";
    let uri = "file:///project/src/Resources/views/storefront/page/index.html.twig";
    let offset = source.find("page_content").unwrap() as u32 + 1;
    let request = offset_request(uri, source, offset, Vec::new(), true);
    let actions =
        TwigCodeActionProvider::new(None).get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Overwrite this block in Extension");
    let command = actions[0].command.as_ref().unwrap();
    assert_eq!(command.command, "shopware.twig.extendBlock");
    assert_eq!(command.arguments[1], "page_content");
}
