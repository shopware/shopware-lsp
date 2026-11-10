//! Administration folding for JavaScript/TypeScript, Twig and Vue.

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, FoldingRangeProvider, FoldingRangeRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::{query as js_query, syntax as js};
use swlsp_parser_twig::ast::{AstNode, HtmlTag};
use swlsp_parser_twig::{query as twig_query, syntax as twig};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, LineIndex, SyntaxNode, SyntaxNodeExt, TextRange};

/// Derives collapsible Administration source regions from the open
/// document's native CST.
#[derive(Debug, Default, Clone, Copy)]
pub struct AdminFoldingProvider;

impl AdminFoldingProvider {
    pub fn new() -> Self {
        Self
    }
}

impl FoldingRangeProvider for AdminFoldingProvider {
    fn get_folding_ranges(
        &self,
        ctx: &Context,
        request: &FoldingRangeRequest,
    ) -> ProviderResult<Vec<protocol::FoldingRange>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let lines = document.line_index();
        Ok(match document.language {
            Some(LanguageId::JavaScript) => javascript_folding_ranges(&root, lines),
            Some(LanguageId::Twig) => twig_folding_ranges(&root, lines),
            Some(LanguageId::Vue) => {
                let mut result = javascript_folding_ranges(&root, lines);
                result.extend(twig_folding_ranges(&root, lines));
                result
            }
            _ => Vec::new(),
        })
    }
}

fn javascript_folding_ranges(root: &SyntaxNode, lines: &LineIndex) -> Vec<protocol::FoldingRange> {
    let mut result = Vec::new();
    for node in js_query::nodes(root, &[js::JS_OBJECT, js::JS_ARRAY, js::JS_BLOCK]) {
        result.extend(node_folding_range(
            node.range_trimmed_trivia(),
            lines,
            true,
            "",
        ));
    }
    let imports = js_query::nodes(root, &[js::JS_IMPORT_STATEMENT]);
    if let [first, .., last] = imports.as_slice() {
        let range = TextRange::new(
            first.range_trimmed_trivia().start(),
            last.range_trimmed_trivia().end(),
        );
        result.extend(node_folding_range(
            range,
            lines,
            false,
            protocol::FOLDING_RANGE_KIND_IMPORTS,
        ));
    }
    for element in root.descendants_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        if token.kind() != js::TK_BLOCK_COMMENT {
            continue;
        }
        result.extend(node_folding_range(
            token.text_range(),
            lines,
            false,
            protocol::FOLDING_RANGE_KIND_COMMENT,
        ));
    }
    result
}

const TWIG_COMPOUND_FOLDING_KINDS: &[Kind] = &[
    twig::TWIG_BLOCK,
    twig::TWIG_IF,
    twig::TWIG_SET,
    twig::TWIG_FOR,
    twig::TWIG_APPLY,
    twig::TWIG_AUTOESCAPE,
    twig::TWIG_EMBED,
    twig::TWIG_SANDBOX,
    twig::TWIG_VERBATIM,
    twig::TWIG_MACRO,
    twig::TWIG_WITH,
    twig::TWIG_CACHE,
    twig::TWIG_COMPONENT,
    twig::TWIG_ASSETIC,
    twig::TWIG_TRANS,
    twig::TWIG_LITERAL_ARRAY,
    twig::TWIG_LITERAL_HASH,
];

fn twig_folding_ranges(root: &SyntaxNode, lines: &LineIndex) -> Vec<protocol::FoldingRange> {
    let mut result = Vec::new();
    for node in twig_query::iterate_nodes(root, TWIG_COMPOUND_FOLDING_KINDS) {
        result.extend(node_folding_range(
            node.range_trimmed_trivia(),
            lines,
            true,
            "",
        ));
    }
    for node in twig_query::iterate_nodes(root, &[twig::HTML_TAG]) {
        let range = node.range_trimmed_trivia();
        let Some(tag) = HtmlTag::cast(node) else {
            continue;
        };
        let has_ending_tag = tag.ending_tag().is_some();
        result.extend(node_folding_range(range, lines, has_ending_tag, ""));
    }
    for node in twig_query::iterate_nodes(root, &[twig::TWIG_COMMENT, twig::HTML_COMMENT]) {
        result.extend(node_folding_range(
            node.range_trimmed_trivia(),
            lines,
            false,
            protocol::FOLDING_RANGE_KIND_COMMENT,
        ));
    }
    result
}

fn node_folding_range(
    range: TextRange,
    lines: &LineIndex,
    preserve_closing_line: bool,
    kind: &str,
) -> Option<protocol::FoldingRange> {
    if range.end() <= range.start() {
        return None;
    }
    let (start_line, _) = lines.position_utf16(range.start().into());
    let (mut end_line, _) = lines.position_utf16(range.end().into());
    if preserve_closing_line && end_line > start_line {
        end_line -= 1;
    }
    (end_line > start_line).then(|| protocol::FoldingRange {
        start_line,
        end_line,
        kind: kind.to_owned(),
        ..Default::default()
    })
}
