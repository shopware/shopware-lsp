//! Serializer string targets such as `'App\Model[]'` navigate to the class.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_serializer::TargetKind;

use crate::common::{php_symbol_location, request_offset, request_uri};

pub struct SerializerDefinitionProvider {
    index: Option<Arc<swlsp_serializer::Index>>,
    php: Option<PhpIndex>,
}

impl SerializerDefinitionProvider {
    pub fn new(index: Option<Arc<swlsp_serializer::Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for SerializerDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(_), Some(php)) = (self.index.as_ref(), self.php.as_ref()) else {
            return Vec::new();
        };
        if request.node.is_none() || !request_uri(request).to_lowercase().ends_with(".php") {
            return Vec::new();
        }
        let (Some(root), Some(offset)) = (request.root.as_ref(), request_offset(request)) else {
            return Vec::new();
        };
        let Some(usage) = swlsp_serializer::usage_at(root, offset) else {
            return Vec::new();
        };
        if usage.kind != TargetKind::String {
            return Vec::new();
        }
        php.find_class(&usage.class)
            .map(|symbol| vec![php_symbol_location(&symbol)])
            .unwrap_or_default()
    }
}

#[cfg(test)]
#[path = "serializer_tests.rs"]
mod tests;
