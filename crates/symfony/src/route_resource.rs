//! Statically resolvable routing imports (`resource:` in YAML, `<import>` in
//! XML, `$routes->import()` in PHP) and their file targets.

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex, OnceLock, PoisonError};

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_STRING, PHP_VARIABLE};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::{XML_ATTRIBUTE, XML_ATTRIBUTE_VALUE};
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::{
    YAML_FLOW_MAPPING, YAML_FLOW_SEQUENCE, YAML_MAPPING, YAML_PAIR, YAML_SCALAR, YAML_SEQUENCE,
};
use swlsp_php::NameResolver;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::php_routes::{php_route_literal_argument, routing_configurator_parameters};
use crate::util::{
    clean_path, dir_path, extension, join_path, relative_path, xml_attr,
    xml_attribute_content_range, yaml_scalar_content_range,
};
use crate::yaml_services::{yaml_property, yaml_scalar};

pub(crate) const MAX_ROUTE_RESOURCE_FILES: usize = 200;

/// A statically resolvable routing import. `loader` is Symfony's optional
/// import type (for example "attribute" or "yaml").
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct RouteResourceReference {
    pub path: String,
    pub loader: String,
    pub namespace: String,
    pub node: Option<SyntaxNode>,
    pub range: TextRange,
    pub nested: bool,
}

/// The persisted form of [`RouteResourceReference`].
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct RouteResourceImport {
    pub path: String,
    pub loader: String,
    pub namespace: String,
    pub file_path: String,
    pub range: TextRange,
    pub nested: bool,
}

impl RouteResourceImport {
    pub fn reference(&self) -> RouteResourceReference {
        RouteResourceReference {
            path: self.path.clone(),
            loader: self.loader.clone(),
            namespace: self.namespace.clone(),
            node: None,
            range: self.range,
            nested: self.nested,
        }
    }
}

/// Recognizes a routing import at `node` in YAML, XML, and PHP
/// `RoutingConfigurator` files.
pub fn route_resource_reference_at(node: &SyntaxNode) -> Option<RouteResourceReference> {
    for current in node.ancestors() {
        match current.kind() {
            PHP_STRING => return php_route_resource_reference_at(&current),
            YAML_SCALAR => return yaml_route_resource_reference_at(&current),
            XML_ATTRIBUTE | XML_ATTRIBUTE_VALUE => {
                return xml_route_resource_reference_at(&current);
            }
            _ => {}
        }
    }
    None
}

/// Each statically resolvable routing import once, in source order.
pub fn route_resource_references(root: &SyntaxNode) -> Vec<RouteResourceReference> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for node in root.descendants() {
        if !matches!(node.kind(), PHP_STRING | YAML_SCALAR | XML_ATTRIBUTE_VALUE) {
            continue;
        }
        let Some(reference) = route_resource_reference_at(&node) else {
            continue;
        };
        if !seen.insert(reference.range) {
            continue;
        }
        result.push(reference);
    }
    result
}

fn yaml_route_resource_reference_at(node: &SyntaxNode) -> Option<RouteResourceReference> {
    let scalar = yaml_route_scalar_at(node)?;
    let pair = yamlquery::ancestor_pair(&scalar)?;
    if !same_route_node(yamlquery::pair_value(&pair).as_ref(), Some(&scalar)) {
        return None;
    }
    let key = yaml_scalar(yamlquery::pair_key(&pair).as_ref());
    let (resource_pair, nested) = match key.as_str() {
        "resource" => (pair, false),
        "path" => {
            let mapping = yaml_route_parent_mapping(&pair)?;
            let owner = yaml_route_parent_pair(&mapping)?;
            if yaml_scalar(yamlquery::pair_key(&owner).as_ref()) != "resource"
                || !same_route_node(yamlquery::pair_value(&owner).as_ref(), Some(&mapping))
            {
                return None;
            }
            (owner, true)
        }
        _ => return None,
    };
    let definition = yaml_route_parent_mapping(&resource_pair)?;
    let path = yamlquery::scalar_value(&scalar);
    let loader = yaml_scalar(yamlquery::property(&definition, "type").as_ref());
    let namespace = if nested {
        yaml_scalar(
            yaml_property(yamlquery::pair_value(&resource_pair).as_ref(), "namespace").as_ref(),
        )
    } else {
        String::new()
    };
    if path.is_empty()
        || path.contains(['\r', '\n'])
        || (!nested && loader.is_empty() && !route_config_resource_path(&path))
    {
        return None;
    }
    Some(RouteResourceReference {
        path,
        loader,
        namespace,
        range: yaml_scalar_content_range(Some(&scalar)),
        node: Some(scalar),
        nested,
    })
}

fn xml_route_resource_reference_at(node: &SyntaxNode) -> Option<RouteResourceReference> {
    let attribute = xmlquery::attribute_at(node)?;
    if xmlquery::attribute_name(&attribute).as_deref() != Some("resource") {
        return None;
    }
    let element = xmlquery::element_at(&attribute)?;
    if xmlquery::element_name(&element).as_deref() != Some("import") {
        return None;
    }
    let parent = xmlquery::parent_element(&element)?;
    if xmlquery::element_name(&parent).as_deref() != Some("routes") {
        return None;
    }
    let path = xmlquery::attribute_value(&attribute).unwrap_or_default();
    if path.is_empty() {
        return None;
    }
    Some(RouteResourceReference {
        path,
        loader: xml_attr(&element, "type"),
        namespace: String::new(),
        range: xml_attribute_content_range(Some(&attribute)),
        node: Some(attribute),
        nested: false,
    })
}

fn php_route_resource_reference_at(node: &SyntaxNode) -> Option<RouteResourceReference> {
    let (literal, call) = php_route_resource_call_at(node)?;
    let path = php_route_literal_argument(&call, 0).filter(|path| !path.is_empty())?;
    let loader = php_route_literal_argument(&call, 1).unwrap_or_default();
    Some(RouteResourceReference {
        path,
        loader,
        namespace: String::new(),
        range: phpquery::string_content_range(&literal),
        node: Some(literal),
        nested: false,
    })
}

/// The content range of the first string argument of a typed
/// `RoutingConfigurator` import call, including an empty literal.
pub fn php_route_resource_completion_range_at(node: &SyntaxNode) -> Option<TextRange> {
    let (literal, _) = php_route_resource_call_at(node)?;
    Some(phpquery::string_content_range(&literal))
}

fn php_route_resource_call_at(node: &SyntaxNode) -> Option<(SyntaxNode, SyntaxNode)> {
    let literal = phpquery::string_at(node)?;
    let call = phpquery::call_at(&literal)?;
    if !swlsp_textutil::eq_ignore_case(&phpquery::call_method_name(&call), "import")
        || phpquery::argument_index(&call, &literal) != Some(0)
    {
        return None;
    }
    let function = phpquery::function_like_at(&call)?;
    let root = call.ancestors().last().unwrap_or_else(|| call.clone());
    let resolver = NameResolver::new(&root);
    let configurators = routing_configurator_parameters(&function, &resolver);
    let receiver =
        phpquery::call_receiver(&call).filter(|receiver| receiver.kind() == PHP_VARIABLE)?;
    if !configurators.contains(&format!("${}", phpquery::variable_name(&receiver))) {
        return None;
    }
    Some((literal, call))
}

fn yaml_route_scalar_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    for current in node.ancestors() {
        match current.kind() {
            YAML_SCALAR => return Some(current),
            YAML_PAIR | YAML_MAPPING | YAML_FLOW_MAPPING | YAML_SEQUENCE | YAML_FLOW_SEQUENCE => {
                return None;
            }
            _ => {}
        }
    }
    None
}

fn yaml_route_parent_mapping(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().skip(1).find(yamlquery::is_mapping)
}

fn yaml_route_parent_pair(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors()
        .skip(1)
        .find(|current| current.kind() == YAML_PAIR)
}

fn same_route_node(left: Option<&SyntaxNode>, right: Option<&SyntaxNode>) -> bool {
    match (left, right) {
        (Some(left), Some(right)) => left == right || left.text_range() == right.text_range(),
        _ => false,
    }
}

fn route_config_resource_path(path: &str) -> bool {
    let lower = swlsp_textutil::lowercase(path);
    [".yaml", ".yml", ".xml", ".php"]
        .iter()
        .any(|extension| lower.contains(extension))
}

fn resource_target(current_path: &str, reference: &RouteResourceReference) -> Option<String> {
    if current_path.is_empty()
        || reference.path.is_empty()
        || reference.path.contains('\0')
        || reference.path.starts_with('@')
    {
        return None;
    }
    let target = reference.path.replace('\\', "/");
    if target.starts_with('/') {
        return Some(clean_path(&target));
    }
    Some(join_path(&[&dir_path(current_path), &target]))
}

/// Resolves an import to deterministic file targets. Directories are
/// recursive and bounded.
pub fn route_resource_files(current_path: &str, reference: &RouteResourceReference) -> Vec<String> {
    match resource_target(current_path, reference) {
        Some(target) => route_resource_files_at_target(&target, reference),
        None => Vec::new(),
    }
}

pub(crate) fn route_resource_files_at_target(
    target: &str,
    reference: &RouteResourceReference,
) -> Vec<String> {
    if route_resource_has_glob(target) {
        return route_resource_glob_files(target, reference);
    }
    let Ok(metadata) = std::fs::metadata(target) else {
        return Vec::new();
    };
    if !metadata.is_dir() {
        if route_resource_file_accepted(target, reference) {
            return vec![target.to_owned()];
        }
        return Vec::new();
    }
    route_resource_directory_files(target, reference, &[])
}

/// Whether `candidate` is selected by an import, without enumerating the
/// imported directory.
pub fn route_resource_matches(
    current_path: &str,
    candidate: &str,
    reference: &RouteResourceReference,
) -> bool {
    if candidate.is_empty() {
        return false;
    }
    match resource_target(current_path, reference) {
        Some(target) => route_resource_matches_target(&target, candidate, reference),
        None => false,
    }
}

pub(crate) fn route_resource_matches_target(
    target: &str,
    candidate: &str,
    reference: &RouteResourceReference,
) -> bool {
    let candidate = clean_path(candidate);
    if route_resource_has_glob(target) {
        return route_resource_glob_expressions(target)
            .iter()
            .any(|expression| {
                expression.is_match(&candidate)
                    && route_resource_file_accepted(&candidate, reference)
            });
    }
    if target == candidate {
        return std::fs::metadata(&candidate).is_ok_and(|metadata| !metadata.is_dir())
            && route_resource_file_accepted(&candidate, reference);
    }
    if !std::fs::metadata(target).is_ok_and(|metadata| metadata.is_dir())
        || !route_resource_file_accepted(&candidate, reference)
    {
        return false;
    }
    relative_path(target, &candidate)
        .is_some_and(|relative| relative != "." && relative != ".." && !relative.starts_with("../"))
}

/// Walks `root` depth-first in lexical order of entry names, collecting
/// accepted files (optionally filtered by `expressions`) up to the limit.
fn route_resource_directory_files(
    root: &str,
    reference: &RouteResourceReference,
    expressions: &[Regex],
) -> Vec<String> {
    let mut result = Vec::new();
    walk_dir(Path::new(root), &mut |path| {
        if !expressions.is_empty()
            && !expressions
                .iter()
                .any(|expression| expression.is_match(path))
        {
            return true;
        }
        if route_resource_file_accepted(path, reference) {
            result.push(clean_path(path));
            if result.len() >= MAX_ROUTE_RESOURCE_FILES {
                return false;
            }
        }
        true
    });
    result.sort();
    result
}

/// Returns false when `visit` asked to stop.
fn walk_dir(directory: &Path, visit: &mut dyn FnMut(&str) -> bool) -> bool {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return true;
    };
    let mut entries: Vec<_> = entries.flatten().collect();
    entries.sort_by_key(|entry| entry.file_name());
    for entry in entries {
        let path = entry.path();
        let is_dir = entry.file_type().is_ok_and(|kind| kind.is_dir());
        if is_dir {
            if !walk_dir(&path, visit) {
                return false;
            }
            continue;
        }
        if !visit(&path.to_string_lossy()) {
            return false;
        }
    }
    true
}

fn route_resource_glob_files(pattern: &str, reference: &RouteResourceReference) -> Vec<String> {
    let root = route_resource_glob_root(pattern);
    if !std::fs::metadata(&root).is_ok_and(|metadata| metadata.is_dir()) {
        return Vec::new();
    }
    let expressions = route_resource_glob_expressions(pattern);
    if expressions.is_empty() {
        return Vec::new();
    }
    route_resource_directory_files(&root, reference, &expressions)
}

fn route_resource_file_accepted(path: &str, reference: &RouteResourceReference) -> bool {
    let extension = swlsp_textutil::lowercase(extension(path));
    match swlsp_textutil::lowercase(&reference.loader).as_str() {
        "annotation" | "attribute" | "attributes" => return extension == ".php",
        "yaml" | "yml" => return extension == ".yaml" || extension == ".yml",
        "xml" => return extension == ".xml",
        "php" => return extension == ".php",
        _ => {}
    }
    if reference.nested || !reference.namespace.is_empty() {
        return extension == ".php";
    }
    matches!(extension.as_str(), ".php" | ".yaml" | ".yml" | ".xml")
}

fn route_resource_has_glob(path: &str) -> bool {
    path.contains(['*', '?', '[', '{'])
}

fn route_resource_glob_root(pattern: &str) -> String {
    let first = pattern.find(['*', '?', '[', '{']).unwrap_or(pattern.len());
    let mut prefix = pattern[..first].to_owned();
    if !prefix.ends_with('/') {
        prefix = dir_path(&prefix);
    }
    if prefix.is_empty() {
        return "/".to_owned();
    }
    clean_path(&prefix)
}

/// The compiled expressions of a glob pattern. Compiled expressions are
/// memoized per pattern because reverse lookups test every routing import
/// against each open file.
fn route_resource_glob_expressions(pattern: &str) -> Arc<[Regex]> {
    const MAX_CACHED_PATTERNS: usize = 1_024;
    static CACHE: OnceLock<Mutex<HashMap<String, Arc<[Regex]>>>> = OnceLock::new();
    let cache = CACHE.get_or_init(Default::default);
    if let Some(expressions) = cache
        .lock()
        .unwrap_or_else(PoisonError::into_inner)
        .get(pattern)
    {
        return expressions.clone();
    }
    let expressions: Arc<[Regex]> = expand_route_resource_braces(pattern, 0)
        .iter()
        .filter_map(|candidate| Regex::new(&route_resource_glob_regexp(candidate)).ok())
        .collect();
    let mut cache = cache.lock().unwrap_or_else(PoisonError::into_inner);
    if cache.len() >= MAX_CACHED_PATTERNS {
        cache.clear();
    }
    cache.insert(pattern.to_owned(), expressions.clone());
    expressions
}

fn expand_route_resource_braces(pattern: &str, depth: usize) -> Vec<String> {
    if depth >= 8 {
        return vec![pattern.to_owned()];
    }
    let Some(start) = pattern.find('{') else {
        return vec![pattern.to_owned()];
    };
    let Some(end) = pattern[start + 1..].find('}') else {
        return vec![pattern.to_owned()];
    };
    let end = end + start + 1;
    let parts: Vec<&str> = pattern[start + 1..end].split(',').collect();
    if parts.len() < 2 {
        return vec![pattern.to_owned()];
    }
    let mut result = Vec::new();
    for part in parts {
        let next = format!("{}{}{}", &pattern[..start], part, &pattern[end + 1..]);
        result.extend(expand_route_resource_braces(&next, depth + 1));
        if result.len() >= 64 {
            result.truncate(64);
            return result;
        }
    }
    result
}

/// Translates a Symfony glob into a regular expression for the `regex`
/// crate, copying the pattern byte by byte.
fn route_resource_glob_regexp(pattern: &str) -> String {
    let bytes = pattern.as_bytes();
    let mut result: Vec<u8> = vec![b'^'];
    let mut index = 0;
    while index < bytes.len() {
        let character = bytes[index];
        match character {
            b'*' => {
                if index + 1 < bytes.len() && bytes[index + 1] == b'*' {
                    index += 1;
                    if index + 1 < bytes.len() && bytes[index + 1] == b'/' {
                        index += 1;
                        result.extend_from_slice(b"(?:.*/)?");
                    } else {
                        result.extend_from_slice(b".*");
                    }
                } else {
                    result.extend_from_slice(b"[^/]*");
                }
            }
            b'?' => result.extend_from_slice(b"[^/]"),
            b'[' => match bytes[index + 1..].iter().position(|&byte| byte == b']') {
                None => result.extend_from_slice(b"\\["),
                Some(end) => {
                    let end = end + index + 1;
                    let class = &bytes[index + 1..end];
                    result.push(b'[');
                    match class.strip_prefix(b"!") {
                        Some(rest) => {
                            result.push(b'^');
                            result.extend_from_slice(rest);
                        }
                        None => result.extend_from_slice(class),
                    }
                    result.push(b']');
                    index = end;
                }
            },
            _ => {
                if b".+()|^$\\".contains(&character) {
                    result.push(b'\\');
                }
                result.push(character);
            }
        }
        index += 1;
    }
    result.push(b'$');
    String::from_utf8_lossy(&result).into_owned()
}

#[cfg(test)]
#[path = "tests_routes/route_resource.rs"]
mod tests;
