//! Ports of `cli_test.go`.

use serde_json::Value;
use swlsp_lsp_protocol::{
    CREATE_FILE_OPERATION, DELETE_FILE_OPERATION, DocumentChange, Position, Range, TextEdit,
    WorkspaceEdit,
};

use super::common::{
    ENV_LOCK, assert_err_contains, path, run, run_ok, run_unlocked, run_with, temp_dir,
    test_options, write,
};
use crate::{
    Cancel, EditMode, Options, apply_protocol_text_edits, apply_workspace_edit, commands,
    parse_position_target, resolve_check_files_with_exclusions,
};

/// Go `t.Setenv` for the duration of `body`, exclusive against every other
/// run in this process.
fn with_env<T>(name: &str, value: &str, body: impl FnOnce() -> T) -> T {
    let _guard = ENV_LOCK.write().unwrap_or_else(|p| p.into_inner());
    let previous = std::env::var_os(name);
    // SAFETY: all other test runs are excluded by the write lock.
    unsafe { std::env::set_var(name, value) };
    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(body));
    unsafe {
        match previous {
            Some(previous) => std::env::set_var(name, previous),
            None => std::env::remove_var(name),
        }
    }
    match result {
        Ok(value) => value,
        Err(panic) => std::panic::resume_unwind(panic),
    }
}

#[test]
fn core_commands_do_not_start_a_workspace() {
    let options = Options {
        version: "1.2.3".into(),
        license: "project license".into(),
        third_party_notices: "dependency notice".into(),
    };
    for (args, contains) in [
        (vec!["help"], "workspace-symbol"),
        (vec!["-h"], "Usage:"),
        (vec!["version"], "shopware-lsp 1.2.3"),
        (vec!["--stdio", "version"], "shopware-lsp 1.2.3"),
        (vec!["licenses"], "dependency notice"),
    ] {
        let run = run_with(options.clone(), &Cancel::new(), &args);
        assert!(run.result.is_ok(), "{args:?}: {:?}", run.result);
        assert!(run.out.contains(contains), "{args:?}: {}", run.out);
    }
}

#[test]
fn default_command_starts_stdio_server() {
    // The log sink is process-wide: keep other runs out while serving.
    let _guard = ENV_LOCK.write().unwrap_or_else(|p| p.into_inner());
    let run = run_unlocked(test_options(), &Cancel::new(), &[]);
    assert!(run.result.is_ok(), "{:?}", run.result);
    assert!(
        run.err.contains("Shopware LSP version: test"),
        "{}",
        run.err
    );
}

#[test]
fn explicit_serve_accepts_editor_transport_flags() {
    let run = run(&["serve", "--stdio", "--clientProcessId", "42"]);
    assert!(run.result.is_ok(), "{:?}", run.result);
    assert!(
        !run.err.contains("flag provided but not defined"),
        "{}",
        run.err
    );
}

#[test]
fn remote_serve_stops_while_waiting_for_a_client() {
    let cancel = Cancel::new();
    cancel.cancel();
    let run = run_with(
        test_options(),
        &cancel,
        &["serve", "--listen", "127.0.0.1:0"],
    );
    let error = run.result.expect_err("cancelled serve must fail");
    assert!(error.canceled, "{error:?}");
}

#[test]
fn profile_write_failure_is_returned() {
    let directory = temp_dir();
    let run = run(&[
        "--profile-mem",
        directory.path().to_str().unwrap(),
        "version",
    ]);
    assert_err_contains(&run, "write profile");
}

#[test]
fn api_json_describes_commands_and_languages() {
    let output = run_ok(&["--json", "api-json"]);
    let result: Value = serde_json::from_str(&output).unwrap();
    let names: Vec<&str> = result["commands"]
        .as_array()
        .unwrap()
        .iter()
        .map(|command| command["name"].as_str().unwrap())
        .collect();
    assert!(names.len() >= 30);
    assert!(names.contains(&"mcp"));
    assert!(names.contains(&"project-info"));
    assert!(names.contains(&"help"));
    let php: Vec<&str> = result["languages"]["php"]
        .as_array()
        .unwrap()
        .iter()
        .map(|extension| extension.as_str().unwrap())
        .collect();
    assert!(php.contains(&".php"));
    assert_eq!(commands().len(), names.len());
}

fn directory_is_empty(path: &std::path::Path) -> bool {
    std::fs::read_dir(path)
        .map(|mut entries| entries.next().is_none())
        .unwrap_or(true)
}

#[test]
fn project_info_reports_unknown_without_creating_workspace_cache() {
    let root = temp_dir();
    let cache_root = temp_dir();
    write(&path(root.path(), "Example.php"), "<?php\n");
    let root_path = root.path().to_str().unwrap().to_owned();
    let run = with_env(
        swlsp_app::CACHE_DIR_ENV,
        cache_root.path().to_str().unwrap(),
        || {
            run_unlocked(
                test_options(),
                &Cancel::new(),
                &["--root", &root_path, "--json", "project-info"],
            )
        },
    );
    assert!(run.result.is_ok(), "{}", run.err);
    let result: Value = serde_json::from_str(&run.out).unwrap();
    assert_eq!(result["supported"], false);
    assert_eq!(result["kind"], "unknown");
    assert!(directory_is_empty(cache_root.path()));
}

#[test]
fn workspace_command_rejects_unknown_project_unless_explicitly_allowed() {
    let root = temp_dir();
    let cache_root = temp_dir();
    let file = path(root.path(), "Example.php");
    write(&file, "<?php\n");
    let root_path = root.path().to_str().unwrap().to_owned();
    let (rejected, allowed) = with_env(
        swlsp_app::CACHE_DIR_ENV,
        cache_root.path().to_str().unwrap(),
        || {
            let rejected = run_unlocked(
                test_options(),
                &Cancel::new(),
                &["--root", &root_path, "--json", "check", &file],
            );
            let empty = directory_is_empty(cache_root.path());
            let allowed = run_unlocked(
                test_options(),
                &Cancel::new(),
                &[
                    "--root",
                    &root_path,
                    "--json",
                    "--allow-unsupported-project",
                    "check",
                    &file,
                ],
            );
            ((rejected, empty), allowed)
        },
    );
    assert_err_contains(&rejected.0, "unsupported project root");
    assert!(rejected.1, "cache must not be created for a rejected root");
    assert!(
        allowed.result.is_ok(),
        "{:?} {}",
        allowed.result,
        allowed.err
    );
}

#[test]
fn parse_position_target_uses_one_based_cli_positions() {
    let target = parse_position_target("/project/Test.php:12:7").unwrap();
    assert_eq!(target.path, "/project/Test.php");
    assert_eq!(
        target.position,
        Position {
            line: 11,
            character: 6
        }
    );
    let target = parse_position_target("/project/Test.php").unwrap();
    assert_eq!(target.position, Position::default());
}

#[test]
fn apply_protocol_text_edits_uses_utf16_positions() {
    let result = apply_protocol_text_edits(
        "a😀b\n",
        &[TextEdit::new(
            Range {
                start: Position {
                    line: 0,
                    character: 1,
                },
                end: Position {
                    line: 0,
                    character: 3,
                },
            },
            "X",
        )],
    )
    .unwrap();
    assert_eq!(result, "aXb\n");
}

#[test]
fn apply_workspace_edit_previews_and_writes_changes() {
    let root = temp_dir();
    let file = path(root.path(), "test.php");
    write(&file, "old\n");
    let mut edit = WorkspaceEdit::default();
    edit.changes.insert(
        swlsp_uriutil::file_uri(&file),
        vec![TextEdit::new(
            Range {
                start: Position::default(),
                end: Position {
                    line: 0,
                    character: 3,
                },
            },
            "new",
        )],
    );
    let mut preview = Vec::new();
    apply_workspace_edit(
        &mut preview,
        Some(&edit),
        EditMode {
            write: false,
            diff: true,
        },
    )
    .unwrap();
    let preview = String::from_utf8(preview).unwrap();
    assert!(preview.contains("-old"));
    assert!(preview.contains("+new"));
    assert_eq!(std::fs::read_to_string(&file).unwrap(), "old\n");

    apply_workspace_edit(
        &mut Vec::new(),
        Some(&edit),
        EditMode {
            write: true,
            diff: false,
        },
    )
    .unwrap();
    assert_eq!(std::fs::read_to_string(&file).unwrap(), "new\n");
}

#[test]
fn apply_workspace_edit_rejects_create_over_existing_empty_file() {
    let root = temp_dir();
    let file = path(root.path(), "existing.php");
    write(&file, "");
    let edit = WorkspaceEdit {
        document_changes: vec![DocumentChange {
            kind: CREATE_FILE_OPERATION.into(),
            uri: swlsp_uriutil::file_uri(&file),
            ..DocumentChange::default()
        }],
        ..WorkspaceEdit::default()
    };
    let error = apply_workspace_edit(
        &mut Vec::new(),
        Some(&edit),
        EditMode {
            write: true,
            diff: false,
        },
    )
    .unwrap_err();
    assert!(
        error.message.contains("create target already exists"),
        "{error}"
    );
}

#[test]
fn apply_workspace_edit_previews_and_applies_delete_file() {
    let root = temp_dir();
    let file = path(root.path(), "obsolete.php");
    write(&file, "obsolete\n");
    let edit = WorkspaceEdit {
        document_changes: vec![DocumentChange {
            kind: DELETE_FILE_OPERATION.into(),
            uri: swlsp_uriutil::file_uri(&file),
            ..DocumentChange::default()
        }],
        ..WorkspaceEdit::default()
    };
    let mut preview = Vec::new();
    apply_workspace_edit(
        &mut preview,
        Some(&edit),
        EditMode {
            write: false,
            diff: true,
        },
    )
    .unwrap();
    assert!(
        String::from_utf8(preview)
            .unwrap()
            .contains("+++ /dev/null")
    );
    assert!(std::path::Path::new(&file).exists());
    apply_workspace_edit(
        &mut Vec::new(),
        Some(&edit),
        EditMode {
            write: true,
            diff: false,
        },
    )
    .unwrap();
    assert!(!std::path::Path::new(&file).exists());
}

#[test]
fn resolve_check_files_expands_directories_and_deduplicates_targets() {
    let root = temp_dir();
    let source_php = path(root.path(), "src/Example.php");
    let source_yaml = path(root.path(), "config/services.yaml");
    let test_php = path(root.path(), "tests/ExampleTest.php");
    let skipped_php = path(root.path(), "node_modules/package/Generated.php");
    let temporary_js = path(
        root.path(),
        "Resources/app/administration/.tmp/vite/generated.js",
    );
    let unsupported = path(root.path(), "README.md");
    for file in [
        &source_php,
        &source_yaml,
        &test_php,
        &skipped_php,
        &temporary_js,
        &unsupported,
    ] {
        write(file, "fixture");
    }
    let files = resolve_check_files_with_exclusions(
        &Cancel::new(),
        "",
        &[
            root.path().to_str().unwrap().to_owned(),
            source_php.clone(),
            path(root.path(), "tests"),
        ],
        &[],
    )
    .unwrap();
    assert_eq!(files, vec![source_yaml, source_php, test_php]);

    let error = resolve_check_files_with_exclusions(
        &Cancel::new(),
        "",
        &[path(root.path(), "missing")],
        &[],
    )
    .unwrap_err();
    assert!(error.message.contains("inspect check target"), "{error}");
}

#[test]
fn resolve_check_files_honors_configured_exclusions_but_keeps_explicit_files() {
    let root = temp_dir();
    let regular = path(root.path(), "src/Service.php");
    let generated = path(root.path(), "src/generated/Model.php");
    write(&regular, "<?php");
    write(&generated, "<?php");
    let root_path = root.path().to_str().unwrap();
    let exclude = vec!["**/generated/**".to_owned()];
    let files = resolve_check_files_with_exclusions(
        &Cancel::new(),
        root_path,
        &[root_path.to_owned()],
        &exclude,
    )
    .unwrap();
    assert_eq!(files, vec![regular]);
    let files = resolve_check_files_with_exclusions(
        &Cancel::new(),
        root_path,
        std::slice::from_ref(&generated),
        &exclude,
    )
    .unwrap();
    assert_eq!(files, vec![generated]);
}

#[test]
fn check_empty_directory_returns_an_empty_json_array() {
    let root = temp_dir();
    write(&path(root.path(), "README.md"), "unsupported");
    let output = run_ok(&[
        "--root",
        root.path().to_str().unwrap(),
        "--allow-unsupported-project",
        "--json",
        "check",
        root.path().to_str().unwrap(),
    ]);
    let value: Value = serde_json::from_str(&output).unwrap();
    assert_eq!(value, serde_json::json!([]));
}

#[test]
fn check_rejects_invalid_worker_count() {
    let root = temp_dir();
    let run = run(&["check", "--workers", "0", root.path().to_str().unwrap()]);
    assert_err_contains(&run, "workers must be at least 1");
}

#[test]
fn config_command_prints_effective_project_configuration() {
    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        r#"{
        "version": 1,
        "features": {"hover": false},
        "check": {"severity": "information", "failOn": "error"}
    }"#,
    );
    let output = run_ok(&["--root", root.path().to_str().unwrap(), "--json", "config"]);
    let result: Value = serde_json::from_str(&output).unwrap();
    assert_eq!(result["effective"]["features"]["hover"], false);
    assert_eq!(result["effective"]["check"]["severity"], "information");
    assert_eq!(result["effective"]["check"]["failOn"], "error");
}

#[test]
fn check_uses_project_failure_policy_and_explicit_flag_wins() {
    let root = temp_dir();
    let php = path(root.path(), "Broken.php");
    write(&php, "<?php function (\n");
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        r#"{
        "version": 1,
        "check": {"severity": "hint", "failOn": "error"}
    }"#,
    );
    let root_path = root.path().to_str().unwrap();
    assert_err_contains(&run(&["--root", root_path, "check", &php]), "fail-on error");
    let off = run(&["--root", root_path, "check", "--fail-on", "off", &php]);
    assert!(off.result.is_ok(), "{:?} {}", off.result, off.err);
}

#[test]
fn cli_rejects_invalid_project_configuration_before_checking() {
    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        r#"{"version":1,"diagnostics":{"rules":{"does.not.exist":"off"}}}"#,
    );
    let root_path = root.path().to_str().unwrap();
    assert_err_contains(
        &run(&["--root", root_path, "check", root_path]),
        "unknown diagnostic rule",
    );
}

#[test]
fn cli_rejects_invalid_nested_extension_configuration() {
    let root = temp_dir();
    let config = path(
        root.path(),
        "custom/plugins/Example/.config/shopware/lsp.yaml",
    );
    write(&config, r#"{"version":1,"domains":{"php":false}}"#);
    let root_path = root.path().to_str().unwrap();
    let run = run(&[
        "--root",
        root_path,
        "--allow-unsupported-project",
        "check",
        root_path,
    ]);
    assert_err_contains(&run, &config);
    assert_err_contains(&run, "nested configuration may only contain diagnostics");
}

#[test]
fn workspace_cli_index_check_and_execute_use_production_lsp() {
    let root = temp_dir();
    let root_path = root.path().to_str().unwrap().to_owned();
    let php = path(root.path(), "src/Controller/FixtureController.php");
    write(
        &php,
        "<?php\nnamespace App\\Controller;\nfinal class FixtureController\n{\n    public function value(): string { return 'ok'; }\n}\n",
    );
    for name in ["A.php", "B.php", "C.php", "D.php"] {
        write(&path(root.path(), &format!("src/{name}")), "<?php\n");
    }
    let run_json = |args: &[&str]| -> Value {
        let mut full = vec![
            "--root",
            root_path.as_str(),
            "--allow-unsupported-project",
            "--json",
        ];
        full.extend_from_slice(args);
        let output = run_ok(&full);
        serde_json::from_str(&output).unwrap_or_else(|error| panic!("{args:?}: {error}: {output}"))
    };

    assert_eq!(run_json(&["index"])["index"]["trackedFiles"], 5);
    let src = path(root.path(), "src");
    let diagnostics = run_json(&["check", "--workers", "4", &src]);
    assert!(diagnostics.is_array() || diagnostics.is_null());

    let commands = run_json(&["execute"]);
    assert!(
        commands
            .as_array()
            .unwrap()
            .iter()
            .any(|command| command == "shopware/symfony/analytics/routes")
    );
    assert!(run_json(&["semtok", &php]).is_array());
    let symbols = run_json(&["workspace-symbol", "FixtureController"]);
    assert!(!symbols.as_array().unwrap().is_empty());

    let position = format!("{php}:3:13");
    let signature = format!("{php}:5:37");
    let call = format!("{php}:5:21");
    for request in [
        vec!["codeaction", position.as_str()],
        vec!["completion", &position],
        vec!["definition", &position],
        vec!["implementation", &position],
        vec!["references", &position],
        vec!["hover", &position],
        vec!["signature", &signature],
        vec!["call-hierarchy", &call],
        vec!["type-hierarchy", &position],
        vec!["highlights", &position],
        vec!["folding-ranges", &php],
        vec!["links", &php],
        vec!["inlay-hints", &php],
        vec!["codelens", &php],
        vec!["selection-ranges", &position],
        vec!["linked-editing", &position],
        vec!["colors", &php],
        vec!["symbols", &php],
    ] {
        run_json(&request);
    }

    assert_eq!(run_json(&["stats"])["root"], root_path.as_str());
    assert_eq!(
        run_json(&["request", "shopware/index/stats"])["trackedFiles"],
        5
    );
    let extensions = run_json(&["execute", "shopware/extension/all"]);
    assert!(extensions.is_array() || extensions.is_null());
    assert_eq!(run_json(&["index", "--force"])["forced"], true);

    let preview = run_ok(&[
        "--root",
        &root_path,
        "--allow-unsupported-project",
        "--json",
        "rename",
        &position,
        "RenamedController",
    ]);
    assert!(preview.contains("RenamedController"), "{preview}");
    assert!(
        std::fs::read_to_string(&php)
            .unwrap()
            .contains("FixtureController")
    );
    run_ok(&[
        "--root",
        &root_path,
        "--allow-unsupported-project",
        "--json",
        "rename",
        "-w",
        &position,
        "RenamedController",
    ]);
    assert!(
        std::fs::read_to_string(&php)
            .unwrap()
            .contains("RenamedController")
    );
}

#[test]
fn workspace_symbol_uses_populated_catalog_without_session() {
    use swlsp_indexer::{
        CancelToken, Store, WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE, WorkspaceSymbol,
        WorkspaceSymbolCatalog, WorkspaceSymbolDocument, WorkspaceSymbolKind,
    };
    let root = temp_dir();
    let root_path = root.path().to_str().unwrap().to_owned();
    let cache_dir = swlsp_app::project_cache_folder(&root_path).unwrap();
    swlsp_indexer::check_and_migrate_cache(&cache_dir).unwrap();
    let store = Store::open(cache_dir.join("indexes.db")).unwrap();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let cancel = CancelToken::new();
    catalog
        .replace_files(
            &cancel,
            &[WorkspaceSymbolDocument {
                path: path(root.path(), "Fixture.php"),
                symbols: vec![WorkspaceSymbol {
                    name: "CachedFixture".into(),
                    kind: WorkspaceSymbolKind::CLASS,
                    priority: WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE,
                    ..WorkspaceSymbol::default()
                }],
            }],
        )
        .unwrap();
    catalog.set_ready(&cancel, true).unwrap();
    catalog.close().unwrap();
    store.close().unwrap();

    // The fast path answers from the catalog: no session, so no index runs
    // and the fixture file (which does not exist) is never needed.
    let output = run_ok(&[
        "--root",
        &root_path,
        "--allow-unsupported-project",
        "workspace-symbol",
        "CachedFixture",
    ]);
    let result: Value = serde_json::from_str(&output).unwrap();
    let symbols = result.as_array().unwrap();
    assert_eq!(symbols.len(), 1);
    assert_eq!(symbols[0]["name"], "CachedFixture");
}

#[test]
fn unsupported_profiles_are_created_and_reported() {
    let directory = temp_dir();
    let memory = directory.path().join("heap.pprof");
    let trace = directory.path().join("trace.out");
    let run = run(&[
        "--profile-mem",
        memory.to_str().unwrap(),
        "--profile-trace",
        trace.to_str().unwrap(),
        "version",
    ]);
    assert!(run.result.is_ok(), "{}", run.err);
    assert!(memory.exists() && trace.exists());
    assert!(
        run.err
            .contains("--profile-mem: heap profiles are not supported"),
        "{}",
        run.err
    );
    assert!(
        run.err
            .contains("--profile-trace: runtime traces are not supported"),
        "{}",
        run.err
    );
}

#[test]
fn help_goes_to_stdout_and_succeeds() {
    let output = run_ok(&["--help"]);
    assert!(output.contains("Usage: shopware-lsp"), "{output}");
    assert!(output.contains("workspace-symbol"));
    let check = run_ok(&["help", "check"]);
    assert!(check.contains("--fail-on <LEVEL>"), "{check}");
    assert!(run_ok(&["check", "-h"]).contains("Usage: shopware-lsp check"));
}

#[test]
fn global_flags_are_accepted_after_the_command() {
    let output = run_ok(&["version", "--json"]);
    let version: Value = serde_json::from_str(&output).unwrap();
    assert_eq!(version["version"], "test");
    assert_eq!(run_ok(&["-V"]), "shopware-lsp test\n");
}

#[test]
fn go_style_single_dash_flags_are_rejected_with_a_hint() {
    let run = run(&["-root", "/tmp", "project-info"]);
    let error = run.result.unwrap_err();
    assert_eq!(error.code, 2);
    assert!(
        error.message.contains("unexpected argument '-root'"),
        "{}",
        error.message
    );
    assert!(error.message.contains("'--root'"), "{}", error.message);
}

#[test]
fn unknown_commands_are_usage_errors() {
    let error = run(&["bogus"]).result.unwrap_err();
    assert_eq!(error.code, 2);
    assert!(
        error.message.contains("unrecognized subcommand 'bogus'"),
        "{}",
        error.message
    );
}

#[test]
fn language_client_transport_flags_parse_for_the_default_server() {
    // Editors start `shopware-lsp [serve] --stdio --clientProcessId=<pid>`.
    for argv in [
        &["--stdio", "--clientProcessId=4242"][..],
        &["serve", "--stdio", "--clientProcessId", "7"][..],
    ] {
        let argv: Vec<&std::ffi::OsStr> = argv.iter().map(std::ffi::OsStr::new).collect();
        let cli = crate::args::ShopwareLsp::parse_from(&argv).unwrap();
        assert!(cli.stdio);
        assert!(cli.client_process_id.is_some());
    }
}
