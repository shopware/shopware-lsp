//! Completion in a module description: the keys and values its schema
//! allows at the cursor, and the entities, properties and locales of the
//! plugin where the schema asks for a name.

use std::collections::BTreeSet;

use swlsp_ide::completion::common::{protocol_range, text_edit};
use swlsp_ide::completion::component_util::request_offset;
use swlsp_language::LanguageId;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_parser_yaml::query as yaml;
use swlsp_parser_yaml::syntax::YAML_PAIR;
use swlsp_shopware_adminmodule::{Names, Schema, Suggestion};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::completion_flow::flow_place;
use super::{Generator, plugin_root};

const LOCALES: &[&str] = &["en-GB", "de-DE"];
const DEFAULT_STEP: usize = 2;

pub struct AdminModuleCompletionProvider {
    generator: Generator,
}

impl AdminModuleCompletionProvider {
    pub(crate) fn new(generator: Generator) -> Self {
        Self { generator }
    }
}

/// Where the cursor is in a description.
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Place {
    /// The keys of the mapping the cursor is in; with the key of the line
    /// when a value is completed.
    pub(crate) path: Vec<String>,
    /// Whether the value of a key is completed, not a key.
    pub(crate) value: bool,
    /// What the completion replaces.
    pub(crate) range: TextRange,
    /// The keys the mapping holds on other lines.
    pub(crate) existing: BTreeSet<String>,
    /// The indentation of the line, and of one level.
    pub(crate) indent: usize,
    pub(crate) step: usize,
    /// Whether a colon follows the key under the cursor.
    pub(crate) complete: bool,
    /// Whether the cursor is in a flow mapping (`{ … }`), where a key is
    /// followed by its value on the same line.
    pub(crate) flow: bool,
}

/// The column of `offset` in its line.
fn column(source: &str, offset: usize) -> usize {
    offset - source[..offset].rfind('\n').map_or(0, |index| index + 1)
}

pub(super) fn key_start(pair: &SyntaxNode) -> Option<usize> {
    let token = yaml::pair_key(pair).and_then(|key| yaml::scalar_token(&key))?;
    Some(usize::from(token.text_range().start()))
}

pub(super) fn key_name(pair: &SyntaxNode) -> String {
    yaml::pair_key(pair)
        .map(|key| yaml::scalar_value(&key))
        .unwrap_or_default()
}

pub(crate) fn place(source: &str, root: &SyntaxNode, offset: usize) -> Option<Place> {
    if offset > source.len() || !source.is_char_boundary(offset) {
        return None;
    }
    if let Some(place) = flow_place(source, root, offset) {
        return place;
    }
    let line_start = source[..offset].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[offset..]
        .find('\n')
        .map_or(source.len(), |index| offset + index);
    let before = &source[line_start..offset];
    let indent = before.len() - before.trim_start_matches(' ').len();
    let typed = &before[indent..];
    if typed.starts_with(['#', '-', '{', '[', '\t']) {
        return None;
    }
    // The pair the line belongs to: the nearest one above that is
    // indented less.
    let pairs = yaml::nodes(root, &[YAML_PAIR]);
    let parent = pairs.iter().rev().find(|pair| {
        key_start(pair).is_some_and(|start| start < line_start && column(source, start) < indent)
    });
    let mut path = match parent {
        Some(pair) => {
            let mut path = yaml::pair_path(pair);
            if path.last().is_none_or(|last| *last != key_name(pair)) {
                path.push(key_name(pair));
            }
            path
        }
        None => Vec::new(),
    };
    let step = parent
        .and_then(key_start)
        .map(|start| indent - column(source, start))
        .unwrap_or(DEFAULT_STEP);
    let size = |index: usize| TextSize::from(index as u32);
    if let Some(colon) = typed
        .find(": ")
        .or_else(|| typed.strip_suffix(':').map(str::len))
    {
        // A value: from the first character after the colon to the end of
        // the word under the cursor.
        let key = typed[..colon].trim().trim_matches(['"', '\'']);
        if key.is_empty() || key.contains(' ') {
            return None;
        }
        let after = line_start + indent + colon + 1;
        let start =
            after + (source[after..offset].len() - source[after..offset].trim_start().len());
        if source[start..offset].contains(' ') {
            return None;
        }
        let end = offset
            + source[offset..line_end]
                .find(char::is_whitespace)
                .unwrap_or(line_end - offset);
        path.push(key.to_owned());
        return Some(Place {
            path,
            value: true,
            range: TextRange::new(size(start), size(end)),
            existing: BTreeSet::new(),
            indent,
            step,
            complete: false,
            flow: false,
        });
    }
    if typed.contains(' ') {
        return None;
    }
    let rest = &source[offset..line_end];
    let word = rest
        .find(|character: char| character == ':' || character.is_whitespace())
        .unwrap_or(rest.len());
    let start = line_start + indent;
    let existing = pairs
        .iter()
        .filter(|pair| {
            let line = key_start(pair).is_none_or(|key| (line_start..=line_end).contains(&key));
            !line && yaml::pair_path(pair).strip_suffix(&[key_name(pair)]) == Some(&path[..])
        })
        .map(key_name)
        .collect();
    Some(Place {
        path,
        value: false,
        range: TextRange::new(size(start), size(offset + word)),
        existing,
        indent,
        step,
        complete: rest[word..].starts_with(':'),
        flow: false,
    })
}

/// The scalar at `path` of the description.
fn scalar(root: &SyntaxNode, path: &[String]) -> Option<String> {
    let (last, parents) = path.split_last()?;
    let mut mapping = yaml::root_value(root)?;
    for key in parents {
        mapping = yaml::pair_value(&yaml::property_pair(&mapping, key)?)?;
    }
    let value = yaml::pair_value(&yaml::property_pair(&mapping, last)?)?;
    let text = yaml::scalar_value(&value);
    (!text.is_empty()).then_some(text)
}

impl AdminModuleCompletionProvider {
    /// The names of the project `names` asks for at `path`.
    fn names(&self, names: Names, plugin: &str, root: &SyntaxNode, path: &[String]) -> Vec<String> {
        match names {
            Names::Locales => LOCALES.iter().map(|locale| (*locale).to_owned()).collect(),
            Names::Entities => self
                .generator
                .entities(plugin)
                .into_iter()
                .map(|entity| entity.name)
                .collect(),
            Names::Fields => {
                // `modules.<name>.entity` of the module the cursor is in.
                let module = path.get(..2).unwrap_or_default();
                let entity = scalar(root, &[module, &["entity".to_owned()]].concat());
                self.generator
                    .entities(plugin)
                    .into_iter()
                    .find(|candidate| Some(&candidate.name) == entity.as_ref())
                    .map(|entity| entity.fields.into_iter().map(|field| field.name).collect())
                    .unwrap_or_default()
            }
        }
    }
}

impl CompletionProvider for AdminModuleCompletionProvider {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        if request.language != Some(LanguageId::Yaml) {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        let Some(plugin) = plugin_root(&filepath::clean(&path)) else {
            return Vec::new();
        };
        let (Some(root), Some(line_index), Some(offset)) = (
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Vec::new();
        };
        let Some(place) = place(request.source(), root, offset as usize) else {
            return Vec::new();
        };
        let lookup = |path: &[String]| scalar(root, path);
        let schema = Schema::bundled();
        let suggestions = if place.value {
            schema.values(&place.path, &lookup)
        } else {
            schema.keys(&place.path, &lookup)
        };
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let named = suggestions
            .names
            .map(|names| (names, self.names(names, &plugin, root, &place.path)))
            .into_iter()
            .flat_map(|(names, labels)| {
                let nested = suggestions.names_nested;
                labels.into_iter().map(move |label| {
                    let kind = match names {
                        Names::Entities => CompletionItemKind::CLASS,
                        Names::Fields => CompletionItemKind::FIELD,
                        Names::Locales => CompletionItemKind::CONSTANT,
                    };
                    let item = Suggestion {
                        label,
                        nested,
                        ..Suggestion::default()
                    };
                    (item, kind)
                })
            });
        let kind = if place.value {
            CompletionItemKind::VALUE
        } else {
            CompletionItemKind::PROPERTY
        };
        let range = protocol_range(place.range, line_index);
        let below = " ".repeat(place.indent + place.step);
        suggestions
            .items
            .iter()
            .cloned()
            .map(|item| (item, kind))
            .chain(named)
            .filter(|(item, _)| place.value || !place.existing.contains(&item.label))
            .map(|(item, kind)| {
                let text = match (place.value, place.complete, item.nested) {
                    (true, ..) | (false, true, _) => item.label.clone(),
                    (false, false, true) if !place.flow => format!("{}:\n{below}", item.label),
                    (false, false, _) => format!("{}: ", item.label),
                };
                protocol::CompletionItem {
                    label: item.label,
                    kind: Some(kind),
                    detail: item.description,
                    text_edit: Some(text_edit(range, text)),
                    ..Default::default()
                }
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}

#[cfg(test)]
#[path = "completion_tests.rs"]
mod tests;
