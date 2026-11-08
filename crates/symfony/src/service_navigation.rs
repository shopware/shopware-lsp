//! Navigation over service definitions and prototypes.

use swlsp_indexer::Result;
use swlsp_php_semantic::Symbol;
use swlsp_syntax::{LineIndex, Tree};

use crate::model::{Service, ServicePrototype};
use crate::service_index::{ServiceIndex, prototype_matches_class};
use crate::util::{GoLower, extension};

/// The statically knowable navigation model of one open Symfony service
/// configuration document.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ServiceConfiguration {
    pub services: Vec<Service>,
    pub prototypes: Vec<ServicePrototype>,
}

/// Parses current editor contents without publishing them to the workspace
/// index, so code lenses reflect unsaved service relationships.
pub fn service_configuration_in_document(
    path: &str,
    tree: Option<&Tree>,
    line_index: &LineIndex,
) -> ServiceConfiguration {
    let Some(tree) = tree else {
        return ServiceConfiguration::default();
    };
    let (services, prototypes) = match extension(path).to_go_lower().as_str() {
        ".php" => {
            let config = crate::php_service_config(path, &tree.root(), line_index);
            (config.services, config.prototypes)
        }
        ".xml" => {
            let (services, _, prototypes) =
                crate::xml_services::parse_xml_service_config_tree(path, Some(tree), line_index);
            (services, prototypes)
        }
        ".yaml" | ".yml" => {
            let (services, _, prototypes) =
                crate::yaml_services::parse_yaml_service_config_tree(path, Some(tree), line_index);
            (services, prototypes)
        }
        _ => (Vec::new(), Vec::new()),
    };
    ServiceConfiguration {
        services,
        prototypes,
    }
}

impl ServiceIndex {
    /// Source-backed declarations for a service ID. Expanded prototype
    /// services point back to the prototype declaration.
    pub fn service_declarations(&self, id: &str) -> Result<Vec<Service>> {
        if id.is_empty() {
            return Ok(Vec::new());
        }
        let mut result = self.services.get_values(id)?;
        result.extend(
            self.expanded_prototype_services()?
                .iter()
                .filter(|service| swlsp_php_types::equal_fold(&service.id, id))
                .cloned(),
        );
        sort_services_by_source(&mut result);
        Ok(result)
    }

    /// Explicit source definitions. Generated prototype services are
    /// excluded because they cannot declare parent/decorator edges.
    pub fn service_definitions(&self) -> Result<Vec<Service>> {
        let mut result = self.services.get_all_values()?;
        sort_services_by_source(&mut result);
        Ok(result)
    }

    /// Classes a prototype registers in the current PHP workspace snapshot.
    pub fn prototype_classes(&self, prototype: &ServicePrototype) -> Vec<Symbol> {
        let Some(php) = self.php_index() else {
            return Vec::new();
        };
        let mut result: Vec<Symbol> = php
            .class_symbols_view()
            .iter()
            .filter(|class| prototype_matches_class(prototype, class))
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            left.path.as_str().cmp(right.path.as_str()).then(
                left.selection_range
                    .start()
                    .cmp(&right.selection_range.start()),
            )
        });
        result
    }
}

fn sort_services_by_source(services: &mut [Service]) {
    services.sort_by_cached_key(|service| {
        (service.path.clone(), service.line, service.id.to_go_lower())
    });
}

#[cfg(test)]
#[path = "tests_refs/service_navigation.rs"]
mod tests;
