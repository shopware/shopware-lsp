use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_shopware_dal::{Definition, Index, JsFieldReferenceKind};

use crate::dal_criteria::CriteriaEntities;
use crate::definition::common::{ext_lower, line_location, request_offset, request_uri};

pub struct DalDefinitionProvider {
    index: Option<Arc<Index>>,
    admin: Option<Arc<swlsp_admin::AdminComponentIndexer>>,
}

impl DalDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index, admin: None }
    }

    /// Resolves a Criteria inherited from an overridden component through
    /// the Administration component index.
    pub fn with_admin(mut self, admin: Option<Arc<swlsp_admin::AdminComponentIndexer>>) -> Self {
        self.admin = admin;
        self
    }
}

fn definition_locations(definitions: &[Definition]) -> Vec<protocol::Location> {
    definitions
        .iter()
        .map(|definition| line_location(&definition.file, i64::from(definition.line)))
        .collect()
}

/// The field `name` on the entity the path under the cursor has reached,
/// when the file shows which repository the Criteria is searched on.
fn entity_field_location(
    index: &Arc<Index>,
    admin: &Option<Arc<swlsp_admin::AdminComponentIndexer>>,
    node: &swlsp_syntax::SyntaxNode,
    offset: u32,
    name: &str,
) -> Option<protocol::Location> {
    let definitions = index.extended_definitions().ok()?;
    let definition = CriteriaEntities::new(index.clone(), admin.clone()).definition(
        &definitions,
        node,
        offset,
    )?;
    if let Some(field) = definition.fields.iter().find(|field| field.name == name) {
        return Some(line_location(
            definition.field_file(field),
            i64::from(field.line),
        ));
    }
    // `product.name` on a product search: the first segment is the entity.
    (definition.name == name
        && swlsp_shopware_dal::js_field_reference_prefix(node, offset).is_empty())
    .then(|| line_location(&definition.file, i64::from(definition.line)))
}

impl GotoDefinitionProvider for DalDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(node)) = (&self.index, request.node.as_ref()) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        match ext_lower(uri).as_str() {
            ".js" | ".ts" | ".vue" => {
                if uri.to_lowercase().ends_with(".vue")
                    && swlsp_lsp::effective_syntax_language(Some(LanguageId::Vue), Some(node))
                        != Some(LanguageId::JavaScript)
                {
                    return Vec::new();
                }
                if swlsp_shopware_dal::is_js_entity_reference(node) {
                    return index
                        .definition(&jsquery::string_value(node))
                        .map(|definitions| definition_locations(&definitions))
                        .unwrap_or_default();
                }
                let kind = swlsp_shopware_dal::js_field_reference_at(node);
                if kind == JsFieldReferenceKind::None {
                    return Vec::new();
                }
                let offset = request_offset(request).unwrap_or(0);
                let (name, association_segment) =
                    swlsp_shopware_dal::js_field_reference_segment(node, offset);
                if let Some(location) =
                    entity_field_location(index, &self.admin, node, offset, &name)
                {
                    return vec![location];
                }
                let Ok(fields) = index.field_definitions(
                    &name,
                    kind == JsFieldReferenceKind::Association || association_segment,
                ) else {
                    return Vec::new();
                };
                fields
                    .iter()
                    .map(|field| line_location(&field.file, i64::from(field.field.line)))
                    .collect()
            }
            ".twig" => {
                if !uri.replace('\\', "/").contains("/Resources/scripts/")
                    || !swlsp_shopware_dal::is_twig_entity_reference(node)
                {
                    return Vec::new();
                }
                let name = twigquery::string_value(node).unwrap_or_default();
                index
                    .definition(&name)
                    .map(|definitions| definition_locations(&definitions))
                    .unwrap_or_default()
            }
            ".php" => {
                if !swlsp_shopware_dal::is_php_field_reference(node) {
                    return Vec::new();
                }
                let name = phpquery::string_value(node);
                let Ok(definitions) = index.definitions() else {
                    return Vec::new();
                };
                let mut locations = Vec::new();
                for definition in &definitions {
                    for field in &definition.fields {
                        if field.name == name {
                            locations.push(line_location(&definition.file, i64::from(field.line)));
                        }
                    }
                }
                locations
            }
            _ => Vec::new(),
        }
    }
}
