//! Symfony `XmlUtils::phpize` for XML configuration values.

use crate::util::Lowercase;

/// The PHP value of an XML configuration string.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum PhpValue {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    Str(String),
}

/// Converts the string content of an XML configuration value into its PHP
/// representation, mirroring Symfony's `XmlUtils::phpize`.
pub(crate) fn phpize(value: &str) -> PhpValue {
    let lower = value.lowercase();
    if value.is_empty() {
        return PhpValue::Str(String::new());
    }
    match lower.as_str() {
        "null" => return PhpValue::Null,
        "true" => return PhpValue::Bool(true),
        "false" => return PhpValue::Bool(false),
        _ => {}
    }
    let text = || PhpValue::Str(value.to_owned());
    let digits = value.strip_prefix('-').unwrap_or(value);
    if !digits.is_empty() && digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return phpize_integer(value, digits);
    }
    if let Some(binary) = value.strip_prefix("0b")
        && !binary.is_empty()
        && binary.bytes().all(|byte| byte == b'0' || byte == b'1')
    {
        return i64::from_str_radix(binary, 2).map_or_else(|_| text(), PhpValue::Int);
    }
    if value.len() > 2
        && value.as_bytes()[..2].eq_ignore_ascii_case(b"0x")
        && value[2..].bytes().all(|byte| byte.is_ascii_hexdigit())
    {
        return i64::from_str_radix(&value[2..], 16).map_or_else(|_| text(), PhpValue::Int);
    }
    if is_php_float(value) {
        return crate::services_yaml_convert::parse_finite_float(value)
            .map_or_else(text, PhpValue::Float);
    }
    text()
}

/// Values with a single leading zero are octal; values that do not survive
/// an integer round trip (for example `08`) stay strings.
fn phpize_integer(value: &str, digits: &str) -> PhpValue {
    let text = || PhpValue::Str(value.to_owned());
    if digits.len() > 1 && digits.starts_with('0') {
        return match i64::from_str_radix(value, 8) {
            Ok(octal) if digits == format!("0{:o}", octal.unsigned_abs()) => PhpValue::Int(octal),
            _ => text(),
        };
    }
    match value.parse::<i64>() {
        Ok(parsed) if parsed.to_string() == value => PhpValue::Int(parsed),
        _ => text(),
    }
}

/// `^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`
fn is_php_float(value: &str) -> bool {
    let bytes = value.as_bytes();
    let mut index = usize::from(matches!(bytes.first(), Some(b'+' | b'-')));
    let digits = |index: &mut usize| {
        let start = *index;
        while *index < bytes.len() && bytes[*index].is_ascii_digit() {
            *index += 1;
        }
        *index - start
    };
    if digits(&mut index) > 0 {
        if bytes.get(index) == Some(&b'.') {
            index += 1;
            digits(&mut index);
        }
    } else if bytes.get(index) == Some(&b'.') {
        index += 1;
        if digits(&mut index) == 0 {
            return false;
        }
    } else {
        return false;
    }
    if matches!(bytes.get(index), Some(b'e' | b'E')) {
        index += 1;
        if matches!(bytes.get(index), Some(b'+' | b'-')) {
            index += 1;
        }
        if digits(&mut index) == 0 {
            return false;
        }
    }
    index == bytes.len()
}

#[cfg(test)]
#[path = "tests_yamlconv/services_yaml_phpize.rs"]
mod tests;
