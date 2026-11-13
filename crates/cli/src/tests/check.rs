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
        &path(root.path(), ".config/shopware-lsp.yaml"),
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

#[test]
fn check_only_reports_and_fixes_the_selected_rules() {
    let (_root, root, imports) = fixable_workspace();
    let run = run(&["--root", &root, "check", "--only", "php.parse", &root]);
    assert_eq!(
        run.out,
        "src/Broken.php:1:17: error: expected ) but reached end of file [php.parse]\n"
    );
    assert!(
        run.err.starts_with(
            "note: only php.parse checked; other findings are not reported\nFound 1 error in 1 of 5 files ("
        ),
        "{}",
        run.err
    );
    // The fixable imports are not selected: they stay.
    let unfixed = super::common::run(&[
        "--root",
        &root,
        "check",
        "--fix",
        "--only",
        "php.parse",
        &root,
    ]);
    assert!(!unfixed.err.contains("Fixed"), "{}", unfixed.err);
    assert_eq!(std::fs::read_to_string(&imports).unwrap(), UNUSED_IMPORTS);

    // A prefix selects every rule below it; values may be comma-separated.
    // The syntax error is not selected, so the run succeeds.
    let fixed = super::common::run(&[
        "--root",
        &root,
        "check",
        "--fix",
        "--only",
        "twig.html.*,php.unusedImport",
        &root,
    ]);
    assert!(fixed.result.is_ok(), "{:?}", fixed.result);
    assert_eq!(fixed.out, "");
    assert!(
        fixed.err.ends_with(")\nFixed 2 problems in 1 file\n"),
        "{}",
        fixed.err
    );
    assert_eq!(
        std::fs::read_to_string(&imports).unwrap(),
        "<?php\n\nnamespace App;\n\nuse App\\Used;\n\nfunction run(Used $used): void {}\n"
    );

    let json = super::common::run(&[
        "--root",
        &root,
        "--json",
        "check",
        "--only",
        "php.parse",
        &root,
    ]);
    let findings: serde_json::Value = serde_json::from_str(&json.out).unwrap();
    assert_eq!(findings.as_array().unwrap().len(), 1);
    assert_eq!(findings[0]["diagnostic"]["code"], "php.parse");
}

#[test]
fn check_only_rejects_a_rule_that_does_not_exist() {
    let (_root, root) = workspace();
    for (value, message) in [
        (
            "php.parse,php.nope",
            "--only matches no diagnostic rule: php.nope",
        ),
        ("nope.*", "--only matches no diagnostic rule: nope.*"),
        // A prefix ends at a dot: `php.pars.*` is not `php.parse`.
        (
            "php.pars.*",
            "--only matches no diagnostic rule: php.pars.*",
        ),
        ("php.parse,", "--only needs a diagnostic rule"),
    ] {
        let run = run(&["--root", &root, "check", "--only", value, &root]);
        let error = run.result.unwrap_err();
        assert_eq!((error.code, error.message.as_str()), (2, message));
        assert_eq!(run.out, "");
    }
}

#[test]
fn check_only_turns_an_opt_in_rule_on_unless_the_project_configures_it() {
    let root = temp_dir();
    let configuration = path(root.path(), ".config/shopware-lsp.yaml");
    write(
        &configuration,
        r#"{"version": 1, "check": {"severity": "error"}}"#,
    );
    write(
        &path(root.path(), "src/PageController.php"),
        "<?php\n\nnamespace App;\n\nclass PageController\n{\n    public function show(\\Twig\\Environment $twig): string\n    {\n        return $twig->render('page.html.twig', ['title' => 1]);\n    }\n}\n",
    );
    write(
        &path(root.path(), "templates/page.html.twig"),
        "{{ title }}{{ missing }}\n",
    );
    let root_path = root.path().to_str().unwrap().to_owned();
    let check = |arguments: &[&str]| {
        let mut all = vec!["--root", &root_path, "--allow-unsupported-project", "check"];
        all.extend(arguments);
        all.push(&root_path);
        run(&all)
    };
    let finding = "templates/page.html.twig:1:15: warning: Variable 'missing' is not defined: no render call, template or assignment provides it [twig.variable.undefined]\n";

    // Off unless configured.
    assert_eq!(
        check(&["--severity", "hint", "--only", "twig.html.*"]).out,
        ""
    );
    // Named, it reports, whatever the configured minimum severity is.
    assert_eq!(check(&["--only", "twig.variable.undefined"]).out, finding);
    assert_eq!(check(&["--only", "twig.variable.*"]).out, finding);
    // An explicit minimum severity still applies.
    assert_eq!(
        check(&["--severity", "error", "--only", "twig.variable.undefined"]).out,
        ""
    );

    // The project configuration decides about a rule it mentions.
    write(
        &configuration,
        r#"{"version": 1, "diagnostics": {"rules": {"twig.variable.undefined": "off"}}}"#,
    );
    let off = check(&["--only", "twig.variable.undefined"]);
    assert_eq!(off.out, "");
    assert!(
        off.err.contains(
            "note: twig.variable.undefined is off in the project configuration and was not checked\n"
        ),
        "{}",
        off.err
    );
    write(
        &configuration,
        r#"{"version": 1, "diagnostics": {"rules": {"twig.variable.undefined": "error"}}}"#,
    );
    assert_eq!(
        check(&["--only", "twig.variable.undefined"]).out,
        finding.replace("warning", "error")
    );
}

#[test]
fn check_annotates_findings_for_github_actions() {
    let (_root, root, _imports) = fixable_workspace();
    let annotations = "::error file=src/Broken.php,line=1,endLine=1,col=17,title=php.parse::expected ) but reached end of file\n\
                       ::notice file=src/Imports.php,line=5,endLine=5,col=5,endColumn=14,title=php.unusedImport::Import First is never used [fixable]\n\
                       ::notice file=src/Imports.php,line=6,endLine=6,col=5,endColumn=15,title=php.unusedImport::Import Second is never used [fixable]\n";
    let named = run(&["--root", &root, "check", "--format", "github", &root]);
    assert_eq!(named.out, annotations);
    // The summary stays on stderr and the exit code is the same.
    assert!(
        named
            .err
            .starts_with("Found 1 error, 2 hints in 2 of 5 files ("),
        "{}",
        named.err
    );
    assert_eq!(named.result.unwrap_err().code, 1);

    // In a GitHub Actions job it is the default, with paths relative to the
    // checkout; a named format still wins.
    let checkout = std::path::Path::new(&root)
        .parent()
        .unwrap()
        .to_str()
        .unwrap()
        .to_owned();
    let project = std::path::Path::new(&root)
        .file_name()
        .unwrap()
        .to_str()
        .unwrap()
        .to_owned();
    let in_actions = |arguments: &[&str]| {
        run_with(
            crate::Options {
                github_workspace: Some(checkout.clone()),
                ..test_options()
            },
            &crate::Cancel::new(),
            arguments,
        )
    };
    let detected = in_actions(&["--root", &root, "check", &root]);
    assert_eq!(
        detected.out,
        annotations.replace("file=src/", &format!("file={project}/src/"))
    );
    let concise = in_actions(&["--root", &root, "check", "--format", "concise", &root]);
    assert!(
        concise.out.starts_with("src/Broken.php:1:17: error: "),
        "{}",
        concise.out
    );
}

#[test]
fn github_annotations_escape_what_the_command_syntax_reserves() {
    let mut finding = finding(
        "file:///project/src/a,b:c.php",
        0,
        2,
        2,
        "100% wrong\nsecond line",
    );
    finding.diagnostic.range.end.line = 2;
    finding.diagnostic.code = Some(json!("rule:one,two"));
    let mut report = Report::new(
        Format::Github,
        Style::default(),
        DisplayPaths::below(None, "/project"),
    );
    // A range over several lines has no columns.
    assert_eq!(
        report.file(&[finding]),
        "::warning file=src/a%2Cb%3Ac.php,line=1,endLine=3,title=rule%3Aone%2Ctwo::100%25 wrong%0Asecond line\n"
    );
}
