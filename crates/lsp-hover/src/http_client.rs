//! Symfony HttpClient option hover.

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::{hover_protocol_range, markdown_hover, php_context};

pub struct HttpClientHoverProvider {
    php: Option<PhpIndex>,
}

impl HttpClientHoverProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl HoverProvider for HttpClientHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(None);
        };
        let (Some(node), Some(line_index)) = (request.node.as_ref(), request.line_index()) else {
            return Ok(None);
        };
        let Some(reference) = swlsp_httpclient::reference_at(node) else {
            return Ok(None);
        };
        if reference.name.is_empty() {
            return Ok(None);
        }
        let context = php_context(Some(php), request);
        if !swlsp_httpclient::validate(php, context.as_ref(), &reference) {
            return Ok(None);
        }
        for option in swlsp_httpclient::options(php) {
            if option.name != reference.name {
                continue;
            }
            let option_type = if option.ty.is_unknown() {
                "mixed".to_owned()
            } else {
                option.ty.to_string()
            };
            return Ok(Some(markdown_hover(
                format!(
                    "**Symfony HttpClient option** `{}`\n\nDefault type: `{}`\n\nDeclared default:\n\n```php\n{}\n```",
                    option.name, option_type, option.default
                ),
                Some(hover_protocol_range(reference.range, line_index)),
            )));
        }
        Ok(None)
    }
}
