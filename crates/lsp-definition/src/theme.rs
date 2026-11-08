use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_scss::query as scssquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_theme::ThemeConfigIndexer;

use crate::common::{ext_lower, request_uri};
use crate::php_common::unclamped_line_location;
use crate::snippet::twig_string_value;

pub struct ThemeDefinitionProvider {
    index: Option<Arc<ThemeConfigIndexer>>,
}

impl ThemeDefinitionProvider {
    pub fn new(index: Option<Arc<ThemeConfigIndexer>>) -> Self {
        Self { index }
    }

    fn field_locations(&self, key: &str) -> Vec<protocol::Location> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        index
            .theme_config_field(key)
            .unwrap_or_default()
            .iter()
            .map(|field| {
                unclamped_line_location(
                    &field.path,
                    field.line.clamp(0, i64::from(u32::MAX)) as u32,
                )
            })
            .collect()
    }
}

impl GotoDefinitionProvider for ThemeDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match ext_lower(request_uri(request)).as_str() {
            ".scss" => match scssquery::variable_at(node) {
                Some(variable) => {
                    self.field_locations(&scssquery::variable_name(&variable).unwrap_or_default())
                }
                None => Vec::new(),
            },
            ".twig" if twigquery::string_in_function(node, &["theme_config"]) => {
                self.field_locations(&twig_string_value(node))
            }
            _ => Vec::new(),
        }
    }
}
