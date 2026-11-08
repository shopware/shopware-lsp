//! Go-to-definition inside Twig templates and for template references in
//! PHP (port of Go `twig_definition.go`, `twig_guard_definition.go`,
//! `twig_see_definition.go`, `twig_types_definition.go` and
//! `twig_test_definition.go`).

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_FILTER, TWIG_LITERAL_NAME, TWIG_VAR};
use swlsp_php::PhpIndex;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::TextRange;
use swlsp_theme::IconProvider;
use swlsp_twig::{GuardKind, PhpAccessResolver, TwigIndexer};
use swlsp_uriutil::{file_uri, filepath};

use crate::common::{ext_lower, php_symbol_location, request_offset, request_uri};
use crate::twig_common::{
    php_context, twig_variable_location, unique_component_locations, zero_line_location,
};

pub struct TwigDefinitionProvider {
    project_root: String,
    twig: Option<Arc<TwigIndexer>>,
    icons: Option<IconProvider>,
    php: Option<PhpIndex>,
}

impl TwigDefinitionProvider {
    /// Go `NewTwigDefinitionProvider`. Without an extension indexer the
    /// `sw_icon` lookup is skipped (Go would dereference nil).
    pub fn new(
        project_root: impl Into<String>,
        twig: Option<Arc<TwigIndexer>>,
        extensions: Option<Arc<ExtensionIndexer>>,
        php: Option<PhpIndex>,
    ) -> Self {
        let project_root = project_root.into();
        Self {
            icons: extensions.map(|extensions| IconProvider::new(project_root.clone(), extensions)),
            project_root,
            twig,
            php,
        }
    }
}

type Locations = Option<Vec<protocol::Location>>;

impl GotoDefinitionProvider for TwigDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        match ext_lower(request_uri(request)).as_str() {
            ".php" => {
                if request.node.is_none() {
                    return Vec::new();
                }
                self.php_definitions(request)
            }
            ".twig" => self.twig_definitions(request),
            _ => Vec::new(),
        }
    }
}

impl TwigDefinitionProvider {
    fn twig_definitions(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        if let Some(locations) = self.twig_see_definitions(request) {
            return locations;
        }
        if let Some(locations) = self.twig_guard_definitions(request) {
            return locations;
        }
        if let Some(locations) = self.twig_types_tag_definitions(request) {
            return locations;
        }
        if let Some(locations) = self.twig_test_definitions(request) {
            return locations;
        }
        if let Some(locations) = self.twig_tag_definitions(request) {
            return locations;
        }
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(locations) = self.twig_variable_definitions(request) {
            return locations;
        }
        if let Some(locations) = self.twig_php_member_definitions(request) {
            return locations;
        }
        let Some(twig) = self.twig.as_deref() else {
            // Go dereferences the nil indexer here; nothing can resolve.
            return Vec::new();
        };

        if swlsp_twig::is_twig_template_string(node) {
            let value = twigquery::literal_string_at(node)
                .and_then(|literal| twigquery::string_value(&literal))
                .unwrap_or_default();
            let files = twig
                .get_twig_files_for_reference(Some(node), &value)
                .unwrap_or_default();
            let Ok(current) = swlsp_uriutil::path(request_uri(request)) else {
                return Vec::new();
            };
            return files
                .iter()
                .filter(|file| file.path != current)
                .map(|file| zero_line_location(&file.path, 0))
                .collect();
        }

        let filter_name = twigquery::closest_node_of_kind(node, &[TWIG_FILTER])
            .and_then(|filter| twigquery::filter_name(&filter))
            .unwrap_or_default();
        if !filter_name.is_empty()
            && request
                .token
                .as_ref()
                .is_some_and(|token| token.text() == filter_name)
        {
            return twig
                .get_twig_filter(&filter_name)
                .unwrap_or_default()
                .iter()
                .map(|filter| zero_line_location(&filter.file_path, one_based(filter.line)))
                .collect();
        }

        if let Some(function_name) =
            twigquery::function_name_at(node).filter(|name| !name.is_empty())
        {
            return twig
                .get_twig_function(&function_name)
                .unwrap_or_default()
                .iter()
                .map(|function| zero_line_location(&function.file_path, one_based(function.line)))
                .collect();
        }

        if twigquery::string_in_tag(node, &["sw_icon"]) {
            let text = twigquery::literal_string_at(node)
                .and_then(|literal| twigquery::string_value(&literal))
                .unwrap_or_default();
            let config = twigquery::hash_string_map(node);
            let pack = config.get("pack").map_or("default", String::as_str);
            if let Some(icons) = &self.icons {
                let icon = icons.icon(pack, &text);
                if !icon.is_empty() {
                    return vec![zero_line_location(&icon, 0)];
                }
            }
        }
        Vec::new()
    }

    fn twig_tag_definitions(&self, request: &DefinitionRequest) -> Locations {
        let twig = self.twig.as_deref()?;
        let offset = request_offset(request)?;
        for usage in swlsp_twig::twig_tag_usages(request.source().as_bytes()) {
            if !contains(usage.range, offset) {
                continue;
            }
            let tags = match twig.resolve_twig_tag(&usage.name) {
                Ok((tags, _)) if !tags.is_empty() => tags,
                _ => return None,
            };
            let mut seen = HashSet::with_capacity(tags.len());
            let mut locations = Vec::with_capacity(tags.len());
            for tag in &tags {
                let key = (tag.file_path.clone(), tag.range);
                if seen.contains(&key) {
                    continue;
                }
                let Some(location) = twig_variable_location(&tag.file_path, tag.range) else {
                    continue;
                };
                seen.insert(key);
                locations.push(location);
            }
            return Some(locations);
        }
        None
    }

    fn twig_php_member_definitions(&self, request: &DefinitionRequest) -> Locations {
        let node = request.node.as_ref()?;
        let root = request.root.as_ref()?;
        let php = self.php.as_ref()?;
        let name_node = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME])?;
        let template_path = swlsp_uriutil::path(request_uri(request)).ok()?;
        let resolution = PhpAccessResolver::new(Some(php), self.twig.as_deref()).resolve_name(
            &template_path,
            root,
            &name_node,
        )?;
        let mut seen = HashSet::new();
        let mut locations = Vec::new();
        for member in &resolution.members {
            let symbol = &member.symbol;
            if seen.contains(&symbol.id) {
                continue;
            }
            let range = if symbol.selection_range.is_empty() {
                symbol.range
            } else {
                symbol.selection_range
            };
            let Some(location) = twig_variable_location(&symbol.path, range) else {
                continue;
            };
            seen.insert(symbol.id.clone());
            locations.push(location);
        }
        if locations.is_empty() {
            None
        } else {
            Some(locations)
        }
    }

    fn twig_variable_definitions(&self, request: &DefinitionRequest) -> Locations {
        let node = request.node.as_ref()?;
        let name_node = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME])?;
        twigquery::closest_node_of_kind(&name_node, &[TWIG_VAR])?;
        if let Some(accessor) = twigquery::closest_node_of_kind(&name_node, &[TWIG_ACCESSOR]) {
            let first = twigquery::iterate_nodes(&accessor, &[TWIG_LITERAL_NAME]).next();
            if first.as_ref() != Some(&name_node) {
                return None;
            }
        }
        let name = name_node.text().to_string();
        let name = name.trim();
        let path = swlsp_uriutil::path(request_uri(request)).ok()?;
        let mut locations = Vec::new();
        let mut seen = HashSet::new();
        if let Some(php) = &self.php {
            let variables = php
                .twig_template_variable_sources(name, &swlsp_twig::template_names(&path))
                .ok()?;
            for variable in &variables {
                let range = TextRange::new(variable.range.start.into(), variable.range.end.into());
                if let Some(location) = twig_variable_location(&variable.file, range) {
                    if !seen.insert((variable.file.clone(), range)) {
                        continue;
                    }
                    locations.push(location);
                }
            }
            if !variables.is_empty() {
                return non_nil(locations);
            }
        }
        let twig = self.twig.as_deref()?;
        let globals = match twig.get_globals(name) {
            Ok(globals) if !globals.is_empty() => globals,
            _ => return None,
        };
        for global in &globals {
            let key = (global.file.clone(), global.range);
            if seen.contains(&key) {
                continue;
            }
            let Some(location) = twig_variable_location(&global.file, global.range) else {
                continue;
            };
            seen.insert(key);
            locations.push(location);
        }
        non_nil(locations)
    }

    fn php_definitions(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(context) = php_context(self.php.as_ref(), request)
            && swlsp_php::assistant_argument_reference(&context, node, "Template").is_some()
        {
            let value = swlsp_parser_php::query::string_value(node);
            return self.php_template_definitions(&value);
        }
        if let (Some(offset), Some(root)) = (request_offset(request), request.root.as_ref()) {
            let references = swlsp_twig::php_template_references("", root);
            if let Some(reference) = swlsp_twig::template_reference_at(&references, offset) {
                return self.php_template_definitions(&reference.template);
            }
        }
        if let Some(template) = swlsp_twig::php_template_like_string(node) {
            // Match the reference plugin's global PHP-string navigation
            // without promoting arbitrary strings to indexed usages or
            // rename targets.
            return self.php_template_definitions(&template);
        }
        Vec::new()
    }

    fn php_template_definitions(&self, name: &str) -> Vec<protocol::Location> {
        let Some(twig) = self.twig.as_deref() else {
            return Vec::new();
        };
        twig.get_twig_files_by_rel_path(name)
            .unwrap_or_default()
            .iter()
            .map(|file| zero_line_location(&file.path, 0))
            .collect()
    }

    fn twig_guard_definitions(&self, request: &DefinitionRequest) -> Locations {
        let twig = self.twig.as_deref()?;
        let offset = request_offset(request)?;
        let reference = swlsp_twig::guard_reference_at(request.source().as_bytes(), offset)?;
        let targets: Vec<(String, i64)> = match reference.kind {
            GuardKind::Function => twig
                .get_twig_function(&reference.name)
                .unwrap_or_default()
                .into_iter()
                .map(|value| (value.file_path, value.line))
                .collect(),
            GuardKind::Filter => twig
                .get_twig_filter(&reference.name)
                .unwrap_or_default()
                .into_iter()
                .map(|value| (value.file_path, value.line))
                .collect(),
            GuardKind::Test => twig
                .get_twig_test(&reference.name)
                .unwrap_or_default()
                .into_iter()
                .map(|value| (value.file_path, value.line))
                .collect(),
            GuardKind::None => Vec::new(),
        };
        Some(line_locations(targets))
    }

    fn twig_see_definitions(&self, request: &DefinitionRequest) -> Locations {
        let root = request.root.as_ref()?;
        let offset = request_offset(request)?;
        let reference = swlsp_twig::see_reference_at(root, offset)?;
        let target = reference.target;
        let mut locations = Vec::new();
        if let Some(twig) = self.twig.as_deref()
            && go_to_lower(&target).ends_with(".twig")
        {
            for file in twig.get_twig_files_by_rel_path(&target).unwrap_or_default() {
                locations.push(protocol::Location {
                    uri: file_uri(&file.path),
                    range: protocol::Range::default(),
                });
            }
        }
        if let Some(php) = &self.php
            && let Some((class_name, member)) = swlsp_twig::see_php_class_and_member(&target)
        {
            if !member.is_empty() {
                for method in php.find_methods(&class_name, &member) {
                    locations.push(php_symbol_location(&method));
                }
            } else if let Some(class) = php.find_class(&class_name) {
                locations.push(php_symbol_location(&class));
            }
        }
        if let Some(relative) = self.twig_see_relative_file(request_uri(request), &target) {
            locations.push(protocol::Location {
                uri: file_uri(&relative),
                range: protocol::Range::default(),
            });
        }
        Some(unique_component_locations(locations))
    }

    fn twig_see_relative_file(&self, document_uri: &str, target: &str) -> Option<String> {
        if self.project_root.is_empty() || target.is_empty() || target.starts_with('@') {
            return None;
        }
        let current = swlsp_uriutil::path(document_uri).ok()?;
        let target = filepath::from_slash(&target.replace('\\', "/"));
        if filepath::is_abs(&target) {
            return None;
        }
        let candidate = filepath::clean(&filepath::join(&[dir(&current), target]));
        let relative = filepath::rel(&self.project_root, &candidate).ok()?;
        if relative == ".." || relative.starts_with("../") {
            return None;
        }
        let metadata = std::fs::metadata(&candidate).ok()?;
        if metadata.is_dir() {
            return None;
        }
        Some(candidate)
    }

    fn twig_types_tag_definitions(&self, request: &DefinitionRequest) -> Locations {
        let php = self.php.as_ref()?;
        let offset = request_offset(request)?;
        let reference =
            swlsp_twig::types_tag_class_reference_at(request.source().as_bytes(), offset)?;
        Some(
            php.find_class(&reference.name)
                .map(|symbol| vec![php_symbol_location(&symbol)])
                .unwrap_or_default(),
        )
    }

    fn twig_test_definitions(&self, request: &DefinitionRequest) -> Locations {
        let twig = self.twig.as_deref()?;
        let offset = request_offset(request)?;
        let root = request.root.as_ref()?;
        let reference = swlsp_twig::test_expression_at(root, offset)?;
        let targets = twig
            .get_twig_test(&reference.name)
            .unwrap_or_default()
            .into_iter()
            .map(|value| (value.file_path, value.line))
            .collect();
        Some(line_locations(targets))
    }
}

/// Go guard/test target deduplication: first target per lowercased path
/// and line, at the start of the clamped zero-based line.
fn line_locations(targets: Vec<(String, i64)>) -> Vec<protocol::Location> {
    let mut seen = HashSet::with_capacity(targets.len());
    let mut locations = Vec::with_capacity(targets.len());
    for (path, line) in targets {
        if path.is_empty() {
            continue;
        }
        if !seen.insert((go_to_lower(&path), line)) {
            continue;
        }
        locations.push(zero_line_location(&path, one_based(line)));
    }
    locations
}

/// A one-based line as a zero-based protocol line (clamped at 0).
fn one_based(line: i64) -> u32 {
    u32::try_from(line.saturating_sub(1).max(0)).unwrap_or(u32::MAX)
}

/// Go `TextRange.Contains`: start <= offset < end.
fn contains(range: TextRange, offset: u32) -> bool {
    u32::from(range.start()) <= offset && offset < u32::from(range.end())
}

/// Go returns a nil slice when nothing was appended; the caller falls
/// through on nil.
fn non_nil(locations: Vec<protocol::Location>) -> Locations {
    if locations.is_empty() {
        None
    } else {
        Some(locations)
    }
}

/// Go `filepath.Dir`.
fn dir(path: &str) -> String {
    match path.rfind('/') {
        Some(index) => filepath::clean(&path[..=index]),
        None => ".".to_owned(),
    }
}

#[cfg(test)]
#[path = "twig_tests.rs"]
mod tests;
