//! The description of generated Administration modules: the file
//! `src/Resources/shopware-lsp/admin-modules.yaml` of a plugin.
//!
//! The format is made to grow without breaking: everything with a name is a
//! key of a mapping, every value is a mapping, an absent key means "as
//! derived from the entity", and unknown keys are errors.

use std::collections::BTreeMap;

use serde::Deserialize;

/// The version of the format this crate reads. The rules that derive a
/// module from an entity belong to the version: version 1 always derives
/// the same module from the same entity.
pub const VERSION: u32 = 1;

/// Where the description lives, relative to the plugin root.
pub const CONFIG_RELATIVE_PATH: &str = "src/Resources/shopware-lsp/admin-modules.yaml";

/// A text per locale (`en-GB: Title`).
pub type Labels = BTreeMap<String, String>;

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    pub version: u32,
    /// `modules:` without a value describes none.
    #[serde(deserialize_with = "nullable")]
    pub modules: BTreeMap<String, Module>,
}

fn nullable<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Default + Deserialize<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields, rename_all = "camelCase")]
pub struct Module {
    /// The technical name of the entity the module manages.
    pub entity: String,
    pub title: Labels,
    pub description: Labels,
    pub icon: Option<String>,
    pub color: Option<String>,
    /// The property that names a record: the link of the list and the
    /// title of the detail page.
    pub label_field: Option<String>,
    pub navigation: Navigation,
    pub acl: Acl,
    /// The locales snippet files are written for; `en-GB` and `de-DE`
    /// when empty.
    pub locales: BTreeMap<String, Locale>,
    pub listing: Listing,
    pub detail: Detail,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Locale {}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Navigation {
    pub enable: Option<bool>,
    /// The id of the menu entry the module is listed below.
    pub parent: Option<String>,
    pub position: Option<i64>,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Acl {
    pub enable: Option<bool>,
    /// The group of the permission grid the roles are listed in.
    pub parent: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Listing {
    /// `false` hands the page over: it is written once more outside
    /// `generated/` and never again.
    pub managed: Option<bool>,
    /// Whether the columns not named here are derived from the entity.
    pub auto: Option<bool>,
    pub limit: Option<u32>,
    pub sorting: Option<Sorting>,
    pub columns: BTreeMap<String, Column>,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Sorting {
    pub field: String,
    pub direction: Option<Direction>,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
pub enum Direction {
    #[default]
    #[serde(rename = "ASC")]
    Ascending,
    #[serde(rename = "DESC")]
    Descending,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields, rename_all = "camelCase")]
pub struct Column {
    pub enable: Option<bool>,
    pub position: Option<i64>,
    pub label: Labels,
    pub sortable: Option<bool>,
    pub align: Option<Align>,
    pub inline_edit: Option<bool>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Align {
    Left,
    Center,
    Right,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Detail {
    pub managed: Option<bool>,
    /// Whether the fields not named in a card are derived from the entity.
    pub auto: Option<bool>,
    pub cards: BTreeMap<String, Card>,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Card {
    pub enable: Option<bool>,
    pub position: Option<i64>,
    pub title: Labels,
    pub fields: BTreeMap<String, Field>,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields, rename_all = "camelCase")]
pub struct Field {
    pub enable: Option<bool>,
    pub position: Option<i64>,
    pub label: Labels,
    pub help_text: Labels,
    pub placeholder: Labels,
    pub required: Option<bool>,
    pub component: Option<Component>,
}

/// Attributes written onto the element as they are.
pub type Props = BTreeMap<String, serde_json::Value>;

/// The input of a field, by its `type`.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(
    tag = "type",
    deny_unknown_fields,
    rename_all = "kebab-case",
    rename_all_fields = "camelCase"
)]
pub enum Component {
    Text {
        #[serde(default)]
        props: Props,
    },
    Textarea {
        #[serde(default)]
        props: Props,
    },
    TextEditor {
        #[serde(default)]
        props: Props,
    },
    Number {
        /// `int` or `float`; derived from the field when absent.
        #[serde(default)]
        number_type: Option<NumberType>,
        #[serde(default)]
        props: Props,
    },
    Switch {
        #[serde(default)]
        props: Props,
    },
    Date {
        #[serde(default)]
        props: Props,
    },
    Datetime {
        #[serde(default)]
        props: Props,
    },
    Select {
        /// The values to choose from; the choices of the field when empty.
        #[serde(default)]
        options: BTreeMap<String, SelectOption>,
        #[serde(default)]
        props: Props,
    },
    EntitySelect {
        #[serde(default)]
        entity: Option<String>,
        #[serde(default)]
        label_property: Option<String>,
        #[serde(default)]
        props: Props,
    },
    /// A component of the plugin: it gets the value, the label and the
    /// error of the field.
    Custom {
        name: String,
        #[serde(default)]
        props: Props,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum NumberType {
    Int,
    Float,
}

#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct SelectOption {
    pub position: Option<i64>,
    pub label: Labels,
}

/// The description could not be read.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum ConfigError {
    #[error("{0}")]
    Decode(String),
    #[error("version {found} is not supported; this server reads version {VERSION}")]
    Version { found: u32 },
}

/// Reads a description. A file without `version` is of the current one.
pub fn parse(source: &str) -> Result<Config, ConfigError> {
    let root = swlsp_parser_yaml::parse(source).root();
    let mut config: Option<Config> =
        swlsp_parser_yaml::query::decode(&root).map_err(|error| ConfigError::Decode(error.0))?;
    let config = config.get_or_insert_default();
    if config.version == 0 {
        config.version = VERSION;
    }
    if config.version != VERSION {
        return Err(ConfigError::Version {
            found: config.version,
        });
    }
    Ok(config.clone())
}
