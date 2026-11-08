//! Symfony service locator (port of `service_locator.go`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_php_types::{equal_fold, go_quote};
use swlsp_symfony::{Service, ServiceIndex, TagMap};
use swlsp_syntax::TextRange;

use crate::profiler::go_lossy;
use crate::request::{Field, decode, present};
use crate::{
    Result, SourceLineResolver, check, errorf, is_zero, lower, range_len, range_start, trim_space,
};

pub const LOCATE_SERVICES_COMMAND: &str = "shopware/symfony/analytics/services/locate";

#[derive(Clone)]
pub struct ServiceLocatorProvider {
    index: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ServiceLocatorRequest {
    pub identifier: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServiceDefinitionSourceEntry {
    pub source: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub end_line: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub preview: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ServiceLocatorEntry {
    pub id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub resolved_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub alias_target: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub decorates: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub autowire: bool,
    #[serde(skip_serializing_if = "is_zero")]
    pub autowire_configured: bool,
    #[serde(skip_serializing_if = "is_zero")]
    pub deprecated: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub deprecation: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub tags: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class_file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub class_line: i64,
    pub definitions: Vec<ServiceDefinitionSourceEntry>,
}

impl ServiceLocatorProvider {
    pub fn new(index: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }

    fn locate_command(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let Some(value) = present(args) else {
            return Err(errorf!("service identifier is required"));
        };
        let members = decode(
            value,
            "ServiceLocatorRequest",
            &[("identifier", Field::String)],
        )
        .map_err(|error| errorf!("invalid service locator request: {error}"))?;
        let request = ServiceLocatorRequest {
            identifier: members.string("identifier"),
        };
        Ok(serde_json::to_value(self.locate(ctx, &request)?)?)
    }

    pub fn locate(
        &self,
        ctx: &Context,
        request: &ServiceLocatorRequest,
    ) -> Result<Vec<ServiceLocatorEntry>> {
        let Some(index) = &self.index else {
            return Err(errorf!("service locator is unavailable"));
        };
        check(ctx)?;
        let identifier = trim_space(&request.identifier);
        if identifier.is_empty() {
            return Err(errorf!("service identifier is required"));
        }
        let definitions = index.get_all_service_definitions()?;
        let mut by_id: HashMap<String, &Service> = HashMap::with_capacity(definitions.len());
        for service in &definitions {
            if !service.id.is_empty() {
                by_id.insert(lower(&service.id), service);
            }
        }
        let class_query = identifier.contains('\\');
        let normalized_class = normalize_service_class(identifier);
        let mut matched: Vec<&Service> = Vec::new();
        for service in &definitions {
            check(ctx)?;
            if class_query {
                let resolved = resolve_catalog_service_class(service, &by_id);
                if equal_fold(&resolved, normalized_class)
                    || equal_fold(normalize_service_class(&service.class), normalized_class)
                    || equal_fold(normalize_service_class(&service.id), normalized_class)
                {
                    matched.push(service);
                }
                continue;
            }
            if equal_fold(&service.id, identifier) {
                matched.push(service);
            }
        }
        if matched.is_empty() {
            return Err(errorf!("service {} was not found", go_quote(identifier)));
        }
        let explicit_sources: HashSet<String> = index
            .service_definitions()?
            .iter()
            .map(service_source_key)
            .collect();
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::with_capacity(matched.len());
        for service in matched {
            check(ctx)?;
            let service = index
                .get_service_by_id(&service.id)?
                .unwrap_or_else(|| service.clone());
            let resolved_class = resolve_catalog_service_class(&service, &by_id);
            let mut entry = ServiceLocatorEntry {
                id: service.id.clone(),
                class_name: normalize_service_class(&service.class).to_owned(),
                resolved_class: resolved_class.clone(),
                alias_target: service.alias_target.clone(),
                decorates: service.decorates.clone(),
                parent: service.parent.clone(),
                autowire: service.autowire,
                autowire_configured: service.autowire_set,
                deprecated: service.deprecated,
                deprecation: service.deprecation.clone(),
                tags: service_tag_names(&service.tags),
                ..ServiceLocatorEntry::default()
            };
            if let Some(php) = &self.php
                && !resolved_class.is_empty()
                && let Some(symbol) = php.find_class(&resolved_class)
            {
                entry.class_file_uri = swlsp_uriutil::file_uri(&symbol.path);
                let offset = if range_len(symbol.selection_range) == 0 {
                    range_start(symbol.range)
                } else {
                    range_start(symbol.selection_range)
                };
                entry.class_line = lines.line(&symbol.path, offset);
            }
            let mut seen_sources = HashSet::new();
            for declaration in index.service_declarations(&service.id)? {
                let key = service_source_key(&declaration);
                if !seen_sources.insert(key.clone()) {
                    continue;
                }
                let source = if explicit_sources.contains(&key) {
                    "explicit"
                } else {
                    "prototype"
                };
                entry
                    .definitions
                    .push(service_definition_source(&declaration, source, &mut lines));
            }
            if entry.definitions.is_empty() {
                entry.definitions.push(ServiceDefinitionSourceEntry {
                    source: "compiled".to_owned(),
                    ..ServiceDefinitionSourceEntry::default()
                });
            }
            result.push(entry);
        }
        result.sort_by_cached_key(|entry| lower(&entry.id));
        Ok(result)
    }
}

impl CommandProvider for ServiceLocatorProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.locate_command(ctx, args));
        BTreeMap::from([(LOCATE_SERVICES_COMMAND.to_owned(), command)])
    }
}

fn resolve_catalog_service_class(service: &Service, by_id: &HashMap<String, &Service>) -> String {
    let mut visited = HashSet::new();
    let mut service = service;
    loop {
        let key = lower(&service.id);
        if !key.is_empty() && !visited.insert(key) {
            return String::new();
        }
        if !service.alias_target.is_empty() {
            let Some(target) = by_id.get(&lower(&service.alias_target)) else {
                return String::new();
            };
            service = target;
            continue;
        }
        let class_name = normalize_service_class(&service.class);
        if !class_name.is_empty() && is_static_service_class(class_name) {
            return class_name.to_owned();
        }
        if !service.parent.is_empty()
            && let Some(parent) = by_id.get(&lower(&service.parent))
        {
            service = parent;
            continue;
        }
        let class_name = normalize_service_class(&service.id);
        if !class_name.is_empty()
            && class_name.contains('\\')
            && is_static_service_class(class_name)
        {
            return class_name.to_owned();
        }
        return String::new();
    }
}

fn normalize_service_class(value: &str) -> &str {
    let value = trim_space(value);
    value.strip_prefix('\\').unwrap_or(value)
}

fn is_static_service_class(value: &str) -> bool {
    !value.is_empty() && !value.contains(['%', '$', '{', '}', '@', ' ', '\t'])
}

fn service_tag_names(tags: &TagMap) -> Vec<String> {
    tags.keys().filter(|tag| !tag.is_empty()).cloned().collect()
}

fn range_string(range: TextRange) -> String {
    format!("{}..{}", u32::from(range.start()), u32::from(range.end()))
}

fn service_source_key(service: &Service) -> String {
    [
        lower(&service.id),
        service.path.clone(),
        service.line.to_string(),
        range_string(service.range),
    ]
    .join("\x00")
}

fn service_definition_source(
    service: &Service,
    source: &str,
    lines: &mut SourceLineResolver,
) -> ServiceDefinitionSourceEntry {
    let mut entry = ServiceDefinitionSourceEntry {
        source: source.to_owned(),
        ..ServiceDefinitionSourceEntry::default()
    };
    if service.path.is_empty() {
        return entry;
    }
    entry.file_uri = swlsp_uriutil::file_uri(&service.path);
    entry.source_line = i64::from(service.line);
    if entry.source_line == 0 {
        entry.source_line = lines.line(&service.path, range_start(service.range));
    }
    let (preview, end_line) =
        service_source_preview(&service.path, service.range, entry.source_line, lines);
    entry.preview = preview;
    entry.end_line = end_line;
    entry
}

/// Go `strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")` on bytes.
fn split_lines(text: &[u8]) -> Vec<Vec<u8>> {
    let mut lines = vec![Vec::new()];
    let mut index = 0;
    while index < text.len() {
        if text[index] == b'\n' || (text[index] == b'\r' && text.get(index + 1) == Some(&b'\n')) {
            if text[index] == b'\r' {
                index += 1;
            }
            lines.push(Vec::new());
        } else {
            lines
                .last_mut()
                .unwrap_or_else(|| unreachable!())
                .push(text[index]);
        }
        index += 1;
    }
    lines
}

fn service_source_preview(
    path: &str,
    range: TextRange,
    source_line: i64,
    lines: &mut SourceLineResolver,
) -> (String, i64) {
    let Ok(source) = std::fs::read(path) else {
        return (String::new(), 0);
    };
    let start = range_start(range) as usize;
    let end = u32::from(range.end()) as usize;
    if range_len(range) == 0 || end > source.len() || start >= end {
        let text_lines = split_lines(&source);
        if source_line <= 0 || source_line as usize > text_lines.len() {
            return (String::new(), 0);
        }
        let line = text_lines[source_line as usize - 1].clone();
        return (truncate_service_preview_lines(vec![line]), source_line);
    }
    let end_line = lines.line(path, u32::from(range.end()) - 1);
    let preview_lines = split_lines(&source[start..end]);
    (truncate_service_preview_lines(preview_lines), end_line)
}

fn truncate_service_preview_lines(mut lines: Vec<Vec<u8>>) -> String {
    const MAX_LINES: usize = 80;
    const MAX_LINE_WIDTH: usize = 120;
    if lines.len() > MAX_LINES {
        lines.truncate(MAX_LINES);
        lines.push(b"...".to_vec());
    }
    for line in &mut lines {
        if line.len() > MAX_LINE_WIDTH {
            line.truncate(MAX_LINE_WIDTH);
            line.extend_from_slice(b"...");
        }
    }
    let joined = lines.join(&b'\n');
    trim_space(&go_lossy(&joined)).to_owned()
}
