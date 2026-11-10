//! Symfony Console helper-name hover.

use swlsp_console::HelperCatalog;
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::hover::{hover_protocol_range, markdown_hover, php_context};

pub struct ConsoleHelperHoverProvider {
    php: Option<PhpIndex>,
    catalog: Option<HelperCatalog>,
}

impl ConsoleHelperHoverProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        let catalog = php.clone().map(HelperCatalog::new);
        Self { php, catalog }
    }
}

impl HoverProvider for ConsoleHelperHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let (Some(php), Some(catalog)) = (self.php.as_ref(), self.catalog.as_ref()) else {
            return Ok(None);
        };
        let (Some(node), Some(line_index)) = (request.node.as_ref(), request.line_index()) else {
            return Ok(None);
        };
        let Some(reference) = swlsp_console::helper_reference_at(node) else {
            return Ok(None);
        };
        if reference.name.is_empty() {
            return Ok(None);
        }
        let context = php_context(Some(php), request);
        if !swlsp_console::validate_helper_reference(php, context.as_ref(), &reference) {
            return Ok(None);
        }
        for helper in catalog.helpers() {
            if helper.name != reference.name {
                continue;
            }
            let mut value = format!(
                "**Symfony Console helper** `{}`\n\nClass: `{}`",
                helper.name, helper.class
            );
            if !helper.summary.is_empty() {
                value.push_str("\n\n");
                value.push_str(&helper.summary);
            }
            return Ok(Some(markdown_hover(
                value,
                Some(hover_protocol_range(reference.range, line_index)),
            )));
        }
        Ok(None)
    }
}
