use std::sync::Arc;
use swlsp_parser_twig::query;
use swlsp_parser_twig::syntax::{TWIG_COMMENT, TWIG_FUNCTION_CALL, TWIG_VERBATIM};

use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::document_memo::DocumentMemo;

/// A static Administration translation key used by `$t()` or `$tc()`.
/// Administration templates embed Vue expressions directly in HTML
/// attributes, where the Twig CST intentionally keeps the value lossless
/// instead of interpreting it as server-side Twig.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AdminTwigReference {
    pub key: String,
    pub range: TextRange,
}

struct Scan {
    reference: AdminTwigReference,
    next: usize,
    /// The closing quote was found.
    complete: bool,
    /// The string is the whole first argument (`$t('key')`, `$t('key', …)`),
    /// not one piece of a computed key such as `$t('prefix.' + name)`.
    whole: bool,
}

/// A scan result: the reference, whether its string is closed, and whether
/// it is the whole first argument.
type ScanEntry = (AdminTwigReference, bool, bool);

/// Every scan of the tree's source in order, before the context check.
/// Memoized per tree snapshot so per-request lookups do not copy and rescan
/// the whole document.
fn admin_twig_scans(root: &SyntaxNode) -> Arc<Vec<ScanEntry>> {
    static MEMO: DocumentMemo<Vec<ScanEntry>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, "", || {
        let source = root.text().to_string();
        let mut result = Vec::new();
        let mut offset = 0;
        while offset < source.len() {
            let Some(scan) = scan_admin_twig_reference(&source, offset) else {
                offset += 1;
                continue;
            };
            offset = scan.next;
            result.push((scan.reference, scan.complete, scan.whole));
        }
        result
    })
}

/// Complete static translation references from both Twig expressions and
/// executable Vue attribute values, in source order.
pub fn admin_twig_references(root: &SyntaxNode) -> Vec<AdminTwigReference> {
    let mut result: Vec<AdminTwigReference> = admin_twig_scans(root)
        .iter()
        .filter(|(reference, complete, whole)| {
            *complete
                && *whole
                && !reference.key.is_empty()
                && admin_twig_reference_allowed(root, u32::from(reference.range.start()))
        })
        .map(|(reference, _, _)| reference.clone())
        .collect();
    result.sort_by_key(|reference| reference.range.start());
    result
}

/// Also accepts an unfinished first argument so completion works before the
/// closing quote or parenthesis has been typed.
pub fn admin_twig_reference_at_offset(
    root: &SyntaxNode,
    offset: u32,
) -> Option<AdminTwigReference> {
    for (reference, complete, _) in admin_twig_scans(root).iter() {
        let (start, end) = (
            u32::from(reference.range.start()),
            u32::from(reference.range.end()),
        );
        if offset < start || offset > end || !admin_twig_reference_allowed(root, start) {
            continue;
        }
        let mut reference = reference.clone();
        if !complete && offset < end {
            // The cursor is a byte offset; keep the cut on a character boundary. The key
            // is the source text from `start`, so cut it relative to that.
            let mut cut = (offset - start) as usize;
            while !reference.key.is_char_boundary(cut) {
                cut -= 1;
            }
            reference.range = TextRange::new(reference.range.start(), (start + cut as u32).into());
            reference.key.truncate(cut);
        }
        return Some(reference);
    }
    None
}

fn scan_admin_twig_reference(source: &str, start: usize) -> Option<Scan> {
    let bytes = source.as_bytes();
    let mut cursor = start;
    if bytes[cursor..].starts_with(b"this.") {
        cursor += "this.".len();
    }
    if cursor >= bytes.len()
        || bytes[cursor] != b'$'
        || (start > 0 && is_snippet_identifier_byte(bytes[start - 1]))
    {
        return None;
    }
    cursor += 1;
    if cursor >= bytes.len() || bytes[cursor] != b't' {
        return None;
    }
    cursor += 1;
    if cursor < bytes.len() && bytes[cursor] == b'c' {
        cursor += 1;
    }
    if cursor < bytes.len() && is_snippet_identifier_byte(bytes[cursor]) {
        return None;
    }
    cursor = skip_snippet_space(bytes, cursor);
    if cursor >= bytes.len() || bytes[cursor] != b'(' {
        return None;
    }
    cursor = skip_snippet_space(bytes, cursor + 1);
    if cursor >= bytes.len() || !matches!(bytes[cursor], b'\'' | b'"' | b'`') {
        return None;
    }
    let quote = bytes[cursor];
    let value_start = cursor + 1;
    cursor = value_start;
    let mut complete = false;
    while cursor < bytes.len() {
        match bytes[cursor] {
            b'\\' => {
                cursor += 2;
                continue;
            }
            current if current == quote => {
                complete = true;
                break;
            }
            b'\r' | b'\n' => break,
            b'$' if quote == b'`' && bytes.get(cursor + 1) == Some(&b'{') => break,
            _ => cursor += 1,
        }
    }
    // An escape at the very end steps the cursor past EOF; clamp it.
    let end = cursor.min(bytes.len());
    let mut key_end = end;
    while !source.is_char_boundary(key_end) {
        key_end -= 1;
    }
    let after = skip_snippet_space(bytes, (cursor + 1).min(bytes.len()));
    let whole = complete && matches!(bytes.get(after), Some(b',' | b')'));
    Some(Scan {
        reference: AdminTwigReference {
            key: source[value_start..key_end].to_owned(),
            range: TextRange::new((value_start as u32).into(), (end as u32).into()),
        },
        next: (cursor + 1).max(start + 1),
        complete,
        whole,
    })
}

fn admin_twig_reference_allowed(root: &SyntaxNode, offset: u32) -> bool {
    let mut node = root.node_at(offset.into());
    if node.is_none() && offset > 0 {
        node = root.node_at((offset - 1).into());
    }
    let Some(node) = node else {
        return false;
    };
    if query::closest_node_of_kind(&node, &[TWIG_COMMENT, TWIG_VERBATIM]).is_some() {
        return false;
    }
    if let Some(attribute) = query::html_attribute_at(&node) {
        let name = query::html_attribute_name(&attribute).unwrap_or_default();
        if name.starts_with(':')
            || name.starts_with('@')
            || name.starts_with('#')
            || name.starts_with("v-")
        {
            return true;
        }
    }
    query::closest_node_of_kind(&node, &[TWIG_FUNCTION_CALL]).is_some()
}

fn skip_snippet_space(bytes: &[u8], mut offset: usize) -> usize {
    while offset < bytes.len() && matches!(bytes[offset], b' ' | b'\t' | b'\r' | b'\n') {
        offset += 1;
    }
    offset
}

fn is_snippet_identifier_byte(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphanumeric()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn root(source: &str) -> SyntaxNode {
        swlsp_parser_twig::parse(source).tree.root()
    }

    #[test]
    fn references_cover_twig_and_vue_expressions() {
        let source = "<div\n    :label=\"$t('admin.bound')\"\n    v-tooltip=\"{ message: $tc( 'admin.tooltip' ) }\"\n    title=\"$t('admin.plain-attribute')\"\n>\n    {{ $t('admin.twig') }}\n    $t('admin.plain-text')\n    {# {{ $t('admin.comment') }} #}\n</div>";
        let references = admin_twig_references(&root(source));
        let keys: Vec<&str> = references
            .iter()
            .map(|reference| reference.key.as_str())
            .collect();
        assert_eq!(keys, ["admin.bound", "admin.tooltip", "admin.twig"]);
        for reference in &references {
            assert_eq!(&source[reference.range], reference.key);
        }
    }

    #[test]
    fn reference_at_offset_supports_incomplete_keys() {
        for source in [
            r#"<mt-button :label="$t('admin.sett" />"#,
            r#"<mt-button :label="this.$tc(  'admin.sett" />"#,
        ] {
            let offset = (source.find("admin.sett").unwrap() + "admin.sett".len()) as u32;
            let reference = admin_twig_reference_at_offset(&root(source), offset).unwrap();
            assert_eq!(reference.key, "admin.sett");
        }
    }

    /// The unmemoized implementation, kept as the reference the memoized one
    /// is tested against.
    fn fresh_reference_at_offset(root: &SyntaxNode, offset: u32) -> Option<AdminTwigReference> {
        let source = root.text().to_string();
        let mut cursor = 0;
        while cursor < source.len() {
            let Some(scan) = scan_admin_twig_reference(&source, cursor) else {
                cursor += 1;
                continue;
            };
            cursor = scan.next;
            let mut reference = scan.reference;
            let (start, end) = (
                u32::from(reference.range.start()),
                u32::from(reference.range.end()),
            );
            if offset < start || offset > end || !admin_twig_reference_allowed(root, start) {
                continue;
            }
            if !scan.complete && offset < end {
                let mut cut = offset as usize;
                while !source.is_char_boundary(cut) {
                    cut -= 1;
                }
                reference.range = TextRange::new(reference.range.start(), (cut as u32).into());
                reference.key = source[start as usize..cut].to_owned();
            }
            return Some(reference);
        }
        None
    }

    #[test]
    fn memoized_reference_at_offset_matches_fresh_scan() {
        let sources = [
            "<div :a=\"$t('admin.ä€b')\" v-x=\"$tc('admin.ünfinished\n\">{{ $t(\"admin.twig\") }}{# $t('c') #}<b @c=\"this.$t(`x.y`)\"/>",
            "<mt-button :label=\"$t('admin.sett\" />{{ $tc( 'é\\'q' ) }}",
        ];
        let roots: Vec<_> = sources.iter().map(|source| root(source)).collect();
        for _ in 0..2 {
            for (source, root) in sources.iter().zip(&roots) {
                for offset in 0..=source.len() as u32 + 1 {
                    assert_eq!(
                        admin_twig_reference_at_offset(root, offset),
                        fresh_reference_at_offset(root, offset),
                        "offset {offset} in {source:?}"
                    );
                }
            }
        }
    }

    #[test]
    fn references_reject_dynamic_template_keys() {
        assert!(admin_twig_references(&root("<div :label=\"$t(`admin.${name}`)\" />")).is_empty());
    }

    #[test]
    fn trailing_escape_does_not_panic() {
        assert!(admin_twig_references(&root("{{ $t('a\\")).is_empty());
        let _ = admin_twig_reference_at_offset(&root("{{ $t('ä\\"), 8);
    }
}
