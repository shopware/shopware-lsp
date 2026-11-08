use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;

use crate::common::php_symbol_location;
use crate::php_common::php_context;

/// Go reads the PHP context from the request; Rust rebuilds it, so the
/// provider needs the PHP index (Go `NewValidationDefinitionProvider()`).
pub struct ValidationDefinitionProvider {
    php: Option<PhpIndex>,
}

impl ValidationDefinitionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl GotoDefinitionProvider for ValidationDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) = swlsp_validation::option_reference_at(context.as_ref(), root, node)
        else {
            return Vec::new();
        };
        let properties = swlsp_validation::constraint_properties_in_context(
            context.as_ref(),
            &reference.constraint,
        );
        swlsp_validation::find_constraint_property(&properties, &reference.name)
            .map(|property| vec![php_symbol_location(property)])
            .unwrap_or_default()
    }
}
