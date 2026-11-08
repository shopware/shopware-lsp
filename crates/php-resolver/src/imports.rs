//! `use` declaration parsing (port of `resolver/imports.go`).

use swlsp_php_semantic::Str;
use swlsp_php_types::{equal_fold, go_to_lower};

use crate::names::NameContext;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum ImportKind {
    #[default]
    Class,
    Function,
    Constant,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Import {
    pub kind: ImportKind,
    pub alias: String,
    pub target: String,
}

impl Import {
    pub fn new(kind: ImportKind, alias: &str, target: &str) -> Self {
        Import {
            kind,
            alias: alias.to_owned(),
            target: target.to_owned(),
        }
    }
}

/// Parses simple, comma-separated, grouped, function, and const use
/// declarations from their lossless CST text.
pub fn parse_use_declaration(source: &str) -> Vec<Import> {
    let mut result = Vec::new();
    visit_use_declaration(source, &mut |value| result.push(value));
    result
}

impl NameContext {
    /// Parses a declaration directly into the context.
    pub fn add_use_declaration(&mut self, source: &str) {
        visit_use_declaration(source, &mut |value| self.add_import(&value));
    }

    pub fn add_import(&mut self, value: &Import) {
        if value.target.is_empty() {
            return;
        }
        let alias = if value.alias.is_empty() {
            split_last(&value.target).0
        } else {
            value.alias.as_str()
        };
        let target = Str::from(value.target.as_str());
        let imports = self.imports_mut();
        match value.kind {
            ImportKind::Function => {
                imports
                    .functions
                    .insert(Str::from(go_to_lower(alias)), target);
            }
            ImportKind::Constant => {
                imports.constants.insert(Str::from(alias), target);
            }
            ImportKind::Class => {
                imports
                    .classes
                    .insert(Str::from(go_to_lower(alias)), target);
            }
        }
    }
}

fn visit_use_declaration(source: &str, visit: &mut dyn FnMut(Import)) {
    let stripped = strip_comments(source);
    let mut source = stripped.trim();
    if let Some(index) = index_fold_ascii(source, "use ") {
        source = source[index + "use ".len()..].trim();
    }
    let source = source.strip_suffix(';').unwrap_or(source).trim();
    let (kind, source) = consume_import_kind(source, ImportKind::Class);

    if let Some(open) = source.find('{') {
        let close = source.rfind('}');
        let Some(close) = close.filter(|&close| close > open) else {
            return;
        };
        let base = source[..open].trim().trim_matches('\\');
        visit_imports(&source[open + 1..close], kind, base, visit);
        return;
    }
    visit_imports(source, kind, "", visit);
}

fn consume_import_kind(source: &str, fallback: ImportKind) -> (ImportKind, &str) {
    let source = source.trim();
    if source.is_empty() {
        return (fallback, source);
    }
    let end = source.find(char::is_whitespace).unwrap_or(source.len());
    let first = &source[..end];
    if equal_fold(first, "function") {
        return (ImportKind::Function, source[end..].trim());
    }
    if equal_fold(first, "const") {
        return (ImportKind::Constant, source[end..].trim());
    }
    (fallback, source)
}

fn parse_import(source: &str, kind: ImportKind, base: &str) -> Option<Import> {
    let source = source.trim();
    if source.is_empty() {
        return None;
    }
    let target_end = source.find(char::is_whitespace).unwrap_or(source.len());
    let mut target = source[..target_end].trim_matches('\\').to_owned();
    if !base.is_empty() {
        target = format!("{base}\\{target}");
    }
    let mut alias = split_last(&target).0.to_owned();
    let (alias_field, before_alias) = last_import_field(source);
    let (keyword, _) = last_import_field(before_alias);
    if !alias_field.is_empty() && equal_fold(keyword, "as") {
        alias = alias_field.to_owned();
    }
    Some(Import {
        kind,
        alias,
        target,
    })
}

fn last_import_field(source: &str) -> (&str, &str) {
    let source = source.trim();
    if source.is_empty() {
        return ("", "");
    }
    match source.rfind(char::is_whitespace) {
        None => (source, ""),
        Some(start) => {
            let width = source[start..].chars().next().map_or(1, char::len_utf8);
            (&source[start + width..], source[..start].trim())
        }
    }
}

/// The last segment of a qualified name and its namespace.
pub(crate) fn split_last(name: &str) -> (&str, &str) {
    match name.rfind('\\') {
        Some(separator) => (&name[separator + 1..], &name[..separator]),
        None => (name, ""),
    }
}

fn visit_imports(source: &str, kind: ImportKind, base: &str, visit: &mut dyn FnMut(Import)) {
    let mut start = 0;
    let mut depth = 0usize;
    for (index, value) in source.char_indices() {
        match value {
            '{' | '[' | '(' => depth += 1,
            '}' | ']' | ')' => depth = depth.saturating_sub(1),
            ',' if depth == 0 => {
                visit_import(&source[start..index], kind, base, visit);
                start = index + 1;
            }
            _ => {}
        }
    }
    visit_import(&source[start..], kind, base, visit);
}

fn visit_import(source: &str, kind: ImportKind, base: &str, visit: &mut dyn FnMut(Import)) {
    let (item_kind, item_text) = consume_import_kind(source.trim(), kind);
    if let Some(parsed) = parse_import(item_text, item_kind, base) {
        visit(parsed);
    }
}

fn strip_comments(source: &str) -> std::borrow::Cow<'_, str> {
    if !source.contains("//") && !source.contains("/*") && !source.contains('#') {
        return std::borrow::Cow::Borrowed(source);
    }
    let bytes = source.as_bytes();
    let mut result: Vec<u8> = Vec::with_capacity(source.len());
    let mut position = 0;
    while position < bytes.len() {
        let rest = &bytes[position..];
        if rest.starts_with(b"//") {
            position += 2;
            while position < bytes.len() && bytes[position] != b'\n' && bytes[position] != b'\r' {
                position += 1;
            }
        } else if rest.starts_with(b"/*") {
            position += 2;
            while position + 1 < bytes.len() && !bytes[position..].starts_with(b"*/") {
                position += 1;
            }
            if position + 1 < bytes.len() {
                position += 2;
            }
        } else if rest[0] == b'#' && !rest.starts_with(b"#[") {
            position += 1;
            while position < bytes.len() && bytes[position] != b'\n' && bytes[position] != b'\r' {
                position += 1;
            }
        } else {
            result.push(rest[0]);
            position += 1;
        }
    }
    // An unterminated block comment can end inside a multi-byte character;
    // Go keeps the raw byte, Rust replaces it.
    match String::from_utf8(result) {
        Ok(text) => std::borrow::Cow::Owned(text),
        Err(error) => {
            std::borrow::Cow::Owned(String::from_utf8_lossy(error.as_bytes()).into_owned())
        }
    }
}

fn index_fold_ascii(source: &str, needle: &str) -> Option<usize> {
    if needle.is_empty() {
        return Some(0);
    }
    let haystack = source.as_bytes();
    let needle = needle.as_bytes();
    if haystack.len() < needle.len() {
        return None;
    }
    (0..=haystack.len() - needle.len())
        .find(|&start| haystack[start..start + needle.len()].eq_ignore_ascii_case(needle))
}
