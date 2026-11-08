//! Bundle (`@AcmeBundle/...`) and directory-scoped routing/service resource
//! completion in YAML, XML and PHP configuration (port of Go
//! `bundle_resource_completion.go`).

use std::collections::HashSet;
use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::{IndexedResourcePaths, RouteResourceResolver};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::{lower_ext, request_offset};
use crate::common::{leading_yaml_indent, protocol_range, text_edit};

const MAX_LOCAL_RESOURCE_CANDIDATES: usize = 500;

static YAML_RESOURCE_PREFIX: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?:^|[-,{])[ \t]*resource[ \t]*:[ \t]*(['"]?)"#).expect("valid regex")
});
static YAML_PATH_PREFIX: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?:^|[-,{])[ \t]*path[ \t]*:[ \t]*(['"]?)"#).expect("valid regex")
});
static XML_RESOURCE_PREFIX: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"(?-u:\b)resource[ \t]*=[ \t]*(['"])"#).expect("valid regex"));

/// Completes bundle and directory-scoped resource paths (Go
/// `BundleResourceCompletionProvider`).
pub struct BundleResourceCompletionProvider {
    resolver: RouteResourceResolver,
    paths: Option<Arc<dyn IndexedResourcePaths>>,
}

impl BundleResourceCompletionProvider {
    pub fn new(php_index: Option<PhpIndex>, paths: Option<Arc<dyn IndexedResourcePaths>>) -> Self {
        Self {
            resolver: RouteResourceResolver::new(php_index, paths.clone()),
            paths,
        }
    }
}

impl CompletionProvider for BundleResourceCompletionProvider {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(range) = bundle_resource_completion_range(
            request.source(),
            offset,
            &lower_ext(&path),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        let edit_range = protocol_range(range, line_index);
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        let cancel = ctx.token().indexer_token();
        for candidate in self.resolver.bundle_resource_candidates(&cancel) {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            let label = candidate
                .value
                .strip_prefix('@')
                .unwrap_or(&candidate.value)
                .to_string();
            append_resource_completion(
                &mut items,
                &mut seen,
                label,
                &candidate.value,
                format!("Symfony bundle resource · {}", candidate.path),
                protocol::CompletionItemKind::FILE,
                edit_range,
            );
        }
        for candidate in local_resource_candidates(ctx, self.paths.as_deref(), &path) {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            let kind = if candidate.directory {
                protocol::CompletionItemKind::FOLDER
            } else {
                protocol::CompletionItemKind::FILE
            };
            append_resource_completion(
                &mut items,
                &mut seen,
                candidate.value.clone(),
                &candidate.value,
                "Resource relative to this configuration file".to_string(),
                kind,
                edit_range,
            );
        }
        // Labels can repeat (a bundle label equal to a local path); detail
        // and edit text settle such ties independently of input order.
        items.sort_unstable_by(crate::common::compare_completion_items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["@", "/", "\\", "'", "\""].map(String::from).to_vec()
    }
}

fn append_resource_completion(
    items: &mut Vec<protocol::CompletionItem>,
    seen: &mut HashSet<String>,
    label: String,
    value: &str,
    detail: String,
    kind: protocol::CompletionItemKind,
    edit_range: protocol::Range,
) {
    if value.is_empty() || !seen.insert(value.to_string()) {
        return;
    }
    items.push(protocol::CompletionItem {
        filter_text: format!("{value} {label}"),
        label,
        kind: Some(kind),
        detail,
        text_edit: Some(text_edit(edit_range, value)),
        ..Default::default()
    });
}

fn bundle_resource_completion_range(
    source: &str,
    offset: u32,
    extension: &str,
    node: Option<&SyntaxNode>,
) -> Option<TextRange> {
    let cursor = offset as usize;
    if cursor > source.len() || !source.is_char_boundary(cursor) {
        return None;
    }
    match extension {
        ".yaml" | ".yml" => yaml_bundle_resource_completion_range(source, cursor),
        ".xml" => xml_bundle_resource_completion_range(source, cursor),
        ".php" => swlsp_symfony::php_route_resource_completion_range_at(node?),
        _ => None,
    }
}

fn text_range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

fn yaml_bundle_resource_completion_range(source: &str, cursor: usize) -> Option<TextRange> {
    let line_start = source[..cursor].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[cursor..]
        .find('\n')
        .map_or(source.len(), |end| cursor + end);
    let before = &source[line_start..cursor];
    let mut captures = YAML_RESOURCE_PREFIX.captures_iter(before).last();
    if captures.is_none() && yaml_nested_resource_path_context(source, line_start, before) {
        captures = YAML_PATH_PREFIX.captures_iter(before).last();
    }
    let captures = captures?;
    let whole = captures.get(0)?;
    let start = line_start + whole.end();
    let quote = captures
        .get(1)
        .filter(|group| !group.is_empty())
        .map_or(0, |group| before.as_bytes()[group.start()]);
    let end = resource_value_end(source, start, line_end, quote);
    if cursor < start || cursor > end {
        return None;
    }
    Some(text_range(start, end))
}

fn xml_bundle_resource_completion_range(source: &str, cursor: usize) -> Option<TextRange> {
    let bytes = source.as_bytes();
    let tag_start = source[..cursor].rfind("<import")?;
    let name_end = tag_start + "<import".len();
    if name_end < bytes.len()
        && !matches!(bytes[name_end], b' ' | b'\t' | b'\r' | b'\n' | b'>' | b'/')
    {
        return None;
    }
    let mut tag_end = source.len();
    if let Some(end) = source[tag_start..].find('>') {
        tag_end = tag_start + end;
        if tag_end < cursor {
            return None;
        }
    }
    let before = &source[tag_start..cursor];
    let captures = XML_RESOURCE_PREFIX.captures_iter(before).last()?;
    let start = tag_start + captures.get(0)?.end();
    let quote = before.as_bytes()[captures.get(1)?.start()];
    let end = resource_value_end(source, start, tag_end, quote);
    if cursor < start || cursor > end {
        return None;
    }
    Some(text_range(start, end))
}

fn yaml_nested_resource_path_context(source: &str, line_start: usize, before: &str) -> bool {
    let Some(path_match) = YAML_PATH_PREFIX.find_iter(before).last() else {
        return false;
    };
    if before[..path_match.start()].contains("resource:") {
        return true;
    }
    let current_indent = leading_yaml_indent(&source[line_start..]);
    let mut search_end = line_start;
    while search_end > 0 {
        let previous_end = search_end - 1;
        let previous_start = source[..previous_end]
            .rfind('\n')
            .map_or(0, |index| index + 1);
        let line = &source[previous_start..previous_end];
        search_end = previous_start;
        if line.trim().is_empty() {
            continue;
        }
        if leading_yaml_indent(line) >= current_indent {
            continue;
        }
        let trimmed = line.trim();
        let Some(rest) = trimmed.strip_prefix("resource:") else {
            return false;
        };
        return rest.trim().is_empty();
    }
    false
}

fn resource_value_end(source: &str, start: usize, limit: usize, quote: u8) -> usize {
    let bytes = source.as_bytes();
    if quote != 0 {
        return bytes[start..limit]
            .iter()
            .position(|byte| *byte == quote)
            .map_or(limit, |end| start + end);
    }
    let mut end = start;
    while end < limit {
        match bytes[end] {
            b' ' | b'\t' | b',' | b'}' | b'#' => return end,
            _ => end += 1,
        }
    }
    end
}

struct LocalResourceCandidate {
    value: String,
    directory: bool,
}

/// Go `filepath.Dir` for slash-separated paths.
fn go_dir(path: &str) -> String {
    let volume = filepath::volume_name_len(path);
    let mut index = path.len();
    while index > volume && !filepath::is_path_separator(path.as_bytes()[index - 1]) {
        index -= 1;
    }
    let dir = filepath::clean(&path[volume..index]);
    if dir == "." && volume > 2 {
        return path[..volume].to_string();
    }
    format!("{}{}", &path[..volume], dir)
}

fn local_resource_candidates(
    ctx: &Context,
    catalog: Option<&dyn IndexedResourcePaths>,
    current_path: &str,
) -> Vec<LocalResourceCandidate> {
    let Some(catalog) = catalog else {
        return Vec::new();
    };
    let root = go_dir(current_path);
    let Ok(paths) = catalog.resource_paths(&root, MAX_LOCAL_RESOURCE_CANDIDATES) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    let current = filepath::clean(current_path);
    for path in paths {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        if filepath::clean(&path) == current {
            continue;
        }
        let Ok(relative) = filepath::rel(&root, &path) else {
            continue;
        };
        let mut value = relative;
        let mut directory = false;
        while value != "." {
            if seen.insert(value.clone()) {
                result.push(LocalResourceCandidate {
                    value: filepath::to_slash(&value),
                    directory,
                });
            }
            value = go_dir(&value);
            directory = true;
        }
    }
    // Values are unique, so a stable sort matches Go's sort.Slice.
    result.sort_by(|left, right| left.value.cmp(&right.value));
    result
}
