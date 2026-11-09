//! Include/embed hash keys to the variables the target template reads.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::TextRange;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use crate::common::{protocol_range, request_offset, request_uri};
use crate::twig_common::{component_file_location, unique_component_locations, uri_path};

pub struct TwigIncludeParameterDefinitionProvider {
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigIncludeParameterDefinitionProvider {
    pub fn new(twig: Option<Arc<TwigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { twig, php }
    }
}

impl GotoDefinitionProvider for TwigIncludeParameterDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(twig), Some(root), Some(lines)) = (
            self.twig.as_deref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        if !uri.to_lowercase().ends_with(".twig") {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(parameter) = swlsp_twig::include_parameter_at(root, request.node.as_ref(), offset)
        else {
            return Vec::new();
        };
        let mut variables = twig
            .find_template_variable(&parameter.name, &[parameter.template.as_str()])
            .unwrap_or_default();
        let current = uri_path(uri);
        if template_matches_twig_path(&parameter.template, &current) {
            variables.retain(|variable| variable.file_path != current);
            variables.extend(
                swlsp_twig::template_input_variables_in_document(&current, root)
                    .into_iter()
                    .filter(|variable| variable.name == parameter.name),
            );
        }
        let mut locations = Vec::new();
        for variable in &variables {
            if variable.file_path == current {
                locations.push(protocol::Location {
                    uri: file_uri(&current),
                    range: protocol_range(lines, variable.range),
                });
                continue;
            }
            if let Some(location) = component_file_location(&variable.file_path, variable.range) {
                locations.push(location);
            }
        }
        if let Some(php) = &self.php {
            for variable in php
                .twig_template_variable_sources(&parameter.name, &[parameter.template.as_str()])
                .unwrap_or_default()
            {
                let range = TextRange::new(variable.range.start.into(), variable.range.end.into());
                if let Some(location) = component_file_location(&variable.file, range) {
                    locations.push(location);
                }
            }
        }
        unique_component_locations(locations)
    }
}

fn template_matches_twig_path(template: &str, path: &str) -> bool {
    swlsp_twig::template_names(path)
        .iter()
        .any(|name| name == template)
}

#[cfg(test)]
#[path = "twig_include_parameter_tests.rs"]
mod tests;
