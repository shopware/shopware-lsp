//! Missing-key insertions into writable catalogue resources (port of Go
//! `insertion.go`).

use std::cmp::Reverse;
use std::collections::BTreeMap;

use swlsp_indexer::{ParsedFile, Result};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{LineIndex, SyntaxNode, Tree};

use crate::parser::{catalogue_metadata, parse_xml_resource, parse_yaml_resource};
use crate::{Index, lower_extension, normalize_domain};

/// One text insertion at a zero-based UTF-16 position of a resource file.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Insertion {
    pub file: String,
    pub format: String,
    pub locale: String,
    pub line: u32,
    pub character: u32,
    pub new_text: String,
}

const MAX_INSERTION_TARGETS: usize = 8;

impl Index {
    /// Safe edits for existing writable YAML and XLIFF resources of `domain`,
    /// one per file, using the key as value.
    pub fn insertions(&self, domain: &str, key: &str) -> Result<Vec<Insertion>> {
        self.insertions_with_value(domain, key, key)
    }

    /// Like [`Index::insertions`], writing `value` as the translation text.
    pub fn insertions_with_value(
        &self,
        domain: &str,
        key: &str,
        value: &str,
    ) -> Result<Vec<Insertion>> {
        let mut result = Vec::new();
        for target in self.insertion_targets(domain)? {
            let Ok(content) = std::fs::read(&target.file) else {
                continue;
            };
            let source = String::from_utf8_lossy(&content);
            if let Some(mut insertion) = insertion_for_source(&target.file, &source, key, value) {
                insertion.locale = target.locale;
                result.push(insertion);
            }
        }
        Ok(result)
    }

    /// Indexed resource files of `domain` that can take an insertion, best
    /// candidates first, without reading or parsing them.
    pub fn insertion_targets(&self, domain: &str) -> Result<Vec<Insertion>> {
        if domain.is_empty() {
            return Ok(Vec::new());
        }
        let domain = normalize_domain(domain);
        let mut files: BTreeMap<String, String> = BTreeMap::new();
        for path in self.messages.get_all_file_paths_view()?.iter() {
            let Some(metadata) = catalogue_metadata(path) else {
                continue;
            };
            if !swlsp_php_types::equal_fold(&metadata.domain, domain) {
                continue;
            }
            if !matches!(
                lower_extension(path).as_str(),
                ".yaml" | ".yml" | ".xlf" | ".xliff" | ".xml"
            ) {
                continue;
            }
            files.entry(path.clone()).or_insert(metadata.locale);
        }
        let mut ordered: Vec<(String, String)> = files.into_iter().collect();
        ordered.sort_by_key(|(file, locale)| Reverse(insertion_file_priority(file, locale)));
        Ok(ordered
            .into_iter()
            .take(MAX_INSERTION_TARGETS)
            .map(|(file, locale)| Insertion {
                format: format_of(&file),
                file,
                locale,
                ..Insertion::default()
            })
            .collect())
    }
}

/// An insertion of `key` against the caller's exact source snapshot, unless
/// the key already exists or the resource is not safely writable.
pub fn insertion_for_source(file: &str, source: &str, key: &str, value: &str) -> Option<Insertion> {
    if key.is_empty() {
        return None;
    }
    let parsed = ParsedFile::from_source(file, source);
    let metadata = catalogue_metadata(file).unwrap_or_default();
    let extension = lower_extension(file);
    let messages = match extension.as_str() {
        ".yaml" | ".yml" => {
            let tree = parsed.syntax_tree()?;
            if !source.trim().is_empty()
                && !yamlquery::root_value(&tree.root())
                    .is_some_and(|root| yamlquery::is_mapping(&root))
            {
                return None;
            }
            parse_yaml_resource(&parsed, &metadata)
        }
        ".xlf" | ".xliff" | ".xml" => parse_xml_resource(&parsed, &metadata),
        _ => Vec::new(),
    };
    if messages.iter().any(|message| message.key == key) {
        return None;
    }
    match extension.as_str() {
        ".yaml" | ".yml" => Some(yaml_insertion(file, source, key, value)),
        ".xlf" | ".xliff" | ".xml" => {
            xliff_insertion(file, source, key, value, parsed.syntax_tree()?)
        }
        _ => None,
    }
}

fn insertion_file_priority(file: &str, locale: &str) -> u32 {
    let mut priority = 0;
    let normalized = go_to_lower(file);
    if !normalized.contains("/vendor/")
        && !normalized.contains("/var/cache/")
        && !normalized.contains("/app/cache/")
    {
        priority += 4;
    }
    if normalized.contains("/src/") || normalized.contains("/app/") {
        priority += 2;
    }
    let locale = go_to_lower(&locale.replace('_', "-"));
    if locale == "en" || locale.starts_with("en-") {
        priority += 1;
    }
    priority
}

fn format_of(path: &str) -> String {
    let extension = lower_extension(path);
    extension.strip_prefix('.').unwrap_or(&extension).to_owned()
}

fn yaml_insertion(path: &str, content: &str, key: &str, value: &str) -> Insertion {
    let eol = line_separator(content);
    let offset = content.len();
    let prefix = if content.ends_with(['\n', '\r']) || content.is_empty() {
        ""
    } else {
        eol
    };
    let new_text = format!(
        "{prefix}'{}': '{}'{eol}",
        yaml_quote(key),
        yaml_quote(value)
    );
    let (line, character) = byte_position(content, offset);
    Insertion {
        file: path.to_owned(),
        format: format_of(path),
        line,
        character,
        new_text,
        ..Insertion::default()
    }
}

fn xliff_insertion(
    path: &str,
    content: &str,
    key: &str,
    value: &str,
    tree: &Tree,
) -> Option<Insertion> {
    let root = xmlquery::elements(&tree.root(), &["xliff"])
        .into_iter()
        .next()?;
    let version = xmlquery::attribute(&root, "version")
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default();
    let file = xmlquery::child_element(&root, &["file"])?;

    let (container, element_name) = if version.starts_with("1.") {
        (xmlquery::child_element(&file, &["body"])?, "trans-unit")
    } else if version.starts_with("2.") {
        (
            xmlquery::child_element(&file, &["group"]).unwrap_or(file),
            "unit",
        )
    } else {
        return None;
    };
    let next_id = next_xliff_id(&container, element_name);
    let escaped_key = escape_html(key);
    let escaped_value = escape_html(value);
    let unit = if element_name == "trans-unit" {
        format!(
            r#"<trans-unit id="{next_id}" resname="{escaped_key}"><source>{escaped_key}</source><target>{escaped_value}</target></trans-unit>"#
        )
    } else {
        format!(
            r#"<unit id="{next_id}"><segment><source>{escaped_key}</source><target>{escaped_value}</target></segment></unit>"#
        )
    };

    let container_text = container.text().to_string();
    let closing = container_text.rfind("</")?;
    let mut offset = u32::from(container.text_range().start()) as usize + closing;
    let container_indent = indentation_at_offset(content, offset);
    let child_indent = match xmlquery::child_elements(&container, &[]).first() {
        Some(child) => indentation_at_offset(content, child_start(child)),
        None => format!("{container_indent}  "),
    };
    let eol = line_separator(content);
    let line_start = line_start_at(content, offset);
    let new_text = if content[line_start..offset].trim().is_empty() {
        offset = line_start;
        format!("{child_indent}{unit}{eol}")
    } else {
        format!("{eol}{child_indent}{unit}{eol}{container_indent}")
    };
    let (line, character) = byte_position(content, offset);
    Some(Insertion {
        file: path.to_owned(),
        format: format_of(path),
        line,
        character,
        new_text,
        ..Insertion::default()
    })
}

fn child_start(node: &SyntaxNode) -> usize {
    u32::from(node.text_range().start()) as usize
}

fn next_xliff_id(container: &SyntaxNode, element_name: &str) -> i64 {
    let mut next = 0;
    for element in xmlquery::elements(container, &[element_name]) {
        let value = xmlquery::attribute(&element, "id")
            .and_then(|attribute| xmlquery::attribute_value(&attribute))
            .unwrap_or_default();
        if let Ok(id) = value.parse::<i64>()
            && id >= next
        {
            next = id + 1;
        }
    }
    next
}

use swlsp_textutil::html_escape as escape_html;

fn yaml_quote(value: &str) -> String {
    value.replace('\'', "''")
}

fn line_separator(content: &str) -> &'static str {
    if content.contains("\r\n") {
        "\r\n"
    } else {
        "\n"
    }
}

fn byte_position(content: &str, offset: usize) -> (u32, u32) {
    let offset = offset.min(content.len());
    LineIndex::new(content).position_utf16(offset as u32)
}

fn line_start_at(content: &str, offset: usize) -> usize {
    let bytes = content.as_bytes();
    let mut offset = offset.min(bytes.len());
    while offset > 0 && bytes[offset - 1] != b'\n' {
        offset -= 1;
    }
    offset
}

fn indentation_at_offset(content: &str, offset: usize) -> String {
    let bytes = content.as_bytes();
    let start = line_start_at(content, offset);
    let mut end = start;
    while end < bytes.len() && matches!(bytes[end], b' ' | b'\t') {
        end += 1;
    }
    content[start..end].to_owned()
}
