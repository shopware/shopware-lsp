//! Semantic decoding of the native CST.
//!
//! The tree is converted into YAML representation nodes (with anchors,
//! aliases and tags) without invoking another parser, then resolved with the
//! YAML 1.2 core schema: integers with an optional base prefix (`0x`, `0o`,
//! `0b`, or a leading `0` for octal), floats, booleans, null, and strings.
//! [`Value`] implements [`serde::Deserializer`], so [`decode`] works for any
//! `DeserializeOwned` target.

use std::collections::HashMap;
use std::fmt;

use serde::de::{self, DeserializeOwned, IntoDeserializer, Visitor};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{parse_int_literal, parse_uint_literal};

use super::*;

/// A resolved YAML value.
#[derive(Clone, Debug, PartialEq)]
pub enum Value {
    Null,
    Bool(bool),
    Int(i64),
    /// Integers above `i64::MAX`.
    Uint(u64),
    Float(f64),
    String(String),
    Sequence(Vec<Value>),
    /// Entries in source order; keys are unique.
    Mapping(Vec<(Value, Value)>),
}

impl Value {
    /// The value of `key` when this is a mapping.
    pub fn get(&self, key: &str) -> Option<&Value> {
        match self {
            Value::Mapping(entries) => entries
                .iter()
                .find(|(candidate, _)| matches!(candidate, Value::String(text) if text == key))
                .map(|(_, value)| value),
            _ => None,
        }
    }

    pub fn as_str(&self) -> Option<&str> {
        match self {
            Value::String(text) => Some(text),
            _ => None,
        }
    }
}

/// A decoding failure (unresolvable tagged scalar or a target type mismatch).
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[error("{0}")]
pub struct DecodeError(pub String);

impl de::Error for DecodeError {
    fn custom<T: fmt::Display>(message: T) -> Self {
        DecodeError(message.to_string())
    }
}

/// Decodes the first document under `root` into `T`.
pub fn decode<T: DeserializeOwned>(root: &SyntaxNode) -> Result<T, DecodeError> {
    T::deserialize(decode_value(root)?)
}

/// Decodes the first document under `root` into a [`Value`]. A missing
/// document decodes to [`Value::Null`].
pub fn decode_value(root: &SyntaxNode) -> Result<Value, DecodeError> {
    let mut decoder = Decoder {
        source: root.text().to_string(),
        arena: Vec::new(),
        anchors: HashMap::new(),
    };
    let value = decoder.value(root_value(root).as_ref(), 0);
    decoder.resolve(value, 0)
}

// -- representation graph -------------------------------------------------------

type Id = usize;

enum Data {
    Scalar(String),
    Mapping(Vec<(Id, Id)>),
    Sequence(Vec<Id>),
    Alias(Id),
}

struct Repr {
    data: Data,
    tag: String,
}

const NULL_TAG: &str = "!!null";
const STR_TAG: &str = "!!str";
const MAX_DEPTH: usize = 128;

struct Decoder {
    source: String,
    arena: Vec<Repr>,
    anchors: HashMap<String, Id>,
}

impl Decoder {
    fn push(&mut self, data: Data, tag: &str) -> Id {
        self.arena.push(Repr {
            data,
            tag: tag.to_owned(),
        });
        self.arena.len() - 1
    }

    fn null(&mut self) -> Id {
        self.push(Data::Scalar(String::new()), NULL_TAG)
    }

    fn value(&mut self, node: Option<&SyntaxNode>, depth: usize) -> Id {
        let Some(node) = node.filter(|_| depth <= MAX_DEPTH) else {
            return self.null();
        };
        if is_mapping(node) {
            let mut entries = Vec::new();
            for pair in pairs(node) {
                let key = self.value(pair_key(&pair).as_ref(), depth + 1);
                let value = self.decorated_value(&pair, pair_value(&pair).as_ref(), depth + 1);
                entries.push((key, value));
            }
            return self.push(Data::Mapping(entries), "!!map");
        }
        if is_sequence(node) {
            let entries = items(node)
                .map(|item| self.decorated_value(&item, item_value(&item).as_ref(), depth + 1))
                .collect();
            return self.push(Data::Sequence(entries), "!!seq");
        }
        let token = match scalar_token(node) {
            Some(token) if !is_null(node) => token,
            _ => return self.null(),
        };
        let text = token.text();
        match token.kind() {
            TK_SINGLE_QUOTED_SCALAR => {
                let value = scalar_value(node).replace("''", "'");
                self.push(Data::Scalar(value), STR_TAG)
            }
            TK_DOUBLE_QUOTED_SCALAR => {
                let value = decode_double_quoted(&scalar_value(node));
                self.push(Data::Scalar(value), STR_TAG)
            }
            TK_BLOCK_SCALAR => {
                let indent = self.block_indent(u32::from(token.text_range().start()) as usize);
                let value = decode_block_scalar(text, indent);
                self.push(Data::Scalar(value), STR_TAG)
            }
            _ => self.plain_scalar(text.trim()),
        }
    }

    fn plain_scalar(&mut self, mut text: &str) -> Id {
        let mut anchor = None;
        let mut tag = "";
        while text.starts_with(['&', '!']) {
            let Some((decoration, remaining)) = text.split_once(' ') else {
                break;
            };
            if let Some(name) = decoration.strip_prefix('&') {
                anchor = Some(name);
            } else {
                tag = decoration;
            }
            text = remaining.trim();
        }
        if let Some(name) = text.strip_prefix('*') {
            return match self.anchors.get(name) {
                Some(target) => {
                    let target = *target;
                    self.push(Data::Alias(target), "")
                }
                None => self.null(),
            };
        }
        let id = self.push(Data::Scalar(text.to_owned()), tag);
        if let Some(anchor) = anchor.filter(|anchor| !anchor.is_empty()) {
            self.anchors.insert(anchor.to_owned(), id);
        }
        id
    }

    fn decorated_value(
        &mut self,
        parent: &SyntaxNode,
        value: Option<&SyntaxNode>,
        depth: usize,
    ) -> Id {
        let result = self.value(value, depth);
        let key = pair_key(parent);
        for child in parent.children() {
            if Some(&child) == value {
                break;
            }
            // The mapping key is not a value decorator.
            if Some(&child) == key.as_ref() || child.kind() != YAML_SCALAR {
                continue;
            }
            for decorator in scalar_value(&child).split_whitespace() {
                if let Some(anchor) = decorator.strip_prefix('&') {
                    self.anchors.insert(anchor.to_owned(), result);
                }
                if decorator.starts_with("!!") {
                    self.arena[result].tag = decorator.to_owned();
                }
            }
        }
        result
    }

    fn block_indent(&self, offset: usize) -> usize {
        if offset > self.source.len() {
            return 0;
        }
        let prefix = &self.source.as_bytes()[..offset];
        let start = prefix
            .iter()
            .rposition(|byte| *byte == b'\n')
            .map_or(0, |index| index + 1);
        prefix[start..]
            .iter()
            .take_while(|byte| **byte == b' ')
            .count()
    }

    // -- resolution -------------------------------------------------------------

    fn resolve(&self, id: Id, depth: usize) -> Result<Value, DecodeError> {
        if depth > MAX_DEPTH * 2 {
            return Err(DecodeError("yaml: document nesting is too deep".to_owned()));
        }
        let repr = &self.arena[id];
        match &repr.data {
            Data::Alias(target) => self.resolve(*target, depth + 1),
            Data::Sequence(entries) => entries
                .iter()
                .map(|entry| self.resolve(*entry, depth + 1))
                .collect::<Result<_, _>>()
                .map(Value::Sequence),
            Data::Mapping(entries) => self.resolve_mapping(entries, depth),
            Data::Scalar(text) => resolve_scalar(&repr.tag, text),
        }
    }

    fn resolve_mapping(&self, entries: &[(Id, Id)], depth: usize) -> Result<Value, DecodeError> {
        let mut mapping: Vec<(Value, Value)> = Vec::new();
        let mut merges = Vec::new();
        for (key, value) in entries {
            if self.is_merge_key(*key) {
                merges.push(*value);
                continue;
            }
            let key = self.resolve(*key, depth + 1)?;
            let value = self.resolve(*value, depth + 1)?;
            match mapping.iter_mut().find(|(existing, _)| *existing == key) {
                Some(entry) => entry.1 = value,
                None => mapping.push((key, value)),
            }
        }
        // Explicit keys win over merged keys; earlier merge sources win over
        // later ones.
        for merge in merges {
            let sources = match self.resolve(merge, depth + 1)? {
                Value::Sequence(sources) => sources,
                source => vec![source],
            };
            for source in sources {
                let Value::Mapping(merged) = source else {
                    return Err(DecodeError(
                        "yaml: map merge requires map or sequence of maps as the value".to_owned(),
                    ));
                };
                for (key, value) in merged {
                    if !mapping.iter().any(|(existing, _)| *existing == key) {
                        mapping.push((key, value));
                    }
                }
            }
        }
        Ok(Value::Mapping(mapping))
    }

    fn is_merge_key(&self, id: Id) -> bool {
        let repr = &self.arena[id];
        matches!(&repr.data, Data::Scalar(text) if text == "<<" && (repr.tag.is_empty() || repr.tag == "!!merge"))
    }
}

fn resolve_scalar(tag: &str, text: &str) -> Result<Value, DecodeError> {
    let tag = tag
        .strip_prefix("tag:yaml.org,2002:")
        .map_or(tag.to_owned(), |short| format!("!!{short}"));
    let mismatch = |resolved: &Value| {
        DecodeError(format!(
            "yaml: cannot decode {} `{text}` as a {tag}",
            short_tag(resolved)
        ))
    };
    match tag.as_str() {
        NULL_TAG => Ok(Value::Null),
        "" | "!!merge" | "!!timestamp" => Ok(resolve_plain(text)),
        "!!bool" => match resolve_plain(text) {
            value @ Value::Bool(_) => Ok(value),
            value => Err(mismatch(&value)),
        },
        "!!int" => match resolve_plain(text) {
            value @ (Value::Int(_) | Value::Uint(_)) => Ok(value),
            value => Err(mismatch(&value)),
        },
        "!!float" => match resolve_plain(text) {
            Value::Int(int) => Ok(Value::Float(int as f64)),
            Value::Uint(int) => Ok(Value::Float(int as f64)),
            value @ Value::Float(_) => Ok(value),
            value => Err(mismatch(&value)),
        },
        // `!!str` and unresolvable (custom) tags keep the text.
        _ => Ok(Value::String(text.to_owned())),
    }
}

fn short_tag(value: &Value) -> &'static str {
    match value {
        Value::Null => "!!null",
        Value::Bool(_) => "!!bool",
        Value::Int(_) | Value::Uint(_) => "!!int",
        Value::Float(_) => "!!float",
        Value::String(_) => "!!str",
        Value::Sequence(_) => "!!seq",
        Value::Mapping(_) => "!!map",
    }
}

/// Core-schema resolution of an untagged plain scalar (yaml.v3 `resolve`).
fn resolve_plain(text: &str) -> Value {
    match text {
        "" | "~" | "null" | "Null" | "NULL" => return Value::Null,
        "true" | "True" | "TRUE" => return Value::Bool(true),
        "false" | "False" | "FALSE" => return Value::Bool(false),
        ".nan" | ".NaN" | ".NAN" => return Value::Float(f64::NAN),
        ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF" => {
            return Value::Float(f64::INFINITY);
        }
        "-.inf" | "-.Inf" | "-.INF" => return Value::Float(f64::NEG_INFINITY),
        _ => {}
    }
    if !text.starts_with(|c: char| c.is_ascii_digit() || matches!(c, '+' | '-' | '.')) {
        return Value::String(text.to_owned());
    }
    let plain = text.replace('_', "");
    if let Some(int) = parse_int_literal(&plain) {
        return Value::Int(int);
    }
    if let Some(uint) = parse_uint_literal(&plain) {
        return Value::Uint(uint);
    }
    if is_yaml_float(&plain)
        && let Ok(float) = plain.parse::<f64>()
        && float.is_finite()
    {
        return Value::Float(float);
    }
    Value::String(text.to_owned())
}

/// `^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`
pub fn is_yaml_float(text: &str) -> bool {
    let bytes = text.as_bytes();
    let mut position = usize::from(matches!(bytes.first(), Some(b'-' | b'+')));
    let digits = |position: &mut usize| {
        let start = *position;
        while bytes.get(*position).is_some_and(u8::is_ascii_digit) {
            *position += 1;
        }
        *position - start
    };
    if bytes.get(position) == Some(&b'.') {
        position += 1;
        if digits(&mut position) == 0 {
            return false;
        }
    } else {
        if digits(&mut position) == 0 {
            return false;
        }
        if bytes.get(position) == Some(&b'.') {
            position += 1;
            digits(&mut position);
        }
    }
    if matches!(bytes.get(position), Some(b'e' | b'E')) {
        position += 1;
        if matches!(bytes.get(position), Some(b'-' | b'+')) {
            position += 1;
        }
        if digits(&mut position) == 0 {
            return false;
        }
    }
    position == bytes.len()
}

/// YAML's double-quoted escape alphabet, applied to the unquoted content.
pub fn decode_double_quoted(text: &str) -> String {
    let bytes = text.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(bytes.len());
    let push_char = |out: &mut Vec<u8>, value: char| {
        out.extend_from_slice(value.encode_utf8(&mut [0; 4]).as_bytes());
    };
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'\\' || i + 1 == bytes.len() {
            out.push(bytes[i]);
            i += 1;
            continue;
        }
        i += 1;
        match bytes[i] {
            b'0' => out.push(0),
            b'a' => out.push(0x07),
            b'b' => out.push(0x08),
            b't' | b'\t' => out.push(b'\t'),
            b'n' => out.push(b'\n'),
            b'v' => out.push(0x0b),
            b'f' => out.push(0x0c),
            b'r' => out.push(b'\r'),
            b'e' => out.push(27),
            b'N' => push_char(&mut out, '\u{85}'),
            b'_' => push_char(&mut out, '\u{a0}'),
            b'L' => push_char(&mut out, '\u{2028}'),
            b'P' => push_char(&mut out, '\u{2029}'),
            escape @ (b'x' | b'u' | b'U') => {
                let width = match escape {
                    b'x' => 2,
                    b'u' => 4,
                    _ => 8,
                };
                let digits = bytes
                    .get(i + 1..i + 1 + width)
                    .filter(|_| i + width < bytes.len());
                if let Some(digits) =
                    digits.filter(|digits| digits.iter().all(u8::is_ascii_hexdigit))
                {
                    let code = digits.iter().fold(0u32, |code, digit| {
                        code * 16 + (*digit as char).to_digit(16).unwrap_or(0)
                    });
                    push_char(&mut out, char::from_u32(code).unwrap_or('\u{fffd}'));
                    i += width;
                } else {
                    out.push(escape);
                }
            }
            other => out.push(other),
        }
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// Decodes the body of a block scalar token (`|` / `>` header included).
pub fn decode_block_scalar(text: &str, base_indent: usize) -> String {
    let text = text.replace("\r\n", "\n");
    let Some((header, body)) = text.split_once('\n') else {
        return String::new();
    };
    let header = header.split_once('#').map_or(header, |(header, _)| header);
    let mut lines: Vec<&[u8]> = body.split('\n').map(str::as_bytes).collect();
    let mut indent: Option<usize> = header
        .chars()
        .find(|c| ('1'..='9').contains(c))
        .map(|c| base_indent + (c as usize - '0' as usize));
    if indent.is_none()
        && let Some(line) = lines.iter().find(|line| !is_blank(line))
    {
        indent = Some(line.iter().take_while(|byte| **byte == b' ').count());
    }
    let indent = indent.unwrap_or(0);
    for line in &mut lines {
        if is_blank(line) {
            *line = b"";
        } else if indent > 0 && line.len() >= indent {
            *line = &line[indent..];
        }
    }
    let folded = header.starts_with('>');
    let mut out: Vec<u8> = Vec::with_capacity(text.len());
    for (i, line) in lines.iter().enumerate() {
        out.extend_from_slice(line);
        let Some(next) = lines.get(i + 1) else {
            continue;
        };
        let ordinary = |line: &[u8]| !line.is_empty() && !line.starts_with(b" ");
        if folded && ordinary(line) && next.is_empty() {
            // A run of N blank lines between ordinary paragraphs contributes N
            // line breaks; the break preceding that run is folded away.
            let following = lines[i + 2..]
                .iter()
                .position(|line| !line.is_empty())
                .map(|offset| i + 2 + offset);
            if following.is_some_and(|following| !lines[following].starts_with(b" ")) {
                continue;
            }
        }
        out.push(if folded && ordinary(line) && ordinary(next) {
            b' '
        } else {
            b'\n'
        });
    }
    let value = String::from_utf8_lossy(&out).into_owned();
    if header.contains('-') {
        return value.trim_end_matches('\n').to_owned();
    }
    if header.contains('+') || value.is_empty() {
        return value;
    }
    format!("{}\n", value.trim_end_matches('\n'))
}

/// Whether `line` holds only white space.
pub fn is_blank(line: &[u8]) -> bool {
    String::from_utf8_lossy(line).trim().is_empty()
}

// -- serde ----------------------------------------------------------------------

impl IntoDeserializer<'_, DecodeError> for Value {
    type Deserializer = Value;

    fn into_deserializer(self) -> Value {
        self
    }
}

impl Value {
    fn scalar_text(&self) -> Option<String> {
        match self {
            Value::Bool(value) => Some(value.to_string()),
            Value::Int(value) => Some(value.to_string()),
            Value::Uint(value) => Some(value.to_string()),
            Value::Float(value) => Some(value.to_string()),
            Value::String(value) => Some(value.clone()),
            _ => None,
        }
    }
}

impl<'de> de::Deserializer<'de> for Value {
    type Error = DecodeError;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DecodeError> {
        match self {
            Value::Null => visitor.visit_unit(),
            Value::Bool(value) => visitor.visit_bool(value),
            Value::Int(value) => visitor.visit_i64(value),
            Value::Uint(value) => visitor.visit_u64(value),
            Value::Float(value) => visitor.visit_f64(value),
            Value::String(value) => visitor.visit_string(value),
            Value::Sequence(values) => {
                let mut access = de::value::SeqDeserializer::new(values.into_iter());
                let result = visitor.visit_seq(&mut access)?;
                access.end()?;
                Ok(result)
            }
            Value::Mapping(entries) => {
                let mut access = de::value::MapDeserializer::new(entries.into_iter());
                let result = visitor.visit_map(&mut access)?;
                access.end()?;
                Ok(result)
            }
        }
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DecodeError> {
        match self {
            Value::Null => visitor.visit_none(),
            value => visitor.visit_some(value),
        }
    }

    /// YAML 1.1 booleans are accepted for typed `bool` targets, like yaml.v3.
    fn deserialize_bool<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DecodeError> {
        if let Value::String(text) = &self {
            match text.as_str() {
                "y" | "Y" | "yes" | "Yes" | "YES" | "on" | "On" | "ON" => {
                    return visitor.visit_bool(true);
                }
                "n" | "N" | "no" | "No" | "NO" | "off" | "Off" | "OFF" => {
                    return visitor.visit_bool(false);
                }
                _ => {}
            }
        }
        self.deserialize_any(visitor)
    }

    /// Any non-null scalar decodes into a string target, like yaml.v3.
    fn deserialize_string<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DecodeError> {
        match self.scalar_text() {
            Some(text) => visitor.visit_string(text),
            None => self.deserialize_any(visitor),
        }
    }

    fn deserialize_str<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DecodeError> {
        self.deserialize_string(visitor)
    }

    fn deserialize_newtype_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        visitor: V,
    ) -> Result<V::Value, DecodeError> {
        visitor.visit_newtype_struct(self)
    }

    fn deserialize_enum<V: Visitor<'de>>(
        self,
        _name: &'static str,
        _variants: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, DecodeError> {
        match self {
            Value::String(variant) => visitor.visit_enum(variant.into_deserializer()),
            Value::Mapping(mut entries) if entries.len() == 1 => {
                let (variant, value) = entries.remove(0);
                visitor.visit_enum(de::value::MapAccessDeserializer::new(
                    de::value::MapDeserializer::new(std::iter::once((variant, value))),
                ))
            }
            other => Err(de::Error::invalid_type(other.unexpected(), &"a YAML enum")),
        }
    }

    serde::forward_to_deserialize_any! {
        i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char bytes byte_buf unit
        unit_struct seq tuple tuple_struct map struct identifier ignored_any
    }
}

impl Value {
    fn unexpected(&self) -> de::Unexpected<'_> {
        match self {
            Value::Null => de::Unexpected::Unit,
            Value::Bool(value) => de::Unexpected::Bool(*value),
            Value::Int(value) => de::Unexpected::Signed(*value),
            Value::Uint(value) => de::Unexpected::Unsigned(*value),
            Value::Float(value) => de::Unexpected::Float(*value),
            Value::String(value) => de::Unexpected::Str(value),
            Value::Sequence(_) => de::Unexpected::Seq,
            Value::Mapping(_) => de::Unexpected::Map,
        }
    }
}
