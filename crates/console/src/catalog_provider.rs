use std::collections::HashMap;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_lsp::{BoxError, Context};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::index::Index;
use crate::model::{Command, Input};

pub const LIST_CATALOG_COMMAND: &str = "shopware/symfony/console/commands";

const CATALOG_LIMIT: usize = 2_000;

/// Lists indexed console commands for the editor command palette and MCP.
pub struct CatalogProvider {
    index: Arc<Index>,
    root: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CatalogRequest {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub query: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_glob: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct CatalogInput {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub shortcut: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mode: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub default: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CatalogEntry {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub canonical: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub class: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub method: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_path: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub arguments: Vec<CatalogInput>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub options: Vec<CatalogInput>,
}

impl CatalogProvider {
    /// `workspace_root` makes entries carry a root-relative `filePath` and
    /// enables `fileGlob` filtering.
    pub fn new(index: Arc<Index>, workspace_root: Option<&str>) -> Self {
        Self {
            index,
            root: workspace_root.map(filepath::clean).unwrap_or_default(),
        }
    }

    pub fn catalog(&self, context: &Context, query: &str) -> Result<Vec<CatalogEntry>, BoxError> {
        self.catalog_with_request(
            context,
            &CatalogRequest {
                query: query.to_owned(),
                ..CatalogRequest::default()
            },
        )
    }

    /// One entry per case-folded command name (the most informative
    /// declaration wins), sorted case-insensitively and capped.
    pub fn catalog_with_request(
        &self,
        context: &Context,
        request: &CatalogRequest,
    ) -> Result<Vec<CatalogEntry>, BoxError> {
        context.check()?;
        let commands = self.index.get_commands()?;
        let query = lowercase(request.query.trim());
        let file_glob = request.file_glob.trim();
        let mut entries: HashMap<String, CatalogEntry> = HashMap::new();
        for command in &commands {
            context.check()?;
            if command.name.is_empty() || !command_matches(command, &query) {
                continue;
            }
            let file_path = command_file_path(&self.root, &command.file);
            if !file_glob.is_empty()
                && (file_path.is_empty() || !swlsp_pathmatch::ant(file_glob, &file_path))
            {
                continue;
            }
            let entry = catalog_entry(command, file_path);
            let key = lowercase(&command.name);
            match entries.get(&key) {
                Some(current) if entry_score(&entry) <= entry_score(current) => {}
                _ => {
                    entries.insert(key, entry);
                }
            }
        }
        let mut result: Vec<(String, CatalogEntry)> = entries.into_iter().collect();
        result.sort_by(|left, right| left.0.cmp(&right.0));
        result.truncate(CATALOG_LIMIT);
        Ok(result.into_iter().map(|(_, entry)| entry).collect())
    }
}

fn command_matches(command: &Command, query: &str) -> bool {
    if query.is_empty() {
        return true;
    }
    let haystack = lowercase(
        &[
            command.name.as_str(),
            &command.canonical,
            &command.description,
            &command.class,
            &command.method,
        ]
        .join("\n"),
    );
    haystack.contains(query)
}

fn catalog_entry(command: &Command, file_path: String) -> CatalogEntry {
    CatalogEntry {
        name: command.name.clone(),
        canonical: command.canonical.clone(),
        description: command.description.clone(),
        class: command.class.clone(),
        method: command.method.clone(),
        file_uri: if command.file.is_empty() {
            String::new()
        } else {
            swlsp_uriutil::file_uri(&command.file)
        },
        file_path,
        arguments: catalog_inputs(&command.arguments),
        options: catalog_inputs(&command.options),
    }
}

/// The slash-separated path relative to the workspace root, or empty when
/// the file lies outside it.
fn command_file_path(root: &str, path: &str) -> String {
    if root.is_empty() || path.is_empty() {
        return String::new();
    }
    match filepath::rel(root, path) {
        Ok(relative)
            if relative != ".."
                && !relative.starts_with(&format!("..{}", std::path::MAIN_SEPARATOR)) =>
        {
            filepath::to_slash(&relative)
        }
        _ => String::new(),
    }
}

fn catalog_inputs(inputs: &[Input]) -> Vec<CatalogInput> {
    inputs
        .iter()
        .filter(|input| !input.name.is_empty())
        .map(|input| CatalogInput {
            name: input.name.clone(),
            shortcut: input.shortcut.clone(),
            mode: input.mode.clone(),
            description: input.description.clone(),
            default: input.default.clone(),
        })
        .collect()
}

fn entry_score(entry: &CatalogEntry) -> u32 {
    u32::from(!entry.file_uri.is_empty())
        + u32::from(!entry.file_path.is_empty())
        + 2 * u32::from(!entry.class.is_empty())
        + u32::from(!entry.method.is_empty())
        + u32::from(entry.arguments.len() + entry.options.len() != 0)
}
