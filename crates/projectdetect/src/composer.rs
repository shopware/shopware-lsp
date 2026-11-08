//! Decoding of `composer.json` and `composer.lock` with the acceptance rules
//! of Go's `encoding/json` for the fields the detector reads: keys match
//! struct fields case-insensitively, `null` leaves a field unset, unknown
//! fields are ignored, and a value of the wrong JSON type is an error.

use serde_json::{Map, Value};

#[derive(Debug, Default)]
pub(crate) struct ComposerManifest {
    pub name: String,
    pub kind: String,
    pub require: Vec<String>,
    pub require_dev: Vec<String>,
    pub shopware_plugin_class: String,
}

#[derive(Debug, Default)]
pub(crate) struct ComposerLock {
    pub packages: Vec<String>,
    pub packages_dev: Vec<String>,
}

fn parse_value(content: &[u8]) -> Result<Value, String> {
    let source = String::from_utf8_lossy(content);
    serde_json::from_str(&source).map_err(|error| error.to_string())
}

fn type_error(value: &Value, target: &str) -> String {
    let kind = match value {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    };
    format!("json: cannot unmarshal {kind} into Go value of type {target}")
}

/// Go struct field lookup: an exact key wins, otherwise a case-insensitive
/// match is used.
fn field<'a>(object: &'a Map<String, Value>, name: &str) -> Option<&'a Value> {
    object.get(name).or_else(|| {
        object
            .iter()
            .filter(|(key, _)| key.eq_ignore_ascii_case(name))
            .map(|(_, value)| value)
            .next_back()
    })
}

fn object<'a>(value: &'a Value, target: &str) -> Result<Option<&'a Map<String, Value>>, String> {
    match value {
        Value::Null => Ok(None),
        Value::Object(object) => Ok(Some(object)),
        other => Err(type_error(other, target)),
    }
}

fn string_field(object: &Map<String, Value>, name: &str) -> Result<String, String> {
    match field(object, name) {
        None | Some(Value::Null) => Ok(String::new()),
        Some(Value::String(value)) => Ok(value.clone()),
        Some(other) => Err(type_error(other, "string")),
    }
}

fn map_keys(object: &Map<String, Value>, name: &str) -> Result<Vec<String>, String> {
    match field(object, name) {
        None => Ok(Vec::new()),
        Some(value) => Ok(self::object(value, "map[string]json.RawMessage")?
            .map(|map| map.keys().cloned().collect())
            .unwrap_or_default()),
    }
}

pub(crate) fn parse_manifest(content: &[u8]) -> Result<ComposerManifest, String> {
    let value = parse_value(content)?;
    let Some(root) = object(&value, "projectdetect.composerManifest")? else {
        return Ok(ComposerManifest::default());
    };
    let mut result = ComposerManifest {
        name: string_field(root, "name")?,
        kind: string_field(root, "type")?,
        require: map_keys(root, "require")?,
        require_dev: map_keys(root, "require-dev")?,
        ..ComposerManifest::default()
    };
    if let Some(extra) = field(root, "extra")
        && let Some(extra) = object(extra, "struct { ShopwarePluginClass string }")?
    {
        result.shopware_plugin_class = string_field(extra, "shopware-plugin-class")?;
    }
    Ok(result)
}

fn package_names(object: &Map<String, Value>, name: &str) -> Result<Vec<String>, String> {
    let Some(value) = field(object, name) else {
        return Ok(Vec::new());
    };
    let items = match value {
        Value::Null => return Ok(Vec::new()),
        Value::Array(items) => items,
        other => return Err(type_error(other, "[]projectdetect.composerPackage")),
    };
    let mut result = Vec::with_capacity(items.len());
    for item in items {
        let name = match object_or_null(item)? {
            Some(package) => string_field(package, "name")?,
            None => String::new(),
        };
        result.push(name);
    }
    Ok(result)
}

fn object_or_null(value: &Value) -> Result<Option<&Map<String, Value>>, String> {
    object(value, "projectdetect.composerPackage")
}

pub(crate) fn parse_lock(content: &[u8]) -> Result<ComposerLock, String> {
    let value = parse_value(content)?;
    let Some(root) = object(&value, "projectdetect.composerLock")? else {
        return Ok(ComposerLock::default());
    };
    Ok(ComposerLock {
        packages: package_names(root, "packages")?,
        packages_dev: package_names(root, "packages-dev")?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn manifest_follows_go_decoding_rules() {
        let manifest = parse_manifest(
            br#"{"Name":"Shopware/Core","type":null,"require":{"a/b":"*"},"EXTRA":{"shopware-plugin-class":"X"},"unknown":[1]}"#,
        )
        .unwrap();
        assert_eq!(manifest.name, "Shopware/Core");
        assert_eq!(manifest.kind, "");
        assert_eq!(manifest.require, ["a/b"]);
        assert_eq!(manifest.shopware_plugin_class, "X");
        assert!(parse_manifest(b"null").is_ok());
        assert!(parse_manifest(br#"{"require":[]}"#).is_err());
        assert!(parse_manifest(br#"{"name":1}"#).is_err());
        assert!(parse_manifest(b"[]").is_err());
        assert!(parse_manifest(b"{} {}").is_err());
    }

    #[test]
    fn lock_follows_go_decoding_rules() {
        let lock =
            parse_lock(br#"{"packages":[{"name":"a/b"},null],"packages-dev":null}"#).unwrap();
        assert_eq!(lock.packages, ["a/b", ""]);
        assert!(lock.packages_dev.is_empty());
        assert!(parse_lock(br#"{"packages":{}}"#).is_err());
        assert!(parse_lock(br#"{"packages":[1]}"#).is_err());
    }
}
