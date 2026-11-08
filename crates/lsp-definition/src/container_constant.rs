//! Go `container_constant_definition.go`: `!php/const`, `!php/enum` and XML
//! `type="constant"` values navigate to the PHP constant or enum.

use std::collections::HashSet;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::ContainerConstantReference;

use crate::common::{ext_lower, php_symbol_location, request_offset, request_uri};

pub struct ContainerConstantDefinitionProvider {
    php: Option<PhpIndex>,
}

impl ContainerConstantDefinitionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl GotoDefinitionProvider for ContainerConstantDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        for reference in constant_definition_references(request) {
            if !reference.range.contains(offset.into()) {
                continue;
            }
            let symbols = swlsp_symfony::resolve_container_value(Some(php), &reference);
            let mut seen = HashSet::new();
            return symbols
                .iter()
                .filter(|symbol| seen.insert(symbol.id.clone()))
                .map(php_symbol_location)
                .collect();
        }
        Vec::new()
    }
}

fn constant_definition_references(request: &DefinitionRequest) -> Vec<ContainerConstantReference> {
    match ext_lower(request_uri(request)).as_str() {
        ".yaml" | ".yml" => swlsp_symfony::yaml_container_constant_references(request.source()),
        ".xml" => match request.root.as_ref() {
            Some(root) => swlsp_symfony::xml_container_constant_references(root),
            None => Vec::new(),
        },
        _ => Vec::new(),
    }
}

#[cfg(test)]
#[path = "container_constant_tests.rs"]
mod tests;
