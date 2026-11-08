//! JSON output that matches Go's `encoding/json`: `json.Marshal` (compact,
//! HTML-escaped) and the CLI's `writeJSON` encoder (two-space indent, no
//! HTML escaping, trailing newline). Both escape U+2028/U+2029 and format
//! floats like Go.

use std::io::{self, Write};

use serde::Serialize;
use serde_json::ser::{CharEscape, CompactFormatter, Formatter, PrettyFormatter};

use crate::gofmt::format_float_json;

enum Layout {
    Compact(CompactFormatter),
    Pretty(PrettyFormatter<'static>),
}

pub struct GoFormatter {
    layout: Layout,
    escape_html: bool,
}

impl GoFormatter {
    fn compact(escape_html: bool) -> Self {
        Self {
            layout: Layout::Compact(CompactFormatter),
            escape_html,
        }
    }

    fn pretty(escape_html: bool) -> Self {
        Self {
            layout: Layout::Pretty(PrettyFormatter::with_indent(b"  ")),
            escape_html,
        }
    }
}

macro_rules! forward {
    ($($name:ident($($arg:ident: $ty:ty),*);)*) => {
        $(
            fn $name<W: ?Sized + Write>(&mut self, writer: &mut W $(, $arg: $ty)*) -> io::Result<()> {
                match &mut self.layout {
                    Layout::Compact(inner) => inner.$name(writer $(, $arg)*),
                    Layout::Pretty(inner) => inner.$name(writer $(, $arg)*),
                }
            }
        )*
    };
}

impl Formatter for GoFormatter {
    forward! {
        begin_array();
        end_array();
        begin_array_value(first: bool);
        end_array_value();
        begin_object();
        end_object();
        begin_object_key(first: bool);
        end_object_key();
        begin_object_value();
        end_object_value();
    }

    fn write_f64<W: ?Sized + Write>(&mut self, writer: &mut W, value: f64) -> io::Result<()> {
        writer.write_all(format_float_json(value).as_bytes())
    }

    fn write_f32<W: ?Sized + Write>(&mut self, writer: &mut W, value: f32) -> io::Result<()> {
        writer.write_all(format_float_json(f64::from(value)).as_bytes())
    }

    fn write_string_fragment<W: ?Sized + Write>(
        &mut self,
        writer: &mut W,
        fragment: &str,
    ) -> io::Result<()> {
        let mut start = 0;
        for (index, character) in fragment.char_indices() {
            let escape: Option<&[u8]> = match character {
                '<' if self.escape_html => Some(b"\\u003c"),
                '>' if self.escape_html => Some(b"\\u003e"),
                '&' if self.escape_html => Some(b"\\u0026"),
                '\u{2028}' => Some(b"\\u2028"),
                '\u{2029}' => Some(b"\\u2029"),
                _ => None,
            };
            if let Some(escape) = escape {
                writer.write_all(&fragment.as_bytes()[start..index])?;
                writer.write_all(escape)?;
                start = index + character.len_utf8();
            }
        }
        writer.write_all(&fragment.as_bytes()[start..])
    }

    fn write_char_escape<W: ?Sized + Write>(
        &mut self,
        writer: &mut W,
        escape: CharEscape,
    ) -> io::Result<()> {
        let text: Vec<u8> = match escape {
            CharEscape::Quote => b"\\\"".to_vec(),
            CharEscape::ReverseSolidus => b"\\\\".to_vec(),
            CharEscape::Solidus => b"/".to_vec(),
            CharEscape::Backspace => b"\\b".to_vec(),
            CharEscape::FormFeed => b"\\f".to_vec(),
            CharEscape::LineFeed => b"\\n".to_vec(),
            CharEscape::CarriageReturn => b"\\r".to_vec(),
            CharEscape::Tab => b"\\t".to_vec(),
            CharEscape::AsciiControl(byte) => format!("\\u{:04x}", byte).into_bytes(),
        };
        writer.write_all(&text)
    }
}

fn encode<T: Serialize + ?Sized>(value: &T, formatter: GoFormatter) -> io::Result<Vec<u8>> {
    let mut buffer = Vec::new();
    let mut serializer = serde_json::Serializer::with_formatter(&mut buffer, formatter);
    value.serialize(&mut serializer).map_err(io::Error::other)?;
    Ok(buffer)
}

/// Go `json.Marshal`.
pub fn marshal<T: Serialize + ?Sized>(value: &T) -> String {
    String::from_utf8(encode(value, GoFormatter::compact(true)).unwrap_or_default())
        .unwrap_or_default()
}

/// The CLI `writeJSON`: indented, no HTML escaping, trailing newline.
pub fn write_json<T: Serialize + ?Sized>(writer: &mut dyn Write, value: &T) -> io::Result<()> {
    let mut buffer = encode(value, GoFormatter::pretty(false))?;
    buffer.push(b'\n');
    writer.write_all(&buffer)
}

/// Go `json.Marshal` of a value after a round trip through `interface{}`:
/// object keys come out sorted.
pub fn marshal_sorted<T: Serialize + ?Sized>(value: &T) -> String {
    match serde_json::to_value(value) {
        Ok(value) => marshal(&value),
        Err(_) => "null".into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_go_encoding() {
        let value = serde_json::json!({"a": "<x>&\u{2028}\u{8}\u{1}", "b": [], "c": {}, "d": 1e-7, "e": [1, 2]});
        assert_eq!(
            marshal(&value),
            "{\"a\":\"\\u003cx\\u003e\\u0026\\u2028\\b\\u0001\",\"b\":[],\"c\":{},\"d\":1e-7,\"e\":[1,2]}"
        );
        let mut output = Vec::new();
        write_json(&mut output, &value).unwrap();
        assert_eq!(
            String::from_utf8(output).unwrap(),
            "{\n  \"a\": \"<x>&\\u2028\\b\\u0001\",\n  \"b\": [],\n  \"c\": {},\n  \"d\": 1e-7,\n  \"e\": [\n    1,\n    2\n  ]\n}\n"
        );
    }
}
