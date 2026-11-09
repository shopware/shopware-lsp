//! PHP folding: portable line folds from the current CST.

use std::collections::BTreeSet;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, FoldingRangeProvider, FoldingRangeRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::syntax as php;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

/// Reads only the current CST and emits portable line folds.
#[derive(Debug, Default, Clone, Copy)]
pub struct PhpFoldingProvider;

impl PhpFoldingProvider {
    pub fn new() -> Self {
        Self
    }
}

impl FoldingRangeProvider for PhpFoldingProvider {
    fn get_folding_ranges(
        &self,
        ctx: &Context,
        request: &FoldingRangeRequest,
    ) -> ProviderResult<Vec<protocol::FoldingRange>> {
        ctx.check()?;
        let document = &request.document;
        if document.language != Some(LanguageId::Php) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut collector = Collector {
            ctx,
            lines: document.line_index(),
            ranges: BTreeSet::new(),
        };
        collector.visit(&root);
        ctx.check()?;
        // The set orders by (start line, end line, kind).
        Ok(collector
            .ranges
            .into_iter()
            .map(|(start_line, end_line, kind)| protocol::FoldingRange {
                start_line,
                end_line,
                kind,
                ..Default::default()
            })
            .collect())
    }
}

struct Collector<'a> {
    ctx: &'a Context,
    lines: &'a LineIndex,
    ranges: BTreeSet<(u32, u32, String)>,
}

impl Collector<'_> {
    fn add(&mut self, range: TextRange, hide_last: bool, kind: &str) {
        if range.is_empty() {
            return;
        }
        let (start, _) = self.lines.position_utf16(range.start().into());
        let (mut end, _) = self.lines.position_utf16(u32::from(range.end()) - 1);
        if hide_last && end > start {
            end -= 1;
        }
        if end > start {
            self.ranges.insert((start, end, kind.to_owned()));
        }
    }

    fn line(&self, offset: TextSize) -> u32 {
        self.lines.position_utf16(offset.into()).0
    }

    fn visit(&mut self, node: &SyntaxNode) {
        if self.ctx.is_cancelled() {
            return;
        }
        self.visit_container(node);

        let mut imports = TextRange::default();
        let mut comments = TextRange::default();
        for element in node.children_with_tokens() {
            if self.ctx.is_cancelled() {
                return;
            }
            match element {
                NodeOrToken::Node(child) => {
                    self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
                    if child.kind() == php::PHP_USE_DECLARATION {
                        let range = child.range_trimmed_trivia();
                        imports = if imports.is_empty() {
                            range
                        } else {
                            TextRange::new(imports.start(), range.end())
                        };
                    } else {
                        self.flush(&mut imports, protocol::FOLDING_RANGE_KIND_IMPORTS);
                    }
                    self.visit(&child);
                }
                NodeOrToken::Token(token) => match token.kind() {
                    php::TK_LINE_COMMENT => {
                        let range = token.text_range();
                        let start = self.line(range.start());
                        let previous = self.line(comments.end());
                        if !comments.is_empty() && start > previous + 1 {
                            self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
                        }
                        comments = if comments.is_empty() {
                            range
                        } else {
                            TextRange::new(comments.start(), range.end())
                        };
                    }
                    php::TK_BLOCK_COMMENT => {
                        self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
                        self.add(
                            token.text_range(),
                            false,
                            protocol::FOLDING_RANGE_KIND_COMMENT,
                        );
                    }
                    php::TK_STRING => {
                        self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
                        self.flush(&mut imports, protocol::FOLDING_RANGE_KIND_IMPORTS);
                        self.add(token.text_range(), false, "");
                    }
                    kind if !kind.is_trivia() => {
                        self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
                        self.flush(&mut imports, protocol::FOLDING_RANGE_KIND_IMPORTS);
                    }
                    _ => {}
                },
            }
        }
        self.flush(&mut imports, protocol::FOLDING_RANGE_KIND_IMPORTS);
        self.flush(&mut comments, protocol::FOLDING_RANGE_KIND_COMMENT);
    }

    fn flush(&mut self, range: &mut TextRange, kind: &str) {
        self.add(*range, false, kind);
        *range = TextRange::default();
    }

    fn visit_container(&mut self, node: &SyntaxNode) {
        let kind = node.kind();
        if matches!(
            kind,
            php::PHP_CLASS_BODY
                | php::PHP_BLOCK
                | php::PHP_ARRAY
                | php::PHP_MATCH_EXPRESSION
                | php::PHP_PROPERTY_HOOK_LIST
                | php::PHP_TRAIT_USE_DECLARATION
        ) {
            let range = node.range_trimmed_trivia();
            let closed = !range.is_empty()
                && node
                    .token_at(range.end() - TextSize::from(1))
                    .is_some_and(|last| {
                        matches!(
                            last.kind(),
                            php::TK_CLOSE_BRACE | php::TK_CLOSE_BRACKET | php::TK_CLOSE_PAREN
                        )
                    });
            self.add(range, closed, "");
        } else if matches!(
            kind,
            php::PHP_IF_STATEMENT
                | php::PHP_FOR_STATEMENT
                | php::PHP_FOREACH_STATEMENT
                | php::PHP_WHILE_STATEMENT
                | php::PHP_SWITCH_STATEMENT
        ) {
            let range = node.range_trimmed_trivia();
            if kind == php::PHP_SWITCH_STATEMENT
                && node.child_token_of_kind(php::TK_OPEN_BRACE).is_some()
            {
                let closed = node.child_token_of_kind(php::TK_CLOSE_BRACE).is_some();
                self.add(range, closed, "");
            }
            // Alternative control syntax keeps its end keyword directly on
            // the statement.
            let alternative = node
                .children_with_tokens()
                .filter_map(NodeOrToken::into_token)
                .any(|token| {
                    matches!(
                        token.text().to_ascii_lowercase().as_str(),
                        "endif" | "endfor" | "endforeach" | "endwhile" | "endswitch"
                    )
                });
            if alternative {
                self.add(range, true, "");
            }
        }
    }
}
