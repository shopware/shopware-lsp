//! Updates Twig and PHP template references when Twig files are renamed
//! (port of Go `twig_template_rename.go`).

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use swlsp_lsp::{Context, FileRenameProvider, FileRenameRequest, ProviderResult, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::LineIndex;
use swlsp_twig::{
    TemplateReference, TwigIndexer, php_template_references, template_names,
    twig_template_references,
};
use swlsp_uriutil::{file_uri, filepath};

use crate::{lower_ext, protocol_range};

pub struct TwigTemplateRenameProvider {
    index: Option<Arc<TwigIndexer>>,
}

impl TwigTemplateRenameProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>) -> Self {
        Self { index }
    }
}

struct Overlay {
    references: Vec<TemplateReference>,
}

struct EditSource {
    uri: String,
    line_index: Arc<LineIndex>,
}

impl FileRenameProvider for TwigTemplateRenameProvider {
    fn will_rename_files(
        &self,
        _ctx: &Context,
        request: &FileRenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>> {
        let Some(index) = &self.index else {
            return Ok(None);
        };
        let (overlays, mut sources) = template_reference_overlays(&request.documents);
        type EditKey = (String, (u32, u32), (u32, u32));
        let mut edits: HashMap<EditKey, protocol::TextEdit> = HashMap::new();

        for rename in &request.params.files {
            let (Ok(old_path), Ok(new_path)) = (
                swlsp_uriutil::path(&rename.old_uri),
                swlsp_uriutil::path(&rename.new_uri),
            ) else {
                continue;
            };
            if lower_ext(&old_path) != ".twig"
                || lower_ext(&new_path) != ".twig"
                || filepath::clean(&old_path) == filepath::clean(&new_path)
            {
                continue;
            }
            let old_names = unambiguous_template_names(index, &old_path)?;
            if old_names.is_empty() {
                continue;
            }
            let new_names = template_names(&new_path);
            if new_names.is_empty() {
                continue;
            }
            let mut effective: Vec<TemplateReference> = index
                .get_template_references(&old_names)?
                .into_iter()
                .filter(|reference| !overlays.contains_key(&filepath::clean(&reference.file_path)))
                .collect();
            for overlay in overlays.values() {
                effective.extend(
                    overlay
                        .references
                        .iter()
                        .filter(|reference| template_name_in(&reference.template, &old_names))
                        .cloned(),
                );
            }
            for reference in &effective {
                if !template_name_in(&reference.template, &old_names) {
                    continue;
                }
                let new_template = replacement_template_name(
                    &old_path,
                    &new_path,
                    &reference.template,
                    &new_names,
                );
                if new_template.is_empty() || new_template == reference.template {
                    continue;
                }
                let key_path = filepath::clean(&reference.file_path);
                if !sources.contains_key(&key_path) {
                    let Some(source) = load_template_edit_source(&reference.file_path) else {
                        continue;
                    };
                    sources.insert(key_path.clone(), source);
                }
                let source = &sources[&key_path];
                let range = protocol_range(&source.line_index, reference.range);
                let key = (
                    source.uri.clone(),
                    (range.start.line, range.start.character),
                    (range.end.line, range.end.character),
                );
                if let Some(existing) = edits.get(&key)
                    && existing.new_text != new_template
                {
                    return Err(format!(
                        "conflicting template rename edits for {} at {}:{}",
                        source.uri, range.start.line, range.start.character
                    )
                    .into());
                }
                edits.insert(key, protocol::TextEdit::new(range, new_template));
            }
        }

        if edits.is_empty() {
            return Ok(None);
        }
        let mut changes: BTreeMap<String, Vec<protocol::TextEdit>> = BTreeMap::new();
        for ((uri, _, _), edit) in edits {
            changes.entry(uri).or_default().push(edit);
        }
        for edits in changes.values_mut() {
            edits.sort_by_key(|edit| {
                (
                    edit.range.start.line,
                    edit.range.start.character,
                    edit.range.end.line,
                    edit.range.end.character,
                )
            });
        }
        Ok(Some(protocol::WorkspaceEdit {
            changes,
            ..protocol::WorkspaceEdit::default()
        }))
    }
}

fn unambiguous_template_names(
    index: &TwigIndexer,
    old_path: &str,
) -> Result<Vec<String>, swlsp_lsp::BoxError> {
    let clean_old = filepath::clean(old_path);
    let mut result = Vec::new();
    for name in template_names(old_path) {
        let files = index.get_twig_files_by_rel_path(&name)?;
        let mut paths: Vec<String> = files
            .iter()
            .map(|file| filepath::clean(&file.path))
            .collect();
        paths.sort();
        paths.dedup();
        if paths.len() == 1 && paths[0] == clean_old {
            result.push(name);
        }
    }
    Ok(result)
}

fn template_reference_overlays(
    documents: &[Arc<TextDocument>],
) -> (HashMap<String, Overlay>, HashMap<String, EditSource>) {
    let mut overlays = HashMap::new();
    let mut sources = HashMap::new();
    for document in documents {
        let Some(root) = document.root() else {
            continue;
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            continue;
        };
        let references = match lower_ext(&path).as_str() {
            ".twig" => twig_template_references(&path, &root),
            ".php" => php_template_references(&path, &root),
            _ => continue,
        };
        let key = filepath::clean(&path);
        sources.insert(
            key.clone(),
            EditSource {
                uri: document.uri.clone(),
                line_index: document.line_index_arc().clone(),
            },
        );
        overlays.insert(key, Overlay { references });
    }
    (overlays, sources)
}

fn load_template_edit_source(path: &str) -> Option<EditSource> {
    let source = std::fs::read(path).ok()?;
    Some(EditSource {
        uri: file_uri(path),
        line_index: Arc::new(LineIndex::new(
            String::from_utf8_lossy(&source).into_owned(),
        )),
    })
}

/// Go `filepath.Dir`.
fn dir(path: &str) -> String {
    let index = path.rfind('/').map_or(0, |index| index + 1);
    filepath::clean(&path[..index])
}

pub(crate) fn replacement_template_name(
    old_path: &str,
    new_path: &str,
    old_name: &str,
    new_names: &[String],
) -> String {
    if dir(old_path) == dir(new_path) {
        return rename_template_basename(old_name, &filepath::base(new_path));
    }
    pick_best_template_name(new_names, old_name)
}

pub(crate) fn rename_template_basename(old_name: &str, new_file_name: &str) -> String {
    let separator = match (old_name.rfind('/'), old_name.rfind(':')) {
        (None, None) => return new_file_name.to_owned(),
        (slash, colon) => slash.max(colon).expect("one separator"),
    };
    format!("{}{new_file_name}", &old_name[..=separator])
}

pub(crate) fn pick_best_template_name(new_names: &[String], old_name: &str) -> String {
    let Some(first) = new_names.first() else {
        return String::new();
    };
    let old_prefix = template_namespace_prefix(old_name);
    new_names
        .iter()
        .find(|name| template_namespace_prefix(name) == old_prefix)
        .unwrap_or(first)
        .clone()
}

fn template_namespace_prefix(name: &str) -> &str {
    if name.starts_with('@') {
        return match name.find('/') {
            Some(slash) if slash > 0 => &name[..slash],
            _ => name,
        };
    }
    match name.find(':') {
        Some(colon) => &name[..colon],
        None => "",
    }
}

fn template_name_in(template: &str, names: &[String]) -> bool {
    let template = normalize_template_name(template);
    names
        .iter()
        .any(|name| normalize_template_name(name) == template)
}

fn normalize_template_name(name: &str) -> String {
    let name = filepath::to_slash(name.trim());
    name.strip_prefix('/').map(str::to_owned).unwrap_or(name)
}
