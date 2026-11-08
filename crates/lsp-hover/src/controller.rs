//! Twig `controller()` reference hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, Visibility};
use swlsp_symfony::ServiceIndex;
use swlsp_uriutil::filepath;

use crate::{hover_protocol_range, markdown_hover, request_uri};

pub struct ControllerHoverProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ControllerHoverProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }
}

impl HoverProvider for ControllerHoverProvider {
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
        if !filepath::ext(request_uri(request)).eq_ignore_ascii_case(".twig") {
            return Ok(None);
        }
        let Some(reference) = swlsp_symfony::twig_controller_reference_at(node) else {
            return Ok(None);
        };
        let resolution = swlsp_symfony::resolve_controller_reference(
            &reference.reference,
            self.services.as_deref(),
            Some(php),
        )?;
        if !resolution.target_exists {
            return Ok(None);
        }
        let mut markdown = String::new();
        let _ = write!(
            markdown,
            "**Symfony controller** `{}`\n\n",
            reference.reference.value.replace('`', "\\`")
        );
        let default_symbol = Symbol::default();
        let class = resolution.class.as_ref().unwrap_or(&default_symbol);
        if resolution.method_declared {
            let method = resolution.method.as_ref().unwrap_or(&default_symbol);
            markdown.push_str("```php\n");
            markdown.push_str(&controller_hover_signature(class, method));
            markdown.push_str("\n```");
            if !method.doc_summary().is_empty() {
                let _ = write!(markdown, "\n\n{}", method.doc_summary());
            }
            if !resolution.method_found {
                markdown.push_str("\n\n**Not callable:** method is not public.");
            }
        } else if resolution.class_found {
            let _ = write!(
                markdown,
                "Resolved class: `{}`\n\nMethod `{}()` is not declared.",
                class.fully_qualified, reference.reference.method
            );
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(reference.range, line_index)),
        )))
    }
}

fn controller_hover_signature(class: &Symbol, method: &Symbol) -> String {
    let mut result = String::new();
    result.push_str(visibility_name(method.visibility));
    result.push_str(" function ");
    result.push_str(class.fully_qualified.trim_start_matches('\\'));
    result.push_str("::");
    result.push_str(&method.name);
    result.push('(');
    for (index, parameter) in method.parameters.iter().enumerate() {
        if index != 0 {
            result.push_str(", ");
        }
        if !parameter.ty.is_unknown() {
            result.push_str(&parameter.ty.to_string());
            result.push(' ');
        }
        result.push_str(&parameter.name);
        if parameter.optional {
            result.push_str(" = …");
        }
    }
    result.push(')');
    if !method.return_type.is_unknown() {
        result.push_str(": ");
        result.push_str(&method.return_type.to_string());
    }
    result
}

fn visibility_name(visibility: Visibility) -> &'static str {
    match visibility {
        Visibility::Protected => "protected",
        Visibility::Private => "private",
        Visibility::Public => "public",
    }
}
