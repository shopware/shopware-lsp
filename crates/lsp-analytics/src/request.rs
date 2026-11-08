//! Go `encoding/json` struct decoding for the small analytics request
//! objects: case-insensitive member names, `null` members keep their zero
//! value, unknown members are ignored, and type mismatches report Go's
//! `*json.UnmarshalTypeError` text.

use serde_json::Value;

#[derive(Clone, Copy)]
pub(crate) enum Field {
    String,
    Int,
    OptionalBool,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Decoded {
    String(String),
    Int(i64),
    Bool(bool),
}

pub(crate) struct Members {
    values: Vec<(&'static str, Decoded)>,
}

impl Members {
    pub(crate) fn string(&self, name: &str) -> String {
        match self.get(name) {
            Some(Decoded::String(value)) => value.clone(),
            _ => String::new(),
        }
    }

    pub(crate) fn int(&self, name: &str) -> i64 {
        match self.get(name) {
            Some(Decoded::Int(value)) => *value,
            _ => 0,
        }
    }

    pub(crate) fn optional_bool(&self, name: &str) -> Option<bool> {
        match self.get(name) {
            Some(Decoded::Bool(value)) => Some(*value),
            _ => None,
        }
    }

    fn get(&self, name: &str) -> Option<&Decoded> {
        self.values
            .iter()
            .rev()
            .find(|(field, _)| *field == name)
            .map(|(_, value)| value)
    }
}

fn kind_name(value: &Value) -> &'static str {
    match value {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

/// Decodes `value` into the members of Go struct `analytics.<type_name>`.
pub(crate) fn decode(
    value: &Value,
    type_name: &str,
    fields: &[(&'static str, Field)],
) -> Result<Members, String> {
    let Value::Object(object) = value else {
        return Err(format!(
            "json: cannot unmarshal {} into Go value of type analytics.{type_name}",
            kind_name(value)
        ));
    };
    let mut members = Members { values: Vec::new() };
    let mut first_error: Option<String> = None;
    for (key, member) in object {
        let field = fields.iter().find(|(name, _)| *name == key).or_else(|| {
            fields
                .iter()
                .find(|(name, _)| swlsp_php_types::equal_fold(name, key))
        });
        let Some((name, kind)) = field else {
            continue;
        };
        if member.is_null() {
            continue;
        }
        let mismatch = |value: String, ty: &str| {
            format!(
                "json: cannot unmarshal {value} into Go struct field {type_name}.{name} of type {ty}"
            )
        };
        let decoded = match (kind, member) {
            (Field::String, Value::String(text)) => Ok(Decoded::String(text.clone())),
            (Field::String, other) => Err(mismatch(kind_name(other).to_owned(), "string")),
            (Field::Int, Value::Number(number)) => match number.as_i64() {
                Some(integer) => Ok(Decoded::Int(integer)),
                None => Err(mismatch(format!("number {number}"), "int")),
            },
            (Field::Int, other) => Err(mismatch(kind_name(other).to_owned(), "int")),
            (Field::OptionalBool, Value::Bool(flag)) => Ok(Decoded::Bool(*flag)),
            (Field::OptionalBool, other) => Err(mismatch(kind_name(other).to_owned(), "bool")),
        };
        match decoded {
            Ok(decoded) => members.values.push((name, decoded)),
            Err(error) => {
                first_error.get_or_insert(error);
            }
        }
    }
    match first_error {
        Some(error) => Err(error),
        None => Ok(members),
    }
}

/// Go's `raw != nil && len(*raw) != 0 && string(*raw) != "null"`.
pub(crate) fn present(args: Option<&Value>) -> Option<&Value> {
    args.filter(|value| !value.is_null())
}
