//! Tool definitions and input validation.
//!
//! `tools.json` is the exact `tools` array Go's MCP SDK advertises (schemas
//! inferred from the Go input/output structs by `jsonschema-go`); it is
//! embedded so `tools/list` is byte-identical. The validator implements the
//! keywords those schemas use (`type`, `properties`, `required`,
//! `additionalProperties`, `items`) with `jsonschema-go`'s error text.

use std::sync::OnceLock;

use serde_json::Value;
use serde_json::value::RawValue;

use crate::gofmt::{format_float_v, go_quote, go_quote_list};

const TOOLS_JSON: &str = include_str!("tools.json");

pub struct ToolDefinition {
    pub name: String,
    pub raw: Box<RawValue>,
    pub input_schema: Value,
}

pub fn tool_definitions() -> &'static [ToolDefinition] {
    static TOOLS: OnceLock<Vec<ToolDefinition>> = OnceLock::new();
    TOOLS.get_or_init(|| {
        let raw: Vec<Box<RawValue>> =
            serde_json::from_str(TOOLS_JSON).expect("embedded MCP tools are valid JSON");
        raw.into_iter()
            .map(|raw| {
                let value: Value = serde_json::from_str(raw.get()).expect("tool is valid JSON");
                ToolDefinition {
                    name: value["name"].as_str().unwrap_or_default().to_owned(),
                    input_schema: value["inputSchema"].clone(),
                    raw,
                }
            })
            .collect()
    })
}

/// Validates `instance` against `schema` (Go `Resolved.Validate`).
pub fn validate(instance: &Value, schema: &Value) -> Result<(), String> {
    validate_at(Some(instance), schema, "root")
}

fn validate_at(instance: Option<&Value>, schema: &Value, path: &str) -> Result<(), String> {
    validate_inner(instance, schema, path).map_err(|error| format!("validating {path}: {error}"))
}

fn json_type(instance: Option<&Value>) -> &'static str {
    match instance {
        None | Some(Value::Null) => "null",
        Some(Value::Bool(_)) => "boolean",
        Some(Value::String(_)) => "string",
        Some(Value::Array(_)) => "array",
        Some(Value::Object(_)) => "object",
        Some(Value::Number(number)) => {
            if number.is_i64() || number.is_u64() {
                "integer"
            } else {
                let value = number.as_f64().unwrap_or(0.0);
                if value.fract() == 0.0 && value.is_finite() {
                    "integer"
                } else {
                    "number"
                }
            }
        }
    }
}

/// Go `fmt` `%v` of a decoded `any` value.
pub fn go_value(instance: Option<&Value>) -> String {
    match instance {
        None | Some(Value::Null) => "<invalid reflect.Value>".into(),
        Some(value) => go_value_nested(value),
    }
}

fn go_value_nested(value: &Value) -> String {
    match value {
        Value::Null => "<nil>".into(),
        Value::Bool(value) => value.to_string(),
        Value::String(value) => value.clone(),
        Value::Number(number) => format_float_v(number.as_f64().unwrap_or(0.0)),
        Value::Array(values) => format!(
            "[{}]",
            values
                .iter()
                .map(go_value_nested)
                .collect::<Vec<_>>()
                .join(" ")
        ),
        Value::Object(map) => format!(
            "map[{}]",
            map.iter()
                .map(|(key, value)| format!("{key}:{}", go_value_nested(value)))
                .collect::<Vec<_>>()
                .join(" ")
        ),
    }
}

fn validate_inner(instance: Option<&Value>, schema: &Value, path: &str) -> Result<(), String> {
    let got = json_type(instance);
    match &schema["type"] {
        Value::String(want) => {
            if !(got == want || (got == "integer" && want == "number")) {
                return Err(format!(
                    "type: {} has type {}, want {}",
                    go_value(instance),
                    go_quote(got),
                    go_quote(want)
                ));
            }
        }
        Value::Array(types) => {
            let types: Vec<&str> = types.iter().filter_map(Value::as_str).collect();
            if !(types.contains(&got) || (got == "integer" && types.contains(&"number"))) {
                return Err(format!(
                    "type: {} has type {}, want one of {}",
                    go_value(instance),
                    go_quote(got),
                    go_quote(&types.join(", "))
                ));
            }
        }
        _ => {}
    }
    match instance {
        Some(Value::Array(items)) => {
            if let Some(item_schema) = schema.get("items") {
                let item_path = format!("{}/items", schema_path(path));
                for item in items {
                    validate_at(Some(item), item_schema, &item_path)?;
                }
            }
        }
        Some(Value::Object(map)) => {
            let mut evaluated = std::collections::BTreeSet::new();
            if let Some(Value::Object(properties)) = schema.get("properties") {
                for (name, property_schema) in properties {
                    let Some(value) = map.get(name) else { continue };
                    let property_path = format!("{}/properties/{name}", schema_path(path));
                    validate_at(Some(value), property_schema, &property_path)?;
                    evaluated.insert(name.as_str());
                }
            }
            match schema.get("additionalProperties") {
                Some(Value::Bool(false)) => {
                    let disallowed: Vec<&str> = map
                        .keys()
                        .map(String::as_str)
                        .filter(|name| !evaluated.contains(name))
                        .collect();
                    if !disallowed.is_empty() {
                        return Err(format!(
                            "unexpected additional properties {}",
                            go_quote_list(&disallowed)
                        ));
                    }
                }
                Some(additional @ Value::Object(_)) => {
                    let additional_path = format!("{}/additionalProperties", schema_path(path));
                    for (name, value) in map {
                        if !evaluated.contains(name.as_str()) {
                            validate_at(Some(value), additional, &additional_path)?;
                        }
                    }
                }
                _ => {}
            }
            if let Some(Value::Array(required)) = schema.get("required") {
                let missing: Vec<&str> = required
                    .iter()
                    .filter_map(Value::as_str)
                    .filter(|name| !map.contains_key(*name))
                    .collect();
                if !missing.is_empty() {
                    return Err(format!(
                        "required: missing properties: {}",
                        go_quote_list(&missing)
                    ));
                }
            }
        }
        _ => {}
    }
    Ok(())
}

fn schema_path(path: &str) -> &str {
    if path == "root" { "" } else { path }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn hover_schema() -> Value {
        tool_definitions()
            .iter()
            .find(|tool| tool.name == "shopware_hover")
            .unwrap()
            .input_schema
            .clone()
    }

    #[test]
    fn reports_go_validation_errors() {
        let schema = hover_schema();
        assert_eq!(
            validate(&json!({}), &schema).unwrap_err(),
            "validating root: required: missing properties: [\"path\" \"line\"]"
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": "x"}), &schema).unwrap_err(),
            "validating root: validating /properties/line: type: x has type \"string\", want \"integer\""
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": 1.5}), &schema).unwrap_err(),
            "validating root: validating /properties/line: type: 1.5 has type \"number\", want \"integer\""
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": 1, "zzz": 1}), &schema).unwrap_err(),
            "validating root: unexpected additional properties [\"zzz\"]"
        );
        assert_eq!(
            validate(&json!({"path": null, "line": 1}), &schema).unwrap_err(),
            "validating root: validating /properties/path: type: <invalid reflect.Value> has type \"null\", want \"string\""
        );
        assert!(validate(&json!({"path": "a", "line": 1.0}), &schema).is_ok());
    }
}
