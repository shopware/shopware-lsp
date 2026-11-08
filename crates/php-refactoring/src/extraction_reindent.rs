use std::collections::HashMap;

use swlsp_parser_php::syntax::TK_STRING;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::descendant_tokens;

pub(crate) fn reindent_extraction(
    source: &str,
    range: TextRange,
    from: &str,
    to: &str,
    statements: &[SyntaxNode],
) -> String {
    reindent_extraction_offsets(source, range, from, to, statements, &mut []).0
}

/// Re-indents the selected source from `from` to `to`, leaving lines that
/// continue a multi-line string literal untouched, and maps each source
/// offset in `positions` into the result. Offset mapping and literal
/// preservation share a single pass, so return rewrites do not repeatedly
/// rebuild every preceding line in a large selection.
pub(crate) fn reindent_extraction_offsets(
    source: &str,
    range: TextRange,
    from: &str,
    to: &str,
    statements: &[SyntaxNode],
    positions: &mut [u32],
) -> (String, HashMap<u32, u32>) {
    let literals: Vec<TextRange> = statements
        .iter()
        .flat_map(descendant_tokens)
        .filter(|token| token.kind() == TK_STRING)
        .map(|token| token.text_range())
        .collect();
    positions.sort_unstable();
    let bytes = source.as_bytes();
    let (range_start, range_end) = (
        u32::from(range.start()) as usize,
        u32::from(range.end()) as usize,
    );
    let mut offsets = HashMap::with_capacity(positions.len());
    let mut result = String::with_capacity(range_end - range_start);
    let mut offset = range_start;
    let (mut literal_index, mut position_index) = (0, 0);
    while offset < range_end {
        let mut end = offset;
        while end < range_end && bytes[end] != b'\n' {
            end += 1;
        }
        while literal_index < literals.len()
            && u32::from(literals[literal_index].end()) as usize <= offset
        {
            literal_index += 1;
        }
        let inside = literals.get(literal_index).is_some_and(|literal| {
            offset > u32::from(literal.start()) as usize
                && offset < u32::from(literal.end()) as usize
        });
        let mut line = &source[offset..end];
        let mut prefix = "";
        let mut removed = 0;
        if !inside {
            prefix = to;
            if offset != range_start && line.starts_with(from) {
                removed = from.len();
                line = &line[removed..];
            }
        }
        while position_index < positions.len() && positions[position_index] as usize <= end {
            let position = positions[position_index] as usize;
            if position >= offset {
                let column = (position - offset).saturating_sub(removed);
                offsets.insert(
                    position as u32,
                    (result.len() + prefix.len() + column) as u32,
                );
            }
            position_index += 1;
        }
        result.push_str(prefix);
        result.push_str(line);
        if end < range_end {
            result.push('\n');
            end += 1;
        }
        offset = end;
    }
    (result, offsets)
}
