//! Tool definitions and input validation.
//!
//! `tools.json` holds the advertised `tools` array: names, descriptions and
//! input and output schemas. The validator implements the keywords those
//! schemas use (`type`, `properties`, `required`, `additionalProperties`,
//! `items`).

use std::sync::OnceLock;

use rmcp::model::Tool;
use serde_json::Value;

const TOOLS_JSON: &str = include_str!("tools.json");

pub struct ToolDefinition {
    pub name: String,
    pub tool: Tool,
    pub input_schema: Value,
}

pub fn tool_definitions() -> &'static [ToolDefinition] {
    static TOOLS: OnceLock<Vec<ToolDefinition>> = OnceLock::new();
    TOOLS.get_or_init(|| {
        let tools: Vec<Value> =
            serde_json::from_str(TOOLS_JSON).expect("embedded MCP tools are valid JSON");
        tools
            .into_iter()
            .map(|value| ToolDefinition {
                name: value["name"].as_str().unwrap_or_default().to_owned(),
                input_schema: value["inputSchema"].clone(),
                tool: serde_json::from_value(value).expect("embedded MCP tool is valid"),
            })
            .collect()
    })
}

/// Why tool arguments do not match their input schema. `path` is the JSON
/// pointer of the offending value (`/` for the arguments object itself).
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum SchemaError {
    /// `value` is the offending value as compact JSON.
    #[error("{path}: expected {expected}, found {value}")]
    Type {
        path: String,
        expected: String,
        value: String,
    },
    #[error("{path}: unexpected properties: {}", .names.join(", "))]
    UnexpectedProperties { path: String, names: Vec<String> },
    #[error("{path}: missing required properties: {}", .names.join(", "))]
    MissingProperties { path: String, names: Vec<String> },
}

/// Validates `instance` against `schema`.
pub fn validate(instance: &Value, schema: &Value) -> Result<(), SchemaError> {
    validate_at(Some(instance), schema, "")
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

/// The offending value in a type error, as compact JSON.
fn display_value(instance: Option<&Value>) -> String {
    instance.map_or_else(|| "null".into(), Value::to_string)
}

/// The pointer shown for `path`: the arguments object itself is `/`.
fn display_path(path: &str) -> String {
    if path.is_empty() {
        "/".into()
    } else {
        path.to_owned()
    }
}

fn validate_at(instance: Option<&Value>, schema: &Value, path: &str) -> Result<(), SchemaError> {
    let found = json_type(instance);
    let type_error = |expected: String| SchemaError::Type {
        path: display_path(path),
        expected,
        value: display_value(instance),
    };
    match &schema["type"] {
        Value::String(want) => {
            if !(found == want || (found == "integer" && want == "number")) {
                return Err(type_error(want.clone()));
            }
        }
        Value::Array(types) => {
            let types: Vec<&str> = types.iter().filter_map(Value::as_str).collect();
            if !(types.contains(&found) || (found == "integer" && types.contains(&"number"))) {
                return Err(type_error(format!("one of {}", types.join(", "))));
            }
        }
        _ => {}
    }
    match instance {
        Some(Value::Array(items)) => {
            if let Some(item_schema) = schema.get("items") {
                for (index, item) in items.iter().enumerate() {
                    validate_at(Some(item), item_schema, &format!("{path}/{index}"))?;
                }
            }
        }
        Some(Value::Object(map)) => {
            let mut evaluated = std::collections::BTreeSet::new();
            if let Some(Value::Object(properties)) = schema.get("properties") {
                for (name, property_schema) in properties {
                    let Some(value) = map.get(name) else { continue };
                    validate_at(Some(value), property_schema, &format!("{path}/{name}"))?;
                    evaluated.insert(name.as_str());
                }
            }
            match schema.get("additionalProperties") {
                Some(Value::Bool(false)) => {
                    let names: Vec<String> = map
                        .keys()
                        .filter(|name| !evaluated.contains(name.as_str()))
                        .cloned()
                        .collect();
                    if !names.is_empty() {
                        return Err(SchemaError::UnexpectedProperties {
                            path: display_path(path),
                            names,
                        });
                    }
                }
                Some(additional @ Value::Object(_)) => {
                    for (name, value) in map {
                        if !evaluated.contains(name.as_str()) {
                            validate_at(Some(value), additional, &format!("{path}/{name}"))?;
                        }
                    }
                }
                _ => {}
            }
            if let Some(Value::Array(required)) = schema.get("required") {
                let names: Vec<String> = required
                    .iter()
                    .filter_map(Value::as_str)
                    .filter(|name| !map.contains_key(*name))
                    .map(str::to_owned)
                    .collect();
                if !names.is_empty() {
                    return Err(SchemaError::MissingProperties {
                        path: display_path(path),
                        names,
                    });
                }
            }
        }
        _ => {}
    }
    Ok(())
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
    fn reports_validation_errors() {
        let schema = hover_schema();
        assert_eq!(
            validate(&json!({}), &schema).unwrap_err().to_string(),
            "/: missing required properties: path, line"
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": "x"}), &schema)
                .unwrap_err()
                .to_string(),
            "/line: expected integer, found \"x\""
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": 1.5}), &schema)
                .unwrap_err()
                .to_string(),
            "/line: expected integer, found 1.5"
        );
        assert_eq!(
            validate(&json!({"path": "a", "line": 1, "zzz": 1}), &schema)
                .unwrap_err()
                .to_string(),
            "/: unexpected properties: zzz"
        );
        assert_eq!(
            validate(&json!({"path": null, "line": 1}), &schema)
                .unwrap_err()
                .to_string(),
            "/path: expected string, found null"
        );
        assert!(validate(&json!({"path": "a", "line": 1.0}), &schema).is_ok());
    }
}
