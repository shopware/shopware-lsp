//! Event names navigate to the event class and listeners; listener method
//! strings navigate to the method.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_event::{Occurrence, ReferenceRole};
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

use crate::definition::common::{file_range_location, php_symbol_location, request_uri};
use crate::definition::php_common::{location_key, php_context};

pub struct EventDefinitionProvider {
    index: Option<Arc<swlsp_event::Index>>,
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
}

impl EventDefinitionProvider {
    pub fn new(
        index: Option<Arc<swlsp_event::Index>>,
        php: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
    ) -> Self {
        Self {
            index,
            php,
            services,
        }
    }

    fn listener_class(&self, listener: &Occurrence) -> String {
        if !listener.class.is_empty() || listener.service.is_empty() {
            return listener.class.clone();
        }
        let Some(services) = self.services.as_deref() else {
            return listener.class.clone();
        };
        match services.get_service_by_id(&listener.service) {
            Ok(Some(service)) => service.class,
            _ => String::new(),
        }
    }

    fn find_methods(&self, class_name: &str, method: &str) -> Vec<swlsp_php_semantic::Symbol> {
        self.php
            .as_ref()
            .map(|php| php.find_methods(class_name, method))
            .unwrap_or_default()
    }
}

impl GotoDefinitionProvider for EventDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(index) = self.index.as_deref() else {
            return Vec::new();
        };
        let (Some(root), Some(node)) = (request.root.as_ref(), request.node.as_ref()) else {
            return Vec::new();
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) =
            swlsp_event::reference_at(context.as_ref(), request_uri(request), root, node)
        else {
            return Vec::new();
        };
        if reference.role == ReferenceRole::Event {
            let Ok(Some(current)) = index.event(&reference.name) else {
                return Vec::new();
            };
            let mut locations = Vec::new();
            if let Some(php) = self.php.as_ref()
                && !current.event_type.is_empty()
                && let Some(symbol) = php.find_class(&current.event_type)
            {
                locations.push(php_symbol_location(&symbol));
            }
            for listener in current.listeners() {
                let class_name = self.listener_class(&listener);
                let methods = self.find_methods(&class_name, &listener.method);
                if !methods.is_empty() {
                    locations.extend(methods.iter().map(php_symbol_location));
                    continue;
                }
                if let Some(location) = file_range_location(&listener.file, listener.range) {
                    locations.push(location);
                }
            }
            return unique_event_locations(locations);
        }
        if reference.role != ReferenceRole::ListenerMethod || self.php.is_none() {
            return Vec::new();
        }
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let Ok(Some(listener)) = swlsp_event::resolve_listener(Some(index), &path, &reference)
        else {
            return Vec::new();
        };
        let methods = self.find_methods(&self.listener_class(&listener), &reference.name);
        unique_event_locations(methods.iter().map(php_symbol_location).collect())
    }
}

fn unique_event_locations(locations: Vec<protocol::Location>) -> Vec<protocol::Location> {
    let mut seen = HashSet::new();
    locations
        .into_iter()
        .filter(|location| seen.insert(location_key(location)))
        .collect()
}

#[cfg(test)]
#[path = "event_tests.rs"]
mod tests;
