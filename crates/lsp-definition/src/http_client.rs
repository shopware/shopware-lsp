//! HttpClient option keys navigate to `HttpClientInterface::OPTIONS_DEFAULTS`.

use std::collections::HashSet;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::common::file_range_location;
use crate::php_common::php_context;

pub struct HttpClientDefinitionProvider {
    php: Option<PhpIndex>,
}

impl HttpClientDefinitionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl GotoDefinitionProvider for HttpClientDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let Some(reference) = swlsp_httpclient::reference_at(node) else {
            return Vec::new();
        };
        if reference.name.is_empty() {
            return Vec::new();
        }
        let context = php_context(Some(php), request);
        if !swlsp_httpclient::validate(php, context.as_ref(), &reference) {
            return Vec::new();
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for option in swlsp_httpclient::options(php) {
            if option.name != reference.name {
                continue;
            }
            if !seen.insert((option.file.clone(), option.range)) {
                continue;
            }
            if let Some(location) = file_range_location(&option.file, option.range) {
                result.push(location);
            }
        }
        result
    }
}

#[cfg(test)]
#[path = "http_client_tests.rs"]
mod tests;
