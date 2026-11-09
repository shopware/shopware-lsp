//! Decodes the native YAML CST into typed extension configuration the way
//! shopware-cli reads `.shopware-extension.yml` (it decodes with the
//! `yaml.v3` library), so the editor sees the same values and errors as the
//! build tool.
//!
//! `swlsp_parser_yaml::query::decode` resolves values into a generic
//! [`Value`](swlsp_parser_yaml::query::Value) and aborts on the first type
//! mismatch. Extension configuration needs yaml.v3's typed behavior instead:
//! string targets keep the original scalar text (`8.0` stays `8.0`), type
//! errors are collected while every other field still decodes, failed
//! sequence elements are dropped, and duplicate keys reject a mapping. The
//! [`Document`] graph resolves tags, anchors and merge keys once; the
//! [`Decoder`] then walks it into the concrete target types implementing
//! [`Target`].

use std::collections::{BTreeMap, HashSet};

use swlsp_parser_yaml::query::{
    decode_block_scalar, decode_double_quoted, is_mapping, is_null, is_sequence, is_yaml_float,
    item_value, items, pair_key, pair_value, pairs, root_value, scalar_token, scalar_value,
};
use swlsp_parser_yaml::syntax::{
    TK_BLOCK_SCALAR, TK_DOUBLE_QUOTED_SCALAR, TK_SINGLE_QUOTED_SCALAR, YAML_SCALAR,
};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{parse_int_literal, parse_uint_literal};

pub(crate) type Id = usize;

pub(crate) const NULL_TAG: &str = "!!null";
const BOOL_TAG: &str = "!!bool";
pub(crate) const STR_TAG: &str = "!!str";
const INT_TAG: &str = "!!int";
const FLOAT_TAG: &str = "!!float";
const TIMESTAMP_TAG: &str = "!!timestamp";
pub(crate) const SEQ_TAG: &str = "!!seq";
pub(crate) const MAP_TAG: &str = "!!map";
pub(crate) const BINARY_TAG: &str = "!!binary";
const MERGE_TAG: &str = "!!merge";
const LONG_TAG_PREFIX: &str = "tag:yaml.org,2002:";

// -- representation graph --------------------------------------------------

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum NodeKind {
    Scalar,
    Mapping(Vec<(Id, Id)>),
    Sequence(Vec<Id>),
    Alias(Id),
}

impl NodeKind {
    fn discriminant(&self) -> u8 {
        match self {
            NodeKind::Scalar => 0,
            NodeKind::Mapping(_) => 1,
            NodeKind::Sequence(_) => 2,
            NodeKind::Alias(_) => 3,
        }
    }
}

#[derive(Clone, Debug)]
pub(crate) struct Node {
    pub kind: NodeKind,
    pub tag: String,
    pub value: String,
    /// Quoted or block style (yaml.v3 `indicatedString` styles).
    pub styled: bool,
}

pub(crate) struct Document {
    nodes: Vec<Node>,
    root: Id,
}

impl Document {
    pub(crate) fn build(root: &SyntaxNode) -> Document {
        let mut builder = Builder {
            source: root.text().to_string(),
            nodes: Vec::new(),
            anchors: Default::default(),
        };
        let root_value = root_value(root);
        let root = builder.value(root_value.as_ref(), 0);
        Document {
            nodes: builder.nodes,
            root,
        }
    }
}

struct Builder {
    source: String,
    nodes: Vec<Node>,
    anchors: std::collections::HashMap<String, Id>,
}

const MAX_DEPTH: usize = 128;

impl Builder {
    fn push(&mut self, kind: NodeKind, tag: &str, value: String, styled: bool) -> Id {
        self.nodes.push(Node {
            kind,
            tag: tag.to_owned(),
            value,
            styled,
        });
        self.nodes.len() - 1
    }

    fn null(&mut self) -> Id {
        self.push(NodeKind::Scalar, NULL_TAG, String::new(), false)
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
            return self.push(NodeKind::Mapping(entries), MAP_TAG, String::new(), false);
        }
        if is_sequence(node) {
            let entries = items(node)
                .map(|item| self.decorated_value(&item, item_value(&item).as_ref(), depth + 1))
                .collect();
            return self.push(NodeKind::Sequence(entries), SEQ_TAG, String::new(), false);
        }
        let token = match scalar_token(node) {
            Some(token) if !is_null(node) => token,
            _ => return self.null(),
        };
        match token.kind() {
            TK_SINGLE_QUOTED_SCALAR => {
                let value = scalar_value(node).replace("''", "'");
                self.push(NodeKind::Scalar, STR_TAG, value, true)
            }
            TK_DOUBLE_QUOTED_SCALAR => {
                let value = decode_double_quoted(&scalar_value(node));
                self.push(NodeKind::Scalar, STR_TAG, value, true)
            }
            TK_BLOCK_SCALAR => {
                let indent = self.block_indent(u32::from(token.text_range().start()) as usize);
                let value = decode_block_scalar(token.text(), indent);
                self.push(NodeKind::Scalar, STR_TAG, value, true)
            }
            _ => self.plain_scalar(token.text().trim()),
        }
    }

    fn plain_scalar(&mut self, mut text: &str) -> Id {
        let mut anchor = "";
        let mut tag = "";
        while text.starts_with(['&', '!']) {
            let Some((decoration, remaining)) = text.split_once(' ') else {
                break;
            };
            if let Some(name) = decoration.strip_prefix('&') {
                anchor = name;
            } else {
                tag = decoration;
            }
            text = remaining.trim();
        }
        if let Some(name) = text.strip_prefix('*') {
            return match self.anchors.get(name) {
                Some(&target) => self.push(NodeKind::Alias(target), "", name.to_owned(), false),
                None => self.null(),
            };
        }
        let id = self.push(NodeKind::Scalar, tag, text.to_owned(), false);
        if !anchor.is_empty() {
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
            for decorator in scalar_value(&child).split_ascii_whitespace() {
                if let Some(anchor) = decorator.strip_prefix('&') {
                    self.anchors.insert(anchor.to_owned(), result);
                }
                if decorator.starts_with("!!") {
                    self.nodes[result].tag = decorator.to_owned();
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
}

// -- scalar resolution -------------------------------------------------------

/// A fatal decoding error (yaml.v3 `failf`): decoding stops, fields decoded so
/// far are kept.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Fatal(pub String);

pub(crate) type R<T> = Result<T, Fatal>;

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Resolved {
    Null,
    Bool(bool),
    Int(i64),
    Uint(u64),
    Float(f64),
    Str(String),
    Timestamp,
}

pub(crate) fn short_tag(tag: &str) -> String {
    match tag.strip_prefix(LONG_TAG_PREFIX) {
        Some(short) => format!("!!{short}"),
        None => tag.to_owned(),
    }
}

fn resolvable_tag(tag: &str) -> bool {
    matches!(
        tag,
        "" | STR_TAG | BOOL_TAG | INT_TAG | FLOAT_TAG | NULL_TAG | TIMESTAMP_TAG
    )
}

/// yaml.v3 `resolve`.
pub(crate) fn resolve(tag: &str, input: &str) -> R<(String, Resolved)> {
    let tag = short_tag(tag);
    if !resolvable_tag(&tag) {
        return Ok((tag, Resolved::Str(input.to_owned())));
    }
    let (rtag, out) = resolve_untyped(&tag, input);
    if tag.is_empty() || tag == rtag || tag == STR_TAG || tag == BINARY_TAG {
        return Ok((rtag.to_owned(), out));
    }
    if tag == FLOAT_TAG
        && let Resolved::Int(value) = out
    {
        return Ok((FLOAT_TAG.to_owned(), Resolved::Float(value as f64)));
    }
    Err(Fatal(format!(
        "yaml: cannot decode {} `{input}` as a {}",
        short_tag(rtag),
        short_tag(&tag)
    )))
}

fn resolve_untyped(tag: &str, input: &str) -> (&'static str, Resolved) {
    let hint = match input.as_bytes().first() {
        None => b'N',
        Some(b'+' | b'-') => b'S',
        Some(b'0'..=b'9') => b'D',
        Some(b'y' | b'Y' | b'n' | b'N' | b't' | b'T' | b'f' | b'F' | b'o' | b'O' | b'~') => b'M',
        Some(b'.') => b'.',
        Some(_) => 0,
    };
    if hint != 0 && tag != STR_TAG && tag != BINARY_TAG {
        match input {
            "true" | "True" | "TRUE" => return (BOOL_TAG, Resolved::Bool(true)),
            "false" | "False" | "FALSE" => return (BOOL_TAG, Resolved::Bool(false)),
            "" | "~" | "null" | "Null" | "NULL" => return (NULL_TAG, Resolved::Null),
            ".nan" | ".NaN" | ".NAN" => return (FLOAT_TAG, Resolved::Float(f64::NAN)),
            ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF" => {
                return (FLOAT_TAG, Resolved::Float(f64::INFINITY));
            }
            "-.inf" | "-.Inf" | "-.INF" => return (FLOAT_TAG, Resolved::Float(f64::NEG_INFINITY)),
            "<<" => return (MERGE_TAG, Resolved::Str("<<".to_owned())),
            _ => {}
        }
        match hint {
            b'.' => {
                if let Some(value) = parse_decimal_float(input) {
                    return (FLOAT_TAG, Resolved::Float(value));
                }
            }
            b'D' | b'S' => {
                if (tag.is_empty() || tag == TIMESTAMP_TAG) && is_timestamp(input) {
                    return (TIMESTAMP_TAG, Resolved::Timestamp);
                }
                let plain = input.replace('_', "");
                if let Some(value) = parse_int_literal(&plain) {
                    return (INT_TAG, Resolved::Int(value));
                }
                if let Some(value) = parse_uint_literal(&plain) {
                    return (INT_TAG, Resolved::Uint(value));
                }
                if is_yaml_float(&plain)
                    && let Some(value) = parse_decimal_float(&plain)
                {
                    return (FLOAT_TAG, Resolved::Float(value));
                }
                for (prefix, radix, negative) in [
                    ("0b", 2, false),
                    ("-0b", 2, true),
                    ("0o", 8, false),
                    ("-0o", 8, true),
                ] {
                    if let Some(digits) = plain.strip_prefix(prefix) {
                        let digits = if negative {
                            format!("-{digits}")
                        } else {
                            digits.to_owned()
                        };
                        if let Some(value) = parse_signed_radix(&digits, radix) {
                            return (INT_TAG, Resolved::Int(value));
                        }
                        if !negative && let Some(value) = parse_unsigned_radix(&digits, radix) {
                            return (INT_TAG, Resolved::Uint(value));
                        }
                        break;
                    }
                }
            }
            _ => {}
        }
    }
    (STR_TAG, Resolved::Str(input.to_owned()))
}

/// `strconv.ParseFloat` for decimal input; out-of-range values fail.
fn parse_decimal_float(text: &str) -> Option<f64> {
    let digits = text.trim_start_matches(['+', '-']);
    if digits.is_empty()
        || !digits
            .bytes()
            .all(|byte| byte.is_ascii_digit() || matches!(byte, b'.' | b'e' | b'E' | b'+' | b'-'))
    {
        return None;
    }
    text.parse::<f64>().ok().filter(|value| value.is_finite())
}

/// `strconv.ParseInt(text, radix, 64)` for an explicit radix.
fn parse_signed_radix(text: &str, radix: u32) -> Option<i64> {
    let (negative, digits) = match text.as_bytes().first()? {
        b'-' => (true, &text[1..]),
        b'+' => (false, &text[1..]),
        _ => (false, text),
    };
    let magnitude = parse_unsigned_radix(digits, radix)? as i128;
    i64::try_from(if negative { -magnitude } else { magnitude }).ok()
}

fn parse_unsigned_radix(digits: &str, radix: u32) -> Option<u64> {
    if digits.is_empty() || !digits.chars().all(|c| c.is_digit(radix)) {
        return None;
    }
    u64::from_str_radix(digits, radix).ok()
}

/// yaml.v3 `parseTimestamp`: `YYYY-M-D`, optionally followed by `T`/`t`/` `
/// and `H:M:S[.fraction]` (with a `Z`/`±hh:mm` zone after `T`/`t`).
fn is_timestamp(text: &str) -> bool {
    let bytes = text.as_bytes();
    let year_digits = bytes
        .iter()
        .take_while(|byte| byte.is_ascii_digit())
        .count();
    if year_digits != 4 || bytes.get(4) != Some(&b'-') {
        return false;
    }
    let mut cursor = Cursor { bytes, position: 5 };
    let year: u32 = text[..4].parse().unwrap_or(0);
    let Some(month) = cursor.number(1, 2) else {
        return false;
    };
    if !cursor.literal(b'-') {
        return false;
    }
    let Some(day) = cursor.number(1, 2) else {
        return false;
    };
    if !(1..=12).contains(&month) || day < 1 || day > days_in_month(year, month) {
        return false;
    }
    if cursor.at_end() {
        return true;
    }
    let separator = bytes[cursor.position];
    cursor.position += 1;
    let zoned = matches!(separator, b'T' | b't');
    if !zoned && separator != b' ' {
        return false;
    }
    let (Some(hour), true, Some(minute), true, Some(second)) = (
        cursor.number(1, 2),
        cursor.literal(b':'),
        cursor.number(1, 2),
        cursor.literal(b':'),
        cursor.number(1, 2),
    ) else {
        return false;
    };
    if hour > 23 || minute > 59 || second > 59 {
        return false;
    }
    if cursor.literal(b'.') {
        let start = cursor.position;
        while cursor
            .bytes
            .get(cursor.position)
            .is_some_and(u8::is_ascii_digit)
        {
            cursor.position += 1;
        }
        if cursor.position == start {
            return false;
        }
    }
    if !zoned {
        return cursor.at_end();
    }
    if cursor.literal(b'Z') {
        return cursor.at_end();
    }
    if !(cursor.literal(b'+') || cursor.literal(b'-')) {
        return false;
    }
    let (Some(zone_hour), true, Some(zone_minute)) = (
        cursor.number(2, 2),
        cursor.literal(b':'),
        cursor.number(2, 2),
    ) else {
        return false;
    };
    zone_hour <= 24 && zone_minute <= 60 && cursor.at_end()
}

struct Cursor<'a> {
    bytes: &'a [u8],
    position: usize,
}

impl Cursor<'_> {
    fn number(&mut self, min: usize, max: usize) -> Option<u32> {
        let start = self.position;
        while self.position - start < max
            && self
                .bytes
                .get(self.position)
                .is_some_and(u8::is_ascii_digit)
        {
            self.position += 1;
        }
        if self.position - start < min {
            return None;
        }
        std::str::from_utf8(&self.bytes[start..self.position])
            .ok()?
            .parse()
            .ok()
    }

    fn literal(&mut self, byte: u8) -> bool {
        if self.bytes.get(self.position) == Some(&byte) {
            self.position += 1;
            return true;
        }
        false
    }

    fn at_end(&self) -> bool {
        self.position == self.bytes.len()
    }
}

fn days_in_month(year: u32, month: u32) -> u32 {
    match month {
        2 if (year.is_multiple_of(4) && !year.is_multiple_of(100)) || year.is_multiple_of(400) => {
            29
        }
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// `base64.StdEncoding.DecodeString` (line breaks are ignored).
fn decode_base64(text: &str) -> Option<Vec<u8>> {
    let input: Vec<u8> = text
        .bytes()
        .filter(|byte| !matches!(byte, b'\r' | b'\n'))
        .collect();
    if !input.len().is_multiple_of(4) {
        return None;
    }
    let value = |byte: u8| -> Option<u32> {
        Some(match byte {
            b'A'..=b'Z' => byte - b'A',
            b'a'..=b'z' => byte - b'a' + 26,
            b'0'..=b'9' => byte - b'0' + 52,
            b'+' => 62,
            b'/' => 63,
            _ => return None,
        } as u32)
    };
    let mut out = Vec::with_capacity(input.len() / 4 * 3);
    let chunks = input.len() / 4;
    for (index, chunk) in input.chunks(4).enumerate() {
        let padding = chunk.iter().rev().take_while(|byte| **byte == b'=').count();
        if padding > 2 || (padding > 0 && index + 1 != chunks) {
            return None;
        }
        let mut bits = 0u32;
        for byte in &chunk[..4 - padding] {
            bits = bits << 6 | value(*byte)?;
        }
        bits <<= 6 * padding as u32;
        let bytes = [(bits >> 16) as u8, (bits >> 8) as u8, bits as u8];
        out.extend_from_slice(&bytes[..3 - padding]);
    }
    Some(out)
}

// -- typed decoding ----------------------------------------------------------

/// A type the configuration decodes into. The default methods report a type
/// error for unsupported node/target combinations.
pub(crate) trait Target {
    /// The type name shown in type errors, spelled as shopware-cli reports
    /// it (`int`, `[]string`, `extension.ConfigStore`).
    fn type_name() -> String;

    /// The type name inside a generic instantiation, where named types are
    /// spelled with their full import path.
    fn qualified_name() -> String {
        Self::type_name()
    }

    /// Decodes a non-alias node. Overridden by pointers and unmarshalers.
    fn decode(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool>
    where
        Self: Sized,
    {
        decoder.dispatch(id, self)
    }

    /// yaml.v3 `null`: nullable kinds are reset.
    fn null(&mut self) -> bool {
        false
    }

    fn scalar(
        &mut self,
        decoder: &mut Decoder<'_>,
        id: Id,
        tag: &str,
        _resolved: Resolved,
    ) -> R<bool> {
        decoder.terror(id, tag, &Self::type_name());
        Ok(false)
    }

    fn mapping(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        decoder.terror(id, MAP_TAG, &Self::type_name());
        Ok(false)
    }

    fn sequence(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        decoder.terror(id, SEQ_TAG, &Self::type_name());
        Ok(false)
    }
}

/// A record type: yaml keys in field order.
pub(crate) trait StructTarget: Target {
    const FIELDS: &'static [&'static str];

    fn field(&mut self, decoder: &mut Decoder<'_>, index: usize, value: Id) -> R<()>;
}

pub(crate) struct Decoder<'a> {
    document: &'a Document,
    pub(crate) terrors: Vec<String>,
    merged_fields: Option<HashSet<String>>,
    decode_count: usize,
    alias_count: usize,
    alias_depth: usize,
    aliases: HashSet<Id>,
}

/// Decodes `root` into `target`; the result is the error text shopware-cli
/// would report, if any.
pub(crate) fn decode_into<T: Target>(root: &SyntaxNode, target: &mut T) -> Option<String> {
    let document = Document::build(root);
    let mut decoder = Decoder::new(&document);
    match decoder.unmarshal(document.root, target) {
        Err(Fatal(message)) => Some(message),
        Ok(_) if decoder.terrors.is_empty() => None,
        Ok(_) => Some(format!(
            "yaml: unmarshal errors:\n  {}",
            decoder.terrors.join("\n  ")
        )),
    }
}

impl<'a> Decoder<'a> {
    pub(crate) fn new(document: &'a Document) -> Self {
        Decoder {
            document,
            terrors: Vec::new(),
            merged_fields: None,
            decode_count: 0,
            alias_count: 0,
            alias_depth: 0,
            aliases: HashSet::new(),
        }
    }

    /// A fresh decoder over the same document (yaml.v3 `Node.Decode` inside
    /// an unmarshaler).
    pub(crate) fn nested(&self) -> Decoder<'a> {
        Decoder::new(self.document)
    }

    pub(crate) fn node(&self, id: Id) -> &'a Node {
        &self.document.nodes[id]
    }

    pub(crate) fn unmarshal<T: Target>(&mut self, id: Id, out: &mut T) -> R<bool> {
        self.decode_count += 1;
        if self.alias_depth > 0 {
            self.alias_count += 1;
        }
        if self.alias_count > 100
            && self.decode_count > 1000
            && self.alias_count as f64 / self.decode_count as f64
                > allowed_alias_ratio(self.decode_count)
        {
            return Err(Fatal(
                "yaml: document contains excessive aliasing".to_owned(),
            ));
        }
        let node = self.node(id);
        if let NodeKind::Alias(target) = node.kind {
            if !self.aliases.insert(id) {
                return Err(Fatal(format!(
                    "yaml: anchor '{}' value contains itself",
                    node.value
                )));
            }
            self.alias_depth += 1;
            let good = self.unmarshal(target, out)?;
            self.alias_depth -= 1;
            self.aliases.remove(&id);
            return Ok(good);
        }
        out.decode(self, id)
    }

    /// Dispatches a non-alias node by kind (yaml.v3 `unmarshal` after
    /// `prepare`).
    pub(crate) fn dispatch<T: Target>(&mut self, id: Id, out: &mut T) -> R<bool> {
        match &self.node(id).kind {
            NodeKind::Scalar => self.scalar(id, out),
            NodeKind::Mapping(_) => {
                if !self.unique_keys(id) {
                    return Ok(false);
                }
                out.mapping(self, id)
            }
            NodeKind::Sequence(_) => out.sequence(self, id),
            NodeKind::Alias(target) => {
                let target = *target;
                self.unmarshal(target, out)
            }
        }
    }

    fn scalar<T: Target>(&mut self, id: Id, out: &mut T) -> R<bool> {
        let node = self.node(id);
        let (tag, resolved) = if indicated_string(node) {
            (STR_TAG.to_owned(), Resolved::Str(node.value.clone()))
        } else {
            let (tag, mut resolved) = resolve(&node.tag, &node.value)?;
            if tag == BINARY_TAG {
                let Resolved::Str(text) = &resolved else {
                    unreachable!("binary tags are unresolvable")
                };
                let Some(data) = decode_base64(text) else {
                    return Err(Fatal(
                        "yaml: !!binary value contains invalid base64 data".to_owned(),
                    ));
                };
                resolved = Resolved::Str(String::from_utf8_lossy(&data).into_owned());
            }
            (tag, resolved)
        };
        if resolved == Resolved::Null {
            return Ok(out.null());
        }
        out.scalar(self, id, &tag, resolved)
    }

    /// yaml.v3 `Node.ShortTag`.
    pub(crate) fn short_tag(&self, id: Id) -> String {
        let node = self.node(id);
        if indicated_string(node) {
            return STR_TAG.to_owned();
        }
        if node.tag.is_empty() || node.tag == "!" {
            return match &node.kind {
                NodeKind::Mapping(_) => MAP_TAG.to_owned(),
                NodeKind::Sequence(_) => SEQ_TAG.to_owned(),
                NodeKind::Alias(target) => self.short_tag(*target),
                NodeKind::Scalar => resolve("", &node.value)
                    .map(|(tag, _)| tag)
                    .unwrap_or_default(),
            };
        }
        short_tag(&node.tag)
    }

    pub(crate) fn terror(&mut self, id: Id, tag: &str, type_name: &str) {
        let node = self.node(id);
        let tag = if node.tag.is_empty() {
            tag
        } else {
            node.tag.as_str()
        };
        let value = if tag != SEQ_TAG && tag != MAP_TAG {
            if node.value.len() > 10 {
                format!(
                    " `{}...`",
                    String::from_utf8_lossy(&node.value.as_bytes()[..7])
                )
            } else {
                format!(" `{}`", node.value)
            }
        } else {
            // yaml.v3 appends the raw value without separator here.
            node.value.clone()
        };
        self.terrors.push(format!(
            "line 0: cannot unmarshal {}{value} into {type_name}",
            short_tag(tag)
        ));
    }

    fn entries(&self, id: Id) -> &'a [(Id, Id)] {
        match &self.node(id).kind {
            NodeKind::Mapping(entries) => entries,
            _ => &[],
        }
    }

    fn unique_keys(&mut self, id: Id) -> bool {
        let entries = self.entries(id);
        let before = self.terrors.len();
        for (index, (first, _)) in entries.iter().enumerate() {
            let first = self.node(*first);
            for (second, _) in &entries[index + 1..] {
                let second = self.node(*second);
                if first.kind.discriminant() == second.kind.discriminant()
                    && first.value == second.value
                {
                    self.terrors.push(format!(
                        "line 0: mapping key {:?} already defined at line 0",
                        second.value
                    ));
                }
            }
        }
        self.terrors.len() == before
    }

    fn is_merge(&self, id: Id) -> bool {
        let node = self.node(id);
        node.kind == NodeKind::Scalar
            && node.value == "<<"
            && (node.tag.is_empty() || node.tag == "!" || short_tag(&node.tag) == MERGE_TAG)
    }

    pub(crate) fn mapping_struct<T: StructTarget>(&mut self, id: Id, out: &mut T) -> R<bool> {
        let mut merged = self.merged_fields.take();
        let mut merge = None;
        let mut done = vec![false; T::FIELDS.len()];
        for &(key, value) in self.entries(id) {
            if self.is_merge(key) {
                merge = Some(value);
                continue;
            }
            let mut name = String::new();
            if !self.unmarshal(key, &mut name)? {
                continue;
            }
            if let Some(merged) = merged.as_mut()
                && !merged.insert(name.clone())
            {
                continue;
            }
            let Some(index) = T::FIELDS.iter().position(|field| *field == name) else {
                continue;
            };
            if done[index] {
                self.terrors.push(format!(
                    "line 0: field {name} already set in type {}",
                    T::type_name()
                ));
                continue;
            }
            done[index] = true;
            out.field(self, index, value)?;
        }
        self.merged_fields = merged;
        if let Some(merge) = merge {
            self.merge(id, merge, out)?;
        }
        Ok(true)
    }

    pub(crate) fn mapping_map(&mut self, id: Id, out: &mut BTreeMap<String, String>) -> R<bool> {
        let mut merged = self.merged_fields.take();
        let mut merge = None;
        let map_is_new = out.is_empty();
        for &(key, value) in self.entries(id) {
            if self.is_merge(key) {
                merge = Some(value);
                continue;
            }
            let mut name = String::new();
            if !self.unmarshal(key, &mut name)? {
                continue;
            }
            if let Some(merged) = merged.as_mut()
                && !merged.insert(name.clone())
            {
                continue;
            }
            let mut element = String::new();
            if self.unmarshal(value, &mut element)?
                || (self.short_tag(value) == NULL_TAG && (map_is_new || !out.contains_key(&name)))
            {
                out.insert(name, element);
            }
        }
        self.merged_fields = merged;
        if let Some(merge) = merge {
            self.merge(id, merge, out)?;
        }
        Ok(true)
    }

    fn merge<T: Target>(&mut self, parent: Id, merge: Id, out: &mut T) -> R<()> {
        let fresh = self.merged_fields.is_none();
        if fresh {
            let mut keys = HashSet::new();
            for &(key, _) in self.entries(parent) {
                self.decode_count += 1;
                if let Some(key) = self.string_key(key)? {
                    keys.insert(key);
                }
            }
            self.merged_fields = Some(keys);
        }
        let want_map =
            || Fatal("yaml: map merge requires map or sequence of maps as the value".to_owned());
        let is_mapping =
            |decoder: &Self, id: Id| matches!(decoder.node(id).kind, NodeKind::Mapping(_));
        match &self.node(merge).kind {
            NodeKind::Mapping(_) => {
                self.unmarshal(merge, out)?;
            }
            NodeKind::Alias(target) => {
                if !is_mapping(self, *target) {
                    return Err(want_map());
                }
                self.unmarshal(merge, out)?;
            }
            NodeKind::Sequence(sources) => {
                for &source in sources {
                    let mapping = match self.node(source).kind {
                        NodeKind::Alias(target) => is_mapping(self, target),
                        NodeKind::Mapping(_) => true,
                        _ => false,
                    };
                    if !mapping {
                        return Err(want_map());
                    }
                    self.unmarshal(source, out)?;
                }
            }
            NodeKind::Scalar => return Err(want_map()),
        }
        if fresh {
            self.merged_fields = None;
        }
        Ok(())
    }

    /// A merge parent's key decoded into `interface{}`, when that yields a
    /// string (only string keys can match struct fields and string maps).
    fn string_key(&self, id: Id) -> R<Option<String>> {
        let node = self.node(id);
        match &node.kind {
            NodeKind::Alias(target) => self.string_key(*target),
            NodeKind::Scalar if indicated_string(node) => Ok(Some(node.value.clone())),
            NodeKind::Scalar => match resolve(&node.tag, &node.value)? {
                (tag, _) if tag == BINARY_TAG => Ok(None),
                (_, Resolved::Str(text)) => Ok(Some(text)),
                _ => Ok(None),
            },
            _ => Ok(None),
        }
    }
}

fn indicated_string(node: &Node) -> bool {
    node.kind == NodeKind::Scalar
        && (short_tag(&node.tag) == STR_TAG
            || ((node.tag.is_empty() || node.tag == "!") && node.styled))
}

fn allowed_alias_ratio(decode_count: usize) -> f64 {
    const LOW: usize = 400_000;
    const HIGH: usize = 4_000_000;
    if decode_count <= LOW {
        0.99
    } else if decode_count >= HIGH {
        0.10
    } else {
        0.99 - 0.89 * ((decode_count - LOW) as f64 / (HIGH - LOW) as f64)
    }
}

// -- primitive targets ----------------------------------------------------------

impl Target for String {
    fn type_name() -> String {
        "string".to_owned()
    }

    fn scalar(
        &mut self,
        decoder: &mut Decoder<'_>,
        id: Id,
        tag: &str,
        resolved: Resolved,
    ) -> R<bool> {
        *self = match resolved {
            Resolved::Str(text) if tag == BINARY_TAG => text,
            _ => decoder.node(id).value.clone(),
        };
        Ok(true)
    }
}

impl Target for bool {
    fn type_name() -> String {
        "bool".to_owned()
    }

    fn scalar(
        &mut self,
        decoder: &mut Decoder<'_>,
        id: Id,
        tag: &str,
        resolved: Resolved,
    ) -> R<bool> {
        let value = match &resolved {
            Resolved::Bool(value) => Some(*value),
            Resolved::Str(text) => match text.as_str() {
                "y" | "Y" | "yes" | "Yes" | "YES" | "on" | "On" | "ON" => Some(true),
                "n" | "N" | "no" | "No" | "NO" | "off" | "Off" | "OFF" => Some(false),
                _ => None,
            },
            _ => None,
        };
        match value {
            Some(value) => {
                *self = value;
                Ok(true)
            }
            None => {
                decoder.terror(id, tag, "bool");
                Ok(false)
            }
        }
    }
}

/// A 64-bit `int`.
impl Target for i64 {
    fn type_name() -> String {
        "int".to_owned()
    }

    fn scalar(
        &mut self,
        decoder: &mut Decoder<'_>,
        id: Id,
        tag: &str,
        resolved: Resolved,
    ) -> R<bool> {
        let value = match resolved {
            Resolved::Int(value) => Some(value),
            Resolved::Uint(value) => i64::try_from(value).ok(),
            // `resolved <= math.MaxInt64` then `int64(resolved)`.
            Resolved::Float(value) if value <= i64::MAX as f64 => Some(value as i64),
            _ => None,
        };
        match value {
            Some(value) => {
                *self = value;
                Ok(true)
            }
            None => {
                decoder.terror(id, tag, "int");
                Ok(false)
            }
        }
    }
}

/// An optional (pointer) field: `null` resets it to `None`; anything else
/// allocates the element before decoding into it (even when that then
/// fails).
impl<T: Target + Default> Target for Option<T> {
    fn type_name() -> String {
        format!("*{}", T::type_name())
    }

    fn qualified_name() -> String {
        format!("*{}", T::qualified_name())
    }

    fn decode(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        if decoder.short_tag(id) == NULL_TAG {
            return decoder.dispatch(id, self);
        }
        self.get_or_insert_with(T::default).decode(decoder, id)
    }

    fn null(&mut self) -> bool {
        *self = None;
        true
    }
}

impl<T: Target + Default> Target for Vec<T> {
    fn type_name() -> String {
        format!("[]{}", T::type_name())
    }

    fn qualified_name() -> String {
        format!("[]{}", T::qualified_name())
    }

    fn null(&mut self) -> bool {
        self.clear();
        true
    }

    fn sequence(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        let NodeKind::Sequence(entries) = &decoder.node(id).kind else {
            return Ok(false);
        };
        // yaml.v3 sizes the slice up front and truncates it to the decoded
        // elements; a fatal error leaves the zero-filled tail in place.
        let mut result: Vec<T> = Vec::with_capacity(entries.len());
        for &entry in entries {
            let mut element = T::default();
            match decoder.unmarshal(entry, &mut element) {
                Ok(true) => result.push(element),
                Ok(false) => {}
                Err(error) => {
                    result.resize_with(entries.len(), T::default);
                    *self = result;
                    return Err(error);
                }
            }
        }
        *self = result;
        Ok(true)
    }
}

impl Target for BTreeMap<String, String> {
    fn type_name() -> String {
        "map[string]string".to_owned()
    }

    fn null(&mut self) -> bool {
        self.clear();
        true
    }

    fn mapping(&mut self, decoder: &mut Decoder<'_>, id: Id) -> R<bool> {
        decoder.mapping_map(id, self)
    }
}

/// Implements [`Target`] and [`StructTarget`] for a record type.
macro_rules! struct_target {
    ($type:ty, $name:literal, { $($key:literal => $field:ident),* $(,)? }) => {
        impl $crate::yaml_decode::Target for $type {
            fn type_name() -> String {
                concat!("extension.", $name).to_owned()
            }

            fn qualified_name() -> String {
                concat!("extension.", $name).to_owned()
            }

            fn mapping(
                &mut self,
                decoder: &mut $crate::yaml_decode::Decoder<'_>,
                id: $crate::yaml_decode::Id,
            ) -> $crate::yaml_decode::R<bool> {
                decoder.mapping_struct(id, self)
            }
        }

        impl $crate::yaml_decode::StructTarget for $type {
            const FIELDS: &'static [&'static str] = &[$($key),*];

            #[allow(unused_assignments)]
            fn field(
                &mut self,
                decoder: &mut $crate::yaml_decode::Decoder<'_>,
                index: usize,
                value: $crate::yaml_decode::Id,
            ) -> $crate::yaml_decode::R<()> {
                let mut position = 0usize;
                $(
                    if index == position {
                        decoder.unmarshal(value, &mut self.$field)?;
                        return Ok(());
                    }
                    position += 1;
                )*
                Ok(())
            }
        }
    };
}

pub(crate) use struct_target;
