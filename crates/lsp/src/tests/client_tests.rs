//! Port of `client_command_filter_test.go`.

use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp_protocol as protocol;

use super::support::*;
use crate::*;

fn command(id: &str) -> Option<protocol::CommandAction> {
    Some(protocol::CommandAction {
        title: id.into(),
        command: id.into(),
        ..Default::default()
    })
}

#[test]
fn client_command_filtering_keeps_only_usable_presentations() {
    let server = Server::new(None, "", "test");
    server
        .configure_client_integration(Some(&protocol::ShopwareClientOptions {
            protocol_version: CLIENT_PROTOCOL_VERSION,
            supported_commands: vec!["shopware.supported".into()],
            ..Default::default()
        }))
        .unwrap();
    let action =
        |title: &str, command: Option<protocol::CommandAction>, edit: bool| protocol::CodeAction {
            title: title.into(),
            command,
            edit: edit.then(protocol::WorkspaceEdit::default),
            ..Default::default()
        };
    let actions = server.filter_code_actions_for_client(vec![
        action("supported", command("shopware.supported"), false),
        action("unsupported", command("shopware.unsupported"), false),
        action("edit", command("shopware.unsupported"), true),
        action("passive", None, false),
    ]);
    assert_eq!(
        actions
            .iter()
            .map(|action| action.title.as_str())
            .collect::<Vec<_>>(),
        ["supported", "edit", "passive"]
    );
    assert!(actions[1].command.is_none());

    let lens = |command: Option<protocol::CommandAction>| protocol::CodeLens {
        command,
        ..Default::default()
    };
    let lenses = server.filter_code_lenses_for_client(vec![
        lens(command("shopware.supported")),
        lens(command("shopware.unsupported")),
        lens(None),
    ]);
    assert_eq!(lenses.len(), 2);

    let mut items = vec![protocol::CompletionItem {
        command: Some(json!({"title": "x", "command": "shopware.unsupported"})),
        ..Default::default()
    }];
    server.filter_completion_commands_for_client(&mut items);
    assert!(items[0].command.is_none());

    let mut hints = vec![protocol::InlayHint {
        label: json!([{"value": "variables", "command": {"title": "x", "command": "shopware.unsupported"}}]),
        ..Default::default()
    }];
    server.filter_inlay_hint_commands_for_client(&mut hints);
    assert!(hints[0].label[0].get("command").is_none());
    assert_eq!(hints[0].label[0]["value"], "variables");

    let resolved = server.filter_resolved_code_action_for_client(action(
        "x",
        command("shopware.unsupported"),
        false,
    ));
    assert!(resolved.command.is_none());
    assert!(resolved.disabled.is_some());
}

#[test]
fn legacy_client_commands_remain_unfiltered() {
    let server = Server::new(None, "", "test");
    let actions = server.filter_code_actions_for_client(vec![protocol::CodeAction {
        title: "legacy".into(),
        command: command("shopware.anything"),
        ..Default::default()
    }]);
    assert_eq!(actions.len(), 1);
}

struct ProfileSuppressedInspection;

impl Inspection for ProfileSuppressedInspection {
    fn definition(&self) -> InspectionDefinition {
        InspectionDefinition {
            id: "test.language".into(),
            description: "Test inspection.",
            domain: "",
            scope: InspectionScope::Language,
            languages: vec![LanguageId::Yaml],
            problems: vec![ProblemDefinition {
                id: "php.parse".into(),
                source: "test".into(),
                default_severity: protocol::DiagnosticSeverity::ERROR,
                disabled_by_default: false,
            }],
        }
    }

    fn inspect(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        reporter.report(Problem {
            id: "php.parse".into(),
            message: "test".into(),
            range: range(0, 5),
            element: element_at(document, 0, 5),
            ..Problem::default()
        })
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        Vec::new()
    }
}

#[test]
fn framework_profile_keeps_suppressed_inspection_registered() {
    let server = Server::new(None, "", "test");
    server
        .configure_client_integration(Some(&protocol::ShopwareClientOptions {
            protocol_version: CLIENT_PROTOCOL_VERSION,
            presentation_profile: "framework".into(),
            ..Default::default()
        }))
        .unwrap();
    server.register_inspection(Arc::new(ProfileSuppressedInspection));
    assert!(
        server
            .registry()
            .inspections
            .inspection("test.language")
            .is_some()
    );
    let document = Arc::new(TextDocument::new("file:///fixture.yaml", "value: bad\n", 1));
    assert!(
        server
            .diagnostics_for_document(&Context::background(), &document)
            .is_empty()
    );

    let full = Server::new(None, "", "test");
    full.register_inspection(Arc::new(ProfileSuppressedInspection));
    assert_eq!(
        full.diagnostics_for_document(&Context::background(), &document)
            .len(),
        1
    );
}
