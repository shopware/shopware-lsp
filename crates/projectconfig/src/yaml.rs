//! Strict YAML decoding with the semantics of `gopkg.in/yaml.v3` using
//! `Decoder.KnownFields(true)`.
//!
//! Scalars are kept in their source representation so string targets
//! receive the exact text (`targetVersion: 6.10` stays `"6.10"`), and the
//! type-mismatch / unknown-field messages carry yaml.v3's wording and line
//! numbers. Errors are collected and reported together like yaml.v3.

use std::collections::BTreeMap;

use saphyr::{MarkedYaml, ScalarStyle, YamlData, YamlLoader};
use saphyr_parser::Parser;

use crate::config::{
    CheckConfig, DiagnosticOverride, DiagnosticsConfig, IndexingConfig, McpConfig, Partial,
    PhpConfig, Severity, ShopwareConfig,
};

pub(crate) enum LoadOutcome<'a> {
    Documents(Vec<MarkedYaml<'a>>),
    SyntaxError(String),
}

pub(crate) fn load_documents(source: &str) -> LoadOutcome<'_> {
    let mut loader: YamlLoader<'_, MarkedYaml<'_>> = YamlLoader::default();
    loader.early_parse(false);
    let mut parser = Parser::new_from_str(source);
    if let Err(error) = parser.load(&mut loader, true) {
        return LoadOutcome::SyntaxError(scan_error(&error));
    }
    if let Some(error) = loader.error() {
        return LoadOutcome::SyntaxError(scan_error(error));
    }
    LoadOutcome::Documents(loader.into_documents())
}

fn scan_error(error: &saphyr::ScanError) -> String {
    format!("yaml: line {}: {}", error.marker().line(), error.info())
}

#[derive(Debug, Clone, PartialEq)]
enum Resolved {
    Null,
    Bool(bool),
    Int(i128),
    Float(f64),
    Str,
}

impl Resolved {
    fn tag(&self) -> &'static str {
        match self {
            Resolved::Null => "!!null",
            Resolved::Bool(_) => "!!bool",
            Resolved::Int(_) => "!!int",
            Resolved::Float(_) => "!!float",
            Resolved::Str => "!!str",
        }
    }
}

fn parse_go_int_base0(value: &str) -> Option<i128> {
    let (negative, rest) = match value.as_bytes().first() {
        Some(b'-') => (true, &value[1..]),
        Some(b'+') => (false, &value[1..]),
        _ => (false, value),
    };
    let lower = rest.to_ascii_lowercase();
    let (radix, digits) = if let Some(digits) = lower.strip_prefix("0x") {
        (16, digits.to_owned())
    } else if let Some(digits) = lower.strip_prefix("0b") {
        (2, digits.to_owned())
    } else if let Some(digits) = lower.strip_prefix("0o") {
        (8, digits.to_owned())
    } else if lower.len() > 1 && lower.starts_with('0') {
        (8, lower[1..].to_owned())
    } else {
        (10, lower)
    };
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_alphanumeric()) {
        return None;
    }
    let magnitude = i128::from_str_radix(&digits, radix).ok()?;
    let value = if negative { -magnitude } else { magnitude };
    // Go ParseInt (int64) or ParseUint (uint64) must accept the value.
    (value >= i64::MIN as i128 && value <= u64::MAX as i128).then_some(value)
}

fn is_yaml_style_float(value: &str) -> bool {
    let bytes = value.as_bytes();
    let mut index = 0;
    if matches!(bytes.first(), Some(b'-' | b'+')) {
        index += 1;
    }
    let digits_start = index;
    while index < bytes.len() && bytes[index].is_ascii_digit() {
        index += 1;
    }
    let integer_digits = index - digits_start;
    if index < bytes.len() && bytes[index] == b'.' {
        index += 1;
        let fraction_start = index;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
        }
        if integer_digits == 0 && index == fraction_start {
            return false;
        }
    } else if integer_digits == 0 {
        return false;
    }
    if index < bytes.len() && (bytes[index] == b'e' || bytes[index] == b'E') {
        index += 1;
        if index < bytes.len() && (bytes[index] == b'-' || bytes[index] == b'+') {
            index += 1;
        }
        let exponent_start = index;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
        }
        if index == exponent_start {
            return false;
        }
    }
    index == bytes.len()
}

fn resolve_plain(text: &str) -> Resolved {
    match text {
        "" | "~" | "null" | "Null" | "NULL" => return Resolved::Null,
        "true" | "True" | "TRUE" => return Resolved::Bool(true),
        "false" | "False" | "FALSE" => return Resolved::Bool(false),
        ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF" => {
            return Resolved::Float(f64::INFINITY);
        }
        "-.inf" | "-.Inf" | "-.INF" => return Resolved::Float(f64::NEG_INFINITY),
        ".nan" | ".NaN" | ".NAN" => return Resolved::Float(f64::NAN),
        _ => {}
    }
    let first = text.as_bytes()[0];
    if first.is_ascii_digit() || first == b'-' || first == b'+' || first == b'.' {
        let plain = text.replace('_', "");
        if first != b'.'
            && let Some(value) = parse_go_int_base0(&plain)
        {
            return Resolved::Int(value);
        }
        if is_yaml_style_float(&plain)
            && let Ok(value) = plain.parse::<f64>()
        {
            return Resolved::Float(value);
        }
    }
    Resolved::Str
}

struct ScalarView<'n> {
    text: &'n str,
    resolved: Resolved,
}

fn scalar<'n>(node: &'n MarkedYaml<'_>) -> Option<ScalarView<'n>> {
    match &node.data {
        YamlData::Representation(text, style, tag) => {
            let explicit_string = tag
                .as_ref()
                .is_some_and(|tag| tag.is_yaml_core_schema() && tag.suffix == "str");
            let resolved = if *style != ScalarStyle::Plain || explicit_string {
                Resolved::Str
            } else {
                resolve_plain(text)
            };
            Some(ScalarView {
                text: text.as_ref(),
                resolved,
            })
        }
        YamlData::BadValue => Some(ScalarView {
            text: "",
            resolved: Resolved::Null,
        }),
        YamlData::Tagged(_, inner) => scalar(inner),
        _ => None,
    }
}

fn is_null(node: &MarkedYaml<'_>) -> bool {
    scalar(node).is_some_and(|view| view.resolved == Resolved::Null)
}

fn line(node: &MarkedYaml<'_>) -> usize {
    node.span.start.line()
}

fn untagged<'n, 'a>(node: &'n MarkedYaml<'a>) -> &'n MarkedYaml<'a> {
    match &node.data {
        YamlData::Tagged(_, inner) => untagged(inner),
        _ => node,
    }
}

pub(crate) struct Decoder {
    errors: Vec<String>,
}

impl Decoder {
    pub(crate) fn new() -> Self {
        Self { errors: Vec::new() }
    }

    pub(crate) fn finish(self) -> Result<(), String> {
        if self.errors.is_empty() {
            return Ok(());
        }
        Err(format!(
            "yaml: unmarshal errors:\n  {}",
            self.errors.join("\n  ")
        ))
    }

    fn type_error(&mut self, node: &MarkedYaml<'_>, target: &str) {
        let node = untagged(node);
        let (tag, value) = match &node.data {
            YamlData::Mapping(_) => ("!!map", String::new()),
            YamlData::Sequence(_) => ("!!seq", String::new()),
            _ => match scalar(node) {
                Some(view) => {
                    let value = if view.text.len() > 10 {
                        let mut end = 7;
                        while !view.text.is_char_boundary(end) {
                            end -= 1;
                        }
                        format!(" `{}...`", &view.text[..end])
                    } else {
                        format!(" `{}`", view.text)
                    };
                    (view.resolved.tag(), value)
                }
                None => ("!!str", String::new()),
            },
        };
        self.errors.push(format!(
            "line {}: cannot unmarshal {tag}{value} into {target}",
            line(node)
        ));
    }

    /// Decodes a mapping into a struct-like target, reporting unknown keys.
    fn decode_struct(
        &mut self,
        node: &MarkedYaml<'_>,
        type_name: &str,
        mut field: impl FnMut(&mut Self, &str, &MarkedYaml<'_>) -> bool,
    ) -> bool {
        let node = untagged(node);
        let YamlData::Mapping(mapping) = &node.data else {
            if !is_null(node) {
                self.type_error(node, type_name);
            }
            return false;
        };
        for (key, value) in mapping {
            let Some(name) = self.string(key) else {
                continue;
            };
            if !field(self, &name, value) {
                self.errors.push(format!(
                    "line {}: field {name} not found in type {type_name}",
                    line(key)
                ));
            }
        }
        true
    }

    /// Decodes into a Go `string`-kind target. Null yields an empty string.
    fn string(&mut self, node: &MarkedYaml<'_>) -> Option<String> {
        self.string_as(node, "string")
    }

    fn string_as(&mut self, node: &MarkedYaml<'_>, target: &str) -> Option<String> {
        match scalar(node) {
            Some(view) if view.resolved == Resolved::Null => Some(String::new()),
            Some(view) => Some(view.text.to_owned()),
            None => {
                self.type_error(node, target);
                None
            }
        }
    }

    fn optional_string(&mut self, node: &MarkedYaml<'_>) -> Option<Option<String>> {
        if is_null(node) {
            return Some(None);
        }
        self.string(node).map(Some)
    }

    fn bool_value(&mut self, node: &MarkedYaml<'_>) -> Option<bool> {
        let Some(view) = scalar(node) else {
            self.type_error(node, "bool");
            return None;
        };
        match view.resolved {
            Resolved::Null => Some(false),
            Resolved::Bool(value) => Some(value),
            Resolved::Str => match view.text {
                "y" | "Y" | "yes" | "Yes" | "YES" | "on" | "On" | "ON" => Some(true),
                "n" | "N" | "no" | "No" | "NO" | "off" | "Off" | "OFF" => Some(false),
                _ => {
                    self.type_error(node, "bool");
                    None
                }
            },
            _ => {
                self.type_error(node, "bool");
                None
            }
        }
    }

    fn optional_bool(&mut self, node: &MarkedYaml<'_>) -> Option<Option<bool>> {
        if is_null(node) {
            return Some(None);
        }
        self.bool_value(node).map(Some)
    }

    fn optional_int(&mut self, node: &MarkedYaml<'_>, target: &str) -> Option<Option<i64>> {
        let Some(view) = scalar(node) else {
            self.type_error(node, target);
            return None;
        };
        match view.resolved {
            Resolved::Null => Some(None),
            Resolved::Int(value) if value >= i64::MIN as i128 && value <= i64::MAX as i128 => {
                Some(Some(value as i64))
            }
            Resolved::Float(value) if value <= i64::MAX as f64 => Some(Some(value as i64)),
            _ => {
                self.type_error(node, target);
                None
            }
        }
    }

    fn optional_string_list(&mut self, node: &MarkedYaml<'_>) -> Option<Option<Vec<String>>> {
        if is_null(node) {
            return Some(None);
        }
        self.string_list(node).map(Some)
    }

    fn string_list(&mut self, node: &MarkedYaml<'_>) -> Option<Vec<String>> {
        let node = untagged(node);
        let YamlData::Sequence(items) = &node.data else {
            if is_null(node) {
                return Some(Vec::new());
            }
            self.type_error(node, "[]string");
            return None;
        };
        Some(items.iter().filter_map(|item| self.string(item)).collect())
    }

    fn map<T>(
        &mut self,
        node: &MarkedYaml<'_>,
        type_name: &str,
        mut value: impl FnMut(&mut Self, &MarkedYaml<'_>) -> Option<T>,
    ) -> BTreeMap<String, T> {
        let node = untagged(node);
        let mut result = BTreeMap::new();
        let YamlData::Mapping(mapping) = &node.data else {
            if !is_null(node) {
                self.type_error(node, type_name);
            }
            return result;
        };
        for (key, item) in mapping {
            let Some(key) = self.string(key) else {
                continue;
            };
            if let Some(item) = value(self, item) {
                result.insert(key, item);
            }
        }
        result
    }

    fn bool_map(&mut self, node: &MarkedYaml<'_>) -> BTreeMap<String, bool> {
        self.map(node, "map[string]bool", |decoder, item| {
            decoder.bool_value(item)
        })
    }

    fn severity_map(&mut self, node: &MarkedYaml<'_>) -> BTreeMap<String, Severity> {
        self.map(
            node,
            "map[string]projectconfig.Severity",
            |decoder, item| {
                decoder
                    .string_as(item, "projectconfig.Severity")
                    .map(Severity::from)
            },
        )
    }

    fn optional_severity(&mut self, node: &MarkedYaml<'_>) -> Option<Option<Severity>> {
        if is_null(node) {
            return Some(None);
        }
        self.string_as(node, "projectconfig.Severity")
            .map(|value| Some(Severity::from(value)))
    }

    pub(crate) fn partial(&mut self, node: &MarkedYaml<'_>) -> Partial {
        let mut result = Partial::default();
        self.decode_struct(node, "projectconfig.Partial", |decoder, name, value| {
            match name {
                "$schema" => {
                    if let Some(schema) = decoder.string(value) {
                        result.schema = schema;
                    }
                }
                "version" => {
                    if let Some(version) = decoder.optional_int(value, "int") {
                        result.version = version;
                    }
                }
                "php" => result.php = decoder.php(value),
                "shopware" => result.shopware = decoder.shopware(value),
                "features" => result.features = decoder.bool_map(value),
                "indexing" => result.indexing = decoder.indexing(value),
                "mcp" => result.mcp = decoder.mcp(value),
                "domains" => result.domains = decoder.bool_map(value),
                "diagnostics" => result.diagnostics = decoder.diagnostics(value),
                "check" => result.check = decoder.check(value),
                _ => return false,
            }
            true
        });
        result
    }

    fn php(&mut self, node: &MarkedYaml<'_>) -> Option<PhpConfig> {
        let mut result = PhpConfig::default();
        self.decode_struct(node, "projectconfig.PHPConfig", |decoder, name, value| {
            match name {
                "extensions" => {
                    if let Some(values) = decoder.optional_string_list(value) {
                        result.extensions = values;
                    }
                }
                "disabledExtensions" => {
                    if let Some(values) = decoder.optional_string_list(value) {
                        result.disabled_extensions = values;
                    }
                }
                _ => return false,
            }
            true
        })
        .then_some(result)
    }

    fn shopware(&mut self, node: &MarkedYaml<'_>) -> Option<ShopwareConfig> {
        let mut result = ShopwareConfig::default();
        self.decode_struct(
            node,
            "projectconfig.ShopwareConfig",
            |decoder, name, value| {
                match name {
                    "targetVersion" => {
                        if let Some(version) = decoder.optional_string(value) {
                            result.target_version = version;
                        }
                    }
                    _ => return false,
                }
                true
            },
        )
        .then_some(result)
    }

    fn indexing(&mut self, node: &MarkedYaml<'_>) -> Option<IndexingConfig> {
        let mut result = IndexingConfig::default();
        self.decode_struct(
            node,
            "projectconfig.IndexingConfig",
            |decoder, name, value| {
                match name {
                    "enabled" => {
                        if let Some(enabled) = decoder.optional_bool(value) {
                            result.enabled = enabled;
                        }
                    }
                    "exclude" => {
                        if let Some(values) = decoder.optional_string_list(value) {
                            result.exclude = values;
                        }
                    }
                    "maxFileSizeMiB" => {
                        if let Some(size) = decoder.optional_int(value, "int64") {
                            result.max_file_size_mib = size;
                        }
                    }
                    _ => return false,
                }
                true
            },
        )
        .then_some(result)
    }

    fn mcp(&mut self, node: &MarkedYaml<'_>) -> Option<McpConfig> {
        let mut result = McpConfig::default();
        self.decode_struct(node, "projectconfig.MCPConfig", |decoder, name, value| {
            match name {
                "tools" => result.tools = decoder.bool_map(value),
                _ => return false,
            }
            true
        })
        .then_some(result)
    }

    fn diagnostics(&mut self, node: &MarkedYaml<'_>) -> Option<DiagnosticsConfig> {
        let mut result = DiagnosticsConfig::default();
        self.decode_struct(
            node,
            "projectconfig.DiagnosticsConfig",
            |decoder, name, value| {
                match name {
                    "enabled" => {
                        if let Some(enabled) = decoder.optional_bool(value) {
                            result.enabled = enabled;
                        }
                    }
                    "inspections" => result.inspections = decoder.bool_map(value),
                    "rules" => result.rules = decoder.severity_map(value),
                    "overrides" => result.overrides = decoder.overrides(value),
                    _ => return false,
                }
                true
            },
        )
        .then_some(result)
    }

    fn overrides(&mut self, node: &MarkedYaml<'_>) -> Vec<DiagnosticOverride> {
        let node = untagged(node);
        let YamlData::Sequence(items) = &node.data else {
            if !is_null(node) {
                self.type_error(node, "[]projectconfig.DiagnosticOverride");
            }
            return Vec::new();
        };
        items
            .iter()
            .map(|item| {
                let mut result = DiagnosticOverride::default();
                self.decode_struct(
                    item,
                    "projectconfig.DiagnosticOverride",
                    |decoder, name, value| {
                        match name {
                            "files" => {
                                if let Some(files) = decoder.string_list(value) {
                                    result.files = files;
                                }
                            }
                            "enabled" => {
                                if let Some(enabled) = decoder.optional_bool(value) {
                                    result.enabled = enabled;
                                }
                            }
                            "inspections" => result.inspections = decoder.bool_map(value),
                            "rules" => result.rules = decoder.severity_map(value),
                            _ => return false,
                        }
                        true
                    },
                );
                result
            })
            .collect()
    }

    fn check(&mut self, node: &MarkedYaml<'_>) -> Option<CheckConfig> {
        let mut result = CheckConfig::default();
        self.decode_struct(node, "projectconfig.CheckConfig", |decoder, name, value| {
            match name {
                "severity" => {
                    if let Some(severity) = decoder.optional_severity(value) {
                        result.severity = severity;
                    }
                }
                "failOn" => {
                    if let Some(severity) = decoder.optional_severity(value) {
                        result.fail_on = severity;
                    }
                }
                _ => return false,
            }
            true
        })
        .then_some(result)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plain_scalar_resolution_matches_yaml_v3() {
        assert_eq!(resolve_plain("1"), Resolved::Int(1));
        assert_eq!(resolve_plain("0x1F"), Resolved::Int(31));
        assert_eq!(resolve_plain("017"), Resolved::Int(15));
        assert_eq!(resolve_plain("0o17"), Resolved::Int(15));
        assert_eq!(resolve_plain("1_000"), Resolved::Int(1000));
        assert_eq!(resolve_plain("-5"), Resolved::Int(-5));
        assert_eq!(resolve_plain("6.5"), Resolved::Float(6.5));
        assert_eq!(resolve_plain(".5"), Resolved::Float(0.5));
        assert_eq!(resolve_plain("1e3"), Resolved::Float(1000.0));
        assert_eq!(resolve_plain("6.5.0"), Resolved::Str);
        assert_eq!(resolve_plain("yes"), Resolved::Str);
        assert_eq!(resolve_plain("~"), Resolved::Null);
        assert_eq!(resolve_plain("TRUE"), Resolved::Bool(true));
    }
}
