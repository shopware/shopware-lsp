use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use serde_json::{Value, json};
use swlsp_console::{CatalogEntry, CatalogInput, CatalogRequest, Index, LIST_CATALOG_COMMAND};
use swlsp_extension::ExtensionIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    BoxError, CommandProvider, Context, DocumentError, DocumentResolver, DocumentSnapshot,
    TextDocument, WorkspaceEditHost,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::WorkspacePlan;
use swlsp_uriutil::file_uri;

use crate::{
    ConsoleCatalogProvider, EditHost, SnippetCommandProvider, SnippetFile, TwigCommandProvider,
};

fn path_string(path: &std::path::Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

// --- console ---------------------------------------------------------------

#[test]
fn console_catalog_provides_commands_aliases_and_inputs() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(dir.path(), None).unwrap());
    let path = "/project/src/Command/CreateUserCommand.php";
    index
        .index(&ParsedFile::new(
            path.to_owned(),
            br"<?php
namespace App\Command;
#[AsCommand(
    name: 'app:user:create',
    description: 'Creates a user',
    aliases: ['app:user:add'],
)]
final class CreateUserCommand
{
    public function __invoke(
        #[Argument(description: 'User ID')] string $userId,
        #[Option(name: 'force', shortcut: 'f')] bool $force = false,
    ): int {
        return 0;
    }
}
"
            .to_vec(),
        ))
        .unwrap();
    let provider = ConsoleCatalogProvider::new(index.clone(), Some("/project"));
    let entries = provider.catalog(&Context::background(), "").unwrap();
    assert_eq!(entries.len(), 2);
    assert_eq!(entries[0].name, "app:user:add");
    assert_eq!(entries[0].canonical, "app:user:create");
    assert_eq!(entries[1].name, "app:user:create");
    assert_eq!(entries[1].description, "Creates a user");
    assert_eq!(entries[1].class, "App\\Command\\CreateUserCommand");
    assert_eq!(entries[1].file_uri, file_uri(path));
    assert_eq!(entries[1].file_path, "src/Command/CreateUserCommand.php");
    assert_eq!(
        entries[1].arguments,
        vec![CatalogInput {
            name: "userId".into(),
            description: "User ID".into(),
            ..CatalogInput::default()
        }]
    );
    assert_eq!(
        entries[1].options,
        vec![CatalogInput {
            name: "force".into(),
            shortcut: "f".into(),
            default: "false".into(),
            ..CatalogInput::default()
        }]
    );
    index.close().unwrap();
}

#[test]
fn console_catalog_command_filters_case_insensitively() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(dir.path(), None).unwrap());
    index
        .index(&ParsedFile::new(
            "/project/src/Commands.php".to_owned(),
            br"<?php
namespace App\Command;
#[AsCommand('app:user:create')]
final class CreateUser {}
#[AsCommand('app:cache:warm')]
final class WarmCache {}
"
            .to_vec(),
        ))
        .unwrap();
    let provider = ConsoleCatalogProvider::new(index.clone(), Some("/project"));
    let ctx = Context::background();
    let args = serde_json::to_value(CatalogRequest {
        query: "USER".into(),
        file_glob: "src/**/Commands.php".into(),
    })
    .unwrap();
    let value = (provider.commands(&ctx)[LIST_CATALOG_COMMAND])(&ctx, Some(&args)).unwrap();
    let expected = vec![CatalogEntry {
        name: "app:user:create".into(),
        canonical: "app:user:create".into(),
        class: "App\\Command\\CreateUser".into(),
        file_uri: "file:///project/src/Commands.php".into(),
        file_path: "src/Commands.php".into(),
        ..CatalogEntry::default()
    }];
    assert_eq!(value, serde_json::to_value(&expected).unwrap());
    let entries = provider
        .catalog_with_request(
            &ctx,
            &CatalogRequest {
                file_glob: "tests/**".into(),
                ..CatalogRequest::default()
            },
        )
        .unwrap();
    assert!(entries.is_empty());
    let unavailable = ConsoleCatalogProvider::unavailable();
    let error = (unavailable.commands(&ctx)[LIST_CATALOG_COMMAND])(&ctx, None).unwrap_err();
    assert_eq!(error.to_string(), "symfony console catalog is unavailable");
    index.close().unwrap();
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
fn twig_block_plan_preserves_unsaved_template() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let extensions = Arc::new(ExtensionIndexer::new(cache.path(), None).unwrap());
    let plugin = path_string(&root.path().join("src/DemoPlugin.php"));
    extensions
        .index(&ParsedFile::new(
            plugin,
            b"<?php\nuse Shopware\\Core\\Framework\\Plugin;\nclass DemoPlugin extends Plugin {}\n"
                .to_vec(),
        ))
        .unwrap();
    let target = path_string(
        &root
            .path()
            .join("src/Resources/views/storefront/page.html.twig"),
    );
    let uri = file_uri(&target);
    let host = Arc::new(EditTestHost {
        snapshots: HashMap::from([(uri.clone(), snapshot(&uri, "{# unsaved ä #}\n", 3))]),
        ..EditTestHost::default()
    });
    let provider = TwigCommandProvider::new(
        &path_string(root.path()),
        Some(extensions.clone()),
        None,
        host.clone(),
    );
    let text_uri = file_uri(&path_string(
        &root
            .path()
            .join("vendor/Resources/views/storefront/page.html.twig"),
    ));
    let args = json!({"textUri": text_uri, "blockName": "page_content", "extension": "DemoPlugin"});
    let result = provider
        .extend_block(&Context::background(), Some(&args))
        .unwrap();
    assert!(!result["edit"].is_null());
    assert_eq!(result["uri"], json!(uri));
    let plan = host.plan.lock().unwrap().clone().unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].version, Some(3));
    let content = plan.documents[0].apply().unwrap();
    assert!(content.contains("{# unsaved ä #}"));
    assert!(content.contains("{% block page_content %}"));
    assert!(!std::path::Path::new(&target).exists());

    // A missing extension and an existing block report Shopware errors.
    let missing = json!({"textUri": text_uri, "blockName": "x", "extension": "Nope"});
    assert_eq!(
        provider
            .extend_block(&Context::background(), Some(&missing))
            .unwrap(),
        json!({"code": "extension.not_found", "message": "Extension not found"})
    );
    extensions.close().unwrap();
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

// --- sjson / pretty --------------------------------------------------------

#[test]
fn sjson_set_matches_go() {
    let cases = [
        ("{}", "a", "x", r#"{"a":"x"}"#),
        ("", "a.b", "x", r#"{"a":{"b":"x"}}"#),
        (r#"{"a":{"b":"old"}}"#, "a.b", "new", r#"{"a":{"b":"new"}}"#),
        (r#"{"a":{"c":1}}"#, "a.b", "x", r#"{"a":{"c":1,"b":"x"}}"#),
        (r#"{"a":"str"}"#, "a.b", "x", r#"{"a":{"b":"x"}}"#),
        (r#"{ "a" : 1 }"#, "b", "<&>", r#"{ "a" : 1 ,"b":"<&>"}"#),
        (r#"{"a.b":1}"#, r"a\.b", "x", r#"{"a.b":"x"}"#),
        (r#"{"a":[1]}"#, "a.2", "x", r#"{"a":[1,null,"x"]}"#),
        (r#"{"a":[1]}"#, "a.-1", "x", r#"{"a":[1,"x"]}"#),
        ("{}", "a.0", "x", r#"{"a":["x"]}"#),
        ("{}", "a*", "x", "{}"),
        ("{}", "k", "line\nä\"", r#"{"k":"line\nä\""}"#),
    ];
    for (json, path, value, expected) in cases {
        assert_eq!(
            crate::sjson::set(json, path, value).unwrap(),
            expected,
            "{json} {path}"
        );
    }
    assert_eq!(
        crate::sjson::set(r#"{"a":[1]}"#, "a.b", "x").unwrap_err().0,
        "cannot set array element for non-numeric key 'b'"
    );
    assert_eq!(
        crate::sjson::set("{}", "", "x").unwrap_err().0,
        "path cannot be empty"
    );
}

#[test]
fn pretty_matches_go() {
    let pretty = |value: &str| String::from_utf8(crate::pretty::pretty(value.as_bytes())).unwrap();
    assert_eq!(pretty("{}"), "{}\n");
    assert_eq!(
        pretty(r#"{"a":{"b":"x","c":[1,2,{"d":true}]},"e":[1, 2,3],"f":[]}"#),
        "{\n  \"a\": {\n    \"b\": \"x\",\n    \"c\": [\n      1,\n      2,\n      {\n        \"d\": true\n      }\n    ]\n  },\n  \"e\": [1, 2, 3],\n  \"f\": []\n}\n"
    );
}

/// Differential replay against Go: `SJSON_ORACLE_CASES` holds JSON lines
/// `{Json, Path, Value}`, `SJSON_ORACLE_GO` the Go results.
#[test]
#[ignore]
fn sjson_oracle_replay() {
    let cases = std::fs::read_to_string(std::env::var("SJSON_ORACLE_CASES").unwrap()).unwrap();
    let go = std::fs::read_to_string(std::env::var("SJSON_ORACLE_GO").unwrap()).unwrap();
    let mut diffs = 0;
    let mut total = 0;
    for (case, expected) in cases.lines().zip(go.lines()) {
        total += 1;
        let case: Value = serde_json::from_str(case).unwrap();
        let expected: String = serde_json::from_str(expected).unwrap();
        let actual = match crate::sjson::set(
            case["Json"].as_str().unwrap(),
            case["Path"].as_str().unwrap(),
            case["Value"].as_str().unwrap(),
        ) {
            Ok(value) => String::from_utf8(crate::pretty::pretty(value.as_bytes())).unwrap(),
            Err(error) => format!("ERR:{error}"),
        };
        if actual != expected {
            diffs += 1;
            if diffs < 5 {
                eprintln!("case {case}\n go {expected:?}\n rs {actual:?}");
            }
        }
    }
    eprintln!("{total} cases, {diffs} diffs");
    assert_eq!(diffs, 0);
}
