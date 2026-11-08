//! Persisted and shared Symfony records.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

/// Tag name → value. Go uses a map; Rust keeps it ordered for deterministic
/// iteration.
pub type TagMap = BTreeMap<String, String>;

/// A Symfony service definition (Go `Service`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Service {
    pub id: String,
    pub class: String,
    pub alias_target: String,
    pub decorates: String,
    pub parent: String,
    pub autowire: bool,
    pub autowire_set: bool,
    pub deprecated: bool,
    pub deprecation: String,
    pub tags: TagMap,
    pub instanceof_tags: TagMap,
    pub path: String,
    pub line: u32,
    pub range: TextRange,
    pub id_range: TextRange,
    pub class_range: TextRange,
    pub decorates_range: TextRange,
    pub parent_range: TextRange,
    pub deprecated_range: TextRange,
}

/// A Symfony container parameter.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Parameter {
    pub name: String,
    pub value: String,
    pub path: String,
    pub line: u32,
}

/// A PSR-4 service registration declared with `load()` / `<prototype>` /
/// `resource:`. Resource and exclude paths are normalized relative to the
/// config file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ServicePrototype {
    pub namespace: String,
    pub resource: String,
    pub excludes: Vec<String>,
    pub autowire: bool,
    pub autowire_set: bool,
    pub tags: TagMap,
    pub instanceof_tags: TagMap,
    pub path: String,
    pub line: u32,
    pub range: TextRange,
    pub namespace_range: TextRange,
    pub resource_range: TextRange,
}

/// A source location of a service definition.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Location {
    pub path: String,
    pub line: u32,
}

/// A global registered directly on the compiled Twig environment service.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContainerTwigGlobal {
    pub name: String,
    pub value: String,
    pub service_id: String,
    pub path: String,
    pub range: TextRange,
}

/// The final runtime metadata emitted for a Symfony UX Twig Component by the
/// compiled dependency-injection container.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ContainerTwigComponent {
    pub name: String,
    pub class: String,
    pub template: String,
    pub template_from_method: String,
    pub path: String,
    pub name_range: TextRange,
    pub class_range: TextRange,
    pub template_range: TextRange,
}

/// A static service reference supplied to a configured service constructor
/// or method call.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct ServiceArgumentReference {
    pub owner_service_id: String,
    pub owner_class: String,
    pub service_id: String,
    pub method_name: String,
    pub parameter_name: String,
    pub parameter_index: i32,
    pub range: TextRange,
    pub format: String,
    pub replacement: String,
}

/// A statically configured call, tag callback, or factory method on a
/// service definition.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct ServiceMethodReference {
    pub owner_service_id: String,
    pub owner_class: String,
    pub receiver_service: String,
    pub receiver_class: String,
    pub method_name: String,
    pub range: TextRange,
    pub format: String,
    pub factory: bool,
}

impl ServiceMethodReference {
    /// The service/class on which the configured method is invoked.
    /// Ordinary post-construction calls use the owning service; factories
    /// carry their explicit receiver.
    pub fn receiver(&self) -> (&str, &str) {
        if !self.receiver_service.is_empty() || !self.receiver_class.is_empty() {
            return (&self.receiver_service, &self.receiver_class);
        }
        (&self.owner_service_id, &self.owner_class)
    }
}

/// The statically knowable result of one PHP service configuration file
/// (Go `phpServiceConfig`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PhpServiceConfig {
    pub services: Vec<Service>,
    pub parameters: Vec<Parameter>,
    pub prototypes: Vec<ServicePrototype>,
    pub references: Vec<ServiceArgumentReference>,
    pub method_references: Vec<ServiceMethodReference>,
}

/// A Symfony route from YAML, XML, PHP, or the compiled generator.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Route {
    pub name: String,
    /// A `Fully\Qualified\Class::CONSTANT` reference when the route name is
    /// a class constant rather than a literal; resolved at query time.
    pub name_constant: String,
    pub path: String,
    pub controller: String,
    pub methods: Vec<String>,
    pub file_path: String,
    pub line: u32,
}

impl Route {
    /// Path placeholders in source order. Symfony's inline requirement and
    /// default forms such as `{!id<\d+>?1}` are normalized to `id`.
    pub fn parameters(&self) -> Vec<String> {
        let path = self.path.as_bytes();
        let mut parameters: Vec<String> = Vec::new();
        let mut offset = 0;
        while offset < path.len() {
            let Some(start) = path[offset..].iter().position(|&b| b == b'{') else {
                break;
            };
            let mut start = start + offset + 1;
            if start < path.len() && path[start] == b'!' {
                start += 1;
            }
            let mut end = start;
            while end < path.len() && is_route_parameter_character(path[end], end == start) {
                end += 1;
            }
            if end > start {
                let name = &self.path[start..end];
                if !parameters.iter().any(|existing| existing == name) {
                    parameters.push(name.to_owned());
                }
            }
            let Some(close) = path[end..].iter().position(|&b| b == b'}') else {
                break;
            };
            offset = end + close + 1;
        }
        parameters
    }
}

pub(crate) fn is_route_parameter_character(value: u8, first: bool) -> bool {
    if value == b'_' || value.is_ascii_lowercase() || value.is_ascii_uppercase() {
        return true;
    }
    !first && value.is_ascii_digit()
}
