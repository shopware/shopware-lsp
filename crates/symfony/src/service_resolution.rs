//! Resolution of service IDs to configured PHP classes.

use std::collections::{HashMap, HashSet};

use swlsp_indexer::Result;

use crate::model::Service;
use crate::service_index::ServiceIndex;
use crate::util::go_trim_space;
use crate::yaml_named_argument::static_yaml_service_class_name;

/// Resolves a service receiver from the current unsaved configuration first,
/// then the workspace index. An explicit class wins over the service ID and
/// aliases are followed with cycle protection.
pub fn resolve_configured_service_class(
    service_id: &str,
    explicit_class: &str,
    local: &HashMap<String, Service>,
    index: Option<&ServiceIndex>,
) -> Result<Option<String>> {
    if let Some(class_name) = static_configured_service_class(explicit_class) {
        return Ok(Some(class_name));
    }
    let mut service_id = service_id.to_owned();
    let mut visited: HashSet<String> = HashSet::new();
    while !service_id.is_empty() {
        if !visited.insert(service_id.clone()) {
            return Ok(None);
        }
        let Some(service) = local.get(&service_id) else {
            break;
        };
        if !service.alias_target.is_empty() {
            service_id = service.alias_target.clone();
            continue;
        }
        return Ok(static_configured_service_class(&service.class));
    }
    match index {
        Some(index) => index.resolve_service_class_name(&service_id),
        None => Ok(None),
    }
}

fn static_configured_service_class(value: &str) -> Option<String> {
    let value = go_trim_space(value.strip_prefix('\\').unwrap_or(value));
    (!value.is_empty() && !value.contains(['%', '$', '{', '}', '@', ' ', '\t']))
        .then(|| value.to_owned())
}

impl ServiceIndex {
    /// Resolves a service ID to its concrete PHP class, following aliases
    /// and rejecting cyclic or dynamic definitions.
    pub fn resolve_service_class_name(&self, service_id: &str) -> Result<Option<String>> {
        let mut service_id = service_id.to_owned();
        let mut visited: HashSet<String> = HashSet::new();
        while !service_id.is_empty() {
            if !visited.insert(service_id.clone()) {
                return Ok(None);
            }
            let Some(service) = self.get_service_by_id(&service_id)? else {
                return Ok(None);
            };
            if !service.alias_target.is_empty() {
                service_id = service.alias_target;
                continue;
            }
            let class_name =
                go_trim_space(service.class.strip_prefix('\\').unwrap_or(&service.class));
            if static_yaml_service_class_name(class_name) {
                return Ok(Some(class_name.to_owned()));
            }
            return Ok(None);
        }
        Ok(None)
    }
}

#[cfg(test)]
#[path = "tests_refs/service_resolution.rs"]
mod tests;
