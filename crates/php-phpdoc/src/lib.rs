//! Parses type-bearing PHPDoc tags into the shared semantic type algebra
//! (port of `internal/php/phpdoc`). Deliberately independent of the CST and
//! the workspace index.

use std::collections::{BTreeMap, HashSet};

use swlsp_php_types::{Type, equal_fold, go_to_lower};

mod references;

pub use references::visit_reference_names;

#[cfg(test)]
mod tests;

/// The type-bearing content of one PHPDoc comment.
///
/// Go's nil maps and slices are empty collections here. `return_type` is
/// `unknown` when the comment has no `@return` tag.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Document {
    pub summary: String,
    pub params: BTreeMap<String, Type>,
    pub param_tags: BTreeMap<String, Vec<String>>,
    pub return_type: Type,
    pub vars: BTreeMap<String, Type>,
    pub properties: Vec<Property>,
    pub methods: Vec<Method>,
    pub templates: Vec<Template>,
    pub extends: Vec<Type>,
    pub implements: Vec<Type>,
    pub uses: Vec<Type>,
    pub throws: Vec<Type>,
    pub assertions: Vec<Assertion>,
    pub aliases: BTreeMap<String, Type>,
    pub imports: BTreeMap<String, TypeImport>,
    pub deprecated: bool,
    pub internal: bool,
    pub is_final: bool,
}

/// A PHPStan/Psalm type alias imported from another class-like declaration.
/// `name` is the alias as declared by the source class.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypeImport {
    pub name: String,
    pub from: String,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Property {
    pub name: String,
    pub ty: Type,
    pub read_only: bool,
    pub write_only: bool,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Method {
    pub name: String,
    pub return_type: Type,
    pub parameters: Vec<Parameter>,
    pub is_static: bool,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Parameter {
    pub name: String,
    pub ty: Type,
    pub optional: bool,
    pub variadic: bool,
    pub tags: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Template {
    pub name: String,
    pub bound: Type,
    pub default: Type,
    pub covariant: bool,
    pub contravariant: bool,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Assertion {
    pub target: String,
    pub ty: Type,
    pub when_true: bool,
    pub conditional: bool,
    pub negated: bool,
}

/// Parses a PHPDoc comment (with or without the `/**` and `*/` delimiters).
pub fn parse(source: &str) -> Document {
    let mut document = Document::default();
    let mut summary: Vec<String> = Vec::new();
    let mut lines = LogicalLineScanner::new(source);
    while let Some(line) = lines.next_line() {
        if line.is_empty() {
            continue;
        }
        if !line.starts_with('@') {
            if document.params.is_empty()
                && document.return_type.is_unknown()
                && document.properties.is_empty()
                && document.methods.is_empty()
            {
                summary.push(line);
            }
            continue;
        }
        let (tag, value) = split_tag(&line);
        apply_tag(&mut document, &tag, value);
    }
    document.summary = summary.join(" ");
    document
}

fn apply_tag(document: &mut Document, tag: &str, value: &str) {
    match tag {
        "@param" | "@phpstan-param" | "@psalm-param" => {
            let (type_text, name, description) = split_type_and_variable(value);
            let parsed = parse_type(type_text);
            if !parsed.is_unknown() && !name.is_empty() {
                document.params.insert(name.to_owned(), parsed);
            }
            let tags = assistant_tags(description);
            if !name.is_empty() && !tags.is_empty() {
                document.param_tags.insert(name.to_owned(), tags);
            }
        }
        "@return" | "@phpstan-return" | "@psalm-return" => {
            let (type_text, _) = split_type_and_description(value);
            document.return_type = parse_type(type_text);
        }
        "@var" | "@phpstan-var" | "@psalm-var" => {
            let (type_text, name, _) = split_type_and_variable(value);
            let parsed = parse_type(type_text);
            if !parsed.is_unknown() {
                document.vars.insert(name.to_owned(), parsed);
            }
        }
        "@property" | "@property-read" | "@property-write" => {
            let (type_text, name, _) = split_type_and_variable(value);
            let parsed = parse_type(type_text);
            if !parsed.is_unknown() && !name.is_empty() {
                document.properties.push(Property {
                    name: name.strip_prefix('$').unwrap_or(name).to_owned(),
                    ty: parsed,
                    read_only: tag == "@property-read",
                    write_only: tag == "@property-write",
                });
            }
        }
        "@method" => {
            if let Some(method) = parse_method(value) {
                document.methods.push(method);
            }
        }
        "@template"
        | "@template-covariant"
        | "@template-contravariant"
        | "@phpstan-template"
        | "@psalm-template" => {
            if let Some(mut template) = parse_template(value) {
                template.covariant = tag.contains("covariant");
                template.contravariant = tag.contains("contravariant");
                document.templates.push(template);
            }
        }
        "@extends" | "@template-extends" | "@phpstan-extends" => {
            append_type(&mut document.extends, value);
        }
        "@implements" | "@template-implements" | "@phpstan-implements" => {
            append_type(&mut document.implements, value);
        }
        "@use" | "@template-use" | "@phpstan-use" => append_type(&mut document.uses, value),
        "@throws" => append_type(&mut document.throws, value),
        "@phpstan-assert"
        | "@psalm-assert"
        | "@phpstan-assert-if-true"
        | "@psalm-assert-if-true"
        | "@phpstan-assert-if-false"
        | "@psalm-assert-if-false" => {
            let (type_text, target, _) = split_type_and_variable(value);
            let type_text = type_text.trim();
            let negated = type_text.starts_with('!');
            let type_text = type_text.strip_prefix('!').unwrap_or(type_text);
            let type_text = type_text.strip_prefix('=').unwrap_or(type_text);
            let parsed = parse_type(type_text);
            if !parsed.is_unknown() && !target.is_empty() {
                document.assertions.push(Assertion {
                    target: target.to_owned(),
                    ty: parsed,
                    when_true: tag.ends_with("-if-true"),
                    conditional: tag.contains("-if-"),
                    negated,
                });
            }
        }
        "@phpstan-type" | "@psalm-type" => {
            let (name, type_text) = split_first(value);
            if !name.is_empty() {
                let type_text = type_text.trim();
                let type_text = type_text.strip_prefix('=').unwrap_or(type_text);
                document
                    .aliases
                    .insert(name.to_owned(), parse_type(type_text));
            }
        }
        "@phpstan-import-type" | "@psalm-import-type" => {
            if let Some((local_name, imported)) = parse_type_import(value) {
                document.imports.insert(local_name, imported);
            }
        }
        "@deprecated" => document.deprecated = true,
        "@internal" => document.internal = true,
        "@final" => document.is_final = true,
        _ => {}
    }
}

fn parse_type_import(source: &str) -> Option<(String, TypeImport)> {
    let fields: Vec<&str> = source.split_whitespace().collect();
    if fields.len() < 3 {
        return None;
    }
    let from = fields.iter().position(|field| equal_fold(field, "from"))?;
    if from != 1 || from + 1 >= fields.len() {
        return None;
    }
    let imported_name = fields[0];
    let source_class = fields[from + 1];
    let mut local_name = imported_name;
    if from + 3 < fields.len() && equal_fold(fields[from + 2], "as") {
        local_name = fields[from + 3];
    }
    Some((
        local_name.to_owned(),
        TypeImport {
            name: imported_name.to_owned(),
            from: source_class.to_owned(),
        },
    ))
}

fn append_type(values: &mut Vec<Type>, source: &str) {
    let (type_text, _) = split_type_and_description(source);
    let value = parse_type(type_text);
    if !value.is_unknown() {
        values.push(value);
    }
}

/// Parses a PHPDoc type; syntax errors yield the `error` type and empty input
/// yields `unknown`.
fn parse_type(source: &str) -> Type {
    swlsp_php_types::parse(source.trim()).unwrap_or(Type::ERROR)
}

fn parse_template(source: &str) -> Option<Template> {
    let (name, rest) = split_first(source);
    if name.is_empty() {
        return None;
    }
    let mut template = Template {
        name: name.to_owned(),
        bound: Type::MIXED,
        default: Type::UNKNOWN,
        ..Template::default()
    };
    let rest = rest.trim();
    let spaced = format!(" {rest}");
    for keyword in [" of ", " as "] {
        if let Some(index) = spaced.find(keyword) {
            let bound_and_default = spaced[index + keyword.len()..].trim();
            if let Some(equal) = top_level_index(bound_and_default, '=') {
                template.bound = parse_type(&bound_and_default[..equal]);
                template.default = parse_type(&bound_and_default[equal + 1..]);
            } else {
                let (type_text, _) = split_type_and_description(bound_and_default);
                template.bound = parse_type(type_text);
            }
            return Some(template);
        }
    }
    if let Some(equal) = top_level_index(rest, '=') {
        template.default = parse_type(&rest[equal + 1..]);
    }
    Some(template)
}

fn parse_method(source: &str) -> Option<Method> {
    let mut source = source.trim();
    let mut method = Method::default();
    if go_to_lower(source).starts_with("static ") {
        method.is_static = true;
        source = slice_from(source, "static ".len()).trim();
    }
    let open = top_level_index(source, '(')?;
    let close = source.rfind(')')?;
    if close < open {
        return None;
    }
    let parts: Vec<&str> = source[..open].split_whitespace().collect();
    let (&name, return_parts) = parts.split_last()?;
    method.name = name.to_owned();
    method.return_type = if return_parts.is_empty() {
        Type::MIXED
    } else {
        parse_type(&return_parts.join(" "))
    };
    for parameter_text in split_top_level(&source[open + 1..close], ',') {
        let parameter_text = parameter_text.trim();
        if parameter_text.is_empty() {
            continue;
        }
        let (type_text, name, description) = split_type_and_variable(parameter_text);
        if name.is_empty() {
            continue;
        }
        let ty = if type_text.trim().is_empty() {
            Type::MIXED
        } else {
            parse_type(type_text)
        };
        method.parameters.push(Parameter {
            name: name.strip_suffix('=').unwrap_or(name).to_owned(),
            ty,
            optional: description.contains('=') || name.ends_with('='),
            variadic: name.contains("..."),
            tags: assistant_tags(description),
        });
    }
    Some(method)
}

/// Collects `#Tag` markers (ASCII word characters) from a description,
/// deduplicated case-insensitively while keeping the first spelling.
fn assistant_tags(source: &str) -> Vec<String> {
    if !source.contains('#') {
        return Vec::new();
    }
    let bytes = source.as_bytes();
    let mut result = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    let mut offset = 0;
    while offset < bytes.len() {
        let Some(found) = bytes[offset..].iter().position(|&byte| byte == b'#') else {
            break;
        };
        let start = offset + found + 1;
        let mut end = start;
        while end < bytes.len() && (bytes[end].is_ascii_alphanumeric() || bytes[end] == b'_') {
            end += 1;
        }
        if end > start {
            let tag = &source[start..end];
            if seen.insert(tag.to_ascii_lowercase()) {
                result.push(tag.to_owned());
            }
        }
        offset = end.max(start + 1);
    }
    result
}

/// Yields trimmed comment lines with leading `*` removed. A tag line absorbs
/// the following non-tag lines, joined with single spaces.
pub(crate) struct LogicalLineScanner<'a> {
    source: &'a str,
    pending: Option<&'a str>,
}

impl<'a> LogicalLineScanner<'a> {
    pub(crate) fn new(source: &'a str) -> Self {
        let source = source.trim();
        let source = source.strip_prefix("/**").unwrap_or(source);
        let source = source.strip_suffix("*/").unwrap_or(source);
        LogicalLineScanner {
            source,
            pending: None,
        }
    }

    pub(crate) fn next_line(&mut self) -> Option<String> {
        let line = self.next_physical()?;
        if !line.starts_with('@') {
            return Some(line.to_owned());
        }
        let mut joined = String::new();
        while let Some(continuation) = self.next_physical() {
            if continuation.starts_with('@') {
                self.pending = Some(continuation);
                break;
            }
            if joined.is_empty() {
                joined.reserve(line.len() + continuation.len() + 1);
                joined.push_str(line);
            }
            joined.push(' ');
            joined.push_str(continuation);
        }
        if joined.is_empty() {
            return Some(line.to_owned());
        }
        Some(joined)
    }

    fn next_physical(&mut self) -> Option<&'a str> {
        if let Some(pending) = self.pending.take() {
            return Some(pending);
        }
        while !self.source.is_empty() {
            let line = match self.source.find('\n') {
                Some(end) => {
                    let line = &self.source[..end];
                    self.source = &self.source[end + 1..];
                    line
                }
                None => std::mem::take(&mut self.source),
            };
            let line = line.trim();
            let line = line.strip_prefix('*').unwrap_or(line).trim();
            if !line.is_empty() {
                return Some(line);
            }
        }
        None
    }
}

/// Splits a tag line into the lowercased tag and its trimmed value.
pub(crate) fn split_tag(line: &str) -> (String, &str) {
    let (tag, rest) = split_first(line);
    (go_to_lower(tag), rest.trim())
}

/// Splits `Type $name description` at the first top-level `$`, dropping a
/// trailing variadic `...` or by-reference `&` from the type.
pub(crate) fn split_type_and_variable(source: &str) -> (&str, &str, &str) {
    let mut depth = 0usize;
    let mut quote: Option<char> = None;
    for (index, value) in source.char_indices() {
        if let Some(open) = quote {
            if value == open {
                quote = None;
            }
            continue;
        }
        match value {
            '\'' | '"' => quote = Some(value),
            '<' | '{' | '(' | '[' => depth += 1,
            '>' | '}' | ')' | ']' => depth = depth.saturating_sub(1),
            '$' if depth == 0 => {
                let type_text = source[..index].trim();
                let type_text = type_text.strip_suffix("...").unwrap_or(type_text).trim();
                let type_text = type_text.strip_suffix('&').unwrap_or(type_text).trim();
                let (name, rest) = split_first(&source[index..]);
                return (type_text, name, rest);
            }
            _ => {}
        }
    }
    let (type_text, rest) = split_type_and_description(source);
    (type_text, "", rest)
}

/// Splits a type from its description at the first top-level space or tab.
pub(crate) fn split_type_and_description(source: &str) -> (&str, &str) {
    let mut depth = 0usize;
    let mut quote: Option<char> = None;
    for (index, value) in source.char_indices() {
        if let Some(open) = quote {
            if value == open {
                quote = None;
            }
            continue;
        }
        match value {
            '\'' | '"' => quote = Some(value),
            '<' | '{' | '(' | '[' => depth += 1,
            '>' | '}' | ')' | ']' => depth = depth.saturating_sub(1),
            ' ' | '\t' if depth == 0 => {
                return (source[..index].trim(), source[index..].trim());
            }
            _ => {}
        }
    }
    (source.trim(), "")
}

/// Splits trimmed `source` at its first space or tab.
fn split_first(source: &str) -> (&str, &str) {
    let source = source.trim();
    match source.find([' ', '\t']) {
        Some(index) => (&source[..index], source[index..].trim()),
        None => (source, ""),
    }
}

fn split_top_level(source: &str, separator: char) -> Vec<&str> {
    let mut result = Vec::new();
    let mut start = 0;
    let mut depth = 0usize;
    for (index, value) in source.char_indices() {
        match value {
            '<' | '{' | '(' | '[' => depth += 1,
            '>' | '}' | ')' | ']' => depth = depth.saturating_sub(1),
            _ => {
                if value == separator && depth == 0 {
                    result.push(&source[start..index]);
                    start = index + value.len_utf8();
                }
            }
        }
    }
    result.push(&source[start..]);
    result
}

fn top_level_index(source: &str, target: char) -> Option<usize> {
    let mut depth = 0usize;
    for (index, value) in source.char_indices() {
        if value == target && depth == 0 {
            return Some(index);
        }
        match value {
            '<' | '{' | '(' | '[' => depth += 1,
            '>' | '}' | ')' | ']' => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    None
}

/// `&source[start..]` that tolerates offsets computed on a differently sized
/// lowercase copy (Go slices bytes there; Rust must stay on a char boundary).
fn slice_from(source: &str, start: usize) -> &str {
    let mut start = start.min(source.len());
    while !source.is_char_boundary(start) {
        start += 1;
    }
    &source[start..]
}
