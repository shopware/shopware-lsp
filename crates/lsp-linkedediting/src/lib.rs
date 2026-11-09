//! Linked editing for Administration component tags.

use swlsp_language::LanguageId;
use swlsp_lsp::{
    Context, LinkedEditingRangeProvider, LinkedEditingRangeRequest, ProviderResult,
    effective_syntax_language, protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlTag};
use swlsp_parser_twig::{query as twig_query, syntax as twig};
use swlsp_syntax::{LineIndex, SyntaxNode, TextRange};

#[cfg(test)]
mod tests;

pub const ADMIN_COMPONENT_TAG_WORD_PATTERN: &str = "[-A-Za-z0-9_]+";

/// Links the opening and closing names of Vue-style component tags in
/// Administration Twig markup. The parser's `HtmlTag` node is the pairing
/// authority, so nested components cannot accidentally link to a same-named
/// sibling and malformed markup remains conservative.
#[derive(Debug, Default, Clone, Copy)]
pub struct AdminLinkedEditingProvider;

impl AdminLinkedEditingProvider {
    pub fn new() -> Self {
        Self
    }
}

impl LinkedEditingRangeProvider for AdminLinkedEditingProvider {
    fn get_linked_editing_ranges(
        &self,
        ctx: &Context,
        request: &LinkedEditingRangeRequest,
    ) -> ProviderResult<Option<protocol::LinkedEditingRanges>> {
        if ctx.is_cancelled()
            || request.document.is_none()
            || effective_syntax_language(request.language, request.node.as_ref())
                != Some(LanguageId::Twig)
        {
            return Ok(None);
        }
        let (Some(root), Some(lines)) = (request.root.as_ref(), request.line_index()) else {
            return Ok(None);
        };
        let position = request.params.position;
        let offset = lines.offset_utf16(position.line, position.character);
        if let Some(tag) = request.node.as_ref().and_then(ancestor_tag)
            && let Some(ranges) = tag_ranges(&tag, offset, lines)
        {
            return Ok(Some(ranges));
        }
        // At the half-open end of a name the node lookup can select the
        // following token. A narrow fallback over complete HtmlTag nodes
        // preserves the editor experience at that valid word boundary without
        // guessing tag pairing.
        for node in twig_query::iterate_nodes(root, &[twig::HTML_TAG]) {
            let Some(tag) = HtmlTag::cast(node) else {
                continue;
            };
            if let Some(ranges) = tag_ranges(&tag, offset, lines) {
                return Ok(Some(ranges));
            }
        }
        Ok(None)
    }
}

fn ancestor_tag(node: &SyntaxNode) -> Option<HtmlTag> {
    node.ancestors()
        .filter(|current| current.kind() == twig::HTML_TAG)
        .find_map(HtmlTag::cast)
}

fn tag_ranges(
    tag: &HtmlTag,
    offset: u32,
    lines: &LineIndex,
) -> Option<protocol::LinkedEditingRanges> {
    let starting = tag.starting_tag()?.name()?;
    let ending = tag.ending_tag()?.name()?;
    let name = starting.text();
    if name.is_empty() || name != ending.text() || !component_tag(name) {
        return None;
    }
    let start_range = starting.text_range();
    let end_range = ending.text_range();
    if !contains_cursor(start_range, offset) && !contains_cursor(end_range, offset) {
        return None;
    }
    Some(protocol::LinkedEditingRanges {
        ranges: vec![
            protocol_range(lines, start_range),
            protocol_range(lines, end_range),
        ],
        word_pattern: ADMIN_COMPONENT_TAG_WORD_PATTERN.to_owned(),
    })
}

fn component_tag(name: &str) -> bool {
    swlsp_admin::is_component_tag(name) || name.eq_ignore_ascii_case("component")
}

fn contains_cursor(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}
