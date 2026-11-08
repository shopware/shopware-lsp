//! Go `messenger_definition.go`: Messenger message classes and handler
//! methods referenced from PHP and service configuration.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_messenger::ReferenceRole;
use swlsp_php::PhpIndex;

use crate::common::{php_symbol_location, request_uri};
use crate::php_common::php_context;

pub struct MessengerDefinitionProvider {
    php: Option<PhpIndex>,
    /// Accepted for parity with Go's optional index argument; definitions
    /// are resolved from the PHP index alone.
    #[allow(dead_code)]
    index: Option<Arc<swlsp_messenger::Index>>,
}

impl MessengerDefinitionProvider {
    pub fn new(php: Option<PhpIndex>, index: Option<Arc<swlsp_messenger::Index>>) -> Self {
        Self { php, index }
    }
}

impl GotoDefinitionProvider for MessengerDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let (Some(root), Some(node)) = (request.root.as_ref(), request.node.as_ref()) else {
            return Vec::new();
        };
        let context = php_context(Some(php), request);
        let Some(reference) =
            swlsp_messenger::reference_at(context.as_ref(), request_uri(request), root, node)
        else {
            return Vec::new();
        };
        if reference.name.is_empty() {
            return Vec::new();
        }
        match reference.role {
            ReferenceRole::Message => php
                .find_class(&reference.name)
                .map(|symbol| vec![php_symbol_location(&symbol)])
                .unwrap_or_default(),
            ReferenceRole::HandlerMethod => php
                .find_methods(&reference.class, &reference.name)
                .iter()
                .map(php_symbol_location)
                .collect(),
            ReferenceRole::None => Vec::new(),
        }
    }
}

#[cfg(test)]
#[path = "messenger_tests.rs"]
mod tests;
