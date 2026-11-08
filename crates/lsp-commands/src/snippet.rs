//! Storefront and Administration snippet commands (port of Go
//! `snippet_command.go`).

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};
use swlsp_rewrite::WorkspacePlan;
use swlsp_snippet::{Snippet, SnippetIndexer};
use swlsp_uriutil::{file_uri, filepath};

use crate::{EditHost, EditResponse, add_replacement, decode_args, dir, target_snapshot};

/// A snippet file candidate or a value to write into one.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct SnippetFile {
    pub path: String,
    pub name: String,
    pub value: String,
}

#[derive(Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
struct FileParams {
    #[serde(rename = "fileUri")]
    file_uri: String,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct CreateParams {
    #[serde(rename = "fileUri")]
    _file_uri: String,
    #[serde(rename = "snippetKey")]
    snippet_key: String,
    snippets: Option<Vec<SnippetFile>>,
}

#[derive(Serialize)]
struct SnippetItem {
    key: String,
    text: String,
    file: String,
}

/// Lists snippets and builds snippet-creation edits.
pub struct SnippetCommandProvider {
    inner: Arc<Inner>,
}

struct Inner {
    snippets: Option<Arc<SnippetIndexer>>,
    host: Arc<dyn EditHost>,
}

impl SnippetCommandProvider {
    pub fn new(snippets: Option<Arc<SnippetIndexer>>, host: Arc<dyn EditHost>) -> Self {
        Self {
            inner: Arc::new(Inner { snippets, host }),
        }
    }

    /// Builds the edit adding `key` to every listed file (Go
    /// `createSnippets`).
    pub fn create_snippets(
        &self,
        ctx: &Context,
        key: &str,
        snippets: &[SnippetFile],
    ) -> Result<EditResponse, BoxError> {
        self.inner.create_snippets(ctx, key, snippets)
    }
}

impl CommandProvider for SnippetCommandProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let mut result: BTreeMap<String, CommandFunc> = BTreeMap::new();
        let mut add =
            |name: &str,
             handler: fn(&Inner, &Context, Option<&Value>) -> Result<Value, BoxError>| {
                let inner = self.inner.clone();
                result.insert(
                    name.to_owned(),
                    Arc::new(move |ctx, args| handler(&inner, ctx, args)),
                );
            };
        add(
            "shopware/snippet/storefront/getPossibleSnippetFiles",
            Inner::get_possible_snippets,
        );
        add("shopware/snippet/storefront/create", |inner, ctx, args| {
            inner.create_snippet(ctx, args, "createSnippet")
        });
        add("shopware/snippet/storefront/all", |inner, _, _| {
            inner.all_snippets(false)
        });
        add("shopware/snippet/admin/all", |inner, _, _| {
            inner.all_snippets(true)
        });
        add(
            "shopware/snippet/admin/getPossibleSnippetFiles",
            Inner::get_possible_admin_snippets,
        );
        add("shopware/snippet/admin/create", |inner, ctx, args| {
            inner.create_snippet(ctx, args, "createAdminSnippet")
        });
        result
    }
}

impl Inner {
    fn all_snippets(&self, admin: bool) -> Result<Value, BoxError> {
        let Some(index) = &self.snippets else {
            return Err("snippet index is unavailable".into());
        };
        let snippets: Arc<[Snippet]> = if admin {
            index
                .all_admin_snippets()
                .map_err(|error| format!("failed to get admin snippets: {error}"))?
        } else {
            index
                .all_frontend_snippets()
                .map_err(|error| format!("failed to get storefront snippets: {error}"))?
        };
        let mut all: HashMap<&str, &Snippet> = HashMap::new();
        for snippet in snippets.iter() {
            all.entry(snippet.key.as_str()).or_insert(snippet);
            let file_name = filepath::base(&snippet.file);
            let preferred = if admin {
                file_name.contains("en-GB") || file_name.contains("en.json")
            } else {
                file_name.contains("en_GB")
            };
            if preferred {
                all.insert(snippet.key.as_str(), snippet);
            }
        }
        if all.is_empty() {
            return Ok(Value::Null);
        }
        let mut items: Vec<SnippetItem> = all
            .into_values()
            .map(|snippet| SnippetItem {
                key: snippet.key.clone(),
                text: snippet.text.clone(),
                file: snippet.file.clone(),
            })
            .collect();
        items.sort_by(|left, right| left.key.cmp(&right.key));
        crate::to_value(&items)
    }

    fn get_possible_snippets(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<Value, BoxError> {
        let params: FileParams = decode_args(args)
            .map_err(|error| format!("invalid arguments for getPossibleSnippets: {error}"))?;
        let file_path = swlsp_uriutil::path(&params.file_uri)
            .map_err(|error| format!("resolve document URI: {error}"))?;
        let mut dir_path = dir(&file_path);
        let mut found = false;
        loop {
            if filepath::base(&dir_path) == "Resources" {
                found = true;
                break;
            }
            let parent = dir(&dir_path);
            if parent == dir_path {
                break;
            }
            dir_path = parent;
        }
        if !found {
            return Err(format!(
                "resources directory not found in any parent directory of {file_path}"
            )
            .into());
        }
        let snippet_dir = filepath::join(&[dir_path.as_str(), "snippet"]);
        let mut possible = self.possible_snippets(ctx, &snippet_dir, false)?;
        if possible.is_empty() {
            possible = vec![SnippetFile {
                path: filepath::join(&[snippet_dir.as_str(), "en_GB", "storefront.en-GB.json"]),
                name: "storefront.en-GB.json".to_owned(),
                value: String::new(),
            }];
        }
        Ok(json!({ "paths": possible }))
    }

    fn get_possible_admin_snippets(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<Value, BoxError> {
        let params: FileParams = decode_args(args)
            .map_err(|error| format!("invalid arguments for getPossibleAdminSnippets: {error}"))?;
        let file_path = swlsp_uriutil::path(&params.file_uri)
            .map_err(|error| format!("resolve document URI: {error}"))?;
        let mut dir_path = dir(&file_path);
        let mut resources_dir = None;
        loop {
            if filepath::base(&dir_path) == "administration" {
                let parent = dir(&dir_path);
                if filepath::base(&parent) == "app" {
                    let grandparent = dir(&parent);
                    if filepath::base(&grandparent) == "Resources" {
                        resources_dir = Some(grandparent);
                        break;
                    }
                }
            }
            let parent = dir(&dir_path);
            if parent == dir_path {
                break;
            }
            dir_path = parent;
        }
        let Some(resources_dir) = resources_dir else {
            return Err(format!(
                "resources/app/administration directory not found in any parent directory of {file_path}"
            )
            .into());
        };
        let src_dir = filepath::join(&[resources_dir.as_str(), "app", "administration", "src"]);
        let mut possible = self.possible_snippets(ctx, &src_dir, true)?;
        if possible.is_empty() {
            let snippet_dir = filepath::join(&[src_dir.as_str(), "snippet"]);
            possible = vec![SnippetFile {
                path: filepath::join(&[snippet_dir.as_str(), "en-GB.json"]),
                name: "en-GB.json".to_owned(),
                value: String::new(),
            }];
        }
        Ok(json!({ "paths": possible }))
    }

    fn create_snippet(
        &self,
        ctx: &Context,
        args: Option<&Value>,
        operation: &str,
    ) -> Result<Value, BoxError> {
        let params: CreateParams = decode_args(args)
            .map_err(|error| format!("invalid arguments for {operation}: {error}"))?;
        let response = self.create_snippets(
            ctx,
            &params.snippet_key,
            params.snippets.as_deref().unwrap_or_default(),
        )?;
        crate::to_value(&response)
    }

    fn create_snippets(
        &self,
        ctx: &Context,
        key: &str,
        snippets: &[SnippetFile],
    ) -> Result<EditResponse, BoxError> {
        let mut plan = WorkspacePlan::default();
        for snippet in snippets {
            let uri = file_uri(&snippet.path);
            let snapshot = target_snapshot(ctx, self.host.as_ref(), &uri)?;
            let source = snapshot
                .as_ref()
                .map_or("{}", |snapshot| snapshot.document.source());
            let content = crate::sjson::set(source, key, &snippet.value)
                .map_err(|error| format!("set snippet {key}: {error}"))?;
            let formatted = crate::pretty::pretty(content.as_bytes());
            add_replacement(
                &mut plan,
                &uri,
                snapshot.as_ref(),
                &String::from_utf8_lossy(&formatted),
            )?;
        }
        let edit = self.host.workspace_edit(ctx, &plan)?;
        Ok(EditResponse { edit: Some(edit) })
    }

    fn possible_snippets(
        &self,
        ctx: &Context,
        directory: &str,
        admin: bool,
    ) -> Result<Vec<SnippetFile>, BoxError> {
        let paths = self.host.resource_paths(ctx, directory, 10_000)?;
        let mut result: Vec<SnippetFile> = paths
            .into_iter()
            .filter(|path| {
                filepath::ext(path) == ".json"
                    && (!admin || filepath::base(&dir(path)) == "snippet")
            })
            .map(|path| SnippetFile {
                name: filepath::base(&path),
                path,
                value: String::new(),
            })
            .collect();
        let preferred = |file: &SnippetFile| {
            file.path.contains("en_GB") || file.name.contains("en-GB") || file.name == "en.json"
        };
        result.sort_by(|left, right| {
            preferred(right)
                .cmp(&preferred(left))
                .then_with(|| left.path.cmp(&right.path))
        });
        Ok(result)
    }
}
