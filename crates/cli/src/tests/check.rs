//! The text report of `check`.

use serde_json::json;

use super::common::*;
use crate::report::{DisplayPaths, Format, Report};
use crate::session::DiagnosticOutput;
use crate::terminal::Style;

/// A workspace with one broken and one clean PHP file.
fn workspace() -> (tempfile::TempDir, String) {
    let root = temp_dir();
    write(&path(root.path(), "src/Broken.php"), "<?php function (\n");
    write(&path(root.path(), "src/Clean.php"), "<?php\n");
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        r#"{"version": 1, "check": {"severity": "hint", "failOn": "error"}}"#,
    );
    let root_path = root.path().to_str().unwrap().to_owned();
    (root, root_path)
}

#[test]
fn check_reports_one_line_per_finding_off_a_terminal() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "check", &root]);
    // Paths are relative to the workspace root; the summary is on stderr,
    // so stdout holds findings only.
    assert_eq!(
        run.out,
        "src/Broken.php:1:17: error: expected ) but reached end of file [php.parse]\n"
    );
    assert!(
        run.err.starts_with("Found 1 error in 1 of 3 files ("),
        "{}",
        run.err
    );
    // The summary is the reason for the exit code.
    let error = run.result.unwrap_err();
    assert_eq!((error.code, error.message.as_str()), (1, ""));
}

#[test]
fn check_groups_findings_by_file_in_the_pretty_format() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "check", "--format", "pretty", &root]);
    assert_eq!(
        run.out,
        "\nsrc/Broken.php\n  1:17  error    expected ) but reached end of file  php.parse\n"
    );
    assert!(
        run.err.starts_with("\n✖ 1 error in 1 of 3 files ("),
        "{}",
        run.err
    );
}

#[test]
fn check_colors_on_request_only() {
    let (_root, root) = workspace();
    let colored = run(&["--root", &root, "--color", "always", "check", &root]);
    assert_eq!(
        colored.out,
        "src/Broken.php:1:17: \u{1b}[31merror\u{1b}[0m: expected ) but reached end of file\u{1b}[2m [php.parse]\u{1b}[0m\n"
    );
    assert!(colored.err.contains("\u{1b}[31m1 error\u{1b}[0m"));
    // Buffers are no terminal, so `auto` stays plain.
    let plain = run(&["--root", &root, "--color", "auto", "check", &root]);
    assert!(!plain.out.contains('\u{1b}') && !plain.err.contains('\u{1b}'));
    assert_err_contains(
        &run(&["--root", &root, "--color", "blue", "check", &root]),
        "unknown --color value",
    );
    assert_err_contains(
        &run(&["--root", &root, "check", "--format", "table", &root]),
        "unknown check format",
    );
}

#[test]
fn check_summarizes_a_clean_run() {
    let (_root, root) = workspace();
    let clean = path(std::path::Path::new(&root), "src/Clean.php");
    let run = run(&["--root", &root, "check", &clean]);
    assert!(run.result.is_ok(), "{:?}", run.result);
    assert_eq!(run.out, "");
    assert!(
        run.err.starts_with("No problems in 1 file ("),
        "{}",
        run.err
    );
    let pretty = super::common::run(&["--root", &root, "check", "--format", "pretty", &clean]);
    assert!(
        pretty.err.starts_with("✓ No problems in 1 file ("),
        "{}",
        pretty.err
    );
}

#[test]
fn check_json_output_is_unchanged_by_the_report() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "--json", "check", &root]);
    let findings: serde_json::Value = serde_json::from_str(&run.out).unwrap();
    assert_eq!(findings.as_array().unwrap().len(), 1);
    assert!(findings[0]["uri"].as_str().unwrap().starts_with("file://"));
    assert_eq!(run.err, "");
    assert_err_contains(&run, "fail-on error");
}

fn finding(uri: &str, line: u32, column: u32, severity: u32, message: &str) -> DiagnosticOutput {
    let position = json!({"line": line, "character": column});
    DiagnosticOutput {
        uri: uri.to_owned(),
        diagnostic: serde_json::from_value(json!({
            "range": {"start": position, "end": position},
            "severity": severity,
            "code": "demo.rule",
            "message": message,
        }))
        .unwrap(),
        related: None,
        fixable: None,
    }
}

#[test]
fn the_pretty_report_aligns_sorts_and_counts() {
    let paths = DisplayPaths::below(None, "/project");
    let mut report = Report::new(Format::Pretty, Style::default(), paths);
    let uri = "file:///project/src/a.php";
    let mut with_related = finding(uri, 119, 4, 2, "second\nmore detail");
    with_related.related = Some(vec![
        serde_json::from_value(json!({
            "location": {
                "uri": "file:///project/config/services.xml",
                "range": {
                    "start": {"line": 11, "character": 8},
                    "end": {"line": 11, "character": 9},
                },
            },
            "message": "declared here",
        }))
        .unwrap(),
    ]);
    let text = report.file(&[
        with_related,
        finding(uri, 2, 0, 1, "first problem"),
        finding(uri, 300, 0, 4, &"long ".repeat(20)),
    ]);
    let long = "long ".repeat(20);
    assert_eq!(
        text,
        format!(
            "\nsrc/a.php\n\
             \x20   3:1  error    first problem  demo.rule\n\
             \x20 120:5  warning  second         demo.rule\n\
             \x20                 more detail\n\
             \x20                 ↳ config/services.xml:12:9: declared here\n\
             \x20 301:1  hint     {}  demo.rule\n",
            long.trim_end()
        )
    );
    assert_eq!(report.file(&[]), "");
    report.file(&[finding("file:///project/b.php", 0, 0, 3, "note")]);
    let summary = report.summary(
        Style::default(),
        1200,
        std::time::Duration::from_millis(3240),
    );
    assert_eq!(
        summary,
        "\n✖ 1 error, 1 warning, 1 info, 1 hint in 2 of 1,200 files (3.2s)\n"
    );
}

#[test]
fn paths_are_shown_relative_to_the_current_directory_first() {
    let uri = "file:///project/plugin/src/a.php";
    // Below the current directory: what a terminal can open from there.
    let paths = DisplayPaths::below(Some("/project/plugin"), "/project");
    assert_eq!(paths.display(uri), "src/a.php");
    // Elsewhere: relative to the workspace root.
    let paths = DisplayPaths::below(Some("/home/me"), "/project");
    assert_eq!(paths.display(uri), "plugin/src/a.php");
    // Outside both (a related location in another checkout): unchanged.
    assert_eq!(paths.display("file:///other/b.php"), "/other/b.php");
}

#[test]
fn check_notes_a_missing_compiled_container_instead_of_missing_services() {
    let root = temp_dir();
    write(
        &path(root.path(), "composer.json"),
        r#"{"name":"acme/app","require":{"symfony/framework-bundle":"^7.0"}}"#,
    );
    write(
        &path(
            root.path(),
            "vendor/symfony/dependency-injection/Attribute/Autowire.php",
        ),
        r"<?php
namespace Symfony\Component\DependencyInjection\Attribute;
#[\Attribute]
class Autowire { public function __construct(public ?string $service = null) {} }
",
    );
    let service = path(root.path(), "src/Service.php");
    write(
        &service,
        r"<?php
namespace App;
use Symfony\Component\DependencyInjection\Attribute\Autowire;
class Service {
    public function __construct(#[Autowire(service: 'cache.object')] private object $cache) {}
}
",
    );
    let root_path = root.path().to_str().unwrap();
    let without = run(&["--root", root_path, "check", &service]);
    assert_eq!(without.out, "", "{}", without.err);
    assert!(
        without
            .err
            .starts_with("note: No compiled Symfony container found below var/cache"),
        "{}",
        without.err
    );
    // Once, however many files meet it, and before the summary.
    assert_eq!(without.err.matches("note:").count(), 1);
    assert!(
        without
            .err
            .trim_end()
            .lines()
            .last()
            .unwrap()
            .starts_with("No problems in 1 file")
    );

    // With a container the reference can be judged.
    write(
        &path(root.path(), "var/cache/dev/App_KernelDevDebugContainer.xml"),
        "<container><services/></container>",
    );
    let with = run(&["--root", root_path, "check", &service]);
    assert!(
        with.out
            .contains("Service 'cache.object' not found [symfony.service.missing]"),
        "{} {}",
        with.out,
        with.err
    );
    assert!(!with.err.contains("note:"), "{}", with.err);
}

const UNUSED_IMPORTS: &str = "<?php\n\nnamespace App;\n\nuse App\\First;\nuse App\\Second;\nuse App\\Used;\n\nfunction run(Used $used): void {}\n";

/// A workspace with two unused imports (fixable) and a syntax error (not).
fn fixable_workspace() -> (tempfile::TempDir, String, String) {
    let (root, root_path) = workspace();
    let imports = path(root.path(), "src/Imports.php");
    write(&imports, UNUSED_IMPORTS);
    write(
        &path(root.path(), "src/Used.php"),
        "<?php\n\nnamespace App;\n\nclass Used {}\n",
    );
    (root, root_path, imports)
}

#[test]
fn check_marks_the_findings_it_can_fix() {
    let (_root, root, imports) = fixable_workspace();
    let run = run(&["--root", &root, "check", &root]);
    assert_eq!(
        run.out,
        "src/Broken.php:1:17: error: expected ) but reached end of file [php.parse]\n\
         src/Imports.php:5:5: hint: Import First is never used [php.unusedImport] [fixable]\n\
         src/Imports.php:6:5: hint: Import Second is never used [php.unusedImport] [fixable]\n"
    );
    assert!(
        run.err.ends_with(")\n2 problems fixable with --fix\n"),
        "{}",
        run.err
    );
    // Reporting changes nothing.
    assert_eq!(std::fs::read_to_string(&imports).unwrap(), UNUSED_IMPORTS);

    let pretty = super::common::run(&["--root", &root, "check", "--format", "pretty", &imports]);
    assert_eq!(
        pretty.out,
        "\nsrc/Imports.php\n  \
         5:5  hint     Import First is never used   php.unusedImport  [fixable]\n  \
         6:5  hint     Import Second is never used  php.unusedImport  [fixable]\n"
    );

    let json = super::common::run(&["--root", &root, "--json", "check", &root]);
    let findings: serde_json::Value = serde_json::from_str(&json.out).unwrap();
    let fixable: Vec<bool> = findings
        .as_array()
        .unwrap()
        .iter()
        .map(|finding| finding["fixable"].as_bool().unwrap())
        .collect();
    assert_eq!(fixable, [false, true, true]);
}

#[test]
fn check_fix_applies_the_fixes_and_reports_the_rest() {
    let (_root, root, imports) = fixable_workspace();
    let broken = path(std::path::Path::new(&root), "src/Broken.php");
    let run = run(&["--root", &root, "check", "--fix", &root]);
    assert_eq!(
        run.out,
        "src/Broken.php:1:17: error: expected ) but reached end of file [php.parse]\n"
    );
    assert!(
        run.err.starts_with("Found 1 error in 1 of 5 files (")
            && run.err.ends_with(")\nFixed 2 problems in 1 file\n"),
        "{}",
        run.err
    );
    assert_eq!(
        std::fs::read_to_string(&imports).unwrap(),
        "<?php\n\nnamespace App;\n\nuse App\\Used;\n\nfunction run(Used $used): void {}\n"
    );
    assert_eq!(
        std::fs::read_to_string(&broken).unwrap(),
        "<?php function (\n"
    );

    // Nothing is left to fix.
    let again = super::common::run(&["--root", &root, "check", "--fix", &imports]);
    assert!(again.result.is_ok(), "{:?}", again.result);
    assert_eq!(again.out, "");
    assert!(
        again.err.starts_with("No problems in 1 file ("),
        "{}",
        again.err
    );
    assert!(!again.err.contains("Fixed"), "{}", again.err);
}
