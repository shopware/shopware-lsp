//! Assetic named formulas read from a compiled Symfony container
//! (`var/cache/<env>/*Container.xml`).

use std::collections::HashSet;
use std::sync::Mutex;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use swlsp_parser_xml::query as xml;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::model::{Kind, Resource};
use crate::paths::{clean, is_abs, join};

const REFRESH_INTERVAL: Duration = Duration::from_millis(500);
const CONFIGURATION_RESOURCE: &str =
    "Symfony\\Bundle\\AsseticBundle\\Factory\\Resource\\ConfigurationResource";

#[derive(Default)]
struct State {
    checked_at: Option<Instant>,
    fingerprint: String,
    cached: Vec<Resource>,
}

pub(crate) struct AsseticCatalog {
    root: String,
    state: Mutex<State>,
}

struct Candidate {
    path: String,
    dev: bool,
    modified: SystemTime,
    size: u64,
}

impl AsseticCatalog {
    pub(crate) fn new(root: &str) -> Self {
        Self {
            root: clean(root),
            state: Mutex::new(State::default()),
        }
    }

    /// Named formulas, rechecked at most every 500 ms unless `force`d and
    /// reparsed only when the candidate containers changed.
    pub(crate) fn resources(&self, force: bool) -> Vec<Resource> {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if !force
            && state
                .checked_at
                .is_some_and(|checked_at| checked_at.elapsed() < REFRESH_INTERVAL)
        {
            return state.cached.clone();
        }
        state.checked_at = Some(Instant::now());
        let candidates = find_candidates(&self.root);
        if candidates.is_empty() {
            state.fingerprint.clear();
            state.cached.clear();
            return Vec::new();
        }
        let fingerprint = candidates_fingerprint(&candidates);
        if fingerprint == state.fingerprint {
            return state.cached.clone();
        }
        let mut resources = Vec::new();
        let mut seen = HashSet::new();
        for candidate in &candidates {
            let Ok(content) = std::fs::read(&candidate.path) else {
                continue;
            };
            let content = String::from_utf8_lossy(&content);
            if !content.contains("assetic.asset_manager")
                || !content.contains("ConfigurationResource")
            {
                continue;
            }
            for resource in parse_named_assets(&self.root, &candidate.path, &content) {
                let key = (
                    resource.name.to_lowercase(),
                    clean(&resource.target),
                    resource.range,
                );
                if seen.insert(key) {
                    resources.push(resource);
                }
            }
        }
        resources.sort_by(|left, right| {
            if !swlsp_php_types::equal_fold(&left.name, &right.name) {
                return left.name.to_lowercase().cmp(&right.name.to_lowercase());
            }
            left.target
                .cmp(&right.target)
                .then(left.range.start().cmp(&right.range.start()))
        });
        state.fingerprint = fingerprint;
        state.cached = resources.clone();
        resources
    }
}

fn find_candidates(root: &str) -> Vec<Candidate> {
    let mut result = Vec::new();
    for cache_root in [join(&[root, "var", "cache"]), join(&[root, "app", "cache"])] {
        let Ok(environments) = std::fs::read_dir(&cache_root) else {
            continue;
        };
        for environment in environments.flatten() {
            if !environment.file_type().is_ok_and(|kind| kind.is_dir()) {
                continue;
            }
            let environment_name = environment.file_name().to_string_lossy().into_owned();
            let environment_dir = join(&[cache_root.as_str(), &environment_name]);
            let Ok(files) = std::fs::read_dir(&environment_dir) else {
                continue;
            };
            for file in files.flatten() {
                let name = file.file_name().to_string_lossy().into_owned();
                if file.file_type().is_ok_and(|kind| kind.is_dir())
                    || !name.ends_with("Container.xml")
                {
                    continue;
                }
                let Ok(metadata) = file.metadata() else {
                    continue;
                };
                result.push(Candidate {
                    path: join(&[environment_dir.as_str(), &name]),
                    dev: environment_name.to_lowercase().starts_with("dev"),
                    modified: metadata.modified().unwrap_or(UNIX_EPOCH),
                    size: metadata.len(),
                });
            }
        }
    }
    // Development containers first, then the newest, then by path.
    result.sort_by(|left, right| {
        right
            .dev
            .cmp(&left.dev)
            .then(right.modified.cmp(&left.modified))
            .then_with(|| left.path.cmp(&right.path))
    });
    result
}

fn candidates_fingerprint(candidates: &[Candidate]) -> String {
    let mut value = String::new();
    for candidate in candidates {
        let nanos = candidate
            .modified
            .duration_since(UNIX_EPOCH)
            .map_or(0, |duration| duration.as_nanos());
        value.push_str(&format!(
            "{}\0{nanos}\0{}\n",
            candidate.path, candidate.size
        ));
    }
    value
}

pub(crate) fn parse_named_assets(root: &str, path: &str, content: &str) -> Vec<Resource> {
    let tree = swlsp_parser_xml::parse(content).tree;
    let mut result = Vec::new();
    for manager in xml::elements(&tree.root(), &["service"]) {
        if attribute_value(&manager, "id") != "assetic.asset_manager" {
            continue;
        }
        for call in xml::child_elements(&manager, &["call"]) {
            if attribute_value(&call, "method") != "addResource" {
                continue;
            }
            for configuration in xml::elements(&call, &["service"]) {
                if attribute_value(&configuration, "class") != CONFIGURATION_RESOURCE {
                    continue;
                }
                let Some(formulas) = xml::child_elements(&configuration, &["argument"])
                    .into_iter()
                    .next()
                else {
                    continue;
                };
                for formula in xml::child_elements(&formulas, &["argument"]) {
                    let name_attribute = xml::attribute(&formula, "key");
                    let name = name_attribute
                        .as_ref()
                        .and_then(xml::attribute_value)
                        .unwrap_or_default();
                    let name = name.trim();
                    if name.is_empty() {
                        continue;
                    }
                    let range = attribute_range(name_attribute.as_ref());
                    let files = formula_files(&formula);
                    if files.is_empty() {
                        result.push(Resource {
                            name: name.to_owned(),
                            file: path.to_owned(),
                            kind: Kind::AsseticNamedAsset,
                            range,
                            ..Resource::default()
                        });
                        continue;
                    }
                    for file in files {
                        result.push(Resource {
                            name: name.to_owned(),
                            file: path.to_owned(),
                            target: resolve_target(root, &file),
                            kind: Kind::AsseticNamedAsset,
                            range,
                            ..Resource::default()
                        });
                    }
                }
            }
        }
    }
    result
}

fn attribute_value(element: &SyntaxNode, name: &str) -> String {
    xml::attribute(element, name)
        .and_then(|attribute| xml::attribute_value(&attribute))
        .unwrap_or_default()
}

/// Leaf `<argument>` values of the formula's first (inputs) argument.
fn formula_files(formula: &SyntaxNode) -> Vec<String> {
    let Some(inputs) = xml::child_elements(formula, &["argument"])
        .into_iter()
        .next()
    else {
        return Vec::new();
    };
    xml::elements(&inputs, &["argument"])
        .iter()
        .filter(|argument| xml::child_elements(argument, &["argument"]).is_empty())
        .map(|argument| xml::text_content(argument).trim().to_owned())
        .filter(|value| !value.is_empty())
        .collect()
}

/// An existing file for an absolute or root-relative formula input; leading
/// `../` segments are retried without them.
fn resolve_target(root: &str, value: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        return String::new();
    }
    let is_file = |path: &str| std::fs::metadata(path).is_ok_and(|info| !info.is_dir());
    if is_abs(value) {
        return if is_file(value) {
            clean(value)
        } else {
            String::new()
        };
    }
    let normalized = value.replace('\\', "/");
    let mut candidates = vec![join(&[root, normalized.as_str()])];
    let mut trimmed = normalized.as_str();
    while let Some(rest) = trimmed.strip_prefix("../") {
        trimmed = rest;
    }
    if trimmed != normalized {
        candidates.push(join(&[root, trimmed]));
    }
    candidates
        .into_iter()
        .find(|candidate| is_file(candidate))
        .map(|candidate| clean(&candidate))
        .unwrap_or_default()
}

/// The attribute value's range without quotes.
fn attribute_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let mut range = node.range_trimmed_trivia();
    let full = node.text().to_string();
    let text = full.trim();
    if let Some(separator) = text.find('=') {
        let value = text[separator + 1..].trim();
        if let Some(start) = full.find(value) {
            let start = node.text_range().start() + TextSize::from(start as u32);
            range = TextRange::at(start, TextSize::from(value.len() as u32));
        }
    }
    if u32::from(range.len()) >= 2 && (text.ends_with('"') || text.ends_with('\'')) {
        range = TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}
