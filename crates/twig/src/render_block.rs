//! `renderBlock()` references and template block catalogs.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::compare_fold;
use crate::indexer::TwigIndexer;
use crate::path::clean;
use crate::reference::{normalize_template_reference, php_string_is_static, string_content_range};

const ABSTRACT_CONTROLLER_CLASS: &str =
    "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController";

/// The template and block string arguments of
/// `AbstractController::renderBlock()`/`renderBlockView()`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RenderBlockReference {
    pub template: String,
    pub block: String,
    pub template_range: TextRange,
    pub block_range: TextRange,
    pub call: SyntaxNode,
}

/// One block declaration reachable from a concrete template, including
/// declarations inherited through Twig extends.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TemplateBlock {
    pub name: String,
    pub documentation: String,
    pub file_path: String,
    pub range: TextRange,
    pub line: i64,
}

pub fn render_block_references_in_php(root: &SyntaxNode) -> Vec<RenderBlockReference> {
    let mut result = Vec::new();
    for call in phpquery::nodes(root, &[PHP_MEMBER_CALL, PHP_SCOPED_CALL, PHP_FUNCTION_CALL]) {
        let name = phpquery::call_method_name(&call);
        if !swlsp_textutil::eq_ignore_case(&name, "renderBlock")
            && !swlsp_textutil::eq_ignore_case(&name, "renderBlockView")
        {
            continue;
        }
        let (Some(template_node), Some(block_node)) = (
            render_block_argument(&call, &["view", "name", "template"], 0),
            render_block_argument(&call, &["block"], 1),
        ) else {
            continue;
        };
        if template_node.kind() != PHP_STRING
            || block_node.kind() != PHP_STRING
            || !php_string_is_static(&template_node)
            || !php_string_is_static(&block_node)
        {
            continue;
        }
        result.push(RenderBlockReference {
            template: normalize_template_reference(&phpquery::string_value(&template_node)),
            block: phpquery::string_value(&block_node),
            template_range: string_content_range(
                &template_node.text().to_string(),
                template_node.text_range(),
            ),
            block_range: string_content_range(
                &block_node.text().to_string(),
                block_node.text_range(),
            ),
            call,
        });
    }
    result
}

/// The reference whose block name contains `offset`. The document scan is
/// memoized per tree snapshot; the call node is recovered from its range.
pub fn render_block_reference_at(root: &SyntaxNode, offset: u32) -> Option<RenderBlockReference> {
    // (template, block, template range, block range, call range, call kind)
    type Scanned = (
        String,
        String,
        TextRange,
        TextRange,
        TextRange,
        swlsp_syntax::Kind,
    );
    static MEMO: crate::DocumentMemo<Vec<Scanned>> = crate::DocumentMemo::new(4);
    let references = MEMO.get_or_compute(root, "", || {
        render_block_references_in_php(root)
            .into_iter()
            .map(|reference| {
                (
                    reference.template,
                    reference.block,
                    reference.template_range,
                    reference.block_range,
                    reference.call.text_range(),
                    reference.call.kind(),
                )
            })
            .collect()
    });
    let (template, block, template_range, block_range, call_range, call_kind) =
        references.iter().find(|reference| {
            offset >= u32::from(reference.3.start()) && offset <= u32::from(reference.3.end())
        })?;
    // The outermost node of the call's kind spanning exactly its range, the
    // first one in preorder as the scan found it.
    let start = match root.covering_element(*call_range) {
        rowan::NodeOrToken::Node(node) => node,
        rowan::NodeOrToken::Token(token) => token.parent()?,
    };
    let call = start
        .ancestors()
        .take_while(|node| node.text_range() == *call_range)
        .filter(|node| node.kind() == *call_kind)
        .last()?;
    Some(RenderBlockReference {
        template: template.clone(),
        block: block.clone(),
        template_range: *template_range,
        block_range: *block_range,
        call,
    })
}

/// Whether the call is made on an `AbstractController` receiver.
pub fn validate_render_block_reference(
    reference: &RenderBlockReference,
    index: Option<&PhpIndex>,
    context: Option<&PhpContext>,
) -> bool {
    index.is_some_and(|index| {
        index.is_method_called_on_class(context, &reference.call, ABSTRACT_CONTROLLER_CLASS)
    })
}

fn render_block_argument(call: &SyntaxNode, names: &[&str], fallback: usize) -> Option<SyntaxNode> {
    for (index, argument) in phpquery::arguments(call).enumerate() {
        let name = phpquery::argument_name(&argument);
        if names
            .iter()
            .any(|candidate| swlsp_textutil::eq_ignore_case(&name, candidate))
        {
            return phpquery::argument_expression(call, index);
        }
    }
    let argument = phpquery::argument(call, fallback)?;
    if !phpquery::argument_name(&argument).is_empty() {
        return None;
    }
    phpquery::argument_expression(call, fallback)
}

fn sort_template_blocks(result: &mut [TemplateBlock]) {
    result.sort_by(|left, right| {
        compare_fold(&left.name, &right.name)
            .then_with(|| left.file_path.cmp(&right.file_path))
            .then(left.range.start().cmp(&right.range.start()))
            .then_with(|| left.name.cmp(&right.name))
    });
}

impl TwigIndexer {
    /// Blocks declared by the templates and their extends ancestors.
    pub fn get_template_blocks<S: AsRef<str>>(
        &self,
        templates: &[S],
    ) -> swlsp_indexer::Result<Vec<TemplateBlock>> {
        let mut visited = HashSet::new();
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        let mut stack: Vec<(String, usize)> = templates
            .iter()
            .rev()
            .map(|template| (template.as_ref().to_owned(), 0))
            .collect();
        // Depth-first, visiting includes in source order.
        while let Some((template, depth)) = stack.pop() {
            if depth > 32 {
                continue;
            }
            let template = normalize_template_reference(&template);
            if template.is_empty() || !visited.insert(template.clone()) {
                continue;
            }
            let files = self.get_twig_files_by_rel_path(&template)?;
            let mut children = Vec::new();
            for file in files {
                for block in file.blocks.values() {
                    if !seen.insert((clean(&file.path), block.name_range, block.name.clone())) {
                        continue;
                    }
                    result.push(TemplateBlock {
                        name: block.name.clone(),
                        documentation: block.documentation.clone(),
                        file_path: file.path.clone(),
                        range: block.name_range,
                        line: block.line,
                    });
                }
                if !file.extends_file.is_empty() {
                    children.push((file.extends_file.clone(), depth + 1));
                }
            }
            stack.extend(children.into_iter().rev());
        }
        sort_template_blocks(&mut result);
        Ok(result)
    }

    /// Every indexed block declaration once, with its source path and exact
    /// name range.
    pub fn get_all_template_blocks(&self) -> swlsp_indexer::Result<Vec<TemplateBlock>> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for file in self.get_all_twig_files()? {
            for block in file.blocks.values() {
                if !seen.insert((clean(&file.path), block.name_range, block.name.clone())) {
                    continue;
                }
                result.push(TemplateBlock {
                    name: block.name.clone(),
                    documentation: block.documentation.clone(),
                    file_path: file.path.clone(),
                    range: block.name_range,
                    line: block.line,
                });
            }
        }
        sort_template_blocks(&mut result);
        Ok(result)
    }
}
