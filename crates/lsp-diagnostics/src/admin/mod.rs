//! Shopware Administration diagnostics (Go `admin_*.go` of
//! `internal/lsp/diagnostics`).

mod analyzer;
mod block_override;
mod components;
mod i18n_deprecation;
mod privilege;
mod registry;
mod slot_migration;
mod twig;
mod twig_migration;

#[cfg(test)]
mod test_fixture;
#[cfg(test)]
mod tests_analyzer_a;
#[cfg(test)]
mod tests_analyzer_b;
#[cfg(test)]
mod tests_analyzer_c;
#[cfg(test)]
mod tests_misc;

pub use analyzer::AdminAnalyzer;
pub use block_override::{
    ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
    ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
    ADMIN_BLOCK_PARENT_REPEATED_CODE, AdminBlockOverrideAnalyzer,
};
pub use i18n_deprecation::AdminI18nDeprecationAnalyzer;
pub use slot_migration::AdminSlotMigrationAnalyzer;
pub use twig_migration::{AdminTwigMigrationAnalyzer, AdminTwigMigrationPayload};

use serde_json::{Map, Value};

/// Go `filepath.Ext`: the suffix from the final `.` of the last path
/// element, or empty.
pub(crate) fn go_ext(path: &str) -> &str {
    let start = path.rfind('/').map_or(0, |index| index + 1);
    match path[start..].rfind('.') {
        Some(index) => &path[start + index..],
        None => "",
    }
}

/// A JSON string list as Go encodes it: an empty (nil) list is `null`.
pub(crate) fn go_string_list(values: Vec<String>) -> Value {
    if values.is_empty() {
        Value::Null
    } else {
        Value::Array(values.into_iter().map(Value::String).collect())
    }
}

/// A JSON string list from a non-nil Go slice (`[]` when empty).
pub(crate) fn string_array(values: Vec<String>) -> Value {
    Value::Array(values.into_iter().map(Value::String).collect())
}

/// Builds a payload object from `(key, value)` pairs.
pub(crate) fn payload(entries: Vec<(&str, Value)>) -> Value {
    let mut map = Map::new();
    for (key, value) in entries {
        map.insert(key.to_owned(), value);
    }
    Value::Object(map)
}

pub(crate) fn text(value: &str) -> Value {
    Value::String(value.to_owned())
}
