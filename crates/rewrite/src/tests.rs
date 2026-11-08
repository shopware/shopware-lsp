use super::*;
use swlsp_language::LanguageId;

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(start.into(), end.into())
}

#[test]
fn builder_coalesces_insertions_in_declaration_order() {
    let mut builder = Builder::new("abc");
    builder.insert(1, "first").unwrap();
    builder.insert(1, "-second").unwrap();
    let edits = builder.finish().unwrap();
    assert_eq!(edits.len(), 1);
    assert_eq!(apply("abc", &edits).unwrap(), "afirst-secondbc");
}

#[test]
fn builder_rejects_overlapping_structural_edits() {
    let mut builder = Builder::new("abcdef");
    builder.replace_range(range(1, 4), "x").unwrap();
    builder.replace_range(range(3, 5), "y").unwrap();
    assert!(builder.finish().unwrap_err().is_overlap());

    let mut builder = Builder::new("abcdef");
    builder.replace_range(range(1, 4), "x").unwrap();
    builder.insert(2, "y").unwrap();
    assert!(builder.finish().unwrap_err().is_overlap());
}

#[test]
fn builder_rejects_ranges_outside_the_source() {
    let mut builder = Builder::new("abc");
    assert!(
        builder
            .replace_range(range(1, 4), "x")
            .unwrap_err()
            .is_invalid_range()
    );
}

#[test]
fn workspace_plan_emits_versioned_utf16_document_edit() {
    let source = "emoji: 😀\n";
    let start = "emoji: ".len() as u32;
    let mut builder = Builder::new(source);
    builder
        .replace_range(range(start, start + "😀".len() as u32), "value")
        .unwrap();
    let edits = builder.finish().unwrap();
    let plan = WorkspacePlan {
        documents: vec![DocumentPlan::new(
            "file:///project/test.yaml",
            Some(7),
            source,
            edits,
        )],
        ..WorkspacePlan::default()
    };
    let edit = plan.workspace_edit().unwrap();
    assert_eq!(edit.document_changes.len(), 1);
    let change = &edit.document_changes[0];
    assert_eq!(change.text_document.as_ref().unwrap().version, Some(7));
    assert_eq!(change.edits[0].range.start.character, 7);
    assert_eq!(change.edits[0].range.end.character, 9);
}

#[test]
fn workspace_plan_marshals_created_file_edit_with_null_version() {
    let plan = WorkspacePlan {
        creates: vec![CreateFilePlan {
            uri: "file:///project/Generated.php".into(),
            content: "<?php\n".into(),
        }],
        ..WorkspacePlan::default()
    };
    let edit = plan.workspace_edit().unwrap();
    let encoded = serde_json::to_value(&edit).unwrap();
    let changes = encoded["documentChanges"].as_array().unwrap();
    assert_eq!(changes.len(), 2);
    let text_document = changes[1]["textDocument"].as_object().unwrap();
    assert!(
        text_document.contains_key("version"),
        "unversioned LSP document edits must include version"
    );
    assert!(text_document["version"].is_null());
    assert_eq!(changes[0]["kind"], "create");
}

#[test]
fn workspace_plan_emits_delete_after_created_content() {
    let plan = WorkspacePlan {
        creates: vec![CreateFilePlan {
            uri: "file:///project/services.yaml".into(),
            content: "services:\n".into(),
        }],
        deletes: vec![DeleteFilePlan {
            uri: "file:///project/services.xml".into(),
            version: Some(4),
            source: "<container/>".into(),
        }],
        ..WorkspacePlan::default()
    };
    let edit = plan.workspace_edit().unwrap();
    assert_eq!(edit.document_changes.len(), 3);
    assert_eq!(
        edit.document_changes[0].kind,
        swlsp_lsp_protocol::CREATE_FILE_OPERATION
    );
    assert_eq!(edit.document_changes[1].edits[0].new_text, "services:\n");
    assert_eq!(
        edit.document_changes[2].kind,
        swlsp_lsp_protocol::DELETE_FILE_OPERATION
    );
    assert_eq!(edit.document_changes[2].uri, "file:///project/services.xml");
    let options = edit.document_changes[2].options.unwrap();
    assert!(!options.recursive);
    assert!(!options.ignore_if_not_exists);
}

#[test]
fn workspace_plan_rejects_duplicate_and_empty_documents() {
    let plan = WorkspacePlan {
        creates: vec![CreateFilePlan {
            uri: "file:///a".into(),
            content: String::new(),
        }],
        deletes: vec![DeleteFilePlan {
            uri: "file:///a".into(),
            ..DeleteFilePlan::default()
        }],
        ..WorkspacePlan::default()
    };
    assert_eq!(
        plan.workspace_edit().unwrap_err(),
        RewriteError::DuplicateDocument("file:///a".into())
    );
    let plan = WorkspacePlan {
        creates: vec![CreateFilePlan::default()],
        ..WorkspacePlan::default()
    };
    assert!(matches!(
        plan.workspace_edit().unwrap_err(),
        RewriteError::EmptyUri { .. }
    ));
}

fn json_document(source: &str) -> swlsp_syntax::Tree {
    LanguageId::Json
        .parse(source)
        .expect("json parser is wired")
        .tree
}

#[test]
fn element_handle_rejects_changed_document_version() {
    let source = r#"{"value": "bad"}"#;
    let tree = json_document(source);
    let element = tree.root().covering_element(range(10, 15));
    let handle = ElementHandle::new("file:///project/test.json", 3, LanguageId::Json, element);
    let error = handle
        .resolve("file:///project/test.json", 4, LanguageId::Json, &tree)
        .unwrap_err();
    assert!(error.is_stale());
    assert!(
        handle
            .resolve("file:///project/test.json", 3, LanguageId::Json, &tree)
            .is_ok()
    );
}

#[test]
fn element_handle_rejects_changed_text() {
    let tree = json_document(r#"{"value": "bad"}"#);
    let handle = ElementHandle::new(
        "file:///t.json",
        1,
        LanguageId::Json,
        tree.root().covering_element(range(10, 15)),
    );
    let changed = json_document(r#"{"value": "bud"}"#);
    assert!(
        handle
            .resolve("file:///t.json", 1, LanguageId::Json, &changed)
            .unwrap_err()
            .is_stale()
    );
}

#[test]
fn element_handle_survives_untyped_json_round_trip() {
    let source = r#"{"value": "transport-hash"}"#;
    let tree = json_document(source);
    let element = tree.root().covering_element(range(10, 26));
    let handle = ElementHandle::new("file:///project/test.json", 3, LanguageId::Json, element);

    let encoded = serde_json::to_string(&handle).unwrap();
    let untyped: serde_json::Value = serde_json::from_str(&encoded).unwrap();
    assert!(untyped["textHash"].is_string());
    let encoded = serde_json::to_string(&untyped).unwrap();
    let decoded: ElementHandle = serde_json::from_str(&encoded).unwrap();
    assert_eq!(handle, decoded);
    decoded
        .resolve("file:///project/test.json", 3, LanguageId::Json, &tree)
        .unwrap();
}

#[test]
fn element_handle_range_uses_go_field_names() {
    let source = r#"{"value": "transport-hash"}"#;
    let tree = json_document(source);
    let element = tree.root().covering_element(range(10, 26));
    let handle = ElementHandle::new("file:///project/test.json", 3, LanguageId::Json, element);

    let mut untyped = serde_json::to_value(&handle).unwrap();
    // Go's cst.TextRange has no JSON tags, so clients see "Start"/"End".
    assert_eq!(
        untyped["range"],
        serde_json::json!({"Start": 10, "End": 26})
    );
    // Go decodes keys case-insensitively; accept the lowercase spelling too.
    untyped["range"] = serde_json::json!({"start": 10, "end": 26});
    let decoded: ElementHandle = serde_json::from_value(untyped).unwrap();
    assert_eq!(handle, decoded);
}

#[test]
fn apply_rejects_ranges_inside_a_character() {
    let source = "a😀b";
    let edits = vec![Edit::new(range(2, 3), "x")];
    assert!(apply(source, &edits).unwrap_err().is_invalid_range());
}

#[test]
fn apply_preserves_unedited_source_and_orders_edits() {
    let source = "one two three";
    let edits = vec![
        Edit::new(range(8, 13), "3"),
        Edit::new(range(0, 3), "1"),
        Edit::new(range(4, 4), "<"),
    ];
    assert_eq!(apply(source, &edits).unwrap(), "1 <two 3");
    assert_eq!(apply(source, &[]).unwrap(), source);
}

/// A plan touching every serialized shape: created files with and without
/// content, a versioned multi-line UTF-16 document edit, a versionless
/// document edit, and a delete.
fn representative_plan() -> WorkspacePlan {
    let source = "a😀b\nline two\n";
    let mut builder = Builder::new(source);
    builder.insert(0, "<").unwrap();
    builder
        .replace_range(range(1, 1 + "😀".len() as u32), "\"x\"")
        .unwrap();
    let second_line = source.find("two").unwrap() as u32;
    builder
        .replace_range(range(second_line, second_line + 3), "")
        .unwrap();
    let versioned = builder.finish().unwrap();

    let mut builder = Builder::new("plain");
    builder.replace_range(range(0, 5), "edited").unwrap();
    let versionless = builder.finish().unwrap();

    WorkspacePlan {
        creates: vec![
            CreateFilePlan {
                uri: "file:///project/New.php".into(),
                content: "<?php\n".into(),
            },
            CreateFilePlan {
                uri: "file:///project/empty.txt".into(),
                content: String::new(),
            },
        ],
        documents: vec![
            DocumentPlan::new("file:///project/a.twig", Some(12), source, versioned),
            DocumentPlan::new("file:///project/b.yaml", None, "plain", versionless),
        ],
        deletes: vec![DeleteFilePlan {
            uri: "file:///project/old.xml".into(),
            version: Some(2),
            source: "<x/>".into(),
        }],
    }
}

/// Golden JSON recorded from the former path (rewrite-private wire types
/// converted by `swlsp_lsp::protocol_workspace_edit`). The JSON sent to
/// clients, the CLI and MCP must stay byte-identical.
const REPRESENTATIVE_PLAN_JSON: &str = concat!(
    r#"{"documentChanges":["#,
    r#"{"kind":"create","uri":"file:///project/New.php","options":{}},"#,
    r#"{"textDocument":{"uri":"file:///project/New.php","version":null},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"<?php\n"}]},"#,
    r#"{"kind":"create","uri":"file:///project/empty.txt","options":{}},"#,
    r#"{"textDocument":{"uri":"file:///project/a.twig","version":12},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"<"},{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"\"x\""},{"range":{"start":{"line":1,"character":5},"end":{"line":1,"character":8}},"newText":""}]},"#,
    r#"{"textDocument":{"uri":"file:///project/b.yaml","version":null},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},"newText":"edited"}]},"#,
    r#"{"kind":"delete","uri":"file:///project/old.xml","options":{}}"#,
    r#"]}"#,
);

#[test]
fn workspace_plan_serializes_byte_identical_protocol_json() {
    let edit = representative_plan().workspace_edit().unwrap();
    assert_eq!(
        serde_json::to_string(&edit).unwrap(),
        REPRESENTATIVE_PLAN_JSON
    );
}
