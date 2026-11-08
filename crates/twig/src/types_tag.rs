//! Twig's static-analysis `{% types %}` tag (port of `types_tag.go`).

use swlsp_syntax::TextRange;

use crate::documentation::normalize_documentation;
use crate::php_usage::is_non_class_twig_type;
use crate::token_parser::{find_bytes, is_twig_tag_space, lossy, range, twig_tag_usages};

/// One variable/type pair, e.g. `{% types { product: 'App\\Product' } %}`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypesTagDeclaration {
    pub name: String,
    pub ty: String,
    pub optional: bool,
    pub documentation: String,
    pub name_range: TextRange,
    pub type_range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypesTagClassReference {
    pub name: String,
    pub raw: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypesTagCompletionContext {
    pub prefix: String,
    pub range: TextRange,
}

/// Complete and editor-incomplete `{% types %}` declarations from the
/// lossless source.
pub fn types_tag_declarations(content: &[u8]) -> Vec<TypesTagDeclaration> {
    types_tag_regions(content)
        .into_iter()
        .flat_map(|(start, end)| parse_types_tag_declarations(content, start, end))
        .collect()
}

/// The PHP class-name component under `offset`.
pub fn types_tag_completion_at(content: &[u8], offset: u32) -> Option<TypesTagCompletionContext> {
    if offset as usize > content.len() {
        return None;
    }
    for declaration in types_tag_declarations(content) {
        let (start, end) = (
            u32::from(declaration.type_range.start()),
            u32::from(declaration.type_range.end()),
        );
        if offset < start || offset > end {
            continue;
        }
        let (component_start, component_end) =
            types_tag_class_component_range(content, start, end, offset);
        if offset < component_start || offset > component_end {
            return None;
        }
        return Some(TypesTagCompletionContext {
            prefix: normalize_twig_type_class_name(&lossy(
                &content[component_start as usize..offset as usize],
            )),
            range: range(component_start as usize, component_end as usize),
        });
    }
    None
}

pub fn types_tag_class_references(content: &[u8]) -> Vec<TypesTagClassReference> {
    types_tag_declarations(content)
        .iter()
        .flat_map(|declaration| types_tag_class_references_in_declaration(content, declaration))
        .collect()
}

pub fn types_tag_class_reference_at(content: &[u8], offset: u32) -> Option<TypesTagClassReference> {
    types_tag_class_references(content)
        .into_iter()
        .find(|reference| {
            offset >= u32::from(reference.range.start())
                && offset <= u32::from(reference.range.end())
        })
}

fn types_tag_regions(content: &[u8]) -> Vec<(usize, usize)> {
    let mut result = Vec::new();
    for usage in twig_tag_usages(content) {
        if !usage.name.eq_ignore_ascii_case("types") {
            continue;
        }
        let start = u32::from(usage.range.end()) as usize;
        let mut end = match find_bytes(&content[start..], b"%}") {
            Some(close) => start + close,
            None => content.len(),
        };
        if end > start && (content[end - 1] == b'-' || content[end - 1] == b'~') {
            end -= 1;
        }
        result.push((start, end));
    }
    result
}

fn skip_spaces(content: &[u8], cursor: &mut usize, end: usize) {
    while *cursor < end && is_twig_tag_space(content[*cursor]) {
        *cursor += 1;
    }
}

fn parse_types_tag_declarations(
    content: &[u8],
    start: usize,
    end: usize,
) -> Vec<TypesTagDeclaration> {
    let mut cursor = start;
    skip_spaces(content, &mut cursor, end);
    if cursor >= end || content[cursor] != b'{' {
        return Vec::new();
    }
    cursor += 1;
    let mut result = Vec::new();
    let mut pending_documentation: Vec<String> = Vec::new();
    while cursor < end {
        skip_types_tag_separators(content, &mut cursor, end, &mut pending_documentation);
        if cursor >= end || content[cursor] == b'}' {
            break;
        }
        let Some((name, name_range)) = parse_types_tag_key(content, &mut cursor, end) else {
            pending_documentation.clear();
            cursor += 1;
            continue;
        };
        let documentation = pending_documentation.join("\n");
        pending_documentation.clear();
        skip_spaces(content, &mut cursor, end);
        let mut optional = false;
        if cursor < end && content[cursor] == b'?' {
            optional = true;
            cursor += 1;
            skip_spaces(content, &mut cursor, end);
        }
        if cursor >= end || content[cursor] != b':' {
            continue;
        }
        cursor += 1;
        skip_spaces(content, &mut cursor, end);
        if cursor >= end || (content[cursor] != b'\'' && content[cursor] != b'"') {
            continue;
        }
        let quote = content[cursor];
        cursor += 1;
        let type_start = cursor;
        while cursor < end {
            if content[cursor] == quote && !types_tag_escaped_quote(content, type_start, cursor) {
                break;
            }
            cursor += 1;
        }
        let type_end = cursor;
        if cursor < end && content[cursor] == quote {
            cursor += 1;
        }
        result.push(TypesTagDeclaration {
            name,
            ty: normalize_twig_type_string(&lossy(&content[type_start..type_end])),
            optional,
            documentation,
            name_range,
            type_range: range(type_start, type_end),
        });
    }
    result
}

fn skip_types_tag_separators(
    content: &[u8],
    cursor: &mut usize,
    end: usize,
    documentation: &mut Vec<String>,
) {
    while *cursor < end {
        while *cursor < end && (is_twig_tag_space(content[*cursor]) || content[*cursor] == b',') {
            *cursor += 1;
        }
        if *cursor >= end || content[*cursor] != b'#' {
            return;
        }
        let documented = *cursor + 1 < end && content[*cursor + 1] == b'#';
        let mut start = *cursor + 1;
        if documented {
            start += 1;
        }
        let mut finish = start;
        while finish < end && content[finish] != b'\n' && content[finish] != b'\r' {
            finish += 1;
        }
        if documented {
            let value = normalize_documentation(&lossy(&content[start..finish]));
            if !value.is_empty() {
                documentation.push(value);
            }
        }
        *cursor = finish;
    }
}

fn parse_types_tag_key(
    content: &[u8],
    cursor: &mut usize,
    end: usize,
) -> Option<(String, TextRange)> {
    if *cursor >= end {
        return None;
    }
    if content[*cursor] == b'\'' || content[*cursor] == b'"' {
        let quote = content[*cursor];
        *cursor += 1;
        let start = *cursor;
        while *cursor < end && content[*cursor] != quote {
            *cursor += 1;
        }
        let finish = *cursor;
        if *cursor < end {
            *cursor += 1;
        }
        return (finish > start).then(|| (lossy(&content[start..finish]), range(start, finish)));
    }
    let start = *cursor;
    while *cursor < end && is_types_tag_key_byte(content[*cursor]) {
        *cursor += 1;
    }
    if *cursor == start {
        return None;
    }
    Some((lossy(&content[start..*cursor]), range(start, *cursor)))
}

fn types_tag_escaped_quote(content: &[u8], start: usize, quote: usize) -> bool {
    let mut slashes = 0;
    let mut index = quote;
    while index > start && content[index - 1] == b'\\' {
        slashes += 1;
        index -= 1;
    }
    slashes % 2 != 0
}

pub(crate) fn normalize_twig_type_string(value: &str) -> String {
    let value = value.replace("\\\\", "\\");
    let value = value.trim();
    value.strip_prefix('\\').unwrap_or(value).to_owned()
}

fn normalize_twig_type_class_name(value: &str) -> String {
    let value = normalize_twig_type_string(value);
    value.strip_prefix('\\').unwrap_or(&value).to_owned()
}

fn types_tag_class_component_range(
    content: &[u8],
    outer_start: u32,
    outer_end: u32,
    offset: u32,
) -> (u32, u32) {
    let mut start = offset;
    while start > outer_start && is_types_tag_class_byte(content[start as usize - 1]) {
        start -= 1;
    }
    let mut end = offset;
    while end < outer_end && is_types_tag_class_byte(content[end as usize]) {
        end += 1;
    }
    (start, end)
}

fn types_tag_class_references_in_declaration(
    content: &[u8],
    declaration: &TypesTagDeclaration,
) -> Vec<TypesTagClassReference> {
    let base = u32::from(declaration.type_range.start()) as usize;
    let end = u32::from(declaration.type_range.end()) as usize;
    if end > content.len() {
        return Vec::new();
    }
    let raw = &content[base..end];
    let mut result = Vec::new();
    let mut cursor = 0;
    while cursor < raw.len() {
        if !is_types_tag_class_start(raw[cursor]) {
            cursor += 1;
            continue;
        }
        let start = cursor;
        while cursor < raw.len() && is_types_tag_class_byte(raw[cursor]) {
            cursor += 1;
        }
        let candidate = lossy(&raw[start..cursor]);
        let name = normalize_twig_type_class_name(&candidate);
        if !types_tag_php_class_name(&name) {
            continue;
        }
        result.push(TypesTagClassReference {
            name,
            raw: candidate,
            range: range(base + start, base + cursor),
        });
    }
    result
}

fn types_tag_php_class_name(value: &str) -> bool {
    let value = value.trim();
    if value.is_empty() || is_non_class_twig_type(value) {
        return false;
    }
    let first = value.strip_prefix('\\').unwrap_or(value).chars().next();
    value.contains('\\') || first.is_some_and(char::is_uppercase)
}

fn is_types_tag_key_byte(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphanumeric()
}

fn is_types_tag_class_start(value: u8) -> bool {
    value == b'\\' || value == b'_' || value.is_ascii_alphabetic() || value >= 0x80
}

fn is_types_tag_class_byte(value: u8) -> bool {
    is_types_tag_class_start(value) || value.is_ascii_digit()
}
