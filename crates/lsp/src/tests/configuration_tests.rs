//! Port of `configuration_test.go`.

use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig::{DiagnosticOverride, DiagnosticsConfig, Partial, PhpConfig, Severity};

use super::support::*;
use crate::configuration::editor_configuration;
use crate::*;

fn ctx() -> Context {
    Context::background()
}

fn initialize_with(
    server: &Server,
    root: &Path,
    configure: impl FnOnce(&mut protocol::InitializeParams),
) -> Result<serde_json::Value, String> {
    let mut params = initialize_params(root);
    configure(&mut params);
    server.initialize(&ctx(), &params)
}

fn diagnostics_for(server: &Server, uri: &str) -> Vec<protocol::Diagnostic> {
    server.documents.open_document(uri, "value: bad\n", 1);
    server
        .diagnostic(
            &ctx(),
            &protocol::DiagnosticParams {
                text_document: protocol::TextDocumentIdentifier::new(uri),
                ..Default::default()
            },
        )
        .items
}

fn diagnostics_for_path(server: &Server, path: &Path) -> Vec<protocol::Diagnostic> {
    diagnostics_for(server, &file_uri(path))
}

#[test]
fn configuration_layers_project_and_editor_values() {
    let dir = temp_dir();
    write_project_configuration(
        dir.path(),
        r#"{"version": 1, "features": {"hover": false}, "php": {"extensions": ["redis"]}}"#,
    );
    let server = Server::new(None, "", "test");
    initialize_with(&server, dir.path(), |params| {
        params.initialization_options.configuration = Some(Partial {
            features: BTreeMap::from([("hover".to_owned(), true)]),
            php: Some(PhpConfig {
                extensions: Some(Vec::new()),
                ..PhpConfig::default()
            }),
            ..Partial::default()
        });
    })
    .unwrap();
    let effective = server.effective_configuration();
    assert!(effective.features["hover"]);
    assert!(effective.php.extensions.as_ref().is_some_and(Vec::is_empty));
    assert_eq!(effective.origins["features.hover"], "editor");
    server.close_all().unwrap();
}

#[test]
fn diagnostic_configuration_disables_and_overrides_rules() {
    for (name, diagnostics, count, severity) in [
        (
            "global",
            DiagnosticsConfig {
                enabled: Some(false),
                ..DiagnosticsConfig::default()
            },
            0,
            None,
        ),
        (
            "inspection",
            DiagnosticsConfig {
                inspections: BTreeMap::from([("test.invalid-value".to_owned(), false)]),
                ..DiagnosticsConfig::default()
            },
            0,
            None,
        ),
        (
            "rule",
            DiagnosticsConfig {
                rules: BTreeMap::from([("test.invalid-value".to_owned(), Severity::Off)]),
                ..DiagnosticsConfig::default()
            },
            0,
            None,
        ),
        (
            "severity",
            DiagnosticsConfig {
                rules: BTreeMap::from([("test.invalid-value".to_owned(), Severity::Error)]),
                ..DiagnosticsConfig::default()
            },
            1,
            Some(protocol::DiagnosticSeverity::ERROR),
        ),
    ] {
        let dir = temp_dir();
        let server = Server::new(None, "", "test");
        server.register_inspection(Arc::new(TestInspection));
        initialize_with(&server, dir.path(), |params| {
            params.initialization_options.configuration = Some(Partial {
                diagnostics: Some(diagnostics),
                ..Partial::default()
            });
        })
        .unwrap();
        let items = diagnostics_for(&server, "file:///project/test.yaml");
        assert_eq!(items.len(), count, "{name}");
        if count > 0 {
            assert_eq!(items[0].severity, severity, "{name}");
        }
        server.close_all().unwrap();
    }
}

/// [`TestInspection`] in the configuration domain `scss`.
struct DomainInspection;

impl Inspection for DomainInspection {
    fn definition(&self) -> InspectionDefinition {
        InspectionDefinition {
            domain: "scss",
            ..TestInspection.definition()
        }
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        TestInspection.inspect(ctx, document, reporter)
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        TestInspection.quick_fixes()
    }
}

#[test]
fn inspections_run_only_while_their_declared_domain_is_enabled() {
    for (enabled, count) in [(true, 1), (false, 0)] {
        let dir = temp_dir();
        let server = Server::new(None, "", "test");
        server.register_inspection(Arc::new(DomainInspection));
        initialize_with(&server, dir.path(), |params| {
            params.initialization_options.configuration = Some(Partial {
                domains: BTreeMap::from([("scss".to_owned(), enabled)]),
                ..Partial::default()
            });
        })
        .unwrap();
        let items = diagnostics_for(&server, "file:///project/test.yaml");
        assert_eq!(items.len(), count, "scss enabled: {enabled}");
        server.close_all().unwrap();
    }
}

#[test]
fn invalid_project_configuration_is_strict_for_cli_and_safe_for_editor() {
    let dir = temp_dir();
    write_project_configuration(dir.path(), r#"{"version":1,"features":{"unknown":false}}"#);
    let cli = Server::new(None, "", "test");
    let error = initialize_with(&cli, dir.path(), |params| {
        params.initialization_options.cli_mode = true
    })
    .unwrap_err();
    assert!(error.contains("unknown feature"), "{error}");
    cli.close_all().unwrap();

    let editor = Server::new(None, "", "test");
    initialize_with(&editor, dir.path(), |_| {}).unwrap();
    assert!(!editor.configuration_catalog().error.is_empty());
    assert!(editor.effective_configuration().features["hover"]);
    editor.close_all().unwrap();
}

#[test]
fn reload_applies_live_settings_and_requests_restart_for_structural_settings() {
    let dir = temp_dir();
    let server = Server::new(None, "", "test");
    initialize_with(&server, dir.path(), |_| {}).unwrap();

    write_project_configuration(dir.path(), r#"{"version":1,"features":{"hover":false}}"#);
    let result = server.reload_project_configuration();
    assert!(result.applied);
    assert!(!result.restart_required);
    assert!(!server.effective_configuration().features["hover"]);

    write_project_configuration(dir.path(), r#"{"version":1,"domains":{"scss":false}}"#);
    let result = server.reload_project_configuration();
    assert!(result.applied);
    assert!(result.restart_required);
    // The running workspace keeps its structural configuration until restart.
    assert!(server.effective_configuration().domains["scss"]);
    server.close_all().unwrap();
}

#[test]
fn nested_configuration_applies_only_inside_its_extension_scope() {
    let dir = temp_dir();
    write_project_configuration(
        dir.path(),
        r#"{"version":1,"diagnostics":{"rules":{"test.invalid-value":"error"}}}"#,
    );
    let plugin = dir.path().join("custom/plugins/Example");
    write_project_configuration(
        &plugin,
        r#"{
            "version":1,
            "diagnostics":{
                "rules":{"test.invalid-value":"off"},
                "overrides":[{"files":["src/Keep.yaml"],"rules":{"test.invalid-value":"warning"}}]
            }
        }"#,
    );
    let server = Server::new(None, "", "test");
    server.register_inspection(Arc::new(TestInspection));
    initialize_with(&server, dir.path(), |_| {}).unwrap();
    assert!(diagnostics_for_path(&server, &plugin.join("src/Ignored.yaml")).is_empty());
    let kept = diagnostics_for_path(&server, &plugin.join("src/Keep.yaml"));
    assert_eq!(kept.len(), 1);
    assert_eq!(
        kept[0].severity,
        Some(protocol::DiagnosticSeverity::WARNING)
    );
    let outside = diagnostics_for_path(&server, &dir.path().join("src/Outside.yaml"));
    assert_eq!(outside.len(), 1);
    assert_eq!(
        outside[0].severity,
        Some(protocol::DiagnosticSeverity::ERROR)
    );
    server.close_all().unwrap();
}

#[test]
fn editor_path_override_wins_nested_extension_configuration() {
    let dir = temp_dir();
    let plugin = dir.path().join("custom/plugins/Example");
    write_project_configuration(
        &plugin,
        r#"{"version":1,"diagnostics":{"rules":{"test.invalid-value":"off"}}}"#,
    );
    let server = Server::new(None, "", "test");
    server.register_inspection(Arc::new(TestInspection));
    initialize_with(&server, dir.path(), |params| {
        params.initialization_options.configuration = Some(Partial {
            diagnostics: Some(DiagnosticsConfig {
                overrides: vec![DiagnosticOverride {
                    files: vec!["custom/plugins/Example/src/Keep.yaml".into()],
                    rules: BTreeMap::from([("test.invalid-value".to_owned(), Severity::Warning)]),
                    ..DiagnosticOverride::default()
                }],
                ..DiagnosticsConfig::default()
            }),
            ..Partial::default()
        });
    })
    .unwrap();
    let diagnostics = diagnostics_for_path(&server, &plugin.join("src/Keep.yaml"));
    assert_eq!(diagnostics.len(), 1);
    assert_eq!(
        diagnostics[0].severity,
        Some(protocol::DiagnosticSeverity::WARNING)
    );
    server.close_all().unwrap();
}

#[test]
fn invalid_nested_reload_keeps_last_known_good_diagnostics() {
    let dir = temp_dir();
    let plugin = dir.path().join("custom/plugins/Example");
    write_project_configuration(
        &plugin,
        r#"{"version":1,"diagnostics":{"rules":{"test.invalid-value":"off"}}}"#,
    );
    let server = Server::new(None, "", "test");
    server.register_inspection(Arc::new(TestInspection));
    initialize_with(&server, dir.path(), |_| {}).unwrap();
    let path = plugin.join("src/Ignored.yaml");
    assert!(diagnostics_for_path(&server, &path).is_empty());

    write_project_configuration(&plugin, r#"{"version":1,"domains":{"php":false}}"#);
    let result = server.reload_project_configuration();
    assert!(result.applied);
    assert!(
        result
            .error
            .contains("nested configuration may only contain diagnostics"),
        "{}",
        result.error
    );
    assert!(diagnostics_for_path(&server, &path).is_empty());
    server.close_all().unwrap();
}

#[test]
fn unknown_diagnostic_ids_fail_cli_and_are_reported_in_editor() {
    let dir = temp_dir();
    write_project_configuration(
        dir.path(),
        r#"{"version":1,"diagnostics":{"rules":{"test.missing":"error"},"inspections":{"test.unknown":false}}}"#,
    );
    let cli = Server::new(None, "", "test");
    cli.register_inspection(Arc::new(TestInspection));
    let error = initialize_with(&cli, dir.path(), |params| {
        params.initialization_options.cli_mode = true
    })
    .unwrap_err();
    assert!(error.contains("validate configuration"), "{error}");
    assert!(
        error.contains(r#"project configuration has unknown inspection "test.unknown""#),
        "{error}"
    );
    assert!(
        error.contains(r#"project configuration has unknown diagnostic rule "test.missing""#),
        "{error}"
    );
    cli.close_all().unwrap();

    let editor = Server::new(None, "", "test");
    editor.register_inspection(Arc::new(TestInspection));
    initialize_with(&editor, dir.path(), |_| {}).unwrap();
    let catalog = editor.configuration_catalog();
    assert!(
        catalog.error.contains("unknown inspection"),
        "{}",
        catalog.error
    );
    assert!(
        catalog
            .errors
            .iter()
            .any(|issue| issue.message.contains("unknown diagnostic rule"))
    );
    editor.close_all().unwrap();
}

#[test]
fn editor_configuration_maps_legacy_options() {
    let options = protocol::InitializationOptions {
        php_extensions: vec!["redis".into()],
        shopware_target_version: "6.7.0.0".into(),
        ..Default::default()
    };
    let editor = editor_configuration(&options);
    assert_eq!(
        editor.php.unwrap().extensions,
        Some(vec!["redis".to_owned()])
    );
    assert_eq!(
        editor.shopware.unwrap().target_version.as_deref(),
        Some("6.7.0.0")
    );
    assert_eq!(
        editor_configuration(&protocol::InitializationOptions::default()),
        Partial::default()
    );
}

#[test]
fn configuration_catalog_serializes_like_go() {
    let server = Server::new(None, "/workspace", "test");
    let encoded = serde_json::to_value(server.configuration_catalog()).unwrap();
    assert_eq!(encoded["inspections"], serde_json::Value::Null);
    assert!(encoded.get("scopes").is_none());
    assert!(encoded.get("error").is_none());
    assert!(encoded["features"].is_array());
    assert!(encoded["mcpTools"].is_array());
    server.register_inspection(Arc::new(TestInspection));
    let encoded = serde_json::to_value(server.configuration_catalog()).unwrap();
    assert_eq!(
        encoded["inspections"],
        serde_json::json!([{
            "id": "test.invalid-value",
            "description": "Test inspection.",
            "languages": ["yaml"],
            "rules": [{"id": "test.invalid-value", "source": "test", "defaultSeverity": "warning", "defaultEnabled": true}],
        }])
    );
}
