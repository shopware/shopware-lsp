//! Symfony UX Twig component usages, props, blocks, computed values and live
//! actions to their declarations.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_textutil::eq_ignore_case;
use swlsp_twigcomponent::{
    Component, ComponentBlockUsage, Index, LiveActionArgumentReference, Prop, PropUsage,
};

use crate::definition::common::{protocol_range, request_offset, request_uri};
use crate::definition::twig_common::{
    component_file_location, unique_component_locations, uri_path, zero_line_location,
};

pub struct TwigComponentDefinitionProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl TwigComponentDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for TwigComponentDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(root), Some(lines)) = (
            self.index.as_deref(),
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
        let path = uri_path(uri);
        if let Some(argument) =
            swlsp_twigcomponent::live_action_argument_reference_at(&path, root, offset)
        {
            return self.live_action_argument_definitions(index, &path, &argument);
        }
        if let Some(action) = swlsp_twigcomponent::live_action_reference_at(&path, root, offset)
            && !action.name.is_empty()
        {
            return self.live_action_definitions(index, &path, &action.name);
        }
        let node = request.node.as_ref();
        if let Some((accessor_root, member, _)) =
            node.and_then(swlsp_twigcomponent::accessor_member_at)
            && accessor_root == "computed"
        {
            let locations = self.computed_definitions(index, &path, &member, uri, lines);
            if !locations.is_empty() {
                return locations;
            }
        }
        if let Some((name, _)) = node.and_then(swlsp_twigcomponent::variable_at) {
            let locations = self.variable_definitions(index, &path, &name, request, lines);
            if !locations.is_empty() {
                return locations;
            }
        }
        if let Some(block) = node.and_then(|node| swlsp_twigcomponent::block_usage_at(node, offset))
        {
            return self.block_definitions(index, &block);
        }
        let Some(usage) = swlsp_twigcomponent::usage_at(&path, root, offset) else {
            let Some(prop) =
                node.and_then(|node| swlsp_twigcomponent::prop_usage_at(root, node, offset))
            else {
                return Vec::new();
            };
            return self.prop_definitions(index, &prop);
        };
        let Ok(components) = index.find(&usage.name) else {
            return Vec::new();
        };
        let mut result = Vec::new();
        for component in &components {
            if let Some(location) = self.class_location(component) {
                result.push(location);
                continue;
            }
            let Ok(templates) = index.template_files(component) else {
                continue;
            };
            for template in &templates {
                if let Some(location) = component_file_location(template, TextRange::default()) {
                    result.push(location);
                }
            }
        }
        unique_component_locations(result)
    }
}

impl TwigComponentDefinitionProvider {
    fn live_action_argument_definitions(
        &self,
        index: &Index,
        path: &str,
        reference: &LiveActionArgumentReference,
    ) -> Vec<protocol::Location> {
        let Ok(actions) = index.live_actions_for_template(path) else {
            return Vec::new();
        };
        let mut result = Vec::new();
        for action in &actions {
            if !eq_ignore_case(&action.name, &reference.action) {
                continue;
            }
            for parameter in &action.parameters {
                if !eq_ignore_case(&parameter.name, &reference.name) {
                    continue;
                }
                if let Some(location) = component_file_location(&action.file, parameter.range) {
                    result.push(location);
                }
            }
        }
        unique_component_locations(result)
    }

    fn live_action_definitions(
        &self,
        index: &Index,
        path: &str,
        name: &str,
    ) -> Vec<protocol::Location> {
        let Ok(actions) = index.live_actions_for_template(path) else {
            return Vec::new();
        };
        let result = actions
            .iter()
            .filter(|action| eq_ignore_case(&action.name, name))
            .filter_map(|action| component_file_location(&action.file, action.range))
            .collect();
        unique_component_locations(result)
    }

    fn block_definitions(
        &self,
        index: &Index,
        usage: &ComponentBlockUsage,
    ) -> Vec<protocol::Location> {
        let Ok(blocks) = index.blocks(&usage.component) else {
            return Vec::new();
        };
        let result = blocks
            .iter()
            .filter(|block| block.name == usage.name && !block.file.is_empty())
            .map(|block| {
                let line = u32::try_from((block.line - 1).max(0)).unwrap_or(u32::MAX);
                zero_line_location(&block.file, line)
            })
            .collect();
        unique_component_locations(result)
    }

    fn variable_definitions(
        &self,
        index: &Index,
        path: &str,
        name: &str,
        request: &DefinitionRequest,
        lines: &LineIndex,
    ) -> Vec<protocol::Location> {
        let Ok((components, props)) = index.context_for_template(path, request.root.as_ref())
        else {
            return Vec::new();
        };
        if components.is_empty() {
            return Vec::new();
        }
        if name == "this" || name == "computed" {
            let result = components
                .iter()
                .filter_map(|component| self.class_location(component))
                .collect();
            return unique_component_locations(result);
        }
        prop_locations(&props, name, path, request_uri(request), lines)
    }

    fn computed_definitions(
        &self,
        index: &Index,
        path: &str,
        name: &str,
        uri: &str,
        lines: &LineIndex,
    ) -> Vec<protocol::Location> {
        let Ok(computed) = index.computed_for_template(path) else {
            return Vec::new();
        };
        prop_locations(&computed, name, path, uri, lines)
    }

    fn prop_definitions(&self, index: &Index, usage: &PropUsage) -> Vec<protocol::Location> {
        let Ok(props) = index.props(&usage.component) else {
            return Vec::new();
        };
        let result = props
            .iter()
            .filter(|prop| eq_ignore_case(&prop.name, &usage.name))
            .filter_map(|prop| component_file_location(&prop.file, prop.range))
            .collect();
        unique_component_locations(result)
    }

    fn class_location(&self, component: &Component) -> Option<protocol::Location> {
        if !component.class.is_empty()
            && let Some(php) = &self.php
            && let Some(class) = php.find_class(&component.class)
        {
            return component_file_location(&class.path, class.selection_range);
        }
        if component.file.is_empty() {
            return None;
        }
        let range = if component.class_range.is_empty() {
            component.name_range
        } else {
            component.class_range
        };
        component_file_location(&component.file, range)
    }
}

/// Props named `name`; props of the current template use the open
/// document's line index.
fn prop_locations(
    props: &[Prop],
    name: &str,
    path: &str,
    uri: &str,
    lines: &LineIndex,
) -> Vec<protocol::Location> {
    let mut result = Vec::new();
    for prop in props {
        if !eq_ignore_case(&prop.name, name) {
            continue;
        }
        if prop.file == path {
            result.push(protocol::Location {
                uri: uri.to_owned(),
                range: protocol_range(lines, prop.range),
            });
            continue;
        }
        if let Some(location) = component_file_location(&prop.file, prop.range) {
            result.push(location);
        }
    }
    unique_component_locations(result)
}

#[cfg(test)]
#[path = "twig_component_tests.rs"]
mod tests;
