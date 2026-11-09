//! Helpers shared by several providers: PHP edits, structural text edits,
//! Twig string escaping and generator request decoding.

use std::collections::HashSet;

use serde::de::DeserializeOwned;
use serde_json::Value;
use swlsp_lsp::{BoxError, CodeActionRequest};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_NAME, PHP_NAMESPACE, PHP_PARAMETER_LIST, TK_CLOSE_BRACE, TK_CLOSE_PAREN, TK_COMMA,
    TK_OPEN_BRACE, TK_OPEN_PAREN, TK_OPEN_TAG,
};
use swlsp_php::NameResolver;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken};
use swlsp_textutil::{eq_ignore_case, lowercase};

pub(crate) fn trim_backslashes(value: &str) -> &str {
    value.trim_matches('\\')
}

/// Byte offset of a node or token boundary.
pub(crate) fn start_of(node: &SyntaxNode) -> u32 {
    u32::from(node.text_range().start())
}

pub(crate) fn end_of(node: &SyntaxNode) -> u32 {
    u32::from(node.text_range().end())
}

pub(crate) fn token_start(token: &SyntaxToken) -> u32 {
    u32::from(token.text_range().start())
}

pub(crate) fn token_end(token: &SyntaxToken) -> u32 {
    u32::from(token.text_range().end())
}

pub(crate) fn trimmed_start(node: &SyntaxNode) -> u32 {
    u32::from(node.range_trimmed_trivia().start())
}

pub(crate) fn trimmed_end(node: &SyntaxNode) -> u32 {
    u32::from(node.range_trimmed_trivia().end())
}

/// The direct child tokens of `node`.
pub(crate) fn child_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> + use<> {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
}

/// The source of the request document (empty without a document).
pub(crate) fn request_source(request: &CodeActionRequest) -> &str {
    request.source()
}

/// Converts byte offsets to a UTF-16 protocol range.
pub(crate) fn offset_range(request: &CodeActionRequest, start: u32, end: u32) -> protocol::Range {
    let Some(line_index) = request.line_index() else {
        return protocol::Range::default();
    };
    let (start_line, start_character) = line_index.position_utf16(start);
    let (end_line, end_character) = line_index.position_utf16(end);
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

pub(crate) fn text_edit(
    request: &CodeActionRequest,
    start: u32,
    end: u32,
    new_text: impl Into<String>,
) -> protocol::TextEdit {
    protocol::TextEdit::new(offset_range(request, start, end), new_text)
}

/// The name to write for `class_name` in the
/// request document and, when needed, the `use` edit that imports it.
pub(crate) fn php_class_qualifier(
    request: &CodeActionRequest,
    class_name: &str,
) -> (String, Option<protocol::TextEdit>) {
    let class_name = trim_backslashes(class_name.trim());
    if class_name.is_empty() {
        return (String::new(), None);
    }
    let (short_name, class_namespace) = match class_name.rfind('\\') {
        Some(separator) => (&class_name[separator + 1..], &class_name[..separator]),
        None => (class_name, ""),
    };
    let Some(root) = request.root.as_ref() else {
        return (format!("\\{class_name}"), None);
    };

    let mut conflict = false;
    let declarations = phpquery::use_declarations(root);
    for declaration in &declarations {
        for imported in parse_use_declaration(&declaration.text().to_string()) {
            if imported.kind != ImportKind::Class {
                continue;
            }
            if eq_ignore_case(trim_backslashes(&imported.target), class_name) {
                return (imported.alias, None);
            }
            if eq_ignore_case(&imported.alias, short_name) {
                conflict = true;
            }
        }
    }
    for class in phpquery::classes(root) {
        if eq_ignore_case(&phpquery::class_name(&class), short_name)
            && !eq_ignore_case(&php_class_fully_qualified_name(root, &class), class_name)
        {
            conflict = true;
        }
    }
    if conflict {
        return (format!("\\{class_name}"), None);
    }
    if eq_ignore_case(
        trim_backslashes(&phpquery::namespace(root)),
        class_namespace,
    ) {
        return (short_name.to_owned(), None);
    }

    let offset = php_import_insertion_offset(root);
    let new_text = if declarations.is_empty() {
        format!("\n\nuse {class_name};")
    } else {
        format!("\nuse {class_name};")
    };
    (
        short_name.to_owned(),
        Some(text_edit(request, offset, offset, new_text)),
    )
}

pub(crate) fn php_import_insertion_offset(root: &SyntaxNode) -> u32 {
    let mut result = 0;
    for declaration in phpquery::use_declarations(root) {
        result = result.max(end_of(&declaration));
    }
    if result != 0 {
        return result;
    }
    if let Some(namespace) = phpquery::nodes(root, &[PHP_NAMESPACE]).first() {
        let text = namespace.text().to_string();
        if let Some(end) = text.find([';', '{']) {
            return start_of(namespace) + end as u32 + 1;
        }
    }
    if let Some(open_tag) = root.first_token()
        && open_tag.kind() == TK_OPEN_TAG
    {
        return token_end(&open_tag);
    }
    0
}

pub(crate) fn php_class_fully_qualified_name(root: &SyntaxNode, class: &SyntaxNode) -> String {
    let class_name = phpquery::class_name(class);
    if class_name.is_empty() {
        return String::new();
    }
    let namespace = phpquery::namespace(root);
    let namespace = trim_backslashes(&namespace);
    if namespace.is_empty() {
        class_name
    } else {
        format!("{namespace}\\{class_name}")
    }
}

#[derive(Debug, Clone, Copy)]
pub(crate) struct PhpSuggestedParameter {
    pub class_name: &'static str,
    pub variable_name: &'static str,
}

/// Lowercase resolved class names used in
/// the parameter types of `method`.
pub(crate) fn php_existing_parameter_types(
    method: &SyntaxNode,
    resolver: &NameResolver,
) -> HashSet<String> {
    let mut result = HashSet::new();
    for parameter in phpquery::parameters(method) {
        let parameter_type = phpquery::parameter_type(&parameter);
        for type_name in parameter_type.split(['?', '|', '&', '(', ')']) {
            let type_name = type_name.trim();
            if type_name.is_empty() {
                continue;
            }
            result.insert(lowercase(trim_backslashes(&resolver.resolve(type_name))));
        }
    }
    result
}

/// Inserts `qualifier $variable_name` before
/// the first optional or variadic parameter, or after the last one.
pub(crate) fn php_required_parameter_edit(
    request: &CodeActionRequest,
    class: &SyntaxNode,
    method: &SyntaxNode,
    qualifier: &str,
    variable_name: &str,
) -> Option<protocol::TextEdit> {
    if qualifier.is_empty() || variable_name.is_empty() {
        return None;
    }
    let source = request_source(request);
    let list = phpquery::direct_child(method, PHP_PARAMETER_LIST)?;
    let (open, close) = php_parameter_list_delimiters(&list);
    let (open, close) = (open?, close?);
    if token_end(&open) > token_start(&close) || token_start(&close) as usize > source.len() {
        return None;
    }
    let new_parameter = format!("{qualifier} ${variable_name}");
    let parameters: Vec<SyntaxNode> = phpquery::parameters(method).collect();
    let multiline =
        source[token_end(&open) as usize..token_start(&close) as usize].contains(['\r', '\n']);

    for parameter in &parameters {
        if !phpquery::parameter_optional(parameter) && !phpquery::parameter_variadic(parameter) {
            continue;
        }
        let offset = trimmed_start(parameter);
        let new_text = if multiline {
            format!("{new_parameter},\n{}", php_line_indentation(source, offset))
        } else {
            format!("{new_parameter}, ")
        };
        return Some(text_edit(request, offset, offset, new_text));
    }

    let Some(last_parameter) = parameters.last() else {
        let offset = token_end(&open);
        let new_text = if multiline {
            format!(
                "\n{}{new_parameter}",
                php_parameter_indentation(source, class, method, token_start(&close))
            )
        } else {
            new_parameter
        };
        return Some(text_edit(request, offset, offset, new_text));
    };

    let last_end = trimmed_end(last_parameter);
    let indent = php_line_indentation(source, trimmed_start(last_parameter));
    if let Some(trailing_comma) = php_trailing_parameter_comma(&list, last_end, token_start(&close))
    {
        let offset = token_end(&trailing_comma);
        let new_text = if multiline {
            format!("\n{indent}{new_parameter},")
        } else {
            format!(" {new_parameter},")
        };
        return Some(text_edit(request, offset, offset, new_text));
    }

    let new_text = if multiline {
        format!(",\n{indent}{new_parameter}")
    } else {
        format!(", {new_parameter}")
    };
    Some(text_edit(request, last_end, last_end, new_text))
}

fn php_parameter_list_delimiters(list: &SyntaxNode) -> (Option<SyntaxToken>, Option<SyntaxToken>) {
    let mut open = None;
    let mut close = None;
    for token in child_tokens(list) {
        if token.kind() == TK_OPEN_PAREN {
            if open.is_none() {
                open = Some(token);
            }
        } else if token.kind() == TK_CLOSE_PAREN {
            close = Some(token);
        }
    }
    (open, close)
}

fn php_trailing_parameter_comma(
    list: &SyntaxNode,
    parameter_end: u32,
    close_start: u32,
) -> Option<SyntaxToken> {
    let mut result = None;
    for token in child_tokens(list) {
        if token.kind() == TK_COMMA
            && token_start(&token) >= parameter_end
            && token_end(&token) <= close_start
        {
            result = Some(token);
        }
    }
    result
}

fn php_parameter_indentation(
    source: &str,
    class: &SyntaxNode,
    method: &SyntaxNode,
    close_offset: u32,
) -> String {
    let close_indent = php_line_indentation(source, close_offset);
    let class_indent = php_line_indentation(source, trimmed_start(class));
    let method_indent = php_line_indentation(source, trimmed_start(method));
    let mut indent_unit = method_indent
        .strip_prefix(class_indent)
        .unwrap_or(method_indent);
    if indent_unit.is_empty() {
        indent_unit = if method_indent.contains('\t') {
            "\t"
        } else {
            "    "
        };
    }
    format!("{close_indent}{indent_unit}")
}

pub(crate) fn php_class_short_name(class_name: &str) -> &str {
    let class_name = trim_backslashes(class_name);
    match class_name.rfind('\\') {
        Some(separator) => &class_name[separator + 1..],
        None => class_name,
    }
}

/// The request document's file path, or
/// its URI when it is not a file URI.
pub(crate) fn request_document_path(request: &CodeActionRequest) -> String {
    let Some(document) = request.document.as_ref() else {
        return String::new();
    };
    swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone())
}

pub(crate) fn php_class_constructor(class: &SyntaxNode) -> Option<SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| eq_ignore_case(&phpquery::method_name(method), "__construct"))
}

pub(crate) fn php_class_is_readonly(class: Option<&SyntaxNode>) -> bool {
    class.is_some_and(|class| {
        child_tokens(class).any(|token| eq_ignore_case(token.text(), "readonly"))
    })
}

/// The first `{` and last `}` child token.
pub(crate) fn php_block_delimiters(
    block: &SyntaxNode,
) -> (Option<SyntaxToken>, Option<SyntaxToken>) {
    let mut open = None;
    let mut close = None;
    for token in child_tokens(block) {
        if token.kind() == TK_OPEN_BRACE {
            if open.is_none() {
                open = Some(token);
            }
        } else if token.kind() == TK_CLOSE_BRACE {
            close = Some(token);
        }
    }
    (open, close)
}

pub(crate) fn php_member_indent_unit(
    source: &str,
    class: Option<&SyntaxNode>,
    member_indent: &str,
) -> String {
    let class_indent = class.map_or("", |class| {
        php_line_indentation(source, trimmed_start(class))
    });
    let unit = member_indent
        .strip_prefix(class_indent)
        .unwrap_or(member_indent);
    if !unit.is_empty() {
        return unit.to_owned();
    }
    if member_indent.contains('\t') {
        return "\t".to_owned();
    }
    "    ".to_owned()
}

pub(crate) fn has_resolved_php_attribute(
    node: &SyntaxNode,
    resolver: &NameResolver,
    targets: &[&str],
) -> bool {
    for attribute in phpquery::attributes(node) {
        let resolved = resolver.resolve(&phpquery::attribute_name(&attribute));
        let name = trim_backslashes(&resolved);
        if targets
            .iter()
            .any(|target| eq_ignore_case(name, trim_backslashes(target)))
        {
            return true;
        }
    }
    false
}

/// The leading spaces/tabs of the line holding
/// `offset`, up to `offset`.
pub(crate) fn php_line_indentation(source: &str, offset: u32) -> &str {
    let offset = offset as usize;
    if offset > source.len() {
        return "";
    }
    let before = &source.as_bytes()[..offset];
    let line_start = before
        .iter()
        .rposition(|byte| *byte == b'\r' || *byte == b'\n')
        .map_or(0, |index| index + 1);
    let line = &source[line_start..offset];
    let trimmed = line.trim_start_matches([' ', '\t']);
    &line[..line.len() - trimmed.len()]
}

/// Methods keyed by lowercase name (last wins).
pub(crate) fn php_methods_by_name(
    class: &SyntaxNode,
) -> std::collections::HashMap<String, SyntaxNode> {
    let mut result = std::collections::HashMap::new();
    for method in phpquery::methods(class) {
        let name = lowercase(&phpquery::method_name(&method));
        if !name.is_empty() {
            result.insert(name, method);
        }
    }
    result
}

pub(crate) fn php_import_block(root: &SyntaxNode, classes: &[String]) -> String {
    let prefix = if phpquery::use_declarations(root).is_empty() {
        "\n\n"
    } else {
        "\n"
    };
    format!("{prefix}use {};", classes.join(";\nuse "))
}

pub(crate) fn php_class_name_used_outside(
    root: &SyntaxNode,
    resolver: &NameResolver,
    class_name: &str,
    excluded: &[Option<&SyntaxNode>],
) -> bool {
    let wanted = trim_backslashes(class_name);
    for name in phpquery::nodes(root, &[PHP_NAME]) {
        let range = name.text_range();
        let skip = excluded.iter().flatten().any(|node| {
            range.start() >= node.text_range().start() && range.end() <= node.text_range().end()
        });
        if skip {
            continue;
        }
        if eq_ignore_case(
            trim_backslashes(&resolver.resolve(&phpquery::name_value(&name))),
            wanted,
        ) {
            return true;
        }
    }
    false
}

pub(crate) fn escape_php_single_quoted(value: &str) -> String {
    value.replace('\\', "\\\\").replace('\'', "\\'")
}

pub(crate) fn escape_twig_single_quoted(value: &str) -> String {
    value.replace('\\', "\\\\").replace('\'', "\\'")
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub(crate) struct PhpSourceReplacement {
    pub start: u32,
    pub end: u32,
    pub text: String,
}

/// Applies non-overlapping replacements
/// back to front; `None` on an invalid or overlapping replacement.
pub(crate) fn apply_php_source_replacements(
    source: &str,
    replacements: &mut [PhpSourceReplacement],
) -> Option<String> {
    replacements.sort_by(|left, right| {
        right
            .start
            .cmp(&left.start)
            .then_with(|| right.end.cmp(&left.end))
    });
    let mut source = source.to_owned();
    let mut boundary = source.len() as u32;
    for replacement in replacements.iter() {
        if replacement.start > replacement.end
            || replacement.end as usize > source.len()
            || replacement.end > boundary
        {
            return None;
        }
        source.replace_range(
            replacement.start as usize..replacement.end as usize,
            &replacement.text,
        );
        boundary = replacement.start;
    }
    Some(source)
}

pub(crate) fn is_administration_twig_path(file_path: &str) -> bool {
    let normalized = swlsp_uriutil::filepath::to_slash(&swlsp_uriutil::filepath::clean(file_path));
    normalized.ends_with(".twig") && normalized.contains("/Resources/app/administration/src/")
}

pub(crate) fn decode_generator_request<T: DeserializeOwned>(
    raw: Option<&Value>,
) -> Result<T, BoxError> {
    let Some(raw) = raw else {
        return Err("missing generator request".into());
    };
    serde_json::from_value(raw.clone())
        .map_err(|error| format!("invalid generator request: {error}").into())
}

/// The leading spaces and tabs of the line holding `offset`.
pub(crate) fn indentation_at(content: &[u8], offset: u32) -> String {
    let start = line_start_offset(content, offset) as usize;
    let mut end = start;
    while end < content.len() && matches!(content[end], b' ' | b'\t') {
        end += 1;
    }
    String::from_utf8_lossy(&content[start..end]).into_owned()
}

pub(crate) fn line_start_offset(content: &[u8], offset: u32) -> u32 {
    let mut offset = (offset as usize).min(content.len());
    while offset > 0 && content[offset - 1] != b'\n' {
        offset -= 1;
    }
    offset as u32
}

/// The offset of the last `delimiter` in
/// `node`, moved left over horizontal whitespace.
pub(crate) fn yaml_flow_closing_offset(node: Option<&SyntaxNode>, delimiter: u8) -> Option<u32> {
    let node = node?;
    let text = node.text().to_string();
    let bytes = text.as_bytes();
    let mut index = bytes.iter().rposition(|byte| *byte == delimiter)?;
    while index > 0 && (bytes[index - 1] == b' ' || bytes[index - 1] == b'\t') {
        index -= 1;
    }
    Some(start_of(node) + index as u32)
}

/// The workspace snapshot linked with `document`, from the memoized analysis
/// shared with other features; analyzes `root` under `path` only when the
/// document has no memoizable analysis (for example, a non-file URI).
pub(crate) fn document_snapshot(
    index: &swlsp_php::PhpIndex,
    document: &swlsp_lsp::TextDocument,
    path: &str,
    root: &SyntaxNode,
) -> std::sync::Arc<swlsp_php_semantic::Snapshot> {
    if let Ok(Some(state)) = swlsp_lsp_phpanalysis::for_document(index, document) {
        return state.snapshot.clone();
    }
    let analyzed =
        index.analyze_document(path, i64::from(document.version), root, document.source());
    index.semantic_snapshot().with_document(&analyzed)
}
