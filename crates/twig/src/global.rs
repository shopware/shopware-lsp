//! Workspace-wide Twig globals from YAML configuration and PHP extensions.

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::NameResolver;
use swlsp_php_semantic::Document;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::extension_parser::method_named;

/// Persisted as its `u8` discriminant.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
#[repr(u8)]
pub enum GlobalSource {
    #[default]
    Yaml = 0,
    PhpExtension = 1,
    Container = 2,
}

impl Serialize for GlobalSource {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_u8(*self as u8)
    }
}

impl<'de> Deserialize<'de> for GlobalSource {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        Ok(match u8::deserialize(deserializer)? {
            1 => Self::PhpExtension,
            2 => Self::Container,
            _ => Self::Yaml,
        })
    }
}

impl GlobalSource {
    pub fn as_u8(self) -> u8 {
        self as u8
    }
}

impl std::fmt::Display for GlobalSource {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Yaml => "Twig configuration",
            Self::PhpExtension => "Twig extension",
            Self::Container => "compiled container",
        })
    }
}

/// One workspace-wide variable available in every Twig template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Global {
    pub name: String,
    #[serde(rename = "type")]
    pub ty: String,
    pub value: String,
    pub service_id: String,
    pub file: String,
    pub range: TextRange,
    pub source: GlobalSource,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct GlobalCatalog {
    pub file: String,
    pub globals: Vec<Global>,
}

pub fn globals_in_yaml(path: &str, root: &SyntaxNode) -> Vec<Global> {
    let Some(value) = yamlquery::root_value(root) else {
        return Vec::new();
    };
    if !yamlquery::is_mapping(&value) {
        return Vec::new();
    }
    let mut result = Vec::new();
    for twig_config in twig_yaml_configurations(&value) {
        let Some(globals) = yamlquery::property(&twig_config, "globals") else {
            continue;
        };
        if !yamlquery::is_mapping(&globals) {
            continue;
        }
        for pair in yamlquery::pairs(&globals) {
            let key = yamlquery::pair_key(&pair);
            let name = key
                .as_ref()
                .map(yamlquery::scalar_value)
                .unwrap_or_default();
            if name.is_empty() {
                continue;
            }
            let value_node = yamlquery::pair_value(&pair);
            let mut global = Global {
                name,
                ty: yaml_global_type(value_node.as_ref()),
                value: value_node
                    .as_ref()
                    .map(yamlquery::scalar_value)
                    .unwrap_or_default(),
                file: path.to_owned(),
                range: yaml_global_scalar_range(key.as_ref()),
                source: GlobalSource::Yaml,
                ..Global::default()
            };
            if let Some(service) = yaml_global_service(value_node.as_ref()) {
                global.service_id = service;
                global.ty = String::new();
            }
            result.push(global);
        }
    }
    unique_globals(result)
}

fn twig_yaml_configurations(root: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    if let Some(twig) = yamlquery::property(root, "twig")
        && yamlquery::is_mapping(&twig)
    {
        result.push(twig);
    }
    for pair in yamlquery::pairs(root) {
        let key = yamlquery::pair_key(&pair)
            .map(|key| yamlquery::scalar_value(&key))
            .unwrap_or_default();
        let Some(value) = yamlquery::pair_value(&pair) else {
            continue;
        };
        if !key.starts_with("when@") || !yamlquery::is_mapping(&value) {
            continue;
        }
        if let Some(twig) = yamlquery::property(&value, "twig")
            && yamlquery::is_mapping(&twig)
        {
            result.push(twig);
        }
    }
    result
}

fn yaml_global_service(node: Option<&SyntaxNode>) -> Option<String> {
    let node = node?;
    if node.kind() != YAML_SCALAR {
        return None;
    }
    let value = yamlquery::scalar_value(node);
    let value = value.trim();
    if value.starts_with("@@") || !value.starts_with('@') {
        return None;
    }
    let value = &value[1..];
    let value = value.strip_prefix('?').unwrap_or(value);
    (!value.is_empty()).then(|| value.to_owned())
}

fn yaml_global_type(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node else {
        return "null".to_owned();
    };
    if yamlquery::is_null(node) {
        return "null".to_owned();
    }
    if yamlquery::is_mapping(node) || yamlquery::is_sequence(node) {
        return "array".to_owned();
    }
    let raw = yamlquery::raw_text(node);
    let raw = raw.trim();
    if raw.is_empty() {
        return String::new();
    }
    if raw.starts_with('\'') || raw.starts_with('"') || raw.starts_with("@@") {
        return "string".to_owned();
    }
    if raw.eq_ignore_ascii_case("true") || raw.eq_ignore_ascii_case("false") {
        return "bool".to_owned();
    }
    if raw.parse::<i64>().is_ok() {
        return "int".to_owned();
    }
    if is_float_literal(raw) {
        return "float".to_owned();
    }
    "string".to_owned()
}

/// Whether `raw` is a float: a decimal number in range, `inf`/`infinity`
/// with an optional sign, or an unsigned `nan` (all case-insensitive).
fn is_float_literal(raw: &str) -> bool {
    let unsigned = raw.strip_prefix(['+', '-']).unwrap_or(raw);
    let lower = unsigned.to_ascii_lowercase();
    if matches!(lower.as_str(), "inf" | "infinity") {
        return true;
    }
    if lower == "nan" {
        return unsigned.len() == raw.len();
    }
    if !unsigned
        .bytes()
        .all(|byte| byte.is_ascii_digit() || matches!(byte, b'.' | b'e' | b'E' | b'+' | b'-'))
    {
        return false;
    }
    raw.parse::<f64>().is_ok_and(f64::is_finite)
}

fn yaml_global_scalar_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    let (mut start, mut end) = (range.start(), range.end());
    if !text.is_empty() && (text[0] == b'\'' || text[0] == b'"') {
        start += TextSize::from(1);
        if text.len() >= 2 && text[text.len() - 1] == text[0] {
            end -= TextSize::from(1);
        }
    }
    TextRange::new(start, end.max(start))
}

pub fn globals_in_php_extension(
    path: &str,
    root: &SyntaxNode,
    document: Option<&Document>,
) -> Vec<Global> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        if !is_twig_extension_class(&class, &resolver) {
            continue;
        }
        let Some(method) = method_named(&class, "getGlobals") else {
            continue;
        };
        for statement in phpquery::nodes(&method, &[PHP_RETURN_STATEMENT]) {
            if phpquery::function_like_at(&statement).as_ref() != Some(&method) {
                continue;
            }
            let Some(array) = statement
                .children()
                .next()
                .and_then(|expression| phpquery::array_at(&expression))
            else {
                continue;
            };
            for item in phpquery::array_items(&array) {
                let key = phpquery::array_item_key(&item);
                let Some(value) = phpquery::array_item_value(&item) else {
                    continue;
                };
                let name = crate::extension_parser::sv(key.as_ref());
                if name.is_empty() {
                    continue;
                }
                result.push(Global {
                    name,
                    ty: php_global_type(&value, document, &resolver),
                    value: value.text().to_string().trim().to_owned(),
                    file: path.to_owned(),
                    range: php_global_string_range(key.as_ref()),
                    source: GlobalSource::PhpExtension,
                    ..Global::default()
                });
            }
        }
    }
    unique_globals(result)
}

pub(crate) fn is_twig_extension_class(class: &SyntaxNode, resolver: &NameResolver) -> bool {
    phpquery::class_extends(class)
        .into_iter()
        .chain(phpquery::class_implements(class))
        .any(|name| {
            let resolved = resolver.resolve(&name);
            let resolved = resolved.strip_prefix('\\').unwrap_or(&resolved);
            matches!(
                swlsp_textutil::lowercase(resolved).as_str(),
                "twig\\extension\\abstractextension"
                    | "twig\\extension\\extensioninterface"
                    | "twig\\extension\\globalsinterface"
                    | "twig_extension"
                    | "twig_extensioninterface"
            )
        })
}

fn php_global_type(
    node: &SyntaxNode,
    document: Option<&Document>,
    resolver: &NameResolver,
) -> String {
    if let Some(document) = document {
        let value = document.type_of(node).ty;
        if !value.is_unknown() {
            return value.to_string();
        }
    }
    match node.kind() {
        PHP_STRING => "string".to_owned(),
        PHP_NUMBER => {
            if node.text().to_string().contains(['.', 'e', 'E']) {
                "float".to_owned()
            } else {
                "int".to_owned()
            }
        }
        PHP_BOOLEAN => "bool".to_owned(),
        PHP_NULL => "null".to_owned(),
        PHP_ARRAY => "array".to_owned(),
        PHP_OBJECT_CREATION => {
            let resolved = resolver.resolve(&phpquery::object_class_name(node));
            resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned()
        }
        _ => String::new(),
    }
}

fn php_global_string_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2
        && (text[0] == b'\'' || text[0] == b'"')
        && text[text.len() - 1] == text[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

pub(crate) fn unique_globals(values: Vec<Global>) -> Vec<Global> {
    let mut seen = std::collections::HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| {
            !value.name.is_empty()
                && seen.insert((
                    swlsp_textutil::lowercase(&value.name),
                    value.file.clone(),
                    value.range,
                ))
        })
        .collect()
}
