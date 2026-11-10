//! `renderBlock()`/`renderBlockView()` block arguments in PHP controllers to
//! the block declarations.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::LineIndex;
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use crate::definition::common::{ext_lower, protocol_range, request_offset, request_uri};
use crate::definition::twig_common::php_context;

pub struct TwigRenderBlockDefinitionProvider {
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigRenderBlockDefinitionProvider {
    pub fn new(twig: Option<Arc<TwigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { twig, php }
    }
}

impl GotoDefinitionProvider for TwigRenderBlockDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(twig), Some(php), Some(root), Some(_)) = (
            self.twig.as_deref(),
            self.php.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        if ext_lower(request_uri(request)) != ".php" {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(reference) = swlsp_twig::render_block_reference_at(root, offset) else {
            return Vec::new();
        };
        if reference.block.is_empty() {
            return Vec::new();
        }
        let context = php_context(Some(php), request);
        if !swlsp_twig::validate_render_block_reference(&reference, Some(php), context.as_ref()) {
            return Vec::new();
        }
        let Ok(blocks) = twig.get_template_blocks(&[reference.template.as_str()]) else {
            return Vec::new();
        };
        let mut result = Vec::new();
        for block in &blocks {
            if !eq_ignore_case(&block.name, &reference.block) {
                continue;
            }
            let Ok(source) = std::fs::read(&block.file_path) else {
                continue;
            };
            let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
            result.push(protocol::Location {
                uri: file_uri(&block.file_path),
                range: protocol_range(&lines, block.range),
            });
        }
        result
    }
}

#[cfg(test)]
#[path = "twig_render_block_tests.rs"]
mod tests;
