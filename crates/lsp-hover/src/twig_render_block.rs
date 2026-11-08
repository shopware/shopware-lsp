//! Hover for Twig blocks rendered from PHP (`renderBlock()`).

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_types::equal_fold;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::filepath;

use crate::{
    escape_markdown, hover_protocol_range, markdown_hover, php_context, request_offset, request_uri,
};

pub struct TwigRenderBlockHoverProvider {
    root: String,
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigRenderBlockHoverProvider {
    pub fn new(
        root: impl Into<String>,
        twig: Option<Arc<TwigIndexer>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            root: root.into(),
            twig,
            php,
        }
    }
}

impl HoverProvider for TwigRenderBlockHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let (Some(twig), Some(php)) = (self.twig.as_deref(), self.php.as_ref()) else {
            return Ok(None);
        };
        let (Some(root), Some(line_index), Some(offset)) = (
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(None);
        };
        if !filepath::ext(request_uri(request)).eq_ignore_ascii_case(".php") {
            return Ok(None);
        }
        let Some(reference) = swlsp_twig::render_block_reference_at(root, offset) else {
            return Ok(None);
        };
        if reference.block.is_empty() {
            return Ok(None);
        }
        let context = php_context(Some(php), request);
        if !swlsp_twig::validate_render_block_reference(&reference, Some(php), context.as_ref()) {
            return Ok(None);
        }
        let blocks = twig.get_template_blocks(&[&reference.template])?;
        let declarations: Vec<_> = blocks
            .iter()
            .filter(|block| equal_fold(&block.name, &reference.block))
            .collect();
        if declarations.is_empty() {
            return Ok(None);
        }
        let mut markdown = format!(
            "**Twig block** `{}`\n\nTemplate: `{}`",
            escape_markdown(&reference.block),
            escape_markdown(&reference.template)
        );
        for block in &declarations {
            let display = match filepath::rel(&self.root, &block.file_path) {
                Ok(relative) => filepath::to_slash(&relative),
                Err(_) => block.file_path.clone(),
            };
            let _ = write!(
                markdown,
                "\n\n- `{}:{}`",
                escape_markdown(&display),
                block.line
            );
        }
        if let Some(block) = declarations
            .iter()
            .find(|block| !block.documentation.is_empty())
        {
            markdown.push_str("\n\n");
            markdown.push_str(&block.documentation);
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(reference.block_range, line_index)),
        )))
    }
}
