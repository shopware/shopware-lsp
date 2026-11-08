//! `enum('App\\Status')` style references in Twig to PHP enums (port of Go
//! `twig_enum_definition.go`).

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::common::{ext_lower, php_symbol_location, request_uri};

pub struct TwigEnumDefinitionProvider {
    php: Option<PhpIndex>,
}

impl TwigEnumDefinitionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl GotoDefinitionProvider for TwigEnumDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(php), Some(node)) = (self.php.as_ref(), request.node.as_ref()) else {
            return Vec::new();
        };
        if ext_lower(request_uri(request)) != ".twig" {
            return Vec::new();
        }
        let Some(reference) = swlsp_twig::enum_reference_at(node) else {
            return Vec::new();
        };
        php.find_class(&reference.name)
            .map(|symbol| vec![php_symbol_location(&symbol)])
            .unwrap_or_default()
    }
}

#[cfg(test)]
#[path = "twig_enum_tests.rs"]
mod tests;
