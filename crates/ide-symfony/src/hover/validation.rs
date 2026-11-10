//! Symfony Validator constraint-option hover.

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNodeExt;

use crate::hover::{hover_protocol_range, markdown_hover, php_context};

pub struct ValidationHoverProvider {
    php: Option<PhpIndex>,
}

impl ValidationHoverProvider {
    /// `php` supplies the request's PHP semantic context.
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl HoverProvider for ValidationHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Ok(None);
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) = swlsp_validation::option_reference_at(context.as_ref(), root, node)
        else {
            return Ok(None);
        };
        let properties = swlsp_validation::constraint_properties_in_context(
            context.as_ref(),
            &reference.constraint,
        );
        let Some(property) =
            swlsp_validation::find_constraint_property(&properties, &reference.name)
        else {
            return Ok(None);
        };
        let mut value = format!(
            "**Symfony constraint option** `{}`\n\nDeclared by `{}`",
            property.name.replace('`', "\\`"),
            property.fully_qualified.replace('`', "\\`")
        );
        if !property.ty.is_unknown() {
            value.push_str("\n\nPHP type: `");
            value.push_str(&property.ty.to_string());
            value.push('`');
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            value,
            Some(hover_protocol_range(
                reference.node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}
