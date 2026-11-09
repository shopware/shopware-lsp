//! Helpers shared by several completion providers.

use std::cmp::Ordering;

use swlsp_lsp::SyntaxContext;
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_textutil::lowercase;

/// Orders items by lowercase label. Labels equal up to
/// case ("error"/"Error") or identical are ordered by
/// [`compare_completion_items`], so the result does not depend on the
/// provider's input order.
pub fn sort_completion_items(items: &mut [protocol::CompletionItem]) {
    if items.len() < 2 {
        return;
    }
    let mut keyed: Vec<(String, protocol::CompletionItem)> = items
        .iter_mut()
        .map(|item| (lowercase(&item.label), std::mem::take(item)))
        .collect();
    keyed.sort_unstable_by(|left, right| {
        left.0
            .cmp(&right.0)
            .then_with(|| compare_completion_items(&left.1, &right.1))
    });
    for (slot, (_, item)) in items.iter_mut().zip(keyed) {
        *slot = item;
    }
}

/// A tie-break between completion items whose primary sort key is equal:
/// label, then detail, edit text, insert text, filter text and sort text.
/// Items equal on all of these render the same in a completion list.
pub fn compare_completion_items(
    left: &protocol::CompletionItem,
    right: &protocol::CompletionItem,
) -> Ordering {
    fn edit_text(item: &protocol::CompletionItem) -> &str {
        item.text_edit
            .as_ref()
            .map_or("", |edit| edit.new_text.as_str())
    }
    left.label
        .cmp(&right.label)
        .then_with(|| left.detail.cmp(&right.detail))
        .then_with(|| edit_text(left).cmp(edit_text(right)))
        .then_with(|| left.insert_text.cmp(&right.insert_text))
        .then_with(|| left.filter_text.cmp(&right.filter_text))
        .then_with(|| left.sort_text.cmp(&right.sort_text))
}

/// A byte range converted to an LSP UTF-16 range.
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

/// `text` cut to `max_len` bytes including a `...` suffix. The cut is at a
/// byte offset; each byte of a character split by the cut becomes U+FFFD.
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
    // One replacement character per byte of the split character.
    for _ in valid..cut {
        result.push('\u{FFFD}');
    }
    result.push_str("...");
    result
}

/// The number of spaces and tabs at the start of `line`.
pub fn leading_yaml_indent(line: &str) -> usize {
    line.bytes()
        .take_while(|byte| *byte == b' ' || *byte == b'\t')
        .count()
}

/// Whether `uri` has a `.yaml` or `.yml` extension (any case).
pub fn is_yaml_file(uri: &str) -> bool {
    let extension = lowercase(swlsp_uriutil::filepath::ext(uri));
    extension == ".yaml" || extension == ".yml"
}

/// The completion detail of a component prop: its type and live/writable
/// marker.
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

/// The request's PHP analysis: the memoized document analysis, or a fresh unnamed
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

/// The lowercase extension of `path`, with its dot (`.twig`).
pub(crate) fn lower_ext(path: &str) -> String {
    swlsp_textutil::lowercase(swlsp_uriutil::filepath::ext(path))
}
