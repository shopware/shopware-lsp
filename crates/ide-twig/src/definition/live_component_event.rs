//! Live component events and their `LiveArg` arguments to the listeners.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_textutil::eq_ignore_case;
use swlsp_twigcomponent::{Index, LiveEventArgumentReference};

use crate::definition::common::{ext_lower, request_offset, request_uri};
use crate::definition::twig_common::{
    component_file_location, unique_component_locations, uri_path,
};

pub struct LiveComponentEventDefinitionProvider {
    index: Option<Arc<Index>>,
}

impl LiveComponentEventDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl GotoDefinitionProvider for LiveComponentEventDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(root), Some(_)) = (
            self.index.as_deref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let path = uri_path(request_uri(request));
        match ext_lower(&path).as_str() {
            ".php" => {
                if let Some(argument) =
                    swlsp_twigcomponent::live_event_argument_reference_at_php(&path, root, offset)
                {
                    return argument_definitions(index, &argument);
                }
                if let Some(event) =
                    swlsp_twigcomponent::live_event_reference_at_php(&path, root, offset)
                {
                    return listener_definitions(index, &event.name);
                }
            }
            ".twig" => {
                if let Some(argument) =
                    swlsp_twigcomponent::live_event_argument_reference_at_twig(&path, root, offset)
                {
                    return argument_definitions(index, &argument);
                }
                if let Some(event) =
                    swlsp_twigcomponent::live_event_reference_at_twig(&path, root, offset)
                {
                    return listener_definitions(index, &event.name);
                }
            }
            _ => {}
        }
        Vec::new()
    }
}

fn listener_definitions(index: &Index, event: &str) -> Vec<protocol::Location> {
    let Ok(listeners) = index.live_listeners() else {
        return Vec::new();
    };
    let result = listeners
        .iter()
        .filter(|listener| eq_ignore_case(&listener.name, event))
        .filter_map(|listener| component_file_location(&listener.file, listener.range))
        .collect();
    unique_component_locations(result)
}

fn argument_definitions(
    index: &Index,
    reference: &LiveEventArgumentReference,
) -> Vec<protocol::Location> {
    let Ok(listeners) = index.live_listeners() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for listener in &listeners {
        if !eq_ignore_case(&listener.name, &reference.event) {
            continue;
        }
        for parameter in &listener.parameters {
            if !parameter.live_arg || !eq_ignore_case(&parameter.name, &reference.name) {
                continue;
            }
            if let Some(location) = component_file_location(&listener.file, parameter.range) {
                result.push(location);
            }
        }
    }
    unique_component_locations(result)
}

#[cfg(test)]
#[path = "live_component_event_tests.rs"]
mod tests;
