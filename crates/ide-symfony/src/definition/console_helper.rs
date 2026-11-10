//! `HelperSet::get('name')` and `Command::getHelper('name')` navigate to the
//! helper class.

use std::collections::HashSet;

use swlsp_console::HelperCatalog;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::definition::common::file_range_location;
use crate::definition::php_common::php_context;

pub struct ConsoleHelperDefinitionProvider {
    php: Option<PhpIndex>,
    catalog: Option<HelperCatalog>,
}

impl ConsoleHelperDefinitionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        let catalog = php.clone().map(HelperCatalog::new);
        Self { php, catalog }
    }
}

impl GotoDefinitionProvider for ConsoleHelperDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(php), Some(catalog)) = (self.php.as_ref(), self.catalog.as_ref()) else {
            return Vec::new();
        };
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let Some(reference) = swlsp_console::helper_reference_at(node) else {
            return Vec::new();
        };
        if reference.name.is_empty() {
            return Vec::new();
        }
        let context = php_context(Some(php), request);
        if !swlsp_console::validate_helper_reference(php, context.as_ref(), &reference) {
            return Vec::new();
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for helper in catalog.helpers() {
            if helper.name != reference.name {
                continue;
            }
            if !seen.insert((helper.file.clone(), helper.range)) {
                continue;
            }
            if let Some(location) = file_range_location(&helper.file, helper.range) {
                result.push(location);
            }
        }
        result
    }
}

#[cfg(test)]
#[path = "console_helper_tests.rs"]
mod tests;
