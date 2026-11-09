//! Provider fan-out tests (colors, call hierarchy, document features,
//! folding ranges, inlay hints, formatting, linked editing, selection ranges,
//! semantic tokens and workspace symbols).

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use serde_json::{Value, json};
use swlsp_lsp_protocol as protocol;

use super::support::*;
use crate::features::encode_semantic_tokens;
use crate::*;

fn ctx() -> Context {
    Context::background()
}

fn doc(uri: &str) -> protocol::TextDocumentIdentifier {
    protocol::TextDocumentIdentifier::new(uri)
}

fn color_params(uri: &str) -> protocol::DocumentColorParams {
    protocol::DocumentColorParams {
        text_document: doc(uri),
        ..Default::default()
    }
}

#[derive(Default)]
struct TestColorProvider {
    colors: Vec<protocol::ColorInformation>,
    presentations: Vec<protocol::ColorPresentation>,
    color_version: Mutex<Option<i32>>,
    presentation_version: Mutex<Option<i32>>,
}

impl DocumentColorProvider for TestColorProvider {
    fn get_document_colors(
        &self,
        _: &Context,
        request: &DocumentColorRequest,
    ) -> ProviderResult<Vec<protocol::ColorInformation>> {
        *self.color_version.lock().unwrap() = Some(request.document.version);
        Ok(self.colors.clone())
    }

    fn get_color_presentations(
        &self,
        _: &Context,
        request: &ColorPresentationRequest,
    ) -> ProviderResult<Vec<protocol::ColorPresentation>> {
        *self.presentation_version.lock().unwrap() = Some(request.document.version);
        Ok(self.presentations.clone())
    }
}

fn color(range: protocol::Range, value: f64) -> protocol::ColorInformation {
    protocol::ColorInformation {
        range,
        color: protocol::Color {
            red: value,
            green: value,
            blue: value,
            alpha: 1.0,
        },
    }
}

#[test]
fn document_colors_aggregate_deduplicate_validate_and_sort() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.scss";
    server
        .documents
        .open_document(uri, "$a: #000;\n$b: #fff;", 7);
    let first = color(protocol_range_of((0, 4), (0, 8)), 0.0);
    let second = color(protocol_range_of((1, 4), (1, 8)), 1.0);
    let invalid = color(protocol_range_of((2, 0), (1, 0)), 0.0);
    server.register_document_color_provider(Arc::new(TestColorProvider {
        colors: vec![second, invalid, first],
        ..Default::default()
    }));
    let observed = Arc::new(TestColorProvider {
        colors: vec![first],
        ..Default::default()
    });
    server.register_document_color_provider(observed.clone());
    let colors = server
        .document_colors(&ctx(), Some(color_params(uri)))
        .unwrap();
    assert_eq!(colors, vec![first, second]);
    assert_eq!(*observed.color_version.lock().unwrap(), Some(7));
    server.close_all().unwrap();
}

#[test]
fn color_presentations_use_live_document_and_deduplicate() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.scss";
    server.documents.open_document(uri, "$a: #000;", 9);
    let presentation = protocol::ColorPresentation {
        label: "#ffffff".into(),
        ..Default::default()
    };
    let observed = Arc::new(TestColorProvider {
        presentations: vec![presentation.clone()],
        ..Default::default()
    });
    server.register_document_color_provider(observed.clone());
    server.register_document_color_provider(Arc::new(TestColorProvider {
        presentations: vec![presentation.clone()],
        ..Default::default()
    }));
    let params = protocol::ColorPresentationParams {
        text_document: doc(uri),
        color: protocol::Color {
            red: 1.0,
            green: 1.0,
            blue: 1.0,
            alpha: 1.0,
        },
        ..Default::default()
    };
    let presentations = server.color_presentations(&ctx(), Some(params)).unwrap();
    assert_eq!(presentations, vec![presentation]);
    assert_eq!(*observed.presentation_version.lock().unwrap(), Some(9));
    server.close_all().unwrap();
}

#[test]
fn color_responses_marshal_empty_results_as_arrays() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.scss";
    server.documents.open_document(uri, "$a: #000;", 1);
    let call = |method: &str, params: Value| server.handle(&ctx(), method, params, false).unwrap();
    let open = json!({"textDocument": {"uri": uri}});
    let absent = json!({"textDocument": {"uri": "file:///workspace/absent.scss"}});
    assert_eq!(call("textDocument/documentColor", open.clone()), json!([]));
    assert_eq!(
        call("textDocument/documentColor", absent.clone()),
        json!([])
    );
    assert_eq!(call("textDocument/colorPresentation", open), json!([]));
    assert_eq!(call("textDocument/colorPresentation", absent), json!([]));
    assert!(server.document_colors(&ctx(), None).unwrap().is_empty());
    assert!(server.color_presentations(&ctx(), None).unwrap().is_empty());
    server.close_all().unwrap();
}

struct TestCallHierarchyProvider {
    prepared: Vec<protocol::CallHierarchyItem>,
    incoming: Vec<protocol::CallHierarchyIncomingCall>,
    outgoing: Vec<protocol::CallHierarchyOutgoingCall>,
    documents: Option<Arc<AtomicUsize>>,
}

impl CallHierarchyProvider for TestCallHierarchyProvider {
    fn prepare_call_hierarchy(
        &self,
        _: &Context,
        _: &CallHierarchyPrepareRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyItem>> {
        Ok(self.prepared.clone())
    }

    fn incoming_calls(
        &self,
        _: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyIncomingCall>> {
        if let Some(count) = &self.documents {
            count.store(request.documents.len(), Ordering::SeqCst);
        }
        Ok(self.incoming.clone())
    }

    fn outgoing_calls(
        &self,
        _: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyOutgoingCall>> {
        if let Some(count) = &self.documents {
            count.store(request.documents.len(), Ordering::SeqCst);
        }
        Ok(self.outgoing.clone())
    }
}

fn hierarchy_item(
    name: &str,
    kind: protocol::SymbolKind,
    uri: &str,
    range: protocol::Range,
    selection: protocol::Range,
) -> protocol::CallHierarchyItem {
    protocol::CallHierarchyItem {
        name: name.into(),
        kind,
        tags: Vec::new(),
        detail: String::new(),
        uri: uri.into(),
        range,
        selection_range: selection,
        data: None,
    }
}

#[test]
fn call_hierarchy_aggregates_providers_and_supplies_open_documents() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.js";
    server
        .documents
        .open_document(uri, "function save() {}\nsave();\n", 1);
    let callee = hierarchy_item(
        "save",
        protocol::SymbolKind::METHOD,
        uri,
        protocol_range_of((0, 0), (0, 15)),
        protocol_range_of((0, 9), (0, 13)),
    );
    let caller = hierarchy_item(
        "component.js",
        protocol::SymbolKind::FILE,
        uri,
        protocol_range_of((0, 0), (2, 0)),
        protocol::Range::default(),
    );
    let first = protocol_range_of((1, 0), (1, 4));
    let second = protocol_range_of((2, 0), (2, 4));
    let count = Arc::new(AtomicUsize::new(0));
    server.register_call_hierarchy_provider(Arc::new(TestCallHierarchyProvider {
        prepared: vec![callee.clone()],
        incoming: vec![protocol::CallHierarchyIncomingCall {
            from: caller.clone(),
            from_ranges: vec![second, first],
        }],
        outgoing: vec![protocol::CallHierarchyOutgoingCall {
            to: callee.clone(),
            from_ranges: vec![second],
        }],
        documents: Some(count.clone()),
    }));
    server.register_call_hierarchy_provider(Arc::new(TestCallHierarchyProvider {
        prepared: vec![callee.clone()],
        incoming: vec![protocol::CallHierarchyIncomingCall {
            from: caller.clone(),
            from_ranges: vec![first],
        }],
        outgoing: vec![protocol::CallHierarchyOutgoingCall {
            to: callee.clone(),
            from_ranges: vec![first],
        }],
        documents: None,
    }));
    let prepared = server
        .prepare_call_hierarchy(
            &ctx(),
            protocol::CallHierarchyPrepareParams {
                text_document: doc(uri),
                position: position(0, 10),
            },
        )
        .unwrap();
    assert_eq!(prepared, vec![callee.clone()]);
    let incoming = server
        .call_hierarchy_incoming_calls(&ctx(), callee)
        .unwrap();
    assert_eq!(incoming.len(), 1);
    assert_eq!(incoming[0].from_ranges, vec![first, second]);
    assert_eq!(count.load(Ordering::SeqCst), 1);
    let outgoing = server
        .call_hierarchy_outgoing_calls(&ctx(), caller)
        .unwrap();
    assert_eq!(outgoing.len(), 1);
    assert_eq!(outgoing[0].from_ranges, vec![first, second]);
    server.close_all().unwrap();
}

struct TestHighlightProvider(Vec<protocol::DocumentHighlight>);

impl DocumentHighlightProvider for TestHighlightProvider {
    fn get_document_highlights(
        &self,
        _: &Context,
        _: &DocumentHighlightRequest,
    ) -> ProviderResult<Vec<protocol::DocumentHighlight>> {
        Ok(self.0.clone())
    }
}

#[test]
fn document_highlights_aggregate_sort_and_prefer_specific_kind() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.js";
    server.documents.open_document(uri, "first\nsecond\n", 1);
    let first = protocol_range_of((0, 0), (0, 5));
    let second = protocol_range_of((1, 0), (1, 6));
    let highlight = |range, kind| protocol::DocumentHighlight {
        range,
        kind: Some(kind),
    };
    server.register_document_highlight_provider(Arc::new(TestHighlightProvider(vec![
        highlight(second, protocol::DocumentHighlightKind::READ),
        highlight(first, protocol::DocumentHighlightKind::TEXT),
    ])));
    server.register_document_highlight_provider(Arc::new(TestHighlightProvider(vec![highlight(
        first,
        protocol::DocumentHighlightKind::WRITE,
    )])));
    let highlights = server
        .document_highlights(
            &ctx(),
            protocol::DocumentHighlightParams {
                text_document: doc(uri),
                position: position(0, 0),
            },
        )
        .unwrap();
    assert_eq!(highlights.len(), 2);
    assert_eq!(highlights[0].range, first);
    assert_eq!(
        highlights[0].kind,
        Some(protocol::DocumentHighlightKind::WRITE)
    );
    assert_eq!(highlights[1].range, second);
    server.close_all().unwrap();
}

struct TestSymbolProvider(Vec<protocol::DocumentSymbol>);

impl DocumentSymbolProvider for TestSymbolProvider {
    fn get_document_symbols(
        &self,
        _: &Context,
        _: &DocumentSymbolRequest,
    ) -> ProviderResult<Vec<protocol::DocumentSymbol>> {
        Ok(self.0.clone())
    }
}

#[test]
fn document_symbols_aggregate_sort_and_deduplicate_providers() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.js";
    server.documents.open_document(uri, "first\nsecond\n", 1);
    let symbol = |name: &str, kind, range, selection_range| protocol::DocumentSymbol {
        name: name.into(),
        detail: String::new(),
        kind,
        deprecated: false,
        range,
        selection_range,
        children: Vec::new(),
    };
    let first = symbol(
        "first",
        protocol::SymbolKind::CLASS,
        protocol_range_of((0, 0), (1, 0)),
        protocol_range_of((0, 0), (0, 5)),
    );
    let second = symbol(
        "second",
        protocol::SymbolKind::METHOD,
        protocol_range_of((1, 0), (1, 6)),
        protocol_range_of((1, 0), (1, 6)),
    );
    server.register_document_symbol_provider(Arc::new(TestSymbolProvider(vec![
        second,
        first.clone(),
    ])));
    server.register_document_symbol_provider(Arc::new(TestSymbolProvider(vec![first])));
    let symbols = server
        .document_symbols(
            &ctx(),
            protocol::DocumentSymbolParams {
                text_document: doc(uri),
                ..Default::default()
            },
        )
        .unwrap();
    assert_eq!(
        symbols
            .iter()
            .map(|symbol| symbol.name.as_str())
            .collect::<Vec<_>>(),
        ["first", "second"]
    );
    server.close_all().unwrap();
}

#[derive(Default)]
struct TestLinkProvider(AtomicBool);

impl DocumentLinkProvider for TestLinkProvider {
    fn get_document_links(
        &self,
        _: &Context,
        request: &DocumentLinkRequest,
    ) -> ProviderResult<Vec<protocol::DocumentLink>> {
        self.0
            .store(request.document.version == 1, Ordering::SeqCst);
        Ok(vec![protocol::DocumentLink {
            target: "file:///project/target.yaml".into(),
            tooltip: "Open target".into(),
            ..Default::default()
        }])
    }
}

#[test]
fn server_collects_document_links_for_open_document() {
    let server = Server::new(None, "", "test");
    let provider = Arc::new(TestLinkProvider::default());
    server.register_document_link_provider(provider.clone());
    server
        .documents
        .open_document("file:///project/config.yaml", "imports: []", 1);
    let links = server
        .document_links(
            &ctx(),
            protocol::DocumentLinkParams {
                text_document: doc("file:///project/config.yaml"),
                ..Default::default()
            },
        )
        .unwrap();
    assert!(provider.0.load(Ordering::SeqCst));
    assert_eq!(links.len(), 1);
    assert_eq!(links[0].target, "file:///project/target.yaml");
    server.close_all().unwrap();
}

struct TestFoldingProvider(Vec<protocol::FoldingRange>);

impl FoldingRangeProvider for TestFoldingProvider {
    fn get_folding_ranges(
        &self,
        _: &Context,
        _: &FoldingRangeRequest,
    ) -> ProviderResult<Vec<protocol::FoldingRange>> {
        Ok(self.0.clone())
    }
}

#[test]
fn folding_ranges_aggregate_deduplicate_validate_and_sort() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.ts";
    server
        .documents
        .open_document(uri, "first\nsecond\nthird\n", 1);
    let fold = |start_line, end_line| protocol::FoldingRange {
        start_line,
        end_line,
        ..Default::default()
    };
    let outer = fold(0, 2);
    let inner = fold(0, 1);
    server.register_folding_range_provider(Arc::new(TestFoldingProvider(vec![
        inner.clone(),
        fold(1, 1),
        outer.clone(),
    ])));
    server.register_folding_range_provider(Arc::new(TestFoldingProvider(vec![outer.clone()])));
    let ranges = server
        .folding_ranges(
            &ctx(),
            protocol::FoldingRangeParams {
                text_document: doc(uri),
                ..Default::default()
            },
        )
        .unwrap();
    assert_eq!(ranges, vec![outer, inner]);
    server.close_all().unwrap();
}

#[derive(Default)]
struct TestInlayHintProvider(AtomicBool);

impl InlayHintProvider for TestInlayHintProvider {
    fn get_inlay_hints(
        &self,
        _: &Context,
        _: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        self.0.store(true, Ordering::SeqCst);
        Ok(vec![protocol::InlayHint {
            label: "test".into(),
            kind: Some(protocol::InlayHintKind::PARAMETER),
            ..Default::default()
        }])
    }
}

#[test]
fn server_collects_inlay_hints_for_open_document() {
    let server = Server::new(None, "", "test");
    let provider = Arc::new(TestInlayHintProvider::default());
    server.register_inlay_hint_provider(provider.clone());
    server
        .documents
        .open_document("file:///project/services.yaml", "services: {}", 1);
    let hints = server
        .inlay_hints(
            &ctx(),
            protocol::InlayHintParams {
                text_document: doc("file:///project/services.yaml"),
                range: protocol_range_of((0, 0), (1, 0)),
                ..Default::default()
            },
        )
        .unwrap();
    assert!(provider.0.load(Ordering::SeqCst));
    assert_eq!(hints.len(), 1);
    assert_eq!(hints[0].label, "test".into());
    server.close_all().unwrap();
}

struct TestFormattingProvider(Option<String>);

impl DocumentFormattingProvider for TestFormattingProvider {
    fn format_document(
        &self,
        _: &Context,
        _: &DocumentFormattingRequest,
    ) -> ProviderResult<Option<String>> {
        Ok(self.0.clone())
    }
}

#[test]
fn formatting_protocol_route() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/example.html.twig";
    server.documents.open_document(uri, "unformatted", 1);
    server.register_document_formatting_provider(Arc::new(TestFormattingProvider(Some(
        "formatted".into(),
    ))));
    let result = server
        .handle(
            &ctx(),
            "textDocument/formatting",
            json!({"textDocument": {"uri": uri}, "options": {"tabSize": 4, "insertSpaces": true}}),
            false,
        )
        .unwrap();
    let edits = result.as_array().unwrap();
    assert_eq!(edits.len(), 1);
    assert_eq!(edits[0]["newText"], "formatted");
    server.close_all().unwrap();
}

#[test]
fn formatting_uses_current_document_and_returns_one_full_utf16_edit() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/example.html.twig";
    server.documents.open_document(uri, "😀\nunformatted", 7);
    server.register_document_formatting_provider(Arc::new(TestFormattingProvider(Some(
        "formatted".into(),
    ))));
    let edits = server
        .formatting(
            &ctx(),
            protocol::DocumentFormattingParams {
                text_document: doc(uri),
                ..Default::default()
            },
        )
        .unwrap();
    assert_eq!(
        edits,
        vec![protocol::TextEdit::new(
            protocol_range_of((0, 0), (1, 11)),
            "formatted"
        )]
    );
    server.close_all().unwrap();
}

#[test]
fn formatting_capability_requires_provider() {
    let server = Server::new(None, "", "test");
    assert!(
        server
            .server_capabilities()
            .get("documentFormattingProvider")
            .is_none()
    );
    server.register_document_formatting_provider(Arc::new(TestFormattingProvider(None)));
    assert_eq!(
        server.server_capabilities()["documentFormattingProvider"],
        true
    );
    crate::server::write(&server.configuration)
        .effective
        .features
        .insert("formatting".into(), false);
    assert!(
        server
            .server_capabilities()
            .get("documentFormattingProvider")
            .is_none()
    );
    server.close_all().unwrap();
}

struct TestLinkedEditingProvider {
    result: Option<protocol::LinkedEditingRanges>,
    observed: Mutex<Option<(i32, bool)>>,
}

impl LinkedEditingRangeProvider for TestLinkedEditingProvider {
    fn get_linked_editing_ranges(
        &self,
        _: &Context,
        request: &LinkedEditingRangeRequest,
    ) -> ProviderResult<Option<protocol::LinkedEditingRanges>> {
        let version = request
            .document
            .as_ref()
            .map_or(0, |document| document.version);
        *self.observed.lock().unwrap() = Some((version, request.node.is_some()));
        Ok(self.result.clone())
    }
}

#[test]
fn linked_editing_ranges_use_first_complete_provider_result() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.html.twig";
    server
        .documents
        .open_document(uri, "<sw-card></sw-card>", 3);
    server.register_linked_editing_range_provider(Arc::new(TestLinkedEditingProvider {
        result: Some(protocol::LinkedEditingRanges {
            ranges: vec![protocol::Range::default()],
            ..Default::default()
        }),
        observed: Mutex::new(None),
    }));
    let expected = protocol::LinkedEditingRanges {
        ranges: vec![
            protocol_range_of((0, 1), (0, 8)),
            protocol_range_of((0, 11), (0, 18)),
        ],
        word_pattern: "[-A-Za-z0-9_]+".into(),
    };
    let observed = Arc::new(TestLinkedEditingProvider {
        result: Some(expected.clone()),
        observed: Mutex::new(None),
    });
    server.register_linked_editing_range_provider(observed.clone());
    let result = server
        .linked_editing_ranges(
            &ctx(),
            protocol::LinkedEditingRangeParams {
                text_document: doc(uri),
                position: position(0, 4),
            },
        )
        .unwrap();
    assert_eq!(result, Some(expected));
    assert_eq!(*observed.observed.lock().unwrap(), Some((3, true)));
    server.close_all().unwrap();
}

struct TestSelectionProvider {
    ranges: Vec<protocol::SelectionRange>,
    version: Mutex<Option<i32>>,
}

impl SelectionRangeProvider for TestSelectionProvider {
    fn get_selection_ranges(
        &self,
        _: &Context,
        request: &SelectionRangeRequest,
    ) -> ProviderResult<Vec<protocol::SelectionRange>> {
        *self.version.lock().unwrap() = Some(request.document.version);
        Ok(self.ranges.clone())
    }
}

#[test]
fn selection_ranges_require_one_result_per_position() {
    let server = Server::new(None, "", "test");
    let uri = "file:///workspace/component.ts";
    server.documents.open_document(uri, "first\nsecond\n", 4);
    server.register_selection_range_provider(Arc::new(TestSelectionProvider {
        ranges: vec![protocol::SelectionRange::default()],
        version: Mutex::new(None),
    }));
    let expected = vec![
        protocol::SelectionRange {
            range: protocol_range_of((0, 0), (0, 5)),
            parent: None,
        },
        protocol::SelectionRange {
            range: protocol_range_of((1, 0), (1, 6)),
            parent: None,
        },
    ];
    let observed = Arc::new(TestSelectionProvider {
        ranges: expected.clone(),
        version: Mutex::new(None),
    });
    server.register_selection_range_provider(observed.clone());
    let ranges = server
        .selection_ranges(
            &ctx(),
            protocol::SelectionRangeParams {
                text_document: doc(uri),
                positions: vec![position(0, 0), position(1, 0)],
                ..Default::default()
            },
        )
        .unwrap();
    assert_eq!(ranges, expected);
    assert_eq!(*observed.version.lock().unwrap(), Some(4));
    server.close_all().unwrap();
}

fn token(source: &str, value: &str, token_type: u32) -> SemanticToken {
    let start = source.find(value).unwrap() as u32;
    SemanticToken {
        range: range(start, start + value.len() as u32),
        token_type,
        modifiers: 0,
    }
}

#[test]
fn encode_semantic_tokens_uses_utf16_and_relative_positions() {
    let source = "😀 @prop name\n@block content";
    let document = TextDocument::new("file:///component.html.twig", source, 1);
    let tokens = vec![
        token(source, "@block", protocol::SEMANTIC_TOKEN_KEYWORD),
        token(source, "name", protocol::SEMANTIC_TOKEN_PROPERTY),
        token(source, "@prop", protocol::SEMANTIC_TOKEN_KEYWORD),
        // Invalid multiline ranges are conservatively excluded.
        SemanticToken {
            range: range(0, source.len() as u32),
            token_type: protocol::SEMANTIC_TOKEN_TYPE,
            modifiers: 0,
        },
    ];
    assert_eq!(
        encode_semantic_tokens(&document, &tokens),
        vec![
            0,
            3,
            5,
            protocol::SEMANTIC_TOKEN_KEYWORD,
            0,
            0,
            6,
            4,
            protocol::SEMANTIC_TOKEN_PROPERTY,
            0,
            1,
            0,
            6,
            protocol::SEMANTIC_TOKEN_KEYWORD,
            0,
        ]
    );
}

struct StaticSemanticTokens(Vec<SemanticToken>);

impl SemanticTokensProvider for StaticSemanticTokens {
    fn get_semantic_tokens(
        &self,
        _: &Context,
        _: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        Ok(self.0.clone())
    }
}

#[test]
fn semantic_tokens_collect_providers_for_open_document() {
    let source = "{# @prop open boolean Description #}";
    let server = Server::new(None, "", "test");
    server.register_semantic_tokens_provider(Arc::new(StaticSemanticTokens(vec![token(
        source,
        "@prop",
        protocol::SEMANTIC_TOKEN_KEYWORD,
    )])));
    server
        .documents
        .open_document("file:///component.html.twig", source, 1);
    let params = |uri: &str| protocol::SemanticTokensParams {
        text_document: doc(uri),
        ..Default::default()
    };
    let result = server
        .semantic_tokens(&ctx(), params("file:///component.html.twig"))
        .unwrap();
    assert_eq!(
        result.data,
        vec![0, 3, 5, protocol::SEMANTIC_TOKEN_KEYWORD, 0]
    );
    let missing = server
        .semantic_tokens(&ctx(), params("file:///missing.html.twig"))
        .unwrap();
    assert!(missing.data.is_empty());
    assert_eq!(serde_json::to_value(&missing).unwrap(), json!({"data": []}));
    server.close_all().unwrap();
}

struct TestWorkspaceSymbols(Vec<protocol::SymbolInformation>);

impl WorkspaceSymbolProvider for TestWorkspaceSymbols {
    fn workspace_symbols(
        &self,
        _: &Context,
        _: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>> {
        Ok(self.0.clone())
    }
}

#[test]
fn workspace_symbols_aggregate_sort_and_deduplicate_providers() {
    let symbol = |name: &str, kind, uri: &str| protocol::SymbolInformation {
        name: name.into(),
        kind,
        location: protocol::Location {
            uri: uri.into(),
            range: protocol::Range::default(),
        },
        container_name: String::new(),
        priority: 0,
    };
    let alpha = symbol(
        "alpha",
        protocol::SymbolKind::FUNCTION,
        "file:///project/alpha.php",
    );
    let beta = symbol(
        "beta",
        protocol::SymbolKind::CLASS,
        "file:///project/beta.php",
    );
    let server = Server::new(None, "", "test");
    server.register_workspace_symbol_provider(Arc::new(TestWorkspaceSymbols(vec![
        beta.clone(),
        alpha.clone(),
    ])));
    server.register_workspace_symbol_provider(Arc::new(TestWorkspaceSymbols(vec![alpha.clone()])));
    let result = server
        .workspace_symbols(
            &ctx(),
            protocol::WorkspaceSymbolParams { query: "a".into() },
        )
        .unwrap();
    assert_eq!(result, vec![alpha, beta]);
    server.close_all().unwrap();
}

#[test]
fn empty_fan_out_results_are_null_and_empty_command_lists_are_arrays() {
    let server = Server::new(None, "", "test");
    let params =
        json!({"textDocument": {"uri": "file:///a.twig"}, "position": {"line": 0, "character": 0}});
    assert_eq!(
        server
            .handle(&ctx(), "textDocument/definition", params.clone(), false)
            .unwrap(),
        Value::Null
    );
    assert_eq!(
        server
            .handle(&ctx(), "textDocument/references", params.clone(), false)
            .unwrap(),
        Value::Null
    );
    assert_eq!(
        server
            .handle(&ctx(), "textDocument/hover", params, false)
            .unwrap(),
        Value::Null
    );
    assert_eq!(
        server
            .handle(&ctx(), "shopware/commands", Value::Null, false)
            .unwrap(),
        json!([])
    );
}
