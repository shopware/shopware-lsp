//! Symfony route option, path and requirement completion in YAML routing
//! files (port of Go `yaml_route_authoring_completion.go`).

use std::collections::HashSet;
use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_symfony::{Route, RouteIndexer};
use swlsp_syntax::{LineIndex, TextRange};

use super::yaml_service_authoring::{
    cursor_in, text_range, trim_quotes, yaml_block_mapping_keys, yaml_flow_mapping_keys,
    yaml_identifier_end, yaml_key_has_colon, yaml_line_bounds, yaml_mapping_key_line,
    yaml_next_line_start, yaml_option_prefix, yaml_parent_mapping_line,
};
use super::{lower_ext, request_offset};
use crate::common::{go_to_lower, leading_yaml_indent, protocol_range, text_edit};

use protocol::CompletionItemKind as Kind;

const YAML_ROUTE_OPTIONS: &[(&str, &str, bool)] = &[
    ("pattern", "legacy path", true),
    ("defaults", "default parameter values", false),
    ("path", "URL path", false),
    ("requirements", "parameter requirements", false),
    ("methods", "HTTP methods", false),
    ("condition", "expression", false),
    ("resource", "imported routing resource", false),
    ("prefix", "imported route prefix", false),
    ("schemes", "URL schemes", false),
    ("host", "route host", false),
    ("controller", "controller callable", false),
];

static YAML_ROUTE_PLACEHOLDER: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\{([A-Za-z0-9_]+)(?:<[^{}]*>)?\}").expect("valid regex"));

/// Completes route options, indexed route paths and requirement keys (Go
/// `YAMLRouteAuthoringCompletionProvider`).
pub struct YamlRouteAuthoringCompletionProvider {
    route_index: Option<Arc<RouteIndexer>>,
}

impl YamlRouteAuthoringCompletionProvider {
    pub fn new(route_index: Option<Arc<RouteIndexer>>) -> Self {
        Self { route_index }
    }
}

impl CompletionProvider for YamlRouteAuthoringCompletionProvider {
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
        if !yaml_routing_file(&path) {
            return Vec::new();
        }
        let extension = lower_ext(&path);
        if extension != ".yaml" && extension != ".yml" {
            return Vec::new();
        }
        let source = request.source();
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(cursor) = cursor_in(source, offset) else {
            return Vec::new();
        };
        if let Some(context) = yaml_route_option_context(source, cursor) {
            return yaml_route_option_items(&context, line_index);
        }
        if let Some(range) = yaml_route_path_context(source, cursor) {
            return self.yaml_route_path_items(ctx, range, line_index);
        }
        let Some(context) = yaml_route_requirement_context(source, cursor) else {
            return Vec::new();
        };
        yaml_route_requirement_items(&context, line_index)
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["_", "{"].map(String::from).to_vec()
    }
}

fn yaml_route_path_context(source: &str, cursor: usize) -> Option<TextRange> {
    if let Some(range) = yaml_flow_route_path_context(source, cursor) {
        return Some(range);
    }
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let indent = leading_yaml_indent(&source[line_start..line_end]);
    if indent == 0 {
        return None;
    }
    let parent = yaml_parent_mapping_line(source, line_start, indent)?;
    if parent.indent != 0 || !parent.empty_value {
        return None;
    }
    if yaml_parent_mapping_line(source, parent.start, parent.indent).is_some() {
        return None;
    }
    yaml_route_path_value_range(source, line_start, line_end, cursor)
}

fn yaml_flow_route_path_context(source: &str, cursor: usize) -> Option<TextRange> {
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let opens = yaml_unclosed_flow_mapping_opens(source, line_start, cursor);
    if opens.len() != 1 || leading_yaml_indent(&source[line_start..line_end]) != 0 {
        return None;
    }
    let open = opens[0];
    let (_, empty) = yaml_mapping_key_line(&source[line_start..open])?;
    if !empty {
        return None;
    }
    let segment_start = yaml_flow_segment_start(source, open + 1, cursor);
    let segment_end = yaml_flow_segment_end(source, cursor, line_end);
    yaml_route_path_value_range(source, segment_start, segment_end, cursor)
}

fn yaml_route_path_value_range(
    source: &str,
    start: usize,
    end: usize,
    cursor: usize,
) -> Option<TextRange> {
    if cursor < start || cursor > end {
        return None;
    }
    let bytes = source.as_bytes();
    let line = &source[start..end];
    let colon = yaml_unquoted_colon(line)?;
    if colon == 0 || trim_quotes(line[..colon].trim()) != "path" {
        return None;
    }
    let mut value_start = start + colon + 1;
    while value_start < end && matches!(bytes[value_start], b' ' | b'\t') {
        value_start += 1;
    }
    if cursor < value_start {
        return None;
    }
    if value_start < end && matches!(bytes[value_start], b'\'' | b'"') {
        let quote = bytes[value_start];
        let content_start = value_start + 1;
        let content_end = bytes[content_start..end]
            .iter()
            .position(|byte| *byte == quote)
            .map_or(end, |close| content_start + close);
        if cursor < content_start || cursor > content_end {
            return None;
        }
        return Some(text_range(content_start, content_end));
    }
    let mut value_end = value_start;
    while value_end < end && !matches!(bytes[value_end], b' ' | b'\t' | b'#') {
        value_end += 1;
    }
    if cursor > value_end {
        return None;
    }
    Some(text_range(value_start, value_end))
}

impl YamlRouteAuthoringCompletionProvider {
    fn yaml_route_path_items(
        &self,
        ctx: &Context,
        range: TextRange,
        line_index: &LineIndex,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.route_index.as_ref() else {
            return Vec::new();
        };
        let Ok(view) = index.get_routes_view() else {
            return Vec::new();
        };
        let mut routes: Vec<&Route> = view.iter().collect();
        // Go sort.Slice by (path, name); equal pairs are interchangeable
        // only when the rest of the route does not matter, which holds here
        // because only path and name are emitted.
        routes.sort_by(|left, right| {
            left.path
                .cmp(&right.path)
                .then_with(|| left.name.cmp(&right.name))
        });
        let edit_range = protocol_range(range, line_index);
        let mut seen: HashSet<String> = HashSet::with_capacity(routes.len());
        let mut items = Vec::with_capacity(routes.len());
        for route in routes {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if route.path.is_empty() || route.name.is_empty() || route.name.starts_with('_') {
                continue;
            }
            if !seen.insert(route.path.clone()) {
                continue;
            }
            items.push(protocol::CompletionItem {
                filter_text: format!("{} {}", route.path, route.name),
                kind: Some(Kind::REFERENCE),
                detail: route.name.clone(),
                text_edit: Some(text_edit(edit_range, route.path.clone())),
                label: route.path.clone(),
                ..Default::default()
            });
        }
        items
    }
}

struct AuthoringKey {
    range: TextRange,
    current: String,
    quote: u8,
    complete: bool,
}

/// Go `yamlAuthoringKeyAt`. Go indexes `source[start]` even when the key
/// starts at the end of the source (and panics); Rust treats that byte as
/// absent.
fn yaml_authoring_key_at(
    source: &str,
    line_start: usize,
    line_end: usize,
    cursor: usize,
) -> Option<AuthoringKey> {
    let bytes = source.as_bytes();
    let indent = leading_yaml_indent(&source[line_start..line_end]);
    let start = line_start + indent;
    if cursor < start || start > line_end || (start < line_end && bytes[start] == b'-') {
        return None;
    }
    let first = bytes.get(start).copied().unwrap_or(0);
    if first == b'\'' || first == b'"' {
        let quote = first;
        let content_start = start + 1;
        let (content_end, token_end) = match bytes[content_start.min(line_end)..line_end]
            .iter()
            .position(|byte| *byte == quote)
        {
            Some(close) => (content_start + close, content_start + close + 1),
            None => (line_end, line_end),
        };
        if cursor < content_start
            || cursor > content_end
            || !yaml_option_prefix(&source[content_start..cursor])
        {
            return None;
        }
        return Some(AuthoringKey {
            range: text_range(start, token_end),
            current: source[content_start..content_end].to_string(),
            quote,
            complete: yaml_key_has_colon(source, token_end, line_end),
        });
    }
    let end = yaml_identifier_end(source, start, line_end);
    if cursor > end || !yaml_option_prefix(&source[start..cursor]) {
        return None;
    }
    Some(AuthoringKey {
        range: text_range(start, end),
        current: source[start..end].to_string(),
        quote: 0,
        complete: yaml_key_has_colon(source, end, line_end),
    })
}

struct RouteOptionContext {
    key: AuthoringKey,
    existing: HashSet<String>,
}

fn yaml_route_option_context(source: &str, cursor: usize) -> Option<RouteOptionContext> {
    if let Some(context) = yaml_flow_route_option_context(source, cursor) {
        return Some(context);
    }
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let key = yaml_authoring_key_at(source, line_start, line_end, cursor)?;
    let indent = leading_yaml_indent(&source[line_start..line_end]);
    let parent = yaml_parent_mapping_line(source, line_start, indent)?;
    if !parent.empty_value || parent.indent != 0 {
        return None;
    }
    if yaml_parent_mapping_line(source, parent.start, parent.indent).is_some() {
        return None;
    }
    Some(RouteOptionContext {
        key,
        existing: yaml_block_mapping_keys(source, parent.start, parent.indent, indent),
    })
}

fn yaml_flow_route_option_context(source: &str, cursor: usize) -> Option<RouteOptionContext> {
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let opens = yaml_unclosed_flow_mapping_opens(source, line_start, cursor);
    if opens.len() != 1 {
        return None;
    }
    let open = opens[0];
    if leading_yaml_indent(&source[line_start..line_end]) != 0 {
        return None;
    }
    let (_, empty) = yaml_mapping_key_line(&source[line_start..open])?;
    if !empty {
        return None;
    }
    let segment_start = yaml_flow_segment_start(source, open + 1, cursor);
    let key = yaml_authoring_key_at(
        source,
        segment_start,
        yaml_flow_segment_end(source, cursor, line_end),
        cursor,
    )?;
    let close = yaml_matching_flow_brace(source, open, line_end).unwrap_or(line_end);
    Some(RouteOptionContext {
        key,
        existing: yaml_flow_mapping_keys(&source[open + 1..close]),
    })
}

fn key_new_text(key: &AuthoringKey, name: &str) -> String {
    let mut new_text = if key.quote != 0 {
        let quote = key.quote as char;
        format!("{quote}{name}{quote}")
    } else {
        name.to_string()
    };
    if !key.complete {
        new_text.push_str(": ");
    }
    new_text
}

fn yaml_route_option_items(
    context: &RouteOptionContext,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let edit_range = protocol_range(context.key.range, line_index);
    YAML_ROUTE_OPTIONS
        .iter()
        .filter(|(name, _, _)| !context.existing.contains(*name) || *name == context.key.current)
        .map(|(name, detail, deprecated)| protocol::CompletionItem {
            label: name.to_string(),
            kind: Some(Kind::PROPERTY),
            detail: format!("Symfony route option · {detail}"),
            deprecated: *deprecated,
            text_edit: Some(text_edit(edit_range, key_new_text(&context.key, name))),
            ..Default::default()
        })
        .collect()
}

struct RouteRequirementContext {
    key: AuthoringKey,
    existing: HashSet<String>,
    placeholders: Vec<String>,
}

fn yaml_route_requirement_context(source: &str, cursor: usize) -> Option<RouteRequirementContext> {
    if let Some(context) = yaml_flow_route_requirement_context(source, cursor) {
        return Some(context);
    }
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let key = yaml_authoring_key_at(source, line_start, line_end, cursor)?;
    let indent = leading_yaml_indent(&source[line_start..line_end]);
    let requirements = yaml_parent_mapping_line(source, line_start, indent)?;
    if requirements.key != "requirements" || !requirements.empty_value {
        return None;
    }
    let route = yaml_parent_mapping_line(source, requirements.start, requirements.indent)?;
    if route.indent != 0 || !route.empty_value {
        return None;
    }
    if yaml_parent_mapping_line(source, route.start, route.indent).is_some() {
        return None;
    }
    let path = yaml_block_mapping_scalar(
        source,
        route.start,
        route.indent,
        requirements.indent,
        "path",
    )
    .or_else(|| {
        yaml_block_mapping_scalar(
            source,
            route.start,
            route.indent,
            requirements.indent,
            "pattern",
        )
    })?;
    Some(RouteRequirementContext {
        key,
        existing: yaml_block_mapping_keys(source, requirements.start, requirements.indent, indent),
        placeholders: yaml_route_placeholders(&path),
    })
}

fn yaml_flow_route_requirement_context(
    source: &str,
    cursor: usize,
) -> Option<RouteRequirementContext> {
    let (line_start, line_end) = yaml_line_bounds(source, cursor);
    let opens = yaml_unclosed_flow_mapping_opens(source, line_start, cursor);
    if opens.len() != 2 || leading_yaml_indent(&source[line_start..line_end]) != 0 {
        return None;
    }
    let (route_open, requirements_open) = (opens[0], opens[1]);
    let (_, route_empty) = yaml_mapping_key_line(&source[line_start..route_open])?;
    if !route_empty {
        return None;
    }
    let requirement_segment = &source
        [yaml_flow_segment_start(source, route_open + 1, requirements_open)..requirements_open];
    let (requirement_key, requirement_empty) = yaml_mapping_key_line(requirement_segment)?;
    if requirement_key != "requirements" || !requirement_empty {
        return None;
    }
    let segment_start = yaml_flow_segment_start(source, requirements_open + 1, cursor);
    let key = yaml_authoring_key_at(
        source,
        segment_start,
        yaml_flow_segment_end(source, cursor, line_end),
        cursor,
    )?;
    let route_close = yaml_matching_flow_brace(source, route_open, line_end).unwrap_or(line_end);
    let route_body = &source[route_open + 1..route_close];
    let path = yaml_flow_mapping_scalar(route_body, "path")
        .or_else(|| yaml_flow_mapping_scalar(route_body, "pattern"))?;
    let requirements_close =
        yaml_matching_flow_brace(source, requirements_open, line_end).unwrap_or(line_end);
    Some(RouteRequirementContext {
        key,
        existing: yaml_flow_mapping_keys(&source[requirements_open + 1..requirements_close]),
        placeholders: yaml_route_placeholders(&path),
    })
}

fn yaml_route_requirement_items(
    context: &RouteRequirementContext,
    line_index: &LineIndex,
) -> Vec<protocol::CompletionItem> {
    let edit_range = protocol_range(context.key.range, line_index);
    context
        .placeholders
        .iter()
        .filter(|placeholder| {
            !context.existing.contains(*placeholder) || **placeholder == context.key.current
        })
        .map(|placeholder| protocol::CompletionItem {
            label: placeholder.clone(),
            kind: Some(Kind::PROPERTY),
            detail: "Symfony route path parameter".to_string(),
            text_edit: Some(text_edit(
                edit_range,
                key_new_text(&context.key, placeholder),
            )),
            ..Default::default()
        })
        .collect()
}

fn yaml_block_mapping_scalar(
    source: &str,
    parent_start: usize,
    parent_indent: usize,
    child_indent: usize,
    wanted: &str,
) -> Option<String> {
    let bytes = source.as_bytes();
    let mut line_start = yaml_next_line_start(source, parent_start);
    while line_start < bytes.len() {
        let mut line_end = line_start;
        while line_end < bytes.len() && bytes[line_end] != b'\n' && bytes[line_end] != b'\r' {
            line_end += 1;
        }
        let line = &source[line_start..line_end];
        if !line.trim().is_empty() {
            let indent = leading_yaml_indent(line);
            if indent <= parent_indent {
                break;
            }
            if indent == child_indent
                && let Some((key, value)) = yaml_mapping_key_value_line(line)
                && key == wanted
            {
                return yaml_scalar_text(value);
            }
        }
        line_start = yaml_next_line_start(source, line_end);
    }
    None
}

fn yaml_flow_mapping_scalar(source: &str, wanted: &str) -> Option<String> {
    for segment in yaml_flow_segments(source) {
        if let Some((key, value)) = yaml_mapping_key_value_line(segment)
            && key == wanted
        {
            return yaml_scalar_text(value);
        }
    }
    None
}

fn yaml_mapping_key_value_line(line: &str) -> Option<(&str, &str)> {
    let trimmed = line.trim();
    let colon = yaml_unquoted_colon(trimmed)?;
    if colon == 0 {
        return None;
    }
    let key = trim_quotes(trimmed[..colon].trim());
    let value = trimmed[colon + 1..].trim();
    if key.is_empty() || value.is_empty() {
        return None;
    }
    Some((key, value))
}

fn yaml_scalar_text(value: &str) -> Option<String> {
    let mut value = value.trim();
    if value.is_empty() || value.starts_with('{') || value.starts_with('[') {
        return None;
    }
    let bytes = value.as_bytes();
    if (bytes[0] == b'\'' || bytes[0] == b'"')
        && bytes.len() >= 2
        && bytes[bytes.len() - 1] == bytes[0]
    {
        value = &value[1..value.len() - 1];
    }
    (!value.is_empty()).then(|| value.to_string())
}

fn yaml_route_placeholders(path: &str) -> Vec<String> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for captures in YAML_ROUTE_PLACEHOLDER.captures_iter(path) {
        let Some(name) = captures.get(1) else {
            continue;
        };
        if seen.insert(name.as_str()) {
            result.push(name.as_str().to_string());
        }
    }
    result
}

fn yaml_routing_file(path: &str) -> bool {
    let slash_path = go_to_lower(&swlsp_uriutil::filepath::to_slash(path));
    let base = go_to_lower(&swlsp_uriutil::filepath::base(path));
    base.contains("routing")
        || slash_path.contains("/routing")
        || base == "routes.yaml"
        || base == "routes.yml"
        || slash_path.contains("/config/routes/")
}

/// Quote state shared by the flow-mapping scanners: a double quote preceded
/// by a backslash (after `start`) does not toggle.
struct Quotes {
    single: bool,
    double: bool,
}

impl Quotes {
    fn new() -> Self {
        Self {
            single: false,
            double: false,
        }
    }

    /// Updates the state for `bytes[position]`; true when the byte is
    /// outside quotes and not itself a quote.
    fn step(&mut self, bytes: &[u8], start: usize, position: usize) -> bool {
        match bytes[position] {
            b'\'' => {
                if !self.double {
                    self.single = !self.single;
                }
                false
            }
            b'"' => {
                if !self.single && (position == start || bytes[position - 1] != b'\\') {
                    self.double = !self.double;
                }
                false
            }
            _ => !self.single && !self.double,
        }
    }
}

fn yaml_unclosed_flow_mapping_opens(source: &str, start: usize, end: usize) -> Vec<usize> {
    let bytes = source.as_bytes();
    let mut stack = Vec::new();
    let mut quotes = Quotes::new();
    for position in start..end {
        if !quotes.step(bytes, start, position) {
            continue;
        }
        match bytes[position] {
            b'{' => stack.push(position),
            b'}' => {
                stack.pop();
            }
            _ => {}
        }
    }
    stack
}

fn yaml_matching_flow_brace(source: &str, open: usize, limit: usize) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut depth = 0i32;
    let mut quotes = Quotes::new();
    for position in open..limit {
        if !quotes.step(bytes, open, position) {
            continue;
        }
        match bytes[position] {
            b'{' => depth += 1,
            b'}' => {
                depth -= 1;
                if depth == 0 {
                    return Some(position);
                }
            }
            _ => {}
        }
    }
    None
}

fn yaml_flow_segment_start(source: &str, start: usize, end: usize) -> usize {
    let bytes = source.as_bytes();
    let mut depth = 0usize;
    let mut quotes = Quotes::new();
    let mut segment_start = start;
    for position in start..end {
        if !quotes.step(bytes, start, position) {
            continue;
        }
        match bytes[position] {
            b'{' | b'[' => depth += 1,
            b'}' | b']' => depth = depth.saturating_sub(1),
            b',' if depth == 0 => segment_start = position + 1,
            _ => {}
        }
    }
    segment_start
}

fn yaml_flow_segment_end(source: &str, start: usize, limit: usize) -> usize {
    let bytes = source.as_bytes();
    let mut depth = 0usize;
    let mut quotes = Quotes::new();
    for position in start..limit {
        if !quotes.step(bytes, start, position) {
            continue;
        }
        match bytes[position] {
            b'{' | b'[' => depth += 1,
            b'}' | b']' => {
                if depth == 0 {
                    return position;
                }
                depth -= 1;
            }
            b',' if depth == 0 => return position,
            _ => {}
        }
    }
    limit
}

fn yaml_flow_segments(source: &str) -> Vec<&str> {
    let mut result = Vec::new();
    let mut start = 0;
    while start <= source.len() {
        let end = yaml_flow_segment_end(source, start, source.len());
        result.push(&source[start..end]);
        if end >= source.len() {
            break;
        }
        start = end + 1;
    }
    result
}

/// Go `yamlUnquotedColon`; `None` for -1.
fn yaml_unquoted_colon(value: &str) -> Option<usize> {
    let bytes = value.as_bytes();
    let mut quotes = Quotes::new();
    (0..bytes.len()).find(|&position| quotes.step(bytes, 0, position) && bytes[position] == b':')
}
