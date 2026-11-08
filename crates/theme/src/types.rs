use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

/// One field of a theme's `config.fields`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ThemeConfigField {
    pub key: String,
    /// Labels by locale.
    pub label: BTreeMap<String, String>,
    #[serde(rename = "type")]
    pub kind: String,
    pub value: String,
    pub editable: bool,
    pub block: String,
    pub order: i64,
    /// The `theme.json` file.
    pub path: String,
    /// One-based line of the field key.
    pub line: i64,
    pub scss: bool,
}
