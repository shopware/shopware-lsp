//! Twig translation extraction tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, Context, TextDocument, WorkspaceEditHost};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::HandleError;
use swlsp_syntax::LineIndex;
use swlsp_translation::Index;

use super::*;
use swlsp_ide::codeaction::testing::{GenerationTestHost, request_for_range, snapshot};

fn translation_index() -> (tempfile::TempDir, tempfile::TempDir, Arc<Index>) {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    let root = tempfile::tempdir().unwrap();
    for (name, source) in [
        ("admin.en.yaml", "existing: Existing\n"),
        ("messages.en.yaml", "existing: Existing\n"),
    ] {
        let path = root.path().join("translations").join(name);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        index
            .index(&ParsedFile::new(
                path.to_str().unwrap().to_owned(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    (cache, root, index)
}

fn extract_range(source: &str, value: &str, occurrence: usize) -> protocol::Range {
    let mut base = 0;
    let mut offset = 0;
    for _ in 0..=occurrence {
        let Some(found) = source[base..].find(value) else {
            return protocol::Range::default();
        };
        offset = base + found;
        base = offset + value.len();
    }
    let line_index = LineIndex::new(source.to_owned());
    let (start_line, start_character) = line_index.position_utf16(offset as u32);
    let (end_line, end_character) = line_index.position_utf16((offset + value.len()) as u32);
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

fn raw(request: &TwigTranslationExtractionRequest) -> serde_json::Value {
    serde_json::to_value(request).unwrap()
}

fn host_with(entries: Vec<(String, swlsp_lsp::DocumentSnapshot)>) -> Arc<GenerationTestHost> {
    let mut host = GenerationTestHost::default();
    host.snapshots.extend(entries);
    Arc::new(host)
}

fn unversioned(uri: &str, source: &str) -> swlsp_lsp::DocumentSnapshot {
    swlsp_lsp::DocumentSnapshot {
        document: Arc::new(TextDocument::new(uri, source, 0)),
        version: None,
    }
}

#[test]
fn code_action_validates_static_html_text() {
    let (_cache, _root, index) = translation_index();
    let provider = TwigTranslationExtractProvider::new(Some(index), None);
    let source = r#"<div title="Attribute text">Visible text</div>{{ dynamic }}"#;
    let document = Arc::new(TextDocument::new(
        "file:///project/templates/page.html.twig",
        source,
        1,
    ));
    for value in ["Attribute text", "Visible text"] {
        let request = request_for_range(document.clone(), extract_range(source, value, 0));
        let actions = provider.get_code_actions(&Context::background(), &request);
        assert_eq!(actions.len(), 1, "{value}");
        assert_eq!(
            actions[0].kind,
            Some(protocol::CodeActionKind::REFACTOR_EXTRACT)
        );
        let command = actions[0].command.as_ref().unwrap();
        assert_eq!(command.command, EXTRACT_TWIG_TRANSLATION_ACTION);
        assert_eq!(
            command.arguments[1],
            serde_json::to_value(request.params.range).unwrap()
        );
    }
    let request = request_for_range(document, extract_range(source, "dynamic", 0));
    assert!(
        provider
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );
}

#[test]
fn prepare_and_generate() {
    let (_cache, _root, index) = translation_index();
    let source = "{% trans_default_domain 'admin' %}\n<p> Hello world </p>";
    let mut request = TwigTranslationExtractionRequest {
        file_uri: "file:///project/templates/page.html.twig".to_owned(),
        source: source.to_owned(),
        range: extract_range(source, "Hello world", 0),
        ..Default::default()
    };
    let host = host_with(vec![(
        request.file_uri.clone(),
        unversioned(&request.file_uri, source),
    )]);
    let provider = TwigTranslationExtractProvider::new(Some(index), Some(host));
    let ctx = Context::background();
    let prepared = provider.prepare(&ctx, Some(&raw(&request))).unwrap();
    assert_eq!(prepared.text, "Hello world");
    assert_eq!(prepared.default_key, "hello.world");
    assert_eq!(prepared.default_domain, "admin");
    assert!(prepared.domains.contains(&"admin".to_owned()));
    assert!(prepared.domains.contains(&"messages".to_owned()));

    request.key = "welcome.title".to_owned();
    request.domain = "admin".to_owned();
    let generated = provider.generate(&ctx, Some(&raw(&request))).unwrap();
    assert_eq!(generated.replacement, "{{ 'welcome.title'|trans }}");
    assert_eq!(generated.targets.len(), 1);
    assert_eq!(generated.targets[0].locale, "en");
    assert!(
        generated.targets[0]
            .new_text
            .contains("'welcome.title': 'Hello world'"),
        "{}",
        generated.targets[0].new_text
    );

    request.key = "public.title".to_owned();
    request.domain = "messages".to_owned();
    let generated = provider.generate(&ctx, Some(&raw(&request))).unwrap();
    assert_eq!(
        generated.replacement,
        "{{ 'public.title'|trans({}, 'messages') }}"
    );
}

#[test]
fn uses_whole_text_at_caret_and_rejects_duplicates() {
    let (_cache, _root, index) = translation_index();
    let source = "<p>  Hello there  </p>";
    let caret = source.find("there").unwrap() as u32 + 2;
    let (line, character) = LineIndex::new(source.to_owned()).position_utf16(caret);
    let position = protocol::Position { line, character };
    let mut request = TwigTranslationExtractionRequest {
        file_uri: "file:///project/templates/page.html.twig".to_owned(),
        source: source.to_owned(),
        range: protocol::Range {
            start: position,
            end: position,
        },
        ..Default::default()
    };
    let host = host_with(vec![(
        request.file_uri.clone(),
        unversioned(&request.file_uri, source),
    )]);
    let provider = TwigTranslationExtractProvider::new(Some(index), Some(host));
    let ctx = Context::background();
    let prepared = provider.prepare(&ctx, Some(&raw(&request))).unwrap();
    assert_eq!(prepared.text, "Hello there");
    assert_eq!(prepared.range, extract_range(source, "Hello there", 0));

    request.key = "existing".to_owned();
    request.domain = "messages".to_owned();
    let error = provider.generate(&ctx, Some(&raw(&request))).unwrap_err();
    assert!(error.to_string().contains("already exists"), "{error}");
}

#[test]
fn plans_after_selection_and_checks_source() {
    let (_cache, _root, index) = translation_index();
    let source_uri = "file:///project/template.html.twig";
    let source = "<p>Hello 😀</p>";
    let host = Arc::new(GenerationTestHost::default());
    let mut inner = Arc::try_unwrap(host).ok().unwrap();
    inner
        .snapshots
        .insert(source_uri.to_owned(), snapshot(source_uri, source, 4));
    let mut request = TwigTranslationExtractionRequest {
        file_uri: source_uri.to_owned(),
        source: source.to_owned(),
        version: Some(4),
        range: extract_range(source, "Hello 😀", 0),
        key: "new.key".to_owned(),
        domain: "messages".to_owned(),
        preview: true,
        ..Default::default()
    };
    let ctx = Context::background();
    let provider = TwigTranslationExtractProvider::new(Some(index.clone()), None);
    let preview = TwigTranslationExtractProvider {
        host: Some(Arc::new(GenerationTestHost {
            snapshots: inner.snapshots.clone(),
            ..Default::default()
        }) as Arc<dyn WorkspaceEditHost>),
        ..provider.clone()
    }
    .generate(&ctx, Some(&raw(&request)))
    .unwrap();
    assert!(preview.edit.is_none());
    assert_eq!(preview.targets.len(), 1);
    assert!(preview.targets[0].new_text.is_empty());
    let target_uri = preview.targets[0].file_uri.clone();
    let target_source = "existing: Unsaved\nother: Extra\n";
    inner
        .snapshots
        .insert(target_uri.clone(), snapshot(&target_uri, target_source, 9));
    let host = Arc::new(inner);
    let provider = TwigTranslationExtractProvider::new(Some(index), Some(host.clone()));
    request.preview = false;
    request.target_uris = vec![target_uri];
    let generated = provider.generate(&ctx, Some(&raw(&request))).unwrap();
    assert!(generated.edit.is_some());
    let plan = host.plan.lock().unwrap().clone().unwrap();
    assert_eq!(plan.documents.len(), 2);
    assert_eq!(plan.documents[0].version, Some(4));
    assert_eq!(plan.documents[1].version, Some(9));
    assert_eq!(
        plan.documents[1].apply().unwrap(),
        format!("{target_source}'new.key': 'Hello 😀'\n")
    );

    // A changed source snapshot is stale.
    let mut changed = GenerationTestHost {
        snapshots: host.snapshots.clone(),
        ..Default::default()
    };
    changed.snapshots.insert(
        source_uri.to_owned(),
        unversioned(source_uri, "<p>Changed</p>"),
    );
    let provider = TwigTranslationExtractProvider {
        host: Some(Arc::new(changed)),
        ..provider
    };
    let error = provider.generate(&ctx, Some(&raw(&request))).unwrap_err();
    assert!(matches!(
        error.downcast_ref::<HandleError>(),
        Some(HandleError::Stale)
    ));
}
