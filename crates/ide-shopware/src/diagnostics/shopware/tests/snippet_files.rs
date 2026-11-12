use swlsp_uriutil::file_uri;

use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

const MAIN: &str = r#"{
    "acme": {
        "title": "Title",
        "text": "Text",
        "button": {"save": "Save", "cancel": "Cancel"},
        "count": 1
    },
    "footer": "Footer"
}"#;

fn snippet_directory(relative: &str, main_name: &str) -> (tempfile::TempDir, String) {
    let directory = tempfile::tempdir().unwrap();
    let snippets = directory.path().join(relative);
    std::fs::create_dir_all(&snippets).unwrap();
    std::fs::write(snippets.join(main_name), MAIN).unwrap();
    let uri = file_uri(snippets.to_str().unwrap());
    (directory, uri)
}

#[test]
fn snippet_file_is_compared_with_the_main_language() {
    let (_directory, uri) = snippet_directory("src/Resources/snippet", "storefront.en-GB.json");
    let analyzer = SnippetFileAnalyzer::new();
    check_problems(
        &analyzer,
        &format!("{uri}/storefront.de-DE.json"),
        r#"{
    "{|shopware.snippet.file.missing-key:{|shopware.snippet.file.missing-key:acme|}|}": {
        "title": "Titel",
        "{|shopware.snippet.file.type-mismatch:button|}": "Speichern",
        "{|shopware.snippet.file.type-mismatch:count|}": "eins",
        "{|shopware.snippet.file.unknown-key:old|}": "Alt"
    }
}"#,
    );
    // An identical structure, the main file itself and a lone file are fine.
    check_problems(&analyzer, &format!("{uri}/storefront.de-DE.json"), MAIN);
    check_problems(&analyzer, &format!("{uri}/storefront.en-GB.json"), "{}");
    // An empty file is reported as a whole.
    check_problems(
        &analyzer,
        &format!("{uri}/storefront.de-DE.json"),
        "{|shopware.snippet.file.missing-key:{}|}",
    );
}

#[test]
fn snippet_file_messages_name_the_keys() {
    let (_directory, uri) = snippet_directory(
        "src/Resources/app/administration/src/module/acme/snippet",
        "en-GB.json",
    );
    let (_, problems) = check_problems(
        &SnippetFileAnalyzer::new(),
        &format!("{uri}/de-DE.json"),
        r#"{"{|shopware.snippet.file.missing-key:{|shopware.snippet.file.missing-key:acme|}|}": {"{|shopware.snippet.file.type-mismatch:title|}": {}, "count": 2, "button": {"save": "S", "cancel": "C"}}}"#,
    );
    let messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    assert_eq!(
        messages,
        [
            "Missing snippets defined in en-GB.json: \"acme.text\"",
            "Missing snippets defined in en-GB.json: \"footer\"",
            "Snippet \"acme.title\" is an object here but a string in en-GB.json",
        ]
    );
}

#[test]
fn snippet_file_prefers_en_json_and_ignores_other_json() {
    let (directory, uri) = snippet_directory("src/Resources/snippet", "en-GB.json");
    std::fs::write(
        directory.path().join("src/Resources/snippet/en.json"),
        r#"{"only": "One"}"#,
    )
    .unwrap();
    let analyzer = SnippetFileAnalyzer::new();
    let (document, _) = check_problems(&analyzer, &format!("{uri}/de.json"), r#"{"only": "Eins"}"#);
    // The sibling files are inputs: changing one changes the revision.
    let before = analyzer.input_revision(&document);
    std::fs::write(
        directory.path().join("src/Resources/snippet/en.json"),
        r#"{"only": "One", "more": "More"}"#,
    )
    .unwrap();
    assert!(before.is_some());
    assert_ne!(before, analyzer.input_revision(&document));
    // Not a snippet directory.
    let other = file_uri(
        directory
            .path()
            .join("src/Resources/config")
            .to_str()
            .unwrap(),
    );
    check_problems(&analyzer, &format!("{other}/de.json"), "{}");
}

#[test]
fn snippet_files_pair_by_stem() {
    let (directory, uri) = snippet_directory("src/Resources/snippet", "storefront.en-GB.json");
    std::fs::write(
        directory
            .path()
            .join("src/Resources/snippet/gallery.en-GB.json"),
        r#"{"gallery": {"title": "Gallery"}}"#,
    )
    .unwrap();
    let analyzer = SnippetFileAnalyzer::new();
    // `gallery.de-DE.json` is compared with `gallery.en-GB.json`, not with
    // the storefront file sorted before it.
    check_problems(
        &analyzer,
        &format!("{uri}/gallery.de-DE.json"),
        r#"{"gallery": {"title": "Galerie"}}"#,
    );
    check_problems(
        &analyzer,
        &format!("{uri}/storefront.de-DE.json"),
        &plain_main(),
    );
    // A file without a locale in its name is not compared.
    check_problems(&analyzer, &format!("{uri}/schema.json"), "{}");
}

fn plain_main() -> String {
    MAIN.to_owned()
}
