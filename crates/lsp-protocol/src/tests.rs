use serde_json::json;

use super::*;

fn encode<T: Serialize>(value: &T) -> Value {
    serde_json::to_value(value).unwrap()
}

/// An empty documentation object is not a valid Documentation value: kind is
/// a MarkupKind, so "" matches neither permitted variant.
#[test]
fn completion_item_omits_unset_documentation() {
    let item = CompletionItem {
        label: "BREADCRUMB_REWORK".to_owned(),
        kind: Some(CompletionItemKind::FUNCTION),
        ..CompletionItem::default()
    };
    assert_eq!(
        encode(&item),
        json!({"label": "BREADCRUMB_REWORK", "kind": 3})
    );
}

#[test]
fn completion_item_keeps_populated_documentation() {
    let item = CompletionItem {
        label: "isActive".to_owned(),
        kind: Some(CompletionItemKind::METHOD),
        documentation: Some(MarkupContent {
            kind: MarkupKind::Markdown,
            value: "Determines whether a feature is active.".to_owned(),
        }),
        ..CompletionItem::default()
    };
    assert_eq!(
        encode(&item),
        json!({"label": "isActive", "kind": 2, "documentation": {"kind": "markdown", "value": "Determines whether a feature is active."}})
    );
}

#[test]
fn completion_list_mixes_documented_and_undocumented_items() {
    let documented = CompletionItem {
        label: "isActive".to_owned(),
        documentation: Some(MarkupContent {
            kind: MarkupKind::PlainText,
            value: "Whether a feature is active.".to_owned(),
        }),
        ..CompletionItem::default()
    };
    let list = CompletionList {
        is_incomplete: false,
        items: vec![
            CompletionItem {
                label: "CACHE_REWORK".to_owned(),
                ..CompletionItem::default()
            },
            documented,
        ],
    };
    let encoded = encode(&list);
    assert_eq!(encoded["isIncomplete"], json!(false));
    assert!(encoded["items"][0].get("documentation").is_none());
    assert!(encoded["items"][1].get("documentation").is_some());
}

#[test]
fn initialize_params_decode_leniently() {
    let params: InitializeParams = serde_json::from_value(json!({
        "processId": 1,
        "rootPath": null,
        "rootUri": "file:///workspace",
        "workspaceFolders": null,
        "initializationOptions": {
            "phpExtensions": ["redis"],
            "cliMode": true,
            "configuration": {"version": 1, "features": {"hover": false}},
            "shopwareClient": {"protocolVersion": 2, "supportedCommands": ["a"]}
        },
        "capabilities": {
            "workspace": {"diagnostics": {"refreshSupport": true}},
            "textDocument": {"diagnostic": {"dynamicRegistration": true}, "codeAction": {"dataSupport": true, "resolveSupport": {"properties": ["edit"]}}},
            "window": null
        }
    }))
    .unwrap();
    assert_eq!(params.root_path, "");
    assert_eq!(params.root_uri, "file:///workspace");
    assert!(params.workspace_folders.is_empty());
    let options = &params.initialization_options;
    assert_eq!(options.php_extensions, ["redis"]);
    assert!(options.cli_mode);
    assert!(!options.configuration.as_ref().unwrap().features["hover"]);
    assert_eq!(
        options.shopware_client.as_ref().unwrap().protocol_version,
        2
    );
    assert!(params.capabilities.workspace.diagnostics.refresh_support);
    assert!(params.capabilities.text_document.diagnostic.is_some());
    let code_action = params
        .capabilities
        .text_document
        .code_action
        .as_ref()
        .unwrap();
    assert!(code_action.data_support);
    assert_eq!(code_action.resolve_support.properties, ["edit"]);

    let empty: InitializeParams = serde_json::from_value(json!({})).unwrap();
    assert_eq!(
        encode(&empty),
        json!({"initializationOptions": {}, "capabilities": {"workspace": {"diagnostics": {}}, "textDocument": {}, "window": {}}})
    );
}

#[test]
fn workspace_edit_matches_go_shape() {
    let range = Range::new(Position::new(0, 1), Position::new(0, 2));
    let edit = WorkspaceEdit {
        changes: [
            ("file:///b".to_owned(), vec![TextEdit::new(range, "x")]),
            ("file:///a".to_owned(), vec![]),
        ]
        .into(),
        document_changes: vec![
            DocumentChange {
                kind: CREATE_FILE_OPERATION.to_owned(),
                uri: "file:///new".to_owned(),
                options: Some(CreateFileOptions {
                    ignore_if_exists: true,
                    ..CreateFileOptions::default()
                }),
                ..DocumentChange::default()
            },
            DocumentChange {
                text_document: Some(OptionalVersionedTextDocumentIdentifier {
                    uri: "file:///new".to_owned(),
                    version: None,
                }),
                edits: vec![TextEdit {
                    insert_text_format: Some(InsertTextFormat::SNIPPET),
                    ..TextEdit::new(range, "$0")
                }],
                ..DocumentChange::default()
            },
        ],
        change_annotations: Default::default(),
    };
    assert_eq!(
        serde_json::to_string(&edit).unwrap(),
        concat!(
            r#"{"changes":{"file:///a":[],"file:///b":[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"newText":"x"}]},"#,
            r#""documentChanges":[{"kind":"create","uri":"file:///new","options":{"ignoreIfExists":true}},"#,
            r#"{"textDocument":{"uri":"file:///new","version":null},"edits":[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"newText":"$0","insertTextFormat":2}]}]}"#
        )
    );
}

#[test]
fn code_action_round_trips_through_resolve() {
    let action = CodeAction {
        title: "Fix".to_owned(),
        kind: Some(CodeActionKind::SOURCE_FIX_ALL),
        is_preferred: true,
        data: Some(json!({"id": 1})),
        ..CodeAction::default()
    };
    let encoded = encode(&action);
    assert_eq!(
        encoded,
        json!({"title": "Fix", "kind": "source.fixAll", "isPreferred": true, "data": {"id": 1}})
    );
    let decoded: CodeAction = serde_json::from_value(encoded).unwrap();
    assert_eq!(decoded, action);
}

#[test]
fn diagnostics_match_go_shape() {
    let diagnostic = Diagnostic {
        severity: Some(DiagnosticSeverity::WARNING),
        code: Some(json!("php.arguments")),
        source: "shopware".to_owned(),
        message: "m".to_owned(),
        ..Diagnostic::default()
    };
    assert_eq!(
        encode(&diagnostic),
        json!({"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 0}}, "severity": 2, "code": "php.arguments", "source": "shopware", "message": "m"})
    );
    let params: CodeActionParams = serde_json::from_value(json!({
        "textDocument": {"uri": "file:///x"},
        "range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 3}},
        "context": {"diagnostics": [{"range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 3}}, "message": "m", "data": null}], "only": null}
    }))
    .unwrap();
    assert_eq!(params.context.diagnostics[0].data, None);
    assert!(params.context.only.is_empty());
    let published = PublishDiagnosticsParams {
        uri: "file:///x".to_owned(),
        ..PublishDiagnosticsParams::default()
    };
    assert_eq!(
        encode(&published),
        json!({"uri": "file:///x", "diagnostics": []})
    );
}

#[test]
fn symbols_and_hierarchy_items_match_go_shape() {
    let symbol = SymbolInformation {
        name: "Product".to_owned(),
        kind: SymbolKind::CLASS,
        location: Location {
            uri: "file:///x.php".to_owned(),
            range: Range::default(),
        },
        container_name: String::new(),
        priority: 7,
    };
    let encoded = encode(&symbol);
    assert_eq!(encoded["kind"], json!(5));
    assert!(encoded.get("priority").is_none());
    assert!(encoded.get("containerName").is_none());

    let item: CallHierarchyItem = serde_json::from_value(json!({
        "name": "run", "kind": 6, "uri": "file:///x.php",
        "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 1}},
        "selectionRange": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 1}},
        "data": {"key": "value"}
    }))
    .unwrap();
    assert_eq!(item.kind, SymbolKind::METHOD);
    assert_eq!(item.data, Some(json!({"key": "value"})));
}

#[test]
fn misc_shapes_match_go() {
    assert_eq!(
        encode(&new_lsp_error("boom", "E1")),
        json!({"code": "E1", "message": "boom"})
    );
    let hint = InlayHint::default();
    assert_eq!(
        encode(&hint),
        json!({"position": {"line": 0, "character": 0}, "label": null})
    );
    let folding = FoldingRange {
        start_line: 1,
        end_line: 3,
        kind: FOLDING_RANGE_KIND_REGION.to_owned(),
        ..FoldingRange::default()
    };
    assert_eq!(
        encode(&folding),
        json!({"startLine": 1, "endLine": 3, "kind": "region"})
    );
    assert_eq!(
        encode(&SemanticTokensLegend::shopware()),
        json!({"tokenTypes": ["keyword", "property", "type", "variable", "string", "number", "class", "function", "operator"], "tokenModifiers": []})
    );
    assert_eq!(
        SEMANTIC_TOKEN_TYPES[SEMANTIC_TOKEN_OPERATOR as usize],
        "operator"
    );
    let event: DidChangeWatchedFilesParams =
        serde_json::from_value(json!({"changes": [{"uri": "file:///x", "type": 3}]})).unwrap();
    assert_eq!(event.changes[0].kind, 3);
    let watcher = FileSystemWatcher {
        glob_pattern: "**/*.php".to_owned(),
        kind: WATCH_CREATE | WATCH_DELETE,
    };
    assert_eq!(
        encode(&watcher),
        json!({"globPattern": "**/*.php", "kind": 5})
    );
    let selection = SelectionRange {
        range: Range::default(),
        parent: Some(Box::new(SelectionRange::default())),
    };
    assert!(encode(&selection)["parent"].get("parent").is_none());
    let color = ColorInformation {
        range: Range::default(),
        color: Color {
            red: 0.1,
            green: 0.2,
            blue: 0.3,
            alpha: 1.0,
        },
    };
    assert_eq!(encode(&color)["color"]["red"], json!(0.1));
    let help: SignatureHelpParams = serde_json::from_value(json!({
        "textDocument": {"uri": "file:///x"},
        "position": {"line": 0, "character": 0},
        "context": {"triggerKind": 2, "isRetrigger": true, "activeSignatureHelp": {"signatures": [{"label": "f()"}], "activeSignature": 0}}
    }))
    .unwrap();
    assert!(help.context.is_retrigger);
    assert_eq!(help.context.active_help.unwrap().signatures[0].label, "f()");
    let request: CommandRequest =
        serde_json::from_value(json!({"command": "shopware.x", "arguments": [1, "a"]})).unwrap();
    assert_eq!(request.arguments, [json!(1), json!("a")]);
}
