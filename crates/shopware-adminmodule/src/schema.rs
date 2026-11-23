//! The JSON schema of the description. Editors validate and complete with
//! it through the YAML language server; what it cannot list, the names of
//! the project, it marks, and the server completes those places.

use std::sync::OnceLock;

use serde_json::Value;

/// The schema as it is published (`schema.json` of this crate).
pub const SCHEMA: &str = include_str!("../schema.json");

/// The keyword that marks keys or values the schema cannot list.
const NAMES_KEYWORD: &str = "x-shopware-names";
/// The key that selects the kind of a component.
const TAG: &str = "type";

/// Names that come from the project.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Names {
    /// The entities of the plugin.
    Entities,
    /// The properties of the entity of the module.
    Fields,
    Locales,
}

/// Names of the project that may stand at a place.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Named {
    pub names: Names,
    /// Whether the name is a key whose value is a mapping.
    pub nested: bool,
}

/// Reads the scalar at a path of mapping keys of the description at hand.
pub type Lookup<'a> = &'a dyn Fn(&[String]) -> Option<String>;

pub struct Schema {
    root: Value,
}

impl Schema {
    /// The schema of this crate.
    pub fn bundled() -> &'static Schema {
        static SCHEMA_TREE: OnceLock<Schema> = OnceLock::new();
        SCHEMA_TREE.get_or_init(|| Schema {
            root: serde_json::from_str(SCHEMA).unwrap_or(Value::Null),
        })
    }

    /// The schema a `$ref` points at, or `node` itself.
    fn target<'a>(&'a self, node: &'a Value) -> &'a Value {
        node.get("$ref")
            .and_then(Value::as_str)
            .and_then(|reference| reference.strip_prefix('#'))
            .and_then(|pointer| self.root.pointer(pointer))
            .unwrap_or(node)
    }

    /// The alternative of a tagged schema the description chose at `path`;
    /// `node` itself when it has no alternatives.
    fn selected<'a>(
        &'a self,
        node: &'a Value,
        path: &[String],
        lookup: Lookup<'_>,
    ) -> Option<&'a Value> {
        let Some(alternatives) = node.get("oneOf").and_then(Value::as_array) else {
            return Some(node);
        };
        let mut tag = path.to_vec();
        tag.push(TAG.to_owned());
        let chosen = lookup(&tag)?;
        alternatives
            .iter()
            .find(|alternative| tag_of(alternative) == Some(chosen.as_str()))
    }

    /// The schema of the value at `path`.
    fn at<'a>(&'a self, path: &[String], lookup: Lookup<'_>) -> Option<&'a Value> {
        let mut node = self.target(&self.root);
        for (depth, key) in path.iter().enumerate() {
            let selected = self.selected(node, &path[..depth], lookup)?;
            let child = selected
                .get("properties")
                .and_then(|properties| properties.get(key))
                .or_else(|| {
                    selected
                        .get("additionalProperties")
                        .filter(|schema| schema.is_object())
                })?;
            node = self.target(child);
        }
        Some(node)
    }

    /// The names that are keys of the mapping at `path`.
    pub fn key_names(&self, path: &[String], lookup: Lookup<'_>) -> Option<Named> {
        let node = self.at(path, lookup)?;
        Some(Named {
            names: names(node)?,
            nested: node
                .get("additionalProperties")
                .is_some_and(|named| is_mapping(self.target(named))),
        })
    }

    /// The names that are values of the scalar at `path`.
    pub fn value_names(&self, path: &[String], lookup: Lookup<'_>) -> Option<Named> {
        let node = self.at(path, lookup)?;
        Some(Named {
            names: names(node).filter(|_| !is_mapping(node))?,
            nested: false,
        })
    }
}

/// The tag of an alternative.
fn tag_of(alternative: &Value) -> Option<&str> {
    alternative
        .get("properties")?
        .get(TAG)?
        .get("const")?
        .as_str()
}

fn is_mapping(node: &Value) -> bool {
    let object = |name: &Value| name.as_str() == Some("object");
    match node.get("type") {
        Some(Value::Array(types)) => types.iter().any(object),
        Some(name) => object(name),
        None => node.get("properties").is_some() || node.get("oneOf").is_some(),
    }
}

fn names(node: &Value) -> Option<Names> {
    match node.get(NAMES_KEYWORD)?.as_str()? {
        "entities" => Some(Names::Entities),
        "fields" => Some(Names::Fields),
        "locales" => Some(Names::Locales),
        _ => None,
    }
}

#[cfg(test)]
#[path = "schema_tests.rs"]
mod tests;
