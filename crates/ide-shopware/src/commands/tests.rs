use super::*;
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use swlsp_lsp::{
    BoxError, CommandProvider, Context, DocumentError, DocumentResolver, DocumentSnapshot,
    TextDocument, WorkspaceEditHost,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::WorkspacePlan;
use swlsp_uriutil::file_uri;

fn path_string(path: &std::path::Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

// --- edits -----------------------------------------------------------------

#[derive(Default)]
struct EditTestHost {
    snapshots: HashMap<String, DocumentSnapshot>,
    plan: Mutex<Option<WorkspacePlan>>,
    reject: String,
    resources: Vec<String>,
}

impl DocumentResolver for EditTestHost {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        if uri == self.reject {
            return Err(Box::new(DocumentError::OutsideWorkspace(uri.to_owned())));
        }
        self.snapshots.get(uri).cloned().ok_or_else(|| {
            Box::new(DocumentError::Read {
                uri: uri.to_owned(),
                source: std::io::Error::from(std::io::ErrorKind::NotFound),
            }) as BoxError
        })
    }
}

impl WorkspaceEditHost for EditTestHost {
    fn workspace_edit(
        &self,
        _ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        *self.plan.lock().unwrap() = Some(plan.clone());
        Ok(plan.workspace_edit()?)
    }
}

impl EditHost for EditTestHost {
    fn resource_paths(
        &self,
        _ctx: &Context,
        directory: &str,
        _limit: usize,
    ) -> Result<Vec<String>, BoxError> {
        Ok(self
            .resources
            .iter()
            .filter(|path| path.starts_with(directory))
            .cloned()
            .collect())
    }
}

fn snapshot(uri: &str, source: &str, version: i32) -> DocumentSnapshot {
    DocumentSnapshot {
        document: Arc::new(TextDocument::new(uri, source, version)),
        version: Some(version),
    }
}

#[test]
fn snippet_plan_preserves_unsaved_target_and_defers_creation() {
    let root = tempfile::tempdir().unwrap();
    let existing = path_string(&root.path().join("en.json"));
    let created = path_string(&root.path().join("de.json"));
    std::fs::write(&existing, r#"{"disk":true}"#).unwrap();
    let uri = file_uri(&existing);
    let host = Arc::new(EditTestHost {
        snapshots: HashMap::from([(uri.clone(), snapshot(&uri, r#"{"unsaved":"ä"}"#, 8))]),
        ..EditTestHost::default()
    });
    let provider = SnippetCommandProvider::new(None, host.clone());
    let response = provider
        .create_snippets(
            &Context::background(),
            "new",
            &[
                SnippetFile {
                    path: existing.clone(),
                    value: "English".into(),
                    ..SnippetFile::default()
                },
                SnippetFile {
                    path: created.clone(),
                    value: "Deutsch".into(),
                    ..SnippetFile::default()
                },
            ],
        )
        .unwrap();
    assert!(response.edit.is_some());
    let plan = host.plan.lock().unwrap().clone().unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].version, Some(8));
    let updated: Value = serde_json::from_str(&plan.documents[0].apply().unwrap()).unwrap();
    assert_eq!(updated, json!({"unsaved": "ä", "new": "English"}));
    assert_eq!(plan.creates.len(), 1);
    let created_value: Value = serde_json::from_str(&plan.creates[0].content).unwrap();
    assert_eq!(created_value, json!({"new": "Deutsch"}));
    assert_eq!(
        std::fs::read_to_string(&existing).unwrap(),
        r#"{"disk":true}"#
    );
    assert!(!std::path::Path::new(&created).exists());
}

#[test]
fn snippet_plan_failure_does_not_write_earlier_files() {
    let root = tempfile::tempdir().unwrap();
    let path = path_string(&root.path().join("en.json"));
    let rejected = path_string(&root.path().join("outside.json"));
    std::fs::write(&path, "{}").unwrap();
    let host = Arc::new(EditTestHost {
        reject: file_uri(&rejected),
        ..EditTestHost::default()
    });
    let result = SnippetCommandProvider::new(None, host.clone()).create_snippets(
        &Context::background(),
        "key",
        &[
            SnippetFile {
                path: path.clone(),
                value: "first".into(),
                ..SnippetFile::default()
            },
            SnippetFile {
                path: rejected,
                value: "second".into(),
                ..SnippetFile::default()
            },
        ],
    );
    assert!(result.is_err());
    assert!(host.plan.lock().unwrap().is_none());
    assert_eq!(std::fs::read_to_string(&path).unwrap(), "{}");
}

#[test]
fn possible_snippet_files_prefer_en_gb_and_suggest_default() {
    let root = "/project/custom/plugins/Demo/src/Resources";
    let host = Arc::new(EditTestHost {
        resources: vec![
            format!("{root}/snippet/de_DE/storefront.de-DE.json"),
            format!("{root}/snippet/en_GB/storefront.en-GB.json"),
            format!("{root}/snippet/readme.md"),
        ],
        ..EditTestHost::default()
    });
    let provider = SnippetCommandProvider::new(None, host);
    let ctx = Context::background();
    let commands = provider.commands(&ctx);
    let args = json!({"fileUri": file_uri(&format!("{root}/views/storefront/page.html.twig"))});
    let value =
        (commands["shopware/snippet/storefront/getPossibleSnippetFiles"])(&ctx, Some(&args))
            .unwrap();
    assert_eq!(
        value,
        json!({"paths": [
            {"path": format!("{root}/snippet/en_GB/storefront.en-GB.json"), "name": "storefront.en-GB.json", "value": ""},
            {"path": format!("{root}/snippet/de_DE/storefront.de-DE.json"), "name": "storefront.de-DE.json", "value": ""},
        ]})
    );
    let admin_args =
        json!({"fileUri": file_uri("/x/Resources/app/administration/src/module/a/index.js")});
    let value =
        (commands["shopware/snippet/admin/getPossibleSnippetFiles"])(&ctx, Some(&admin_args))
            .unwrap();
    assert_eq!(
        value,
        json!({"paths": [{"path": "/x/Resources/app/administration/src/snippet/en-GB.json", "name": "en-GB.json", "value": ""}]})
    );
    let error = (commands["shopware/snippet/storefront/getPossibleSnippetFiles"])(
        &ctx,
        Some(&json!({"fileUri": "file:///tmp/a.twig"})),
    )
    .unwrap_err();
    assert_eq!(
        error.to_string(),
        "resources directory not found in any parent directory of /tmp/a.twig"
    );
}

// --- JSON key insertion / pretty printing ---------------------------------

#[test]
fn set_string_replaces_inserts_and_creates_nested_objects() {
    let cases = [
        ("{}", "a", "x", r#"{"a":"x"}"#),
        ("", "a.b", "x", r#"{"a":{"b":"x"}}"#),
        ("  \n", "a", "x", r#"{"a":"x"}"#),
        (r#"{"a":{"b":"old"}}"#, "a.b", "new", r#"{"a":{"b":"new"}}"#),
        (r#"{"a":{"c":1}}"#, "a.b", "x", r#"{"a":{"c":1,"b":"x"}}"#),
        (r#"{"a":{}}"#, "a.b.c", "x", r#"{"a":{"b":{"c":"x"}}}"#),
        (r#"{"a":"str"}"#, "a.b", "x", r#"{"a":{"b":"x"}}"#),
        (r#"{"a":[1]}"#, "a.b", "x", r#"{"a":{"b":"x"}}"#),
        (r#"{"a":{"b":1}}"#, "a", "x", r#"{"a":"x"}"#),
        (r#"{ "a" : 1 }"#, "b", "<&>", r#"{ "a" : 1,"b":"<&>" }"#),
        (r#"{"a":1,}"#, "b", "x", r#"{"a":1,"b":"x"}"#),
        (r#"{"a.b":1}"#, r"a\.b", "x", r#"{"a.b":"x"}"#),
        (r#"{"\u0061":1}"#, "a", "x", r#"{"\u0061":"x"}"#),
        ("{}", "a*", "x", r#"{"a*":"x"}"#),
        ("{}", "k", "line\nä\"", r#"{"k":"line\nä\""}"#),
        (
            "{\n  \"a\": {\n    \"b\": \"x\"\n  },\n  \"z\": 1\n}\n",
            "a.c",
            "y",
            "{\n  \"a\": {\n    \"b\": \"x\",\"c\":\"y\"\n  },\n  \"z\": 1\n}\n",
        ),
    ];
    for (json, path, value, expected) in cases {
        assert_eq!(
            crate::commands::json_edit::set_string(json, path, value).unwrap(),
            expected,
            "{json} {path}"
        );
    }
    let error = |json: &str, path: &str| {
        crate::commands::json_edit::set_string(json, path, "x")
            .unwrap_err()
            .to_string()
    };
    assert_eq!(error("{}", ""), "key path is empty");
    assert_eq!(error("[1]", "a"), "document root is not a JSON object");
    assert_eq!(
        error(r#"{"a":1"#, "b"),
        "JSON object at byte 0 is not closed"
    );
}

#[test]
fn pretty_indents_objects_and_keeps_short_arrays_inline() {
    let pretty =
        |value: &str| String::from_utf8(crate::commands::pretty::pretty(value.as_bytes())).unwrap();
    assert_eq!(pretty("{}"), "{}\n");
    assert_eq!(
        pretty(r#"{"a":{"b":"x","c":[1,2,{"d":true}]},"e":[1, 2,3],"f":[]}"#),
        "{\n  \"a\": {\n    \"b\": \"x\",\n    \"c\": [\n      1,\n      2,\n      {\n        \"d\": true\n      }\n    ]\n  },\n  \"e\": [1, 2, 3],\n  \"f\": []\n}\n"
    );
}
