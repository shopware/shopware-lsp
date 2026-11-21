use swlsp_uriutil::file_uri;

use crate::inspections::shopware::*;
use crate::inspections::test_support::check_fix;

const MAIN: &str = r#"{
    "acme": {
        "headline": "Head \"line\"",
        "title": "Title",
        "button": {
            "save": "Save",
            "cancel": "Cancel"
        },
        "text": "Text",
        "empty": {},
        "list": ["a", "b"]
    },
    "footer": "Footer",
    "legal": {
        "imprint": "Imprint"
    }
}"#;

fn snippet_directory(main: &str) -> (tempfile::TempDir, String) {
    let directory = tempfile::tempdir().unwrap();
    let snippets = directory.path().join("src/Resources/snippet");
    std::fs::create_dir_all(&snippets).unwrap();
    std::fs::write(snippets.join("storefront.en-GB.json"), main).unwrap();
    let uri = file_uri(snippets.join("storefront.de-DE.json").to_str().unwrap());
    (directory, uri)
}

fn fixed(main: &str, before: &str, after: &str) -> String {
    let (_directory, uri) = snippet_directory(main);
    check_fix(
        new_snippet_files().as_ref(),
        &uri,
        before,
        ADD_MISSING_SNIPPETS_FIX_ID,
        after,
    )
    .title
}

#[test]
fn missing_snippets_are_added_in_the_order_of_the_main_file() {
    let title = fixed(
        MAIN,
        r#"{
  "acme": {
    "title": "Titel",
    "{|shopware.snippet.file.missing-key:button|}": {
      "cancel": "Abbrechen"
    },
    "old": "Alt"
  }
}"#,
        r#"{
  "acme": {
    "headline": "Head \"line\"",
    "title": "Titel",
    "button": {
      "save": "Save",
      "cancel": "Abbrechen"
    },
    "text": "Text",
    "empty": {},
    "list": [
      "a",
      "b"
    ],
    "old": "Alt"
  },
  "footer": "Footer",
  "legal": {
    "imprint": "Imprint"
  }
}"#,
    );
    assert_eq!(
        title,
        "Shopware: Add all missing snippets from storefront.en-GB.json"
    );
}

#[test]
fn missing_snippets_fill_an_empty_file_and_an_empty_object() {
    fixed(
        "{\n\t\"a\": {\n\t\t\"b\": \"B\"\n\t},\n\t\"c\": \"C\"\n}\n",
        "{|shopware.snippet.file.missing-key:{}|}\n",
        "{\n\t\"a\": {\n\t\t\"b\": \"B\"\n\t},\n\t\"c\": \"C\"\n}\n",
    );
    fixed(
        r#"{"a": {"b": "B", "d": {"e": "E"}}, "c": "C"}"#,
        "{\n    \"{|shopware.snippet.file.missing-key:a|}\": {},\n    \"c\": \"Z\"\n}\n",
        "{\n    \"a\": {\n        \"b\": \"B\",\n        \"d\": {\n            \"e\": \"E\"\n        }\n    },\n    \"c\": \"Z\"\n}\n",
    );
}

#[test]
fn missing_snippets_stay_on_the_line_of_a_one_line_object() {
    fixed(
        r#"{"a": {"b": "B", "d": {"e": "E"}, "f": "F"}}"#,
        r#"{"{|shopware.snippet.file.missing-key:a|}": {"d": {"e": "x"}}}"#,
        r#"{"a": {"b": "B", "d": {"e": "x"}, "f": "F"}}"#,
    );
}

#[test]
fn a_value_of_another_type_is_left_alone() {
    // "acme" is a text here: its keys are not added below it.
    fixed(
        r#"{"acme": {"title": "Title"}, "footer": "Footer"}"#,
        "{\n    \"{|shopware.snippet.file.missing-key:acme|}\": \"x\"\n}",
        "{\n    \"acme\": \"x\",\n    \"footer\": \"Footer\"\n}",
    );
}

/// `source` with the edits of the fix applied.
fn filled(main: &str, source: &str) -> String {
    let Some(mut edits) = super::super::snippet_files::missing_snippet_edits(main, source) else {
        return source.to_owned();
    };
    edits.sort_by_key(|(range, _)| std::cmp::Reverse(range.start()));
    let mut result = source.to_owned();
    for (range, text) in edits {
        result.replace_range(usize::from(range.start())..usize::from(range.end()), &text);
    }
    result
}

/// Every object key of `value`, as the path to it.
fn key_paths(value: &serde_json::Value, prefix: &[String], found: &mut Vec<Vec<String>>) {
    let Some(object) = value.as_object() else {
        return;
    };
    for (key, child) in object {
        let path = [prefix, std::slice::from_ref(key)].concat();
        key_paths(child, &path, found);
        found.push(path);
    }
}

fn without(value: &serde_json::Value, path: &[String]) -> serde_json::Value {
    let mut value = value.clone();
    let mut object = &mut value;
    for key in &path[..path.len() - 1] {
        object = &mut object[key.as_str()];
    }
    object
        .as_object_mut()
        .unwrap()
        .remove(&path[path.len() - 1]);
    value
}

#[test]
fn a_file_without_any_one_key_is_filled_to_the_main_file() {
    let main: serde_json::Value = serde_json::from_str(MAIN).unwrap();
    let mut paths = Vec::new();
    key_paths(&main, &[], &mut paths);
    assert_eq!(paths.len(), 12);
    for path in paths {
        let reduced = without(&main, &path);
        for source in [
            serde_json::to_string_pretty(&reduced).unwrap(),
            serde_json::to_string(&reduced).unwrap(),
        ] {
            let result = filled(MAIN, &source);
            let parsed: serde_json::Value = serde_json::from_str(&result)
                .unwrap_or_else(|error| panic!("{error} in\n{result}"));
            assert_eq!(parsed, main, "without {path:?}:\n{result}");
            // A second run has nothing to add.
            assert_eq!(filled(MAIN, &result), result);
        }
    }
}
