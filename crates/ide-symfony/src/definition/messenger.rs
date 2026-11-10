//! Messenger message classes and handler methods referenced from PHP and
//! service configuration.

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_messenger::ReferenceRole;
use swlsp_php::PhpIndex;

use crate::definition::common::{php_symbol_location, request_uri};
use crate::definition::php_common::php_context;

pub struct MessengerDefinitionProvider {
    php: Option<PhpIndex>,
}

impl MessengerDefinitionProvider {
    /// Definitions are resolved from the PHP index alone.
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
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
