//! Decoding of the small analytics request objects: member names match
//! exactly or ignoring case, `null` members keep their default value,
//! unknown members are ignored, and a member of the wrong JSON type is an
//! error.

use serde_json::Value;

/// A request object that does not have the expected shape.
#[derive(Debug, thiserror::Error)]
pub enum RequestError {
    #[error("expected a JSON object, found {found}")]
    NotAnObject { found: &'static str },
    #[error("member {member:?} must be {expected}, found {found}")]
    MemberType {
        member: &'static str,
        expected: &'static str,
        found: String,
    },
}

#[derive(Clone, Copy)]
pub enum Field {
    String,
    Int,
    OptionalBool,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Decoded {
    String(String),
    Int(i64),
    Bool(bool),
}

pub struct Members {
    values: Vec<(&'static str, Decoded)>,
}

impl Members {
    pub fn string(&self, name: &str) -> String {
        match self.get(name) {
            Some(Decoded::String(value)) => value.clone(),
            _ => String::new(),
        }
    }

    pub fn int(&self, name: &str) -> i64 {
        match self.get(name) {
            Some(Decoded::Int(value)) => *value,
            _ => 0,
        }
    }

    pub fn optional_bool(&self, name: &str) -> Option<bool> {
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

/// Decodes the request object `value` into the declared `fields`. When
/// several members are invalid, the first one in document order is reported.
pub fn decode(value: &Value, fields: &[(&'static str, Field)]) -> Result<Members, RequestError> {
    let Value::Object(object) = value else {
        return Err(RequestError::NotAnObject {
            found: kind_name(value),
        });
    };
    let mut members = Members { values: Vec::new() };
    let mut first_error: Option<RequestError> = None;
    for (key, member) in object {
        let field = fields.iter().find(|(name, _)| *name == key).or_else(|| {
            fields
                .iter()
                .find(|(name, _)| swlsp_textutil::eq_ignore_case(name, key))
        });
        let Some(&(name, kind)) = field else {
            continue;
        };
        if member.is_null() {
            continue;
        }
        let mismatch = |found: String, expected: &'static str| RequestError::MemberType {
            member: name,
            expected,
            found,
        };
        let decoded = match (kind, member) {
            (Field::String, Value::String(text)) => Ok(Decoded::String(text.clone())),
            (Field::String, other) => Err(mismatch(kind_name(other).to_owned(), "a string")),
            (Field::Int, Value::Number(number)) => match number.as_i64() {
                Some(integer) => Ok(Decoded::Int(integer)),
                None => Err(mismatch(format!("number {number}"), "an integer")),
            },
            (Field::Int, other) => Err(mismatch(kind_name(other).to_owned(), "an integer")),
            (Field::OptionalBool, Value::Bool(flag)) => Ok(Decoded::Bool(*flag)),
            (Field::OptionalBool, other) => Err(mismatch(kind_name(other).to_owned(), "a boolean")),
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

/// The command arguments, unless they are absent or JSON `null`.
pub fn present(args: Option<&Value>) -> Option<&Value> {
    args.filter(|value| !value.is_null())
}
