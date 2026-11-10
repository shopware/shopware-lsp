use std::sync::Arc;

use swlsp_feature::FeatureIndexer;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_scss::query as scssquery;
use swlsp_parser_twig::query as twigquery;

use crate::definition::common::{ext_lower, request_uri};
use crate::definition::php_common::unclamped_line_location;
use crate::definition::snippet::twig_string_value;

pub struct FeatureDefinitionProvider {
    index: Option<Arc<FeatureIndexer>>,
}

impl FeatureDefinitionProvider {
    pub fn new(index: Option<Arc<FeatureIndexer>>) -> Self {
        Self { index }
    }

    fn locations(&self, name: &str) -> Vec<protocol::Location> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        index
            .feature_by_name(name)
            .unwrap_or_default()
            .iter()
            .map(|feature| unclamped_line_location(&feature.file, feature.line))
            .collect()
    }
}

impl GotoDefinitionProvider for FeatureDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match ext_lower(request_uri(request)).as_str() {
            ".twig" if twigquery::string_in_function(node, &["feature"]) => {
                self.locations(&twig_string_value(node))
            }
            ".php" if phpquery::string_in_call(node, 0, &["Feature::isActive"]) => {
                self.locations(&phpquery::string_value(node))
            }
            ".scss" => match scssquery::string_argument_in_function(node, &["feature"]) {
                Some(string) => {
                    self.locations(&scssquery::string_value(&string).unwrap_or_default())
                }
                None => Vec::new(),
            },
            ".js" | ".ts"
                if jsquery::string_in_call(
                    node,
                    0,
                    &["Feature.isActive", "Shopware.Feature.isActive"],
                ) =>
            {
                self.locations(&jsquery::string_value(node))
            }
            _ => Vec::new(),
        }
    }
}
