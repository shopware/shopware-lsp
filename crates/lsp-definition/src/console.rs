//! Console command names and input names.

use std::sync::Arc;

use swlsp_console::{Index, InputKind, ReferenceRole};
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::common::{ext_lower, file_range_location, request_uri};
use crate::php_common::php_context;

pub struct ConsoleDefinitionProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl ConsoleDefinitionProvider {
    /// `php` supplies the request's PHP context.
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for ConsoleDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(index) = self.index.as_deref() else {
            return Vec::new();
        };
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if ext_lower(request_uri(request)) != ".php" {
            return Vec::new();
        }
        let Some(reference) = swlsp_console::reference_at(node) else {
            return Vec::new();
        };
        let context = php_context(self.php.as_ref(), request);
        if !swlsp_console::validate_reference(context.as_ref(), &reference) {
            return Vec::new();
        }
        if reference.role == ReferenceRole::Command {
            let Ok(commands) = index.get_command(&reference.name) else {
                return Vec::new();
            };
            return commands
                .iter()
                .filter_map(|command| file_range_location(&command.file, command.range))
                .collect();
        }
        let Ok(inputs) = swlsp_console::inputs_for_reference(context.as_ref(), index, &reference)
        else {
            return Vec::new();
        };
        inputs
            .iter()
            .filter(|input| {
                input.name == reference.name
                    || (input.kind == InputKind::Option && input.shortcut == reference.name)
            })
            .filter_map(|input| file_range_location(&input.file, input.range))
            .collect()
    }
}

#[cfg(test)]
#[path = "console_tests.rs"]
mod tests;
