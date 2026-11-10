//! Scaffolding commands.
//!
//! Symfony scaffolds return generated content for the client to create;
//! Shopware scaffolds and the entity designer return validated
//! [`swlsp_rewrite::WorkspacePlan`] edits. Nothing here writes files.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::Value;
use swlsp_console::Index as ConsoleIndex;
use swlsp_lsp::{BoxError, CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_shopware_dal::Index as DalIndex;
use swlsp_shopware_entityschema::IndexedCatalog;

mod entity_schema;
mod paths;
mod shopware;
mod symfony;

pub use entity_schema::*;
pub use shopware::{ShopwareRequest, ShopwareResponse};
pub use symfony::{Request, Response};

#[cfg(test)]
mod tests;

pub const CREATE_SYMFONY_SCAFFOLD_COMMAND: &str = "shopware/symfony/scaffold/create";
pub const CREATE_SHOPWARE_SCAFFOLD_COMMAND: &str = "shopware/scaffold/create";

pub(crate) type Result<T, E = BoxError> = std::result::Result<T, E>;

type Handler = fn(&Provider, &Context, Option<&Value>) -> Result<Value>;

/// Builds a message-only error from format arguments.
macro_rules! errorf {
    ($($arg:tt)*) => { $crate::scaffold::BoxError::from(format!($($arg)*)) };
}
pub(crate) use errorf;

/// Scaffold and entity-designer command provider.
#[derive(Clone)]
pub struct Provider {
    pub(crate) root: String,
    pub(crate) php: Option<PhpIndex>,
    pub(crate) entities: Option<Arc<IndexedCatalog>>,
    pub(crate) console: Option<Arc<ConsoleIndex>>,
    pub(crate) dal: Option<Arc<DalIndex>>,
}

impl Provider {
    pub fn new(
        root: &str,
        php: Option<PhpIndex>,
        console: Option<Arc<ConsoleIndex>>,
        dal: Option<Arc<DalIndex>>,
    ) -> Self {
        let entities = php
            .as_ref()
            .map(|php| Arc::new(IndexedCatalog::new(php.clone(), None, None)));
        Provider {
            root: swlsp_uriutil::filepath::clean(root),
            php,
            entities,
            console,
            dal,
        }
    }

    /// Installs the production catalog, including workspace index
    /// readiness. Tests which exercise only the importer can keep the
    /// constructor's lightweight PHP-index catalog.
    pub fn set_entity_catalog(&mut self, catalog: Arc<IndexedCatalog>) {
        self.entities = Some(catalog);
    }

    pub fn set_console(&mut self, console: Option<Arc<ConsoleIndex>>) {
        self.console = console;
    }

    fn command(&self, handler: Handler) -> CommandFunc {
        let provider = self.clone();
        Arc::new(move |ctx, args| handler(&provider, ctx, args))
    }
}

impl CommandProvider for Provider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let entries: [(&str, Handler); 8] = [
            (CREATE_SYMFONY_SCAFFOLD_COMMAND, |p, c, a| {
                to_value(p.create(c, a)?)
            }),
            (CREATE_SHOPWARE_SCAFFOLD_COMMAND, |p, c, a| {
                to_value(p.create_shopware(c, a)?)
            }),
            (ENTITY_SCHEMA_BOOTSTRAP_COMMAND, |p, c, a| {
                to_value(p.entity_schema_bootstrap(c, a)?)
            }),
            (ENTITY_SCHEMA_SEARCH_COMMAND, |p, c, a| {
                to_value(p.entity_schema_search(c, a)?)
            }),
            (ENTITY_SCHEMA_LOAD_COMMAND, |p, c, a| {
                to_value(p.entity_schema_load(c, a)?)
            }),
            (ENTITY_SCHEMA_PREVIEW_COMMAND, |p, c, a| {
                to_value(p.entity_schema_preview(c, a)?)
            }),
            (ENTITY_SCHEMA_APPLY_COMMAND, |p, c, a| {
                to_value(p.entity_schema_apply(c, a)?)
            }),
            (ENTITY_SCHEMA_RECONCILE_COMMAND, |p, c, a| {
                to_value(p.entity_schema_reconcile(c, a)?)
            }),
        ];
        entries
            .into_iter()
            .map(|(name, handler)| (name.to_owned(), self.command(handler)))
            .collect()
    }
}

fn to_value<T: Serialize>(value: T) -> Result<Value> {
    Ok(serde_json::to_value(value)?)
}

/// Decodes command arguments: `null` members keep their default value and a
/// `null` payload decodes to the default struct.
pub(crate) fn decode_args<T: DeserializeOwned + Default>(
    args: Option<&Value>,
    missing: &str,
    invalid: &str,
) -> Result<T> {
    let Some(args) = args else {
        return Err(errorf!("{missing}"));
    };
    let mut value = args.clone();
    strip_nulls(&mut value);
    if value.is_null() {
        return Ok(T::default());
    }
    serde_json::from_value(value).map_err(|error| errorf!("{invalid}: {error}"))
}

fn strip_nulls(value: &mut Value) {
    match value {
        Value::Object(map) => {
            map.retain(|_, member| !member.is_null());
            map.values_mut().for_each(strip_nulls);
        }
        Value::Array(items) => items.iter_mut().for_each(strip_nulls),
        _ => {}
    }
}

/// Serializes a JSON value with object keys sorted.
pub(crate) struct Sorted<'a>(pub &'a Value);

impl Serialize for Sorted<'_> {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        use serde::ser::{SerializeMap, SerializeSeq};
        match self.0 {
            Value::Object(map) => {
                let mut entries: Vec<_> = map.iter().collect();
                entries.sort_by(|left, right| left.0.cmp(right.0));
                let mut out = serializer.serialize_map(Some(entries.len()))?;
                for (key, value) in entries {
                    out.serialize_entry(key, &Sorted(value))?;
                }
                out.end()
            }
            Value::Array(items) => {
                let mut out = serializer.serialize_seq(Some(items.len()))?;
                for item in items {
                    out.serialize_element(&Sorted(item))?;
                }
                out.end()
            }
            other => other.serialize(serializer),
        }
    }
}

pub(crate) fn pascal_name(name: &str) -> String {
    let mut result = String::with_capacity(name.len());
    let mut upper = true;
    for current in name.chars() {
        if current == '-' || current == '_' {
            upper = true;
            continue;
        }
        if upper {
            result.push(swlsp_textutil::uppercase_char(current));
            upper = false;
        } else {
            result.push(current);
        }
    }
    result
}

pub(crate) fn snake_case(value: &str) -> String {
    let characters: Vec<char> = value.chars().collect();
    let mut result = String::with_capacity(value.len() + 4);
    for (index, &current) in characters.iter().enumerate() {
        if current.is_uppercase()
            && index > 0
            && (characters[index - 1].is_lowercase()
                || characters[index - 1].is_numeric()
                || index + 1 < characters.len() && characters[index + 1].is_lowercase())
        {
            result.push('_');
        }
        result.push(swlsp_textutil::lowercase_char(current));
    }
    result.trim_matches('_').to_owned()
}
