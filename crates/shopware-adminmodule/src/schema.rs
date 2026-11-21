//! The JSON schema of the description, and what it offers at a place of a
//! description: the schema editors validate with is the one completion
//! reads, so the two cannot differ.

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

/// A key or a value the schema allows at a place.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Suggestion {
    pub label: String,
    pub description: String,
    /// A key whose value is a mapping.
    pub nested: bool,
}

/// What may stand at a place.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Suggestions {
    pub items: Vec<Suggestion>,
    /// Names of the project that may stand there as well.
    pub names: Option<Names>,
    /// Whether such a name is a key whose value is a mapping.
    pub names_nested: bool,
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

    fn description(&self, node: &Value) -> String {
        node.get("description")
            .or_else(|| self.target(node).get("description"))
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned()
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
            .find(|alternative| tag_of(alternative).is_some_and(|(name, _)| name == chosen))
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

    /// The keys of the mapping at `path`.
    pub fn keys(&self, path: &[String], lookup: Lookup<'_>) -> Suggestions {
        let Some(node) = self.at(path, lookup) else {
            return Suggestions::default();
        };
        let names = names(node);
        let names_nested = node
            .get("additionalProperties")
            .is_some_and(|named| is_mapping(self.target(named)));
        let Some(selected) = self.selected(node, path, lookup) else {
            // Nothing is known about a component before its type.
            let item = Suggestion {
                label: TAG.to_owned(),
                description: self.description(node),
                nested: false,
            };
            return Suggestions {
                items: vec![item],
                names,
                names_nested,
            };
        };
        let items = selected
            .get("properties")
            .and_then(Value::as_object)
            .into_iter()
            .flatten()
            .map(|(name, child)| {
                let target = self.target(child);
                Suggestion {
                    label: name.clone(),
                    description: self.description(child),
                    nested: is_mapping(target),
                }
            })
            .collect();
        Suggestions {
            items,
            names,
            names_nested,
        }
    }

    /// The values of the scalar at `path`.
    pub fn values(&self, path: &[String], lookup: Lookup<'_>) -> Suggestions {
        let Some((key, parent_path)) = path.split_last() else {
            return Suggestions::default();
        };
        let Some(parent) = self.at(parent_path, lookup) else {
            return Suggestions::default();
        };
        if let (TAG, Some(alternatives)) =
            (key.as_str(), parent.get("oneOf").and_then(Value::as_array))
        {
            let items = alternatives
                .iter()
                .filter_map(tag_of)
                .map(|(label, description)| Suggestion {
                    label: label.to_owned(),
                    description: description.to_owned(),
                    nested: false,
                })
                .collect();
            return Suggestions {
                items,
                ..Suggestions::default()
            };
        }
        let Some(node) = self.at(path, lookup) else {
            return Suggestions::default();
        };
        let literal = |value: &Value| Suggestion {
            label: match value {
                Value::String(text) => text.clone(),
                other => other.to_string(),
            },
            ..Suggestion::default()
        };
        let items = if let Some(values) = node.get("enum").and_then(Value::as_array) {
            values.iter().map(literal).collect()
        } else if let Some(value) = node.get("const") {
            vec![literal(value)]
        } else if node.get("type").and_then(Value::as_str) == Some("boolean") {
            vec![literal(&Value::Bool(true)), literal(&Value::Bool(false))]
        } else {
            Vec::new()
        };
        Suggestions {
            items,
            names: names(node).filter(|_| !is_mapping(node)),
            names_nested: false,
        }
    }
}

/// The tag of an alternative and what it says about it.
fn tag_of(alternative: &Value) -> Option<(&str, &str)> {
    let tag = alternative.get("properties")?.get(TAG)?;
    Some((
        tag.get("const")?.as_str()?,
        tag.get("description")
            .and_then(Value::as_str)
            .unwrap_or_default(),
    ))
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
