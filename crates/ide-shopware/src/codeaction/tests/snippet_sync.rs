//! The action of a main language file that fills the other locale files.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_ide::codeaction::testing::request_at;
use swlsp_lsp::{BoxError, DocumentSnapshot, TextDocument};

use super::*;

/// Open documents, and the files on disk for the rest.
#[derive(Default)]
struct Documents(HashMap<String, Arc<TextDocument>>);

impl DocumentResolver for Documents {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        if let Some(document) = self.0.get(uri) {
            return Ok(DocumentSnapshot {
                document: document.clone(),
                version: Some(document.version),
            });
        }
        let source = std::fs::read_to_string(swlsp_uriutil::path(uri)?)?;
        Ok(DocumentSnapshot {
            document: Arc::new(TextDocument::new(uri.to_owned(), &source, 0)),
            version: None,
        })
    }
}

const MAIN: &str = "{\n    \"a\": \"A\",\n    \"b\": {\n        \"c\": \"C\"\n    }\n}\n";

fn directory(files: &[(&str, &str)]) -> (tempfile::TempDir, String) {
    let directory = tempfile::tempdir().unwrap();
    let snippets = directory.path().join("src/Resources/snippet");
    std::fs::create_dir_all(&snippets).unwrap();
    for (name, source) in files {
        std::fs::write(snippets.join(name), source).unwrap();
    }
    let uri = file_uri(snippets.to_str().unwrap());
    (directory, uri)
}

fn actions(documents: Documents, uri: &str, source: &str) -> Vec<protocol::CodeAction> {
    SnippetSyncCodeActionProvider::new(Arc::new(documents))
        .get_code_actions(&Context::background(), &request_at(uri, source, 0, 0))
}

/// The text of each edit by the name of its file.
fn texts(action: &protocol::CodeAction) -> Vec<(String, Vec<String>)> {
    action
        .edit
        .as_ref()
        .unwrap()
        .changes
        .iter()
        .map(|(uri, edits)| {
            (
                filepath::base(uri),
                edits.iter().map(|edit| edit.new_text.clone()).collect(),
            )
        })
        .collect()
}

#[test]
fn the_main_file_fills_every_incomplete_locale_file() {
    let (_directory, uri) = directory(&[
        ("storefront.en-GB.json", "{}"),
        ("storefront.de-DE.json", "{\n    \"a\": \"A\"\n}\n"),
        ("storefront.fr-FR.json", "{\n  \"b\": {}\n}\n"),
        ("storefront.nl-NL.json", MAIN),
        // Another stem has another main file.
        ("other.de-DE.json", "{}"),
    ]);
    // The unsaved main file is what the others are filled from.
    let found = actions(
        Documents::default(),
        &format!("{uri}/storefront.en-GB.json"),
        MAIN,
    );
    assert_eq!(found.len(), 1);
    assert_eq!(
        found[0].title,
        "Shopware: Add missing snippets to 2 locale files"
    );
    assert_eq!(
        texts(&found[0]),
        [
            (
                "storefront.de-DE.json".to_owned(),
                vec![",\n    \"b\": {\n        \"c\": \"C\"\n    }".to_owned()]
            ),
            (
                "storefront.fr-FR.json".to_owned(),
                vec![
                    "\"a\": \"A\",\n  ".to_owned(),
                    "\n    \"c\": \"C\"\n  ".to_owned()
                ]
            ),
        ]
    );
}

#[test]
fn an_open_locale_file_is_read_as_the_editor_has_it() {
    let (_directory, uri) = directory(&[("en-GB.json", MAIN), ("de-DE.json", "{}")]);
    let german = format!("{uri}/de-DE.json");
    // On disk everything is missing; the open document lacks one key, on
    // another line than the file on disk has it.
    let open = "{\n\n\n    \"a\": \"A\"\n}\n";
    let documents = Documents(HashMap::from([(
        german.clone(),
        Arc::new(TextDocument::new(german.clone(), open, 4)),
    )]));
    let found = actions(documents, &format!("{uri}/en-GB.json"), MAIN);
    assert_eq!(
        found[0].title,
        "Shopware: Add missing snippets to de-DE.json"
    );
    let edits = &found[0].edit.as_ref().unwrap().changes[&german];
    assert_eq!(edits.len(), 1);
    assert_eq!(
        (edits[0].range.start.line, edits[0].range.start.character),
        (3, 12)
    );
}

#[test]
fn nothing_is_offered_without_something_to_add() {
    let (_directory, uri) = directory(&[
        ("en-GB.json", MAIN),
        ("de-DE.json", MAIN),
        ("notes.json", "{}"),
    ]);
    // Complete files, a file that is not the main one, and no snippet file.
    assert!(actions(Documents::default(), &format!("{uri}/en-GB.json"), MAIN).is_empty());
    assert!(actions(Documents::default(), &format!("{uri}/de-DE.json"), "{}").is_empty());
    assert!(
        actions(
            Documents::default(),
            "file:///project/config/en-GB.json",
            MAIN
        )
        .is_empty()
    );
}
