use crate::completion::SnippetCompletionProvider;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_snippet::SnippetIndexer;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// A request for `document` at `offset` with an explicit node.
fn request_for(
    document: &Arc<TextDocument>,
    node: Option<SyntaxNode>,
    offset: u32,
) -> CompletionRequest {
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: document.root(),
            token: None,
            node,
        },
    }
}

fn completions(
    provider: &dyn CompletionProvider,
    request: &CompletionRequest,
) -> Vec<protocol::CompletionItem> {
    provider.get_completions(&Context::background(), request)
}

fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

/// The item labelled `label`; panics when it is missing.
#[track_caller]
fn require_completion(items: &[protocol::CompletionItem], label: &str) -> protocol::CompletionItem {
    items
        .iter()
        .find(|item| item.label == label)
        .cloned()
        .unwrap_or_else(|| panic!("{label:?} not in {:?}", labels(items)))
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

#[test]
fn snippet_completion_pattern_matching() {
    for (name, code, frontend, admin) in [
        (
            "frontend trans filter",
            "{{ 'snippet.key'|trans }}",
            true,
            false,
        ),
        (
            "admin $tc function",
            "{{ $tc('snippet.key') }}",
            false,
            true,
        ),
        ("admin $t function", "{{ $t('snippet.key') }}", false, true),
        ("regular string", "{{ 'just a string' }}", false, false),
        (
            "regular function call",
            "{{ someFunc('arg') }}",
            false,
            false,
        ),
    ] {
        let parse = swlsp_parser_twig::parse(code);
        let root = parse.root();
        let strings = twigquery::nodes(&root, &[TWIG_LITERAL_STRING]);
        let first = strings.first().expect("string node");
        assert_eq!(
            twigquery::string_in_filter(first, &["trans"]),
            frontend,
            "{name}"
        );
        assert_eq!(
            twigquery::string_in_function(first, &["$tc", "$t"]),
            admin,
            "{name}"
        );
    }
}

fn snippet_index(root: &Path) -> Arc<SnippetIndexer> {
    let index = SnippetIndexer::new(root.join("cache"), None).unwrap();
    index_file(
        &index,
        &p(&root.join("src/Resources/app/administration/src/module/demo/snippet/en-GB.json")),
        r#"{"admin":{"settings":{"title":"Settings"}}}"#,
    );
    Arc::new(index)
}

#[test]
fn admin_snippet_completion_inside_vue_bound_twig_attribute() {
    let dir = tempfile::tempdir().unwrap();
    let provider = SnippetCompletionProvider::new(Some(snippet_index(dir.path())));
    let source = r#"<mt-button :label="$t('admin.sett" />"#;
    let document = Arc::new(TextDocument::new(
        "file:///project/src/Resources/app/administration/view.html.twig",
        source,
        1,
    ));
    let offset = (source.find("admin.sett").unwrap() + "admin.sett".len()) as u32;
    let node = document.root().unwrap().node_at(TextSize::from(offset - 1));
    let request = request_for(&document, node, offset);
    require_completion(&completions(&provider, &request), "admin.settings.title");
}

#[test]
fn admin_snippet_completion_in_javascript_references() {
    let dir = tempfile::tempdir().unwrap();
    let provider = SnippetCompletionProvider::new(Some(snippet_index(dir.path())));
    for (name, source) in [
        ("injected translator", "translator.$t('admin.sett')"),
        (
            "module title",
            "Module.register('demo', { title: 'admin.sett' });",
        ),
        (
            "navigation label",
            "Shopware.Module.register('demo', {\n            navigation: [{ label: 'admin.sett' }],\n        });",
        ),
    ] {
        let document = Arc::new(TextDocument::new(
            "file:///project/src/Resources/app/administration/index.ts",
            source,
            1,
        ));
        let offset = (source.find("admin.sett").unwrap() + "admin.sett".len()) as u32;
        let node = document.root().unwrap().node_at(TextSize::from(offset));
        let request = request_for(&document, node, offset);
        let items = completions(&provider, &request);
        assert!(
            items
                .iter()
                .any(|item| item.label == "admin.settings.title"),
            "{name}: {:?}",
            labels(&items)
        );
    }
}
