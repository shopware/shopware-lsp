//! The `format` command.

use super::common::*;

const UNFORMATTED: &str = "{% block content %}\n<div>\n<span>a</span>\n</div>\n{% endblock %}\n";
const FORMATTED: &str =
    "{% block content %}\n    <div>\n        <span>a</span>\n    </div>\n{% endblock %}\n";

/// A workspace with one template that is formatted and one that is not.
fn workspace() -> (tempfile::TempDir, String) {
    let root = temp_dir();
    write(&path(root.path(), "views/page.html.twig"), UNFORMATTED);
    write(&path(root.path(), "views/clean.html.twig"), FORMATTED);
    write(&path(root.path(), "config/services.xml"), "<container/>\n");
    write(
        &path(root.path(), ".config/shopware-lsp.yaml"),
        r#"{"version": 1}"#,
    );
    let root_path = root.path().to_str().unwrap().to_owned();
    (root, root_path)
}

fn read(root: &str, file: &str) -> String {
    std::fs::read_to_string(format!("{root}/{file}")).unwrap()
}

#[test]
fn format_previews_a_diff_and_fails() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "format", &root]);
    assert!(run.out.contains("-</div>\n+    <div>\n"), "{}", run.out);
    assert_eq!(
        run.err.lines().last(),
        Some("1 of 2 files would be reformatted; pass --write to format")
    );
    let error = run.result.unwrap_err();
    assert_eq!((error.code, error.message.as_str()), (1, ""));
    assert_eq!(read(&root, "views/page.html.twig"), UNFORMATTED);
}

#[test]
fn format_check_lists_the_files() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "format", "--check", &root]);
    assert_eq!(run.out, "views/page.html.twig\n");
    assert_eq!(run.result.unwrap_err().code, 1);
    assert_eq!(read(&root, "views/page.html.twig"), UNFORMATTED);
}

#[test]
fn format_write_formats_and_is_then_clean() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "format", "--write", &root]);
    run.result.unwrap();
    assert_eq!(run.out, "views/page.html.twig\n");
    assert_eq!(run.err.lines().last(), Some("Formatted 1 of 2 files"));
    assert_eq!(read(&root, "views/page.html.twig"), FORMATTED);

    let run = super::common::run(&["--root", &root, "format", &root]);
    run.result.unwrap();
    assert_eq!(run.out, "");
    assert_eq!(run.err.lines().last(), Some("2 files already formatted"));
}

#[test]
fn format_takes_the_indentation_from_its_flags() {
    let (_root, root) = workspace();
    let file = format!("{root}/views/page.html.twig");
    run_ok(&[
        "--root",
        &root,
        "format",
        "-w",
        "--indent-width",
        "2",
        &file,
    ]);
    assert_eq!(
        read(&root, "views/page.html.twig"),
        FORMATTED.replace("    ", "  ")
    );
    run_ok(&["--root", &root, "format", "-w", "--use-tabs", &file]);
    assert_eq!(
        read(&root, "views/page.html.twig"),
        FORMATTED.replace("    ", "\t")
    );
}

#[test]
fn format_reports_json_and_refuses_files_without_a_formatter() {
    let (_root, root) = workspace();
    let run = run(&["--root", &root, "--json", "format", &root]);
    let output: serde_json::Value = serde_json::from_str(&run.out).unwrap();
    assert_eq!(
        output,
        serde_json::json!({"written": false, "files": 2, "changed": ["views/page.html.twig"]})
    );

    let run = super::common::run(&["--root", &root, "format", &format!("{root}/config")]);
    assert_eq!(
        run.result.unwrap_err().message,
        "format: no file to format: only PHP, Twig, JSON, JavaScript, TypeScript and SCSS files have a formatter"
    );
}

#[test]
fn format_formats_json_scripts_and_styles_and_skips_syntax_errors() {
    let (_root, root) = workspace();
    write(&path(_root.path(), "theme.json"), "{\n\"name\":\"Swag\"}");
    write(&path(_root.path(), "broken.json"), "{\"name\":");
    write(&path(_root.path(), "src/main.ts"), "const a = {b:\"c\"}\n");
    write(&path(_root.path(), "src/base.scss"), ".card{color:red}\n");
    let run = run(&["--root", &root, "format", "-w", &root]);
    run.result.unwrap();
    assert_eq!(
        run.out,
        "src/base.scss\nsrc/main.ts\ntheme.json\nviews/page.html.twig\n"
    );
    assert_eq!(read(&root, "theme.json"), "{\n    \"name\": \"Swag\"\n}\n");
    assert_eq!(read(&root, "broken.json"), "{\"name\":");
    assert_eq!(read(&root, "src/main.ts"), "const a = { b: 'c' };\n");
    assert_eq!(
        read(&root, "src/base.scss"),
        ".card {\n    color: red;\n}\n"
    );
}

const PHP: &str =
    "<?php declare(strict_types=1);\n\nclass A {\n    function f() { return array(1); }\n}\n";

#[test]
fn format_formats_php_to_per_coding_style() {
    let (_root, root) = workspace();
    write(&path(_root.path(), "src/A.php"), PHP);
    let run = run(&["--root", &root, "format", "-w", &format!("{root}/src")]);
    run.result.unwrap();
    assert_eq!(run.out, "src/A.php\n");
    assert_eq!(
        read(&root, "src/A.php"),
        "<?php\n\ndeclare(strict_types=1);\n\nclass A\n{\n    public function f()\n    {\n        return [1];\n    }\n}\n"
    );
}

#[test]
fn format_keeps_the_declare_on_the_opening_tag_when_configured() {
    let (_root, root) = workspace();
    write(
        &path(_root.path(), ".config/shopware-lsp.yaml"),
        "version: 1\nphp:\n  declareOnOpeningTag: true\n",
    );
    write(&path(_root.path(), "src/A.php"), PHP);
    run_ok(&["--root", &root, "format", "-w", &format!("{root}/src")]);
    assert!(
        read(&root, "src/A.php").starts_with("<?php declare(strict_types=1);\n\nclass A\n{\n"),
        "{}",
        read(&root, "src/A.php")
    );
}

#[test]
fn format_splits_php_lines_at_the_configured_width() {
    let (_root, root) = workspace();
    let source = format!("<?php\n\n$a = foo('{}', $b);\n", "x".repeat(90));
    write(&path(_root.path(), "src/A.php"), &source);
    run_ok(&["--root", &root, "format", "-w", &format!("{root}/src")]);
    assert!(
        read(&root, "src/A.php").contains("foo(\n    '"),
        "{}",
        read(&root, "src/A.php")
    );

    write(
        &path(_root.path(), ".config/shopware-lsp.yaml"),
        "version: 1\nphp:\n  lineWidth: 120\n",
    );
    write(&path(_root.path(), "src/A.php"), &source);
    let run = run(&["--root", &root, "format", &format!("{root}/src")]);
    run.result.unwrap();
    assert_eq!(read(&root, "src/A.php"), source);
}
