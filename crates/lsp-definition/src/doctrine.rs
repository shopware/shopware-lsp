//! Go `doctrine_definition.go`: Doctrine type registrations and DBAL
//! table/column strings.

use std::sync::Arc;

use swlsp_doctrine::DbalReferenceRole;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::TextSize;

use crate::common::{
    ext_lower, file_range_location, php_symbol_location, request_offset, request_uri,
};
use crate::php_common::php_context;

pub struct DoctrineDefinitionProvider {
    index: Option<Arc<swlsp_doctrine::Index>>,
    php: Option<PhpIndex>,
}

impl DoctrineDefinitionProvider {
    pub fn new(index: Option<Arc<swlsp_doctrine::Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for DoctrineDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(index) = self.index.as_deref() else {
            return Vec::new();
        };
        let (Some(root), Some(_)) = (request.root.as_ref(), request.document.as_ref()) else {
            return Vec::new();
        };
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let offset = request_offset(request).unwrap_or_default();
        if let Some(registration) =
            swlsp_doctrine::type_registration_reference_at(&path, root, TextSize::from(offset))
            && !registration.class.is_empty()
            && let Some(php) = self.php.as_ref()
            && let Some(symbol) = php.find_class(&registration.class)
        {
            return vec![php_symbol_location(&symbol)];
        }
        if ext_lower(&path) != ".php" {
            return Vec::new();
        }
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) = index.dbal_reference_at(context.as_ref(), root, node) else {
            return Vec::new();
        };
        match reference.role {
            DbalReferenceRole::Table => {
                if let Ok(Some(model)) = index.definition_for_table(&reference.name) {
                    return file_range_location(&model.file, model.class_range)
                        .into_iter()
                        .collect();
                }
            }
            DbalReferenceRole::Column => {
                if let Ok(Some((model, field))) =
                    index.field_for_column(&reference.table, &reference.name)
                {
                    return file_range_location(&model.file, field.range)
                        .into_iter()
                        .collect();
                }
            }
            DbalReferenceRole::Alias => {}
        }
        Vec::new()
    }
}

#[cfg(test)]
#[path = "doctrine_tests.rs"]
mod tests;
