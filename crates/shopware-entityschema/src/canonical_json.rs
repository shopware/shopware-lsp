//! The canonical JSON encoding of entity schema snapshots.
//!
//! Snapshot IDs committed to plugin repositories are SHA-256 digests of this
//! encoding, so its bytes are fixed and must never change:
//!
//! - strings escape `<`, `>`, `&`, U+2028 and U+2029 as `\uXXXX` (HTML-safe);
//! - fields declared with [`null_if_empty`] encode an empty list as `null`;
//! - integral floats below 1e15 ([`integral_f64`]) encode without a fraction;
//! - [`to_vec`] is compact, [`to_vec_indent`] indents by two spaces.

use std::io;

use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::ser::{CompactFormatter, Formatter, PrettyFormatter};

/// The compact canonical encoding of `value`.
pub fn to_vec<T: Serialize + ?Sized>(value: &T) -> serde_json::Result<Vec<u8>> {
    let mut out = Vec::new();
    let mut serializer =
        serde_json::Serializer::with_formatter(&mut out, CanonicalFormatter(CompactFormatter));
    value.serialize(&mut serializer)?;
    Ok(out)
}

/// The canonical encoding of `value`, indented by two spaces.
pub fn to_vec_indent<T: Serialize + ?Sized>(value: &T) -> serde_json::Result<Vec<u8>> {
    let mut out = Vec::new();
    let mut serializer = serde_json::Serializer::with_formatter(
        &mut out,
        CanonicalFormatter(PrettyFormatter::with_indent(b"  ")),
    );
    value.serialize(&mut serializer)?;
    Ok(out)
}

/// Decodes `source`, treating `null` object members as absent so they take
/// their serde default.
pub fn from_slice<T: serde::de::DeserializeOwned>(source: &[u8]) -> serde_json::Result<T> {
    let mut value: serde_json::Value = serde_json::from_slice(source)?;
    strip_nulls(&mut value);
    serde_json::from_value(value)
}

fn strip_nulls(value: &mut serde_json::Value) {
    match value {
        serde_json::Value::Object(map) => {
            map.retain(|_, member| !member.is_null());
            for member in map.values_mut() {
                strip_nulls(member);
            }
        }
        serde_json::Value::Array(items) => {
            for item in items {
                strip_nulls(item);
            }
        }
        _ => {}
    }
}

struct CanonicalFormatter<F>(F);

impl<F: Formatter> Formatter for CanonicalFormatter<F> {
    fn begin_array<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.begin_array(writer)
    }
    fn end_array<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.end_array(writer)
    }
    fn begin_array_value<W: ?Sized + io::Write>(
        &mut self,
        writer: &mut W,
        first: bool,
    ) -> io::Result<()> {
        self.0.begin_array_value(writer, first)
    }
    fn end_array_value<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.end_array_value(writer)
    }
    fn begin_object<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.begin_object(writer)
    }
    fn end_object<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.end_object(writer)
    }
    fn begin_object_key<W: ?Sized + io::Write>(
        &mut self,
        writer: &mut W,
        first: bool,
    ) -> io::Result<()> {
        self.0.begin_object_key(writer, first)
    }
    fn end_object_key<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.end_object_key(writer)
    }
    fn begin_object_value<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.begin_object_value(writer)
    }
    fn end_object_value<W: ?Sized + io::Write>(&mut self, writer: &mut W) -> io::Result<()> {
        self.0.end_object_value(writer)
    }

    fn write_string_fragment<W: ?Sized + io::Write>(
        &mut self,
        writer: &mut W,
        fragment: &str,
    ) -> io::Result<()> {
        let mut start = 0;
        for (index, character) in fragment.char_indices() {
            let escape = match character {
                '<' => "\\u003c",
                '>' => "\\u003e",
                '&' => "\\u0026",
                '\u{2028}' => "\\u2028",
                '\u{2029}' => "\\u2029",
                _ => continue,
            };
            writer.write_all(&fragment.as_bytes()[start..index])?;
            writer.write_all(escape.as_bytes())?;
            start = index + character.len_utf8();
        }
        writer.write_all(&fragment.as_bytes()[start..])
    }
}

/// Serializes an empty list as `null`.
pub fn null_if_empty<S: Serializer, T: Serialize>(
    value: &[T],
    serializer: S,
) -> Result<S::Ok, S::Error> {
    if value.is_empty() {
        serializer.serialize_none()
    } else {
        value.serialize(serializer)
    }
}

/// Accepts `null` as the default value.
pub fn nullable<'de, D: Deserializer<'de>, T: Deserialize<'de> + Default>(
    deserializer: D,
) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

/// Serializes an integral float below 1e15 as an integer (`3`, not `3.0`).
pub fn integral_f64<S: Serializer>(value: &f64, serializer: S) -> Result<S::Ok, S::Error> {
    if value.is_finite() && value.fract() == 0.0 && value.abs() < 1e15 {
        serializer.serialize_i64(*value as i64)
    } else {
        serializer.serialize_f64(*value)
    }
}

/// [`integral_f64`] for an optional value; `None` serializes as `null`.
pub fn integral_opt_f64<S: Serializer>(
    value: &Option<f64>,
    serializer: S,
) -> Result<S::Ok, S::Error> {
    match value {
        Some(value) => integral_f64(value, serializer),
        None => serializer.serialize_none(),
    }
}

pub(crate) fn is_false(value: &bool) -> bool {
    !*value
}

pub(crate) fn is_zero_f64(value: &f64) -> bool {
    *value == 0.0
}

pub(crate) fn is_zero_i64(value: &i64) -> bool {
    *value == 0
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn escapes_html_characters_and_line_separators() {
        let encoded = to_vec(&serde_json::json!({"a": "<b>&\u{2028}\u{8}\u{c}\u{1}"})).unwrap();
        assert_eq!(
            String::from_utf8(encoded).unwrap(),
            "{\"a\":\"\\u003cb\\u003e\\u0026\\u2028\\b\\f\\u0001\"}"
        );
    }
}
