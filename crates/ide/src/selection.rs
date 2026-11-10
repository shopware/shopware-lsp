//! Selection range providers.
//!
//! Both providers expand through the lossless CST ancestry of the open
//! document: the exact non-trivia token first, then every syntax node and
//! finally the whole document.

use swlsp_lsp::{Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

pub fn selection_ranges(
    ctx: &Context,
    root: &SyntaxNode,
    line_index: &LineIndex,
    positions: &[protocol::Position],
) -> ProviderResult<Vec<protocol::SelectionRange>> {
    let mut result = Vec::with_capacity(positions.len());
    for position in positions {
        ctx.check()?;
        result.push(selection_range_at(root, line_index, *position));
    }
    Ok(result)
}

pub fn selection_range_at(
    root: &SyntaxNode,
    line_index: &LineIndex,
    position: protocol::Position,
) -> protocol::SelectionRange {
    let mut offset = TextSize::from(line_index.offset_utf16(position.line, position.character));
    let cursor = offset;
    let root_range = root.text_range();
    let mut token = root.token_at(offset);
    let mut node = root.node_at(offset);
    if token.is_none() && offset > root_range.start() {
        offset -= TextSize::from(1);
        token = root.token_at(offset);
        node = root.node_at(offset);
    }

    let mut ranges: Vec<TextRange> = Vec::new();
    let mut add = |candidate: TextRange, allow_empty: bool| {
        if candidate.start() > cursor
            || candidate.end() < cursor
            || (!allow_empty && candidate.is_empty())
        {
            return;
        }
        let contains_child = ranges.last().is_none_or(|child| {
            candidate.start() <= child.start()
                && candidate.end() >= child.end()
                && candidate != *child
        });
        if contains_child {
            ranges.push(candidate);
        }
    };
    if let Some(token) = token.filter(|token| !token.kind().is_trivia()) {
        add(token.text_range(), false);
    }
    for current in node.iter().flat_map(SyntaxNode::ancestors) {
        add(current.range_trimmed_trivia(), false);
    }
    add(root_range, true);
    if ranges.is_empty() {
        ranges.push(root_range);
    }

    let mut parent: Option<Box<protocol::SelectionRange>> = None;
    for range in ranges.iter().rev() {
        parent = Some(Box::new(protocol::SelectionRange {
            range: swlsp_lsp::protocol_range(line_index, *range),
            parent,
        }));
    }
    parent.map(|value| *value).unwrap_or_default()
}
