//! Helpers shared by several completion providers.

use swlsp_lsp::SyntaxContext;
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_syntax::{LineIndex, TextRange};

/// Go `strings.ToLower`: maps every rune through `unicode.ToLower` (one
/// rune to one rune, unlike Rust's context-sensitive `str::to_lowercase`).
pub fn go_to_lower(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value.chars().map(go_lower_rune).collect()
}

/// Go `unicode.ToLower` for one rune.
pub fn go_lower_rune(c: char) -> char {
    if c == '\u{130}' {
        return 'i';
    }
    let mut lower = c.to_lowercase();
    match (lower.next(), lower.next()) {
        (Some(first), None) => first,
        _ => c,
    }
}

/// Go `sortCompletionItems`: `slices.SortFunc` by lowercase label (not
/// stable; uses Go's pdqsort so equal labels keep Go's order).
pub fn sort_completion_items(items: &mut [protocol::CompletionItem]) {
    if items.len() < 2 {
        return;
    }
    let mut keyed: Vec<(String, protocol::CompletionItem)> = items
        .iter_mut()
        .map(|item| (go_to_lower(&item.label), std::mem::take(item)))
        .collect();
    // Labels equal up to case ("error"/"Error") are ordered by the label
    // itself, so the result does not depend on the provider's input order.
    swlsp_gosort::sort_slice(&mut keyed, |left, right| {
        left.0 < right.0 || (left.0 == right.0 && left.1.label < right.1.label)
    });
    for (slot, (_, item)) in items.iter_mut().zip(keyed) {
        *slot = item;
    }
}

/// A byte range converted to an LSP UTF-16 range (Go
/// `completionProtocolRange`, `namedArgumentCompletionRange`,
/// `containerConstantCompletionRange`).
pub fn protocol_range(range: TextRange, line_index: &LineIndex) -> protocol::Range {
    let (start_line, start_character) = line_index.position_utf16(range.start().into());
    let (end_line, end_character) = line_index.position_utf16(range.end().into());
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

/// A completion `TextEdit` (`{"range": ..., "newText": ...}`).
pub fn text_edit(range: protocol::Range, new_text: impl Into<String>) -> protocol::TextEdit {
    protocol::TextEdit {
        range,
        new_text: new_text.into(),
        ..Default::default()
    }
}

/// Go `truncateText`: cuts at a byte offset (a cut multi-byte tail becomes one
/// U+FFFD per byte, as Go's JSON encoder writes it).
pub fn truncate_text(text: &str, max_len: usize) -> String {
    if text.len() <= max_len {
        return text.to_string();
    }
    let cut = max_len.saturating_sub(3);
    let mut valid = cut;
    while !text.is_char_boundary(valid) {
        valid -= 1;
    }
    let mut result = String::with_capacity(cut + 6);
    result.push_str(&text[..valid]);
    // Go's JSON encoder writes one U+FFFD per invalid byte.
    for _ in valid..cut {
        result.push('\u{FFFD}');
    }
    result.push_str("...");
    result
}

/// Go `leadingYAMLIndent`: spaces and tabs at the start of `line`.
pub fn leading_yaml_indent(line: &str) -> usize {
    line.bytes()
        .take_while(|byte| *byte == b' ' || *byte == b'\t')
        .count()
}

/// Go `isYAMLFile`.
pub fn is_yaml_file(uri: &str) -> bool {
    let extension = go_to_lower(swlsp_uriutil::filepath::ext(uri));
    extension == ".yaml" || extension == ".yml"
}

/// Go `componentPropDetail`.
pub fn component_prop_detail(prop: &swlsp_twigcomponent::Prop) -> String {
    let suffix = if prop.live && prop.writable {
        "writable live prop"
    } else if prop.live {
        "live prop"
    } else {
        ""
    };
    if prop.ty.is_empty() {
        return suffix.to_string();
    }
    if suffix.is_empty() {
        return prop.ty.clone();
    }
    format!("{} • {}", prop.ty, suffix)
}

/// The request's PHP analysis, as Go's PHP context enricher attaches it to
/// `context.Context`: the memoized document analysis, or a fresh unnamed
/// analysis when the document has no file path. `None` outside PHP
/// documents or without a tree.
pub fn php_context(index: &PhpIndex, syntax: &SyntaxContext) -> Option<PhpContext> {
    if syntax.language != Some(swlsp_language::LanguageId::Php) {
        return None;
    }
    let document = syntax.document.as_deref()?;
    match swlsp_lsp_phpanalysis::for_document(index, document) {
        Ok(Some(state)) => Some(index.analyzed_snapshot_context(
            syntax.node.as_ref(),
            state.document.clone(),
            Some(state.snapshot.clone()),
        )),
        Ok(None) => None,
        Err(_) => {
            let root = syntax.root.as_ref()?;
            Some(index.document_context(
                "",
                i64::from(document.version),
                syntax.node.as_ref(),
                root,
                document.source(),
            ))
        }
    }
}
