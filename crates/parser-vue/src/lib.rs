//! Vue single-file component parser (port of Go `internal/parser/vue`).
//!
//! The SFC is split into top-level sections; `<template>` bodies are parsed
//! with the Twig/HTML parser, `<script>` bodies with the JavaScript parser and
//! `<style>` bodies with the SCSS parser. Embedded green trees are inserted
//! as-is: green nodes are position independent, so every embedded node keeps
//! its exact absolute range in the SFC.

pub mod syntax;

use std::sync::Arc;

use rowan::{GreenNode, GreenToken, NodeOrToken};
use swlsp_syntax::{Kind, Parse, ParseError, TextRange, TextSize, Tree};

use syntax::*;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum SectionKind {
    Template,
    Script,
    Style,
    Custom,
}

impl SectionKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Template => "template",
            Self::Script => "script",
            Self::Style => "style",
            Self::Custom => "custom",
        }
    }

    fn node_kind(self) -> Kind {
        match self {
            Self::Template => VUE_TEMPLATE_SECTION,
            Self::Script => VUE_SCRIPT_SECTION,
            Self::Style => VUE_STYLE_SECTION,
            Self::Custom => VUE_CUSTOM_SECTION,
        }
    }
}

/// One top-level Vue SFC block. All ranges are absolute byte ranges in the
/// original file.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Section {
    pub kind: SectionKind,
    /// Lowercased tag name.
    pub name: String,
    pub open_range: TextRange,
    pub body_range: TextRange,
    /// Empty when the section is self-closing or unterminated.
    pub close_range: TextRange,
    pub setup: bool,
    pub language: String,
}

/// Parse result including the section table.
#[derive(Clone, Debug)]
pub struct VueParse {
    pub parse: Parse,
    pub sections: Vec<Section>,
}

pub fn parse(source: &str) -> Parse {
    parse_sfc(source).parse
}

/// Like [`parse`], but the tree shares `shared` instead of copying it.
pub fn parse_shared(shared: Arc<str>) -> Parse {
    parse_sfc_shared(shared).parse
}

pub fn parse_sfc(source: &str) -> VueParse {
    parse_sfc_shared(Arc::from(source))
}

/// Like [`parse_sfc`], but the tree shares `shared` instead of copying it.
pub fn parse_sfc_shared(shared: Arc<str>) -> VueParse {
    let source: &str = &shared;
    let sections = sections(source);
    let mut children: Vec<NodeOrToken<GreenNode, GreenToken>> = Vec::new();
    let mut errors: Vec<ParseError> = Vec::new();
    let mut position = 0u32;
    for section in &sections {
        let open_start = u32::from(section.open_range.start());
        if position < open_start {
            children.push(token(source, TK_TEXT, position, open_start));
        }
        let mut section_children = vec![token(
            source,
            TK_SECTION_OPEN,
            section.open_range.start().into(),
            section.open_range.end().into(),
        )];
        let body = &source[section.body_range];
        let base = section.body_range.start();
        let embedded = match section.kind {
            SectionKind::Template => Some(swlsp_parser_twig::parse(body)),
            SectionKind::Script => Some(swlsp_parser_js::parse(body)),
            SectionKind::Style => Some(swlsp_parser_scss::parse(body)),
            SectionKind::Custom => None,
        };
        match embedded {
            Some(parsed) => {
                section_children.push(NodeOrToken::Node(parsed.tree.green().clone()));
                errors.extend(parsed.errors.into_iter().map(|mut error| {
                    error.range += base;
                    error
                }));
            }
            None => {
                if !section.body_range.is_empty() {
                    section_children.push(token(
                        source,
                        TK_TEXT,
                        section.body_range.start().into(),
                        section.body_range.end().into(),
                    ));
                }
            }
        }
        if !section.close_range.is_empty() {
            section_children.push(token(
                source,
                TK_SECTION_CLOSE,
                section.close_range.start().into(),
                section.close_range.end().into(),
            ));
            position = section.close_range.end().into();
        } else {
            position = section.body_range.end().into();
        }
        children.push(NodeOrToken::Node(GreenNode::new(
            raw(section.kind.node_kind()),
            section_children,
        )));
    }
    let len = source.len() as u32;
    if position < len {
        children.push(token(source, TK_TEXT, position, len));
    }
    let green = GreenNode::new(raw(VUE_DOCUMENT), children);
    VueParse {
        parse: Parse {
            tree: Tree::new(Arc::clone(&shared), green),
            errors,
        },
        sections,
    }
}

fn raw(kind: Kind) -> rowan::SyntaxKind {
    rowan::SyntaxKind(kind.0)
}

fn token(source: &str, kind: Kind, start: u32, end: u32) -> NodeOrToken<GreenNode, GreenToken> {
    NodeOrToken::Token(GreenToken::new(
        raw(kind),
        &source[start as usize..end as usize],
    ))
}

fn range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

fn find_byte(bytes: &[u8], from: usize, needle: u8) -> Option<usize> {
    bytes[from..]
        .iter()
        .position(|&b| b == needle)
        .map(|i| i + from)
}

fn find_str(bytes: &[u8], from: usize, needle: &[u8]) -> Option<usize> {
    bytes[from..]
        .windows(needle.len())
        .position(|window| window == needle)
        .map(|i| i + from)
}

/// Splits an SFC into its top-level sections.
pub fn sections(source: &str) -> Vec<Section> {
    let bytes = source.as_bytes();
    let mut result = Vec::new();
    let mut cursor = 0;
    while cursor < bytes.len() {
        let Some(open) = find_byte(bytes, cursor, b'<') else {
            break;
        };
        let tag = scan_tag(bytes, open);
        if !tag.ok || tag.closing || tag.name.is_empty() {
            cursor = open + 1;
            continue;
        }
        let kind = section_kind(&tag.name);
        if kind == SectionKind::Custom && is_ordinary_document_tag(&tag.name) {
            cursor = tag.end;
            continue;
        }
        let open_text = &bytes[open..tag.end];
        let mut section = Section {
            kind,
            setup: has_boolean_attribute(open_text, b"setup"),
            language: String::from_utf8_lossy(attribute_value(open_text, b"lang")).into_owned(),
            open_range: range(open, tag.end),
            body_range: range(tag.end, tag.end),
            close_range: TextRange::default(),
            name: tag.name,
        };
        if tag.self_closing {
            result.push(section);
            cursor = tag.end;
            continue;
        }
        let Some((close_start, close_end)) = matching_section_close(bytes, tag.end, &section.name)
        else {
            section.body_range = range(tag.end, bytes.len());
            result.push(section);
            break;
        };
        section.body_range = range(tag.end, close_start);
        section.close_range = range(close_start, close_end);
        result.push(section);
        cursor = close_end;
    }
    result
}

fn section_kind(name: &str) -> SectionKind {
    match name {
        "template" => SectionKind::Template,
        "script" => SectionKind::Script,
        "style" => SectionKind::Style,
        _ => SectionKind::Custom,
    }
}

struct ScannedTag {
    name: String,
    end: usize,
    closing: bool,
    self_closing: bool,
    ok: bool,
}

fn scan_tag(source: &[u8], start: usize) -> ScannedTag {
    let mut tag = ScannedTag {
        name: String::new(),
        end: start,
        closing: false,
        self_closing: false,
        ok: false,
    };
    if start >= source.len() || source[start] != b'<' {
        return tag;
    }
    if source[start..].starts_with(b"<!--") {
        tag.end = match find_str(source, start + 4, b"-->") {
            Some(close) => close + 3,
            None => source.len(),
        };
        return tag;
    }
    let mut index = start + 1;
    while index < source.len() && is_space(source[index]) {
        index += 1;
    }
    if index < source.len() && source[index] == b'/' {
        tag.closing = true;
        index += 1;
    }
    let name_start = index;
    while index < source.len() && is_tag_name_byte(source[index]) {
        index += 1;
    }
    if index == name_start {
        tag.end = start + 1;
        return tag;
    }
    tag.name = String::from_utf8_lossy(&source[name_start..index]).to_ascii_lowercase();
    tag.ok = true;
    let mut quote = 0u8;
    while index < source.len() {
        if quote != 0 {
            match find_byte(source, index, quote) {
                Some(found) => index = found + 1,
                None => break,
            }
            quote = 0;
            continue;
        }
        let Some(found) = source[index..]
            .iter()
            .position(|&b| matches!(b, b'\'' | b'"' | b'>'))
        else {
            break;
        };
        index += found;
        let value = source[index];
        if value == b'\'' || value == b'"' {
            quote = value;
            index += 1;
            continue;
        }
        // value == '>'
        tag.self_closing = ends_with_slash_trimmed(&source[start + 1..index]);
        tag.end = index + 1;
        return tag;
    }
    tag.end = source.len();
    tag
}

fn matching_section_close(source: &[u8], start: usize, name: &str) -> Option<(usize, usize)> {
    let mut depth = 1;
    let mut cursor = start;
    while cursor < source.len() {
        let next = find_byte(source, cursor, b'<')?;
        if source[next..].starts_with(b"<!--") {
            cursor = find_str(source, next + 4, b"-->")? + 3;
            continue;
        }
        let tag = scan_tag(source, next);
        if !tag.ok {
            cursor = next + 1;
            continue;
        }
        if tag.name == name {
            if tag.closing {
                depth -= 1;
                if depth == 0 {
                    return Some((next, tag.end));
                }
            } else if !tag.self_closing && name == "template" {
                depth += 1;
            }
        }
        cursor = tag.end.max(next + 1);
    }
    None
}

fn has_boolean_attribute(tag: &[u8], name: &[u8]) -> bool {
    !attribute_value(tag, name).is_empty() || has_bare_attribute(tag, name)
}

fn has_bare_attribute(tag: &[u8], name: &[u8]) -> bool {
    // Go: strings.Fields(strings.Trim(tag, "<>/ \t\r\n")) + EqualFold.
    let is_trim = |b: &u8| matches!(b, b'<' | b'>' | b'/' | b' ' | b'\t' | b'\r' | b'\n');
    let start = tag.iter().position(|b| !is_trim(b)).unwrap_or(tag.len());
    let end = tag
        .iter()
        .rposition(|b| !is_trim(b))
        .map_or(start, |i| i + 1);
    let trimmed = String::from_utf8_lossy(&tag[start..end]);
    let name = std::str::from_utf8(name).unwrap_or_default();
    trimmed
        .split_whitespace()
        .any(|field| equal_fold(field, name))
}

/// Go `strings.EqualFold`: simple Unicode case folding per rune.
fn equal_fold(a: &str, b: &str) -> bool {
    let mut left = a.chars();
    let mut right = b.chars();
    loop {
        match (left.next(), right.next()) {
            (None, None) => return true,
            (Some(x), Some(y)) if fold(x) == fold(y) => {}
            _ => return false,
        }
    }
}

fn fold(c: char) -> char {
    // Characters whose simple fold lands in ASCII, as Go's orbit tables do.
    match c {
        '\u{017F}' => 's',
        '\u{212A}' => 'k',
        _ => simple_lower(c),
    }
}

/// Go `unicode.ToLower`: one-to-one simple lowercase mapping.
fn simple_lower(c: char) -> char {
    if c == '\u{0130}' {
        return 'i';
    }
    let mut lower = c.to_lowercase();
    match (lower.next(), lower.next()) {
        (Some(single), None) => single,
        _ => c,
    }
}

/// Go `strings.ToLower` on possibly non-UTF-8 bytes.
fn go_to_lower(tag: &[u8]) -> Vec<u8> {
    if tag.is_ascii() {
        return tag.to_ascii_lowercase();
    }
    let mut out = Vec::with_capacity(tag.len());
    for chunk in tag.utf8_chunks() {
        let mut buf = [0u8; 4];
        for c in chunk.valid().chars() {
            out.extend_from_slice(simple_lower(c).encode_utf8(&mut buf).as_bytes());
        }
        // Go maps each invalid byte to U+FFFD.
        for _ in chunk.invalid() {
            out.extend_from_slice("\u{FFFD}".as_bytes());
        }
    }
    out
}

fn attribute_value<'a>(tag: &'a [u8], name: &[u8]) -> &'a [u8] {
    let lower = go_to_lower(tag);
    let mut cursor = 0;
    while cursor < lower.len() {
        let Some(index) = find_str(&lower, cursor, name) else {
            return b"";
        };
        let before_ok = index == 0 || !is_tag_name_byte(lower[index - 1]);
        let mut after = index + name.len();
        let after_ok = after >= lower.len() || !is_tag_name_byte(lower[after]);
        if !before_ok || !after_ok {
            cursor = after;
            continue;
        }
        while after < tag.len() && is_space(tag[after]) {
            after += 1;
        }
        if after >= tag.len() || tag[after] != b'=' {
            cursor = after;
            continue;
        }
        after += 1;
        while after < tag.len() && is_space(tag[after]) {
            after += 1;
        }
        if after >= tag.len() {
            return b"";
        }
        let quote = tag[after];
        if quote == b'\'' || quote == b'"' {
            return match find_byte(tag, after + 1, quote) {
                Some(end) => &tag[after + 1..end],
                None => b"",
            };
        }
        let mut end = after;
        while end < tag.len() && !is_space(tag[end]) && tag[end] != b'>' {
            end += 1;
        }
        let value = &tag[after..end];
        return value.strip_suffix(b"/").unwrap_or(value);
    }
    b""
}

/// Go `strings.TrimSpace(...)` followed by `HasSuffix("/")`.
fn ends_with_slash_trimmed(bytes: &[u8]) -> bool {
    String::from_utf8_lossy(bytes).trim().ends_with('/')
}

fn is_ordinary_document_tag(name: &str) -> bool {
    matches!(
        name,
        "div"
            | "span"
            | "main"
            | "section"
            | "article"
            | "header"
            | "footer"
            | "button"
            | "form"
            | "input"
            | "label"
            | "ul"
            | "ol"
            | "li"
            | "p"
            | "a"
    )
}

fn is_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}

fn is_tag_name_byte(value: u8) -> bool {
    value.is_ascii_alphanumeric() || matches!(value, b'-' | b'_' | b':')
}

#[cfg(test)]
mod tests;
