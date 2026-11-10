//! `!php/const` / `!php/enum` values in YAML and `type="constant"` XML
//! arguments.

use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax as xmlsyntax;
use swlsp_php::PhpIndex;
use swlsp_php_project::{Model, Version};
use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_syntax::SyntaxNodeExt as _;
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

use crate::util::xml_attr;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum ContainerValueKind {
    #[default]
    Constant,
    Enum,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ContainerConstantReference {
    pub name: String,
    pub range: TextRange,
    pub kind: ContainerValueKind,
}

pub fn supports_yaml_container_enum(model: Option<&Model>) -> bool {
    symfony_yaml_version(model).at_least(6, 2)
}

pub fn supports_yaml_container_enum_class(model: Option<&Model>) -> bool {
    symfony_yaml_version(model).at_least(7, 1)
}

fn symfony_yaml_version(model: Option<&Model>) -> Version {
    model
        .and_then(|model| {
            model.dependency_version(&[
                "symfony/yaml",
                "symfony/framework-bundle",
                "symfony/symfony",
            ])
        })
        .unwrap_or(Version::new(7, 1, 0))
}

fn range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

/// All container value references of a YAML document.
pub fn yaml_container_constant_references(content: &str) -> Vec<ContainerConstantReference> {
    yaml_references(content.as_bytes())
}

fn yaml_references(content: &[u8]) -> Vec<ContainerConstantReference> {
    let mut result = Vec::new();
    let mut line_start = 0;
    while line_start < content.len() {
        let line_end = content[line_start..]
            .iter()
            .position(|&byte| byte == b'\n')
            .map_or(content.len(), |end| end + line_start);
        result.extend(yaml_constant_references_in_line(
            &content[line_start..line_end],
            line_start,
        ));
        if line_end == content.len() {
            break;
        }
        line_start = line_end + 1;
    }
    result
}

/// The partially typed reference ending at `offset`, if any.
pub fn yaml_container_constant_completion_at(
    content: &str,
    offset: u32,
) -> Option<ContainerConstantReference> {
    let offset = offset as usize;
    if offset > content.len() {
        return None;
    }
    let mut probe = content.as_bytes()[..offset].to_vec();
    probe.push(b'X');
    let references = yaml_references(&probe);
    references.into_iter().rev().find_map(|mut reference| {
        if usize::from(reference.range.end()) != probe.len() || !reference.name.ends_with('X') {
            return None;
        }
        reference.name.pop();
        reference.range = TextRange::new(
            reference.range.start(),
            reference.range.end() - TextSize::from(1),
        );
        Some(reference)
    })
}

/// `<argument type="constant">` values of an XML document.
pub fn xml_container_constant_references(root: &SyntaxNode) -> Vec<ContainerConstantReference> {
    let mut result = Vec::new();
    for argument in xmlquery::elements(root, &["argument"]) {
        if !xml_attr(&argument, "type").eq_ignore_ascii_case("constant") {
            continue;
        }
        for text in xmlquery::nodes(&argument, &[xmlsyntax::XML_TEXT]) {
            let Some((name, range)) = trimmed_constant_text(&text) else {
                continue;
            };
            result.push(ContainerConstantReference {
                name,
                range,
                kind: ContainerValueKind::Constant,
            });
            break;
        }
    }
    result
}

/// Declarations of a `Class::NAME` or global constant name.
pub fn resolve_container_constant(index: Option<&PhpIndex>, name: &str) -> Vec<Symbol> {
    let Some(index) = index else {
        return Vec::new();
    };
    let name = name.trim();
    if name.is_empty() {
        return Vec::new();
    }
    if let Some(scope) = name.find("::") {
        if scope == 0 || scope + 2 >= name.len() {
            return Vec::new();
        }
        return index.find_constants(&name[..scope], &name[scope + 2..]);
    }
    index.find_global_constants(name)
}

/// Resolves a parsed YAML/XML value reference. Enum tags accept one enum
/// case or, since Symfony 7.1, an enum class.
pub fn resolve_container_value(
    index: Option<&PhpIndex>,
    reference: &ContainerConstantReference,
) -> Vec<Symbol> {
    if reference.kind != ContainerValueKind::Enum {
        return resolve_container_constant(index, &reference.name);
    }
    let Some(php) = index else {
        return Vec::new();
    };
    if !reference.name.contains("::") {
        let name = reference.name.strip_prefix('\\').unwrap_or(&reference.name);
        return php
            .find_class(name)
            .filter(|symbol| symbol.kind == SymbolKind::Enum)
            .into_iter()
            .collect();
    }
    resolve_container_constant(index, &reference.name)
        .into_iter()
        .filter(|symbol| symbol.kind == SymbolKind::EnumCase)
        .collect()
}

fn yaml_constant_references_in_line(
    line: &[u8],
    line_start: usize,
) -> Vec<ContainerConstantReference> {
    let mut result = Vec::new();
    let mut quote = 0u8;
    let mut escaped = false;
    let mut cursor = 0;
    while cursor < line.len() {
        let value = line[cursor];
        if quote != 0 {
            if quote == b'"' && value == b'\\' && !escaped {
                escaped = true;
                cursor += 1;
                continue;
            }
            if value == quote && !escaped {
                if quote == b'\'' && cursor + 1 < line.len() && line[cursor + 1] == b'\'' {
                    cursor += 2;
                    continue;
                }
                quote = 0;
            }
            escaped = false;
            cursor += 1;
            continue;
        }
        match value {
            b'\'' | b'"' => {
                quote = value;
                cursor += 1;
                continue;
            }
            b'#' => return result,
            _ => {}
        }
        let Some((marker, kind)) = yaml_container_value_marker(&line[cursor..]) else {
            cursor += 1;
            continue;
        };
        let mut after = cursor + marker.len();
        if after < line.len() && line[after] != b':' && !is_space(line[after]) {
            cursor += 1;
            continue;
        }
        if after < line.len() && line[after] == b':' {
            after += 1;
        }
        while after < line.len() && is_space(line[after]) {
            after += 1;
        }
        let mut start = after;
        let mut end = after;
        if after < line.len() && (line[after] == b'\'' || line[after] == b'"') {
            let delimiter = line[after];
            start += 1;
            end = start;
            while end < line.len() && line[end] != delimiter {
                end += 1;
            }
        } else {
            while end < line.len() && is_name_byte(line[end]) {
                end += 1;
            }
        }
        if kind == ContainerValueKind::Enum && line[start..end].ends_with(b"->value") {
            end -= "->value".len();
        }
        if end > start {
            result.push(ContainerConstantReference {
                name: String::from_utf8_lossy(&line[start..end]).into_owned(),
                range: range(line_start + start, line_start + end),
                kind,
            });
        }
        // Resume after the character that ended the reference.
        cursor = end + 1;
    }
    result
}

fn yaml_container_value_marker(content: &[u8]) -> Option<(&'static str, ContainerValueKind)> {
    [
        ("!php/const", ContainerValueKind::Constant),
        ("!php/enum", ContainerValueKind::Enum),
    ]
    .into_iter()
    .find(|(marker, _)| content.starts_with(marker.as_bytes()))
}

fn trimmed_constant_text(node: &SyntaxNode) -> Option<(String, TextRange)> {
    let text = node.text_string();
    let left = text.len() - text.trim_start_matches([' ', '\t', '\r', '\n']).len();
    let right = text.trim_end_matches([' ', '\t', '\r', '\n']).len();
    if right <= left {
        return None;
    }
    let start = usize::from(node.text_range().start());
    Some((
        text[left..right].to_owned(),
        range(start + left, start + right),
    ))
}

fn is_space(value: u8) -> bool {
    value == b' ' || value == b'\t'
}

fn is_name_byte(value: u8) -> bool {
    value == b'\\'
        || value == b':'
        || value == b'_'
        || value.is_ascii_alphanumeric()
        || value >= 0x80
}

#[cfg(test)]
#[path = "tests_refs/container_constant.rs"]
mod tests;
