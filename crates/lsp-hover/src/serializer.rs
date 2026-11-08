//! Symfony Serializer deserialize-target hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_serializer::Index;

use crate::{hover_protocol_range, markdown_hover, request_offset, request_uri};

pub struct SerializerHoverProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl SerializerHoverProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl HoverProvider for SerializerHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(_node), Some(root), Some(line_index), Some(offset)) = (
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(None);
        };
        if !request_uri(request).to_lowercase().ends_with(".php") {
            return Ok(None);
        }
        let Some(usage) = swlsp_serializer::usage_at(root, offset) else {
            return Ok(None);
        };
        let usages = index.usages(&usage.class)?;
        let mut markdown = format!(
            "**Symfony Serializer target** `{}`",
            usage.class.replace('`', "\\`")
        );
        if let Some(php) = self.php.as_ref()
            && let Some(symbol) = php.find_class(&usage.class)
            && !symbol.doc_summary().is_empty()
        {
            let _ = write!(markdown, "\n\n{}", symbol.doc_summary());
        }
        let _ = write!(markdown, "\n\n{} indexed deserialize use(s)", usages.len());
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(usage.range, line_index)),
        )))
    }
}
