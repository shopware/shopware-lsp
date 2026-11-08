//! Ports of `inspection_test.go`, `code_action_policy_test.go`,
//! `code_action_legacy_test.go` and `command_edits_test.go`.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig::{DiagnosticsConfig, Partial, Severity};
use swlsp_rewrite::{Builder, CreateFilePlan, DeleteFilePlan, DocumentPlan, Edit, WorkspacePlan};

use super::support::*;
use crate::inspection::{DIAGNOSTIC_ENVELOPE_KEY, decode_diagnostic_envelope};
use crate::*;

fn ctx() -> Context {
    Context::background()
}

fn code_action_params(
    uri: &str,
    diagnostics: Vec<protocol::Diagnostic>,
    only: &[&str],
) -> protocol::CodeActionParams {
    protocol::CodeActionParams {
        text_document: protocol::TextDocumentIdentifier::new(uri),
        range: diagnostics
            .first()
            .map(|diagnostic| diagnostic.range)
            .unwrap_or_default(),
        context: protocol::CodeActionContext {
            diagnostics,
            only: only.iter().map(|value| (*value).to_owned()).collect(),
        },
    }
}

fn open_test_document(server: &Server, uri: &str, version: i32) -> Arc<TextDocument> {
    server.documents.open_document(uri, "value: bad\n", version);
    server.documents.get_document(uri).unwrap()
}

#[test]
fn inspection_binds_and_lazily_resolves_exact_quick_fix() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    enable_resolve_support(&server);
    server.register_inspection(Arc::new(TestInspection));
    let document = open_test_document(&server, &uri, 3);
    let diagnostics = server.collect_diagnostics(&ctx(), &document);
    assert_eq!(diagnostics.len(), 1);
    let data = diagnostics[0].data.as_ref().unwrap().as_object().unwrap();
    assert!(!data.contains_key("replacement"));
    assert!(data.contains_key(DIAGNOSTIC_ENVELOPE_KEY));
    let envelope = decode_diagnostic_envelope(diagnostics[0].data.as_ref()).unwrap();
    assert_eq!(envelope.payload, Some(json!({"replacement": "good"})));
    // The anchor travels with a string hash (JavaScript-safe).
    assert!(data[DIAGNOSTIC_ENVELOPE_KEY]["anchor"]["textHash"].is_string());

    let actions = server.code_action(
        &ctx(),
        &code_action_params(&uri, diagnostics, &["quickfix"]),
    );
    assert_eq!(actions.len(), 1);
    assert!(actions[0].edit.is_none());
    assert!(actions[0].is_preferred);

    let resolved = server.resolve_code_action(&ctx(), actions[0].clone());
    assert!(resolved.disabled.is_none(), "{:?}", resolved.disabled);
    let edit = resolved.edit.unwrap();
    assert_eq!(edit.document_changes.len(), 1);
    assert_eq!(
        edit.document_changes[0]
            .text_document
            .as_ref()
            .unwrap()
            .version,
        Some(3)
    );
    assert_eq!(edit.document_changes[0].edits[0].new_text, "good");
}

#[test]
fn workspace_plan_delete_requires_current_in_workspace_snapshot() {
    let dir = temp_dir();
    let path = dir.path().join("services.xml");
    let uri = file_uri(&path);
    std::fs::write(&path, "<container/>").unwrap();
    let server = Server::new(None, &root_string(&dir), "test");
    server.documents.open_document(&uri, "<container/>", 4);
    let plan = WorkspacePlan {
        deletes: vec![DeleteFilePlan {
            uri: uri.clone(),
            version: Some(4),
            source: "<container/>".into(),
        }],
        ..WorkspacePlan::default()
    };
    server.validate_workspace_plan(&ctx(), &plan).unwrap();

    let mut stale = plan.clone();
    stale.deletes[0].source = "<changed/>".into();
    let error = server.validate_workspace_plan(&ctx(), &stale).unwrap_err();
    assert!(is_stale_error(error.as_ref()), "{error}");

    let outside_dir = temp_dir();
    let outside_path = outside_dir.path().join("services.xml");
    std::fs::write(&outside_path, "<container/>").unwrap();
    let outside = WorkspacePlan {
        deletes: vec![DeleteFilePlan {
            uri: file_uri(&outside_path),
            version: None,
            source: "<container/>".into(),
        }],
        ..WorkspacePlan::default()
    };
    assert!(server.validate_workspace_plan(&ctx(), &outside).is_err());
}

struct DefaultDisabledInspection;

impl Inspection for DefaultDisabledInspection {
    fn definition(&self) -> InspectionDefinition {
        test_definition("test.default-disabled", true)
    }

    fn inspect(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        reporter.report(Problem {
            id: "test.default-disabled".into(),
            range: range(7, 10),
            element: element_at(document, 7, 10),
            message: "Opt-in diagnostic".into(),
            ..Problem::default()
        })
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        Vec::new()
    }
}

#[test]
fn inspection_rule_can_be_disabled_by_default_and_explicitly_enabled() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    server.register_inspection(Arc::new(DefaultDisabledInspection));
    let document = open_test_document(&server, &uri, 1);
    assert!(server.collect_diagnostics(&ctx(), &document).is_empty());

    let result = server.replace_editor_configuration(Partial {
        diagnostics: Some(DiagnosticsConfig {
            rules: BTreeMap::from([("test.default-disabled".to_owned(), Severity::Warning)]),
            ..DiagnosticsConfig::default()
        }),
        ..Partial::default()
    });
    assert!(result.applied, "{result:?}");
    assert_eq!(server.collect_diagnostics(&ctx(), &document).len(), 1);

    let catalog = server.configuration_catalog();
    let inspection = catalog
        .inspections
        .iter()
        .find(|inspection| inspection.id == "test.default-disabled")
        .expect("default-disabled inspection missing from catalog");
    assert_eq!(inspection.rules.len(), 1);
    assert!(!inspection.rules[0].default_enabled);
}

#[test]
fn inspection_resolve_disables_stale_action() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    enable_resolve_support(&server);
    server.register_inspection(Arc::new(TestInspection));
    let document = open_test_document(&server, &uri, 3);
    let diagnostics = server.collect_diagnostics(&ctx(), &document);
    let action = server
        .code_action(&ctx(), &code_action_params(&uri, diagnostics, &[]))
        .remove(0);
    server
        .documents
        .update_document(&uri, "value: changed\n", 4);
    let resolved = server.resolve_code_action(&ctx(), action);
    assert!(
        resolved
            .disabled
            .as_ref()
            .unwrap()
            .reason
            .contains("document changed")
    );
    assert!(resolved.edit.is_none());
}

struct MultipleFixInspection;

impl Inspection for MultipleFixInspection {
    fn definition(&self) -> InspectionDefinition {
        TestInspection.definition()
    }

    fn inspect(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        report_invalid_value(
            document,
            reporter,
            vec![
                bind_fix(TEST_FIX_ID, json!({"replacement": "good"})),
                bind_fix(TEST_FIX_ID, json!({"replacement": "better"})),
            ],
        )
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        TestInspection.quick_fixes()
    }
}

#[test]
fn inspection_preserves_multiple_bindings_for_the_same_quick_fix() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    server.register_inspection(Arc::new(MultipleFixInspection));
    let document = open_test_document(&server, &uri, 3);
    let diagnostics = server.collect_diagnostics(&ctx(), &document);
    assert_eq!(diagnostics.len(), 1);
    let actions = server.code_action(
        &ctx(),
        &code_action_params(&uri, diagnostics, &["quickfix"]),
    );
    assert_eq!(actions.len(), 2);
    assert_eq!(actions[0].title, "Replace with good");
    assert_eq!(actions[1].title, "Replace with better");
    assert_eq!(
        actions[0].edit.as_ref().unwrap().document_changes[0].edits[0].new_text,
        "good"
    );
    assert_eq!(
        actions[1].edit.as_ref().unwrap().document_changes[0].edits[0].new_text,
        "better"
    );
}

#[test]
fn inspection_code_action_honors_context_only() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    server.register_inspection(Arc::new(TestInspection));
    let document = open_test_document(&server, &uri, 3);
    let diagnostics = server.collect_diagnostics(&ctx(), &document);
    assert!(
        server
            .code_action(
                &ctx(),
                &code_action_params(&uri, diagnostics, &["refactor"])
            )
            .is_empty()
    );
}

#[test]
fn inspection_registration_rejects_invalid_definitions() {
    struct Duplicate;
    impl Inspection for Duplicate {
        fn definition(&self) -> InspectionDefinition {
            TestInspection.definition()
        }
        fn inspect(
            &self,
            _: &Context,
            _: &Arc<TextDocument>,
            _: &mut dyn ProblemReporter,
        ) -> Result<(), BoxError> {
            Ok(())
        }
        fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
            Vec::new()
        }
    }
    let server = Server::new(None, "", "test");
    server.register_inspection(Arc::new(TestInspection));
    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        server.register_inspection(Arc::new(Duplicate));
    }));
    assert!(result.is_err());
    // A failed registration leaves the registry unchanged.
    assert_eq!(server.registry().inspections.by_id.len(), 1);
}

#[test]
fn inspection_actions_recheck_current_policy() {
    for (name, config) in [
        (
            "inspection",
            DiagnosticsConfig {
                inspections: BTreeMap::from([("test.invalid-value".to_owned(), false)]),
                ..DiagnosticsConfig::default()
            },
        ),
        (
            "rule",
            DiagnosticsConfig {
                rules: BTreeMap::from([("test.invalid-value".to_owned(), Severity::Off)]),
                ..DiagnosticsConfig::default()
            },
        ),
        (
            "all",
            DiagnosticsConfig {
                enabled: Some(false),
                ..DiagnosticsConfig::default()
            },
        ),
    ] {
        let dir = temp_dir();
        let server = Server::new(None, &root_string(&dir), "test");
        server.register_inspection(Arc::new(TestInspection));
        enable_resolve_support(&server);
        let uri = file_uri(&dir.path().join("test.yaml"));
        let document = open_test_document(&server, &uri, 1);
        let diagnostics = server.collect_diagnostics(&ctx(), &document);
        assert_eq!(diagnostics.len(), 1, "{name}");
        let params = code_action_params(&uri, diagnostics, &[]);
        let mut actions = server.code_action(&ctx(), &params);
        assert_eq!(actions.len(), 1, "{name}");
        let mut action = actions.remove(0);
        action.command = Some(protocol::CommandAction {
            command: "stale.command".into(),
            ..Default::default()
        });
        let response = server.replace_editor_configuration(Partial {
            diagnostics: Some(config),
            ..Partial::default()
        });
        assert!(response.error.is_empty(), "{name}: {}", response.error);
        assert!(
            server.collect_diagnostics(&ctx(), &document).is_empty(),
            "{name}"
        );
        let resolved = server.resolve_code_action(&ctx(), action);
        assert!(resolved.disabled.is_some(), "{name}");
        assert!(
            resolved.edit.is_none() && resolved.command.is_none(),
            "{name}"
        );
        assert!(server.code_action(&ctx(), &params).is_empty(), "{name}");
        server.close_all().unwrap();
    }
}

#[test]
fn workspace_plan_rejects_open_outside_workspace() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let outside = temp_dir();
    let uri = file_uri(&outside.path().join("outside.yaml"));
    server.documents.open_document(&uri, "value: bad\n", 1);
    let mut builder = Builder::new("value: bad\n");
    builder.insert(0, "# changed\n").unwrap();
    let plan = WorkspacePlan {
        documents: vec![DocumentPlan::new(
            uri.clone(),
            Some(1),
            "value: bad\n",
            builder.finish().unwrap(),
        )],
        ..WorkspacePlan::default()
    };
    let error = server.validate_workspace_plan(&ctx(), &plan).unwrap_err();
    assert!(
        error.to_string().contains("outside the workspace"),
        "{error}"
    );
    let error = server.resolve_snapshot(&ctx(), &uri).unwrap_err();
    assert!(
        error.to_string().contains("outside the workspace"),
        "{error}"
    );
    server.close_all().unwrap();
}

struct LegacyTestActionProvider(protocol::CodeAction);

impl ActionProvider for LegacyTestActionProvider {
    fn get_code_actions(
        &self,
        _ctx: &Context,
        _request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        vec![self.0.clone()]
    }

    fn code_action_kinds(&self) -> Vec<protocol::CodeActionKind> {
        vec![protocol::CodeActionKind::QUICKFIX]
    }
}

fn edit_action(uri: &str, edits: Vec<protocol::TextEdit>) -> protocol::CodeAction {
    protocol::CodeAction {
        edit: Some(protocol::WorkspaceEdit {
            changes: BTreeMap::from([(uri.to_owned(), edits)]),
            ..Default::default()
        }),
        ..Default::default()
    }
}

#[test]
fn legacy_actions_are_versioned_and_reject_stale_targets() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    enable_resolve_support(&server);
    let uri = file_uri(&dir.path().join("test.yaml"));
    server.documents.open_document(&uri, "value: bad\n", 7);
    let mut action = edit_action(
        &uri,
        vec![protocol::TextEdit::new(
            protocol_range_of((0, 7), (0, 10)),
            "good",
        )],
    );
    action.title = "Replace".into();
    action.kind = Some(protocol::CodeActionKind::QUICKFIX);
    server.register_action_provider(Arc::new(LegacyTestActionProvider(action)));
    let params = protocol::CodeActionParams {
        text_document: protocol::TextDocumentIdentifier::new(uri.clone()),
        ..Default::default()
    };
    let actions = server.code_action(&ctx(), &params);
    assert_eq!(actions.len(), 1);
    assert!(actions[0].disabled.is_none());
    let edit = actions[0].edit.as_ref().unwrap();
    assert!(edit.changes.is_empty());
    assert_eq!(
        edit.document_changes[0]
            .text_document
            .as_ref()
            .unwrap()
            .version,
        Some(7)
    );
    assert!(
        server
            .resolve_code_action(&ctx(), actions[0].clone())
            .disabled
            .is_none()
    );
    server
        .documents
        .update_document(&uri, "value: different\n", 8);
    let resolved = server.resolve_code_action(&ctx(), actions[0].clone());
    assert!(resolved.disabled.is_some());
    assert!(resolved.edit.is_none());
    server.close_all().unwrap();
}

#[test]
fn legacy_actions_reject_changed_closed_target() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let path = dir.path().join("test.yaml");
    let uri = file_uri(&path);
    std::fs::write(&path, "value: old\n").unwrap();
    let action = server.normalize_provider_action(
        &ctx(),
        edit_action(
            &uri,
            vec![protocol::TextEdit::new(
                protocol::Range::default(),
                "# comment\n",
            )],
        ),
    );
    assert!(action.disabled.is_none(), "{:?}", action.disabled);
    std::fs::write(&path, "value: changed\n").unwrap();
    let resolved = server.resolve_code_action(&ctx(), action);
    assert!(resolved.disabled.is_some());
    assert!(resolved.edit.is_none());
    server.close_all().unwrap();
}

#[test]
fn legacy_actions_validate_ranges_and_containment() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let uri = file_uri(&dir.path().join("test.yaml"));
    let outside_dir = temp_dir();
    let outside = file_uri(&outside_dir.path().join("outside.yaml"));
    server.documents.open_document(&uri, "value: 😀\n", 1);
    server.documents.open_document(&outside, "value: 😀\n", 1);
    for (name, action) in [
        (
            "outside",
            edit_action(
                &outside,
                vec![protocol::TextEdit::new(
                    protocol::Range::default(),
                    "# outside\n",
                )],
            ),
        ),
        (
            "surrogate",
            edit_action(
                &uri,
                vec![protocol::TextEdit::new(
                    protocol_range_of((0, 8), (0, 8)),
                    "x",
                )],
            ),
        ),
        (
            "overlap",
            edit_action(
                &uri,
                vec![
                    protocol::TextEdit::new(protocol_range_of((0, 0), (0, 5)), "x"),
                    protocol::TextEdit::new(protocol_range_of((0, 0), (0, 4)), "y"),
                ],
            ),
        ),
    ] {
        let result = server.normalize_provider_action(&ctx(), action);
        assert!(result.disabled.is_some(), "{name}");
        assert!(result.edit.is_none(), "{name}");
    }
    server.close_all().unwrap();
}

#[test]
fn command_workspace_edit_rejects_changed_open_target() {
    let dir = temp_dir();
    let path = dir.path().join("snippet.json");
    let uri = file_uri(&path);
    std::fs::write(&path, "{}").unwrap();
    let server = Server::new(None, &root_string(&dir), "test");
    server.documents.open_document(&uri, r#"{"open":true}"#, 5);
    let snapshot = server.resolve_document(&ctx(), &uri).unwrap();
    assert_eq!(snapshot.document.source(), r#"{"open":true}"#);
    let plan = WorkspacePlan {
        documents: vec![DocumentPlan::new(
            uri.clone(),
            snapshot.version,
            snapshot.document.source_arc().clone(),
            vec![Edit::new(range(0, 13), r#"{"open":false}"#)],
        )],
        ..WorkspacePlan::default()
    };
    server.workspace_edit(&ctx(), &plan).unwrap();
    server
        .documents
        .update_document(&uri, r#"{"open":true,"new":1}"#, 6);
    let error = server.workspace_edit(&ctx(), &plan).unwrap_err();
    assert!(is_stale_error(error.as_ref()), "{error}");
    server.close_all().unwrap();
}

#[test]
fn command_create_rejects_unsaved_open_target() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("new.json"));
    let server = Server::new(None, &root_string(&dir), "test");
    let plan = WorkspacePlan {
        creates: vec![CreateFilePlan {
            uri: uri.clone(),
            content: "{}".into(),
        }],
        ..WorkspacePlan::default()
    };
    server
        .documents
        .open_document(&uri, r#"{"unsaved":true}"#, 1);
    let error = server.workspace_edit(&ctx(), &plan).unwrap_err();
    assert!(error.to_string().contains("already open"), "{error}");
    server.close_all().unwrap();
}

#[test]
fn optional_documents_become_create_targets() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let missing = file_uri(&dir.path().join("missing.json"));
    let existing_path = dir.path().join("existing.json");
    std::fs::write(&existing_path, "{}").unwrap();
    let existing = file_uri(&existing_path);
    assert!(
        resolve_optional_document(&ctx(), &server, &missing)
            .unwrap()
            .is_none()
    );
    let snapshot = resolve_optional_document(&ctx(), &server, &existing)
        .unwrap()
        .unwrap();
    assert_eq!(snapshot.version, None);
    let mut plan = WorkspacePlan::default();
    add_document_replacement(&mut plan, &missing, None, "{\"a\":1}").unwrap();
    add_document_replacement(&mut plan, &existing, Some(&snapshot), "{\"b\":2}").unwrap();
    let edit = server.workspace_edit(&ctx(), &plan).unwrap();
    let encoded = serde_json::to_value(&edit).unwrap();
    assert_eq!(encoded["documentChanges"][0]["kind"], "create");
    assert_eq!(
        encoded["documentChanges"][1]["edits"][0]["newText"],
        "{\"a\":1}"
    );
    assert_eq!(
        encoded["documentChanges"][2]["textDocument"]["version"],
        serde_json::Value::Null
    );
    server.close_all().unwrap();
}
