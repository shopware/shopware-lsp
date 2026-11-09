//! Registry references in Twig templates.

use crate::*;

use twigast::AstNode;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AdminTwigRegistryReference {
    pub kind: AdminSymbolKind,
    pub name: String,
    pub range: TextRange,
}

fn text_range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

/// Complete registry references from executable Vue/Twig expression
/// contexts, sorted by position.
pub fn twig_registry_references(root: &SyntaxNode) -> Vec<AdminTwigRegistryReference> {
    let source = root.text().to_string();
    let mut result = Vec::new();
    let mut offset = 0;
    while offset < source.len() {
        let (reference, next) = match scan_twig_acl_reference(&source, offset) {
            Ok(found) => found,
            Err(_) => {
                offset += 1;
                continue;
            }
        };
        offset = next;
        if reference.name.is_empty()
            || !twig_registry_reference_allowed(root, u32::from(reference.range.start()))
        {
            continue;
        }
        result.push(reference);
    }
    result.extend(twig_route_references(root, false));
    result.sort_by_key(|reference| reference.range.start());
    result
}

/// Also recognizes an unfinished literal, so completion works for
/// `acl.can('<caret>')`.
pub fn twig_registry_reference_at_offset(
    root: &SyntaxNode,
    offset: u32,
) -> Option<AdminTwigRegistryReference> {
    let source = root.text().to_string();
    let mut cursor = 0;
    while cursor < source.len() {
        let (reference, next) = match scan_twig_acl_reference(&source, cursor) {
            Ok(found) => found,
            Err(_) => {
                cursor += 1;
                continue;
            }
        };
        cursor = next;
        if offset < u32::from(reference.range.start())
            || offset > u32::from(reference.range.end())
            || !twig_registry_reference_allowed(root, u32::from(reference.range.start()))
        {
            continue;
        }
        return Some(reference);
    }
    twig_route_references(root, true)
        .into_iter()
        .find(|reference| {
            offset >= u32::from(reference.range.start())
                && offset <= u32::from(reference.range.end())
        })
}

fn twig_route_references(
    root: &SyntaxNode,
    include_incomplete: bool,
) -> Vec<AdminTwigRegistryReference> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_ATTRIBUTE]) {
        let Some(inner) = twigast::HtmlAttribute::cast(node.clone())
            .and_then(|attribute| attribute.value())
            .and_then(|value| value.inner())
        else {
            continue;
        };
        let text = inner.syntax().text().to_string();
        if !is_admin_route_attribute(
            &twigquery::html_attribute_name(&node).unwrap_or_default(),
            &text,
        ) {
            continue;
        }
        for reference in
            scan_twig_route_names(&text, u32::from(inner.syntax().text_range().start()))
        {
            if !reference.name.is_empty() || include_incomplete {
                result.push(reference);
            }
        }
    }
    result
}

fn is_admin_route_attribute(name: &str, value: &str) -> bool {
    let name = match name.find('.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    if matches!(
        name,
        ":to" | "v-bind:to" | ":route" | "v-bind:route" | ":router-link" | "v-bind:router-link"
    ) {
        return true;
    }
    if !name.starts_with(':') && !name.starts_with('@') && !name.starts_with("v-") {
        return false;
    }
    value.contains("$router.push")
        || value.contains("$router.replace")
        || value.contains("$router.resolve")
}

fn scan_twig_route_names(source: &str, base: u32) -> Vec<AdminTwigRegistryReference> {
    const PROPERTY: &[u8] = b"name";
    let bytes = source.as_bytes();
    let mut result = Vec::new();
    let mut offset = 0;
    while offset < bytes.len() {
        if offset + PROPERTY.len() > bytes.len()
            || &bytes[offset..offset + PROPERTY.len()] != PROPERTY
            || (offset > 0 && is_admin_identifier_byte(bytes[offset - 1]))
            || (offset + PROPERTY.len() < bytes.len()
                && is_admin_identifier_byte(bytes[offset + PROPERTY.len()]))
        {
            offset += 1;
            continue;
        }
        let mut cursor = skip_admin_expression_space(source, offset + PROPERTY.len());
        if cursor >= bytes.len() || bytes[cursor] != b':' {
            offset += PROPERTY.len();
            continue;
        }
        cursor = skip_admin_expression_space(source, cursor + 1);
        if cursor >= bytes.len() || (bytes[cursor] != b'\'' && bytes[cursor] != b'"') {
            offset = cursor + 1;
            continue;
        }
        let quote = bytes[cursor];
        let value_start = cursor + 1;
        cursor = value_start;
        while cursor < bytes.len()
            && bytes[cursor] != quote
            && is_admin_registry_name_byte(bytes[cursor])
        {
            cursor += 1;
        }
        result.push(AdminTwigRegistryReference {
            kind: AdminSymbolKind::ModuleRoute,
            name: source[value_start..cursor].to_owned(),
            range: TextRange::new(
                TextSize::from(base + value_start as u32),
                TextSize::from(base + cursor as u32),
            ),
        });
        offset = (cursor + 1).max(offset + 1);
    }
    result
}

/// Scans an `acl.can('<privilege>')` reference at `start`. `Ok` carries the
/// reference and the next scan offset; `Err` the next offset after a
/// mismatch.
fn scan_twig_acl_reference(
    source: &str,
    start: usize,
) -> Result<(AdminTwigRegistryReference, usize), usize> {
    const RECEIVER: &[u8] = b"acl";
    const METHOD: &[u8] = b"can";
    let bytes = source.as_bytes();
    if start + RECEIVER.len() > bytes.len()
        || &bytes[start..start + RECEIVER.len()] != RECEIVER
        || (start > 0 && is_admin_identifier_byte(bytes[start - 1]))
        || (start + RECEIVER.len() < bytes.len()
            && is_admin_identifier_byte(bytes[start + RECEIVER.len()]))
    {
        return Err(start + 1);
    }
    let mut cursor = skip_admin_expression_space(source, start + RECEIVER.len());
    if cursor >= bytes.len() || bytes[cursor] != b'.' {
        return Err(start + RECEIVER.len());
    }
    cursor = skip_admin_expression_space(source, cursor + 1);
    if cursor + METHOD.len() > bytes.len()
        || &bytes[cursor..cursor + METHOD.len()] != METHOD
        || (cursor + METHOD.len() < bytes.len()
            && is_admin_identifier_byte(bytes[cursor + METHOD.len()]))
    {
        return Err(cursor + 1);
    }
    cursor = skip_admin_expression_space(source, cursor + METHOD.len());
    if cursor >= bytes.len() || bytes[cursor] != b'(' {
        return Err(cursor + 1);
    }
    cursor = skip_admin_expression_space(source, cursor + 1);
    if cursor >= bytes.len() || (bytes[cursor] != b'\'' && bytes[cursor] != b'"') {
        return Err(cursor + 1);
    }
    let quote = bytes[cursor];
    let value_start = cursor + 1;
    cursor = value_start;
    while cursor < bytes.len()
        && bytes[cursor] != quote
        && is_admin_registry_name_byte(bytes[cursor])
    {
        cursor += 1;
    }
    Ok((
        AdminTwigRegistryReference {
            kind: AdminSymbolKind::Privilege,
            name: source[value_start..cursor].to_owned(),
            range: text_range(value_start, cursor),
        },
        (cursor + 1).max(start + 1),
    ))
}

fn twig_registry_reference_allowed(root: &SyntaxNode, offset: u32) -> bool {
    let mut node = root.node_at(TextSize::from(offset));
    if node.is_none() && offset > 0 {
        node = root.node_at(TextSize::from(offset - 1));
    }
    let Some(node) = node else {
        return false;
    };
    if twigquery::closest_node_of_kind(
        &node,
        &[twigsyntax::TWIG_COMMENT, twigsyntax::TWIG_VERBATIM],
    )
    .is_some()
    {
        return false;
    }
    if let Some(attribute) = twigquery::html_attribute_at(&node) {
        let name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
        return name.starts_with(':')
            || name.starts_with('@')
            || name.starts_with('#')
            || name.starts_with("v-");
    }
    twigquery::closest_node_of_kind(&node, &[twigsyntax::TWIG_FUNCTION_CALL]).is_some()
}

fn skip_admin_expression_space(source: &str, mut offset: usize) -> usize {
    let bytes = source.as_bytes();
    while offset < bytes.len() && matches!(bytes[offset], b' ' | b'\t' | b'\r' | b'\n') {
        offset += 1;
    }
    offset
}

fn is_admin_identifier_byte(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphanumeric()
}

fn is_admin_registry_name_byte(value: u8) -> bool {
    is_admin_identifier_byte(value) || matches!(value, b'.' | b':' | b'-')
}
