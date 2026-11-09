//! Controller references in YAML/XML routes and Twig `controller()`/`@see`
//! navigate to the action or class.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::{ControllerReference, ServiceIndex};

use crate::common::{ext_lower, php_symbol_location, request_offset, request_uri};

pub struct ControllerDefinitionProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ControllerDefinitionProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }

    fn twig_reference(
        &self,
        php: &PhpIndex,
        request: &DefinitionRequest,
    ) -> Option<ControllerReference> {
        if let (Some(root), Some(offset)) = (request.root.as_ref(), request_offset(request))
            && let Some(see) = swlsp_twig::see_reference_at(root, offset)
            && let Some(reference) = swlsp_symfony::parse_twig_controller_reference(&see.target)
        {
            if php.find_class(&reference.target).is_some() {
                return None;
            }
            return Some(reference);
        }
        let node = request.node.as_ref()?;
        swlsp_symfony::twig_controller_reference_at(node).map(|reference| reference.reference)
    }
}

impl GotoDefinitionProvider for ControllerDefinitionProvider {
    fn get_definition(
        &self,
        ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let reference = match ext_lower(request_uri(request)).as_str() {
            ".yaml" | ".yml" => request
                .node
                .as_ref()
                .and_then(swlsp_symfony::yaml_controller_reference)
                .map(|(reference, _)| reference),
            ".xml" => request
                .node
                .as_ref()
                .and_then(swlsp_symfony::xml_controller_reference)
                .map(|(reference, _)| reference),
            ".twig" => self.twig_reference(php, request),
            _ => return Vec::new(),
        };
        let Some(reference) = reference else {
            return Vec::new();
        };
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let Ok(resolution) = swlsp_symfony::resolve_controller_reference(
            &reference,
            self.services.as_deref(),
            Some(php),
        ) else {
            return Vec::new();
        };
        if resolution.method_declared
            && let Some(method) = resolution.method.as_ref()
        {
            return vec![php_symbol_location(method)];
        }
        if resolution.class_found
            && let Some(class) = resolution.class.as_ref()
        {
            return vec![php_symbol_location(class)];
        }
        Vec::new()
    }
}

#[cfg(test)]
#[path = "controller_tests.rs"]
mod tests;
