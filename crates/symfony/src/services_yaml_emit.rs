//! A port of the parts of `gopkg.in/yaml.v3` (v3.0.1) the services
//! conversion depends on: the node model, `Encoder.Encode` of a node tree
//! with a custom indent, scalar resolution, and the libyaml emitter. Output
//! is byte-identical to Go for the trees the converter builds (no anchors,
//! comments, or directives).

const STR_TAG: &str = "!!str";
const NULL_TAG: &str = "!!null";
const BOOL_TAG: &str = "!!bool";
const INT_TAG: &str = "!!int";
const FLOAT_TAG: &str = "!!float";
const TIMESTAMP_TAG: &str = "!!timestamp";
const MAP_TAG: &str = "!!map";
const SEQ_TAG: &str = "!!seq";
const MERGE_TAG: &str = "!!merge";
const LONG_TAG_PREFIX: &str = "tag:yaml.org,2002:";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum NodeKind {
    Scalar,
    Mapping,
    Sequence,
}

/// `yaml.Node` restricted to what the converter uses.
#[derive(Clone, Debug)]
pub(crate) struct Node {
    pub(crate) kind: NodeKind,
    pub(crate) tag: String,
    pub(crate) value: String,
    pub(crate) flow: bool,
    pub(crate) literal: bool,
    pub(crate) content: Vec<Node>,
}

impl Node {
    pub(crate) fn mapping() -> Node {
        Node {
            kind: NodeKind::Mapping,
            tag: MAP_TAG.to_owned(),
            value: String::new(),
            flow: false,
            literal: false,
            content: Vec::new(),
        }
    }

    pub(crate) fn sequence() -> Node {
        Node {
            kind: NodeKind::Sequence,
            tag: SEQ_TAG.to_owned(),
            ..Node::mapping()
        }
    }

    /// `Node.SetString`.
    pub(crate) fn string(value: &str) -> Node {
        Node {
            kind: NodeKind::Scalar,
            tag: STR_TAG.to_owned(),
            value: value.to_owned(),
            literal: value.contains('\n'),
            ..Node::mapping()
        }
    }

    pub(crate) fn null(representation: &str) -> Node {
        Node::tagged(NULL_TAG, representation)
    }

    pub(crate) fn tagged(tag: &str, value: &str) -> Node {
        Node {
            kind: NodeKind::Scalar,
            tag: tag.to_owned(),
            value: value.to_owned(),
            ..Node::mapping()
        }
    }

    /// A scalar as produced by `Node.Encode` of a non-string Go value: the
    /// formatted value re-parsed with its implicit tag.
    pub(crate) fn implicit(value: String) -> Node {
        let tag = resolve(&value).to_owned();
        Node {
            kind: NodeKind::Scalar,
            tag,
            value,
            ..Node::mapping()
        }
    }
}

fn short_tag(tag: &str) -> String {
    if let Some(rest) = tag.strip_prefix(LONG_TAG_PREFIX) {
        return format!("!!{rest}");
    }
    tag.to_owned()
}

fn long_tag(tag: &str) -> String {
    if let Some(rest) = tag.strip_prefix("!!") {
        return format!("{LONG_TAG_PREFIX}{rest}");
    }
    tag.to_owned()
}

// ---------------------------------------------------------------------------
// resolve.go

/// The implicit tag of a plain scalar (`resolve("", in)`).
pub(crate) fn resolve(input: &str) -> &'static str {
    let hint = match input.as_bytes().first() {
        None => b'N',
        Some(b'+' | b'-') => b'S',
        Some(b'0'..=b'9') => b'D',
        Some(b'y' | b'Y' | b'n' | b'N' | b't' | b'T' | b'f' | b'F' | b'o' | b'O' | b'~') => b'M',
        Some(b'.') => b'.',
        Some(_) => 0,
    };
    if hint == 0 {
        return STR_TAG;
    }
    match input {
        "true" | "True" | "TRUE" | "false" | "False" | "FALSE" => return BOOL_TAG,
        "" | "~" | "null" | "Null" | "NULL" => return NULL_TAG,
        ".nan" | ".NaN" | ".NAN" | ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF"
        | "-.inf" | "-.Inf" | "-.INF" => return FLOAT_TAG,
        "<<" => return MERGE_TAG,
        _ => {}
    }
    match hint {
        b'.' => {
            if go_parse_float(input).is_some() {
                return FLOAT_TAG;
            }
        }
        b'D' | b'S' => {
            if parse_timestamp(input) {
                return TIMESTAMP_TAG;
            }
            let plain = input.replace('_', "");
            if go_parse_int_base0(&plain).is_some() || go_parse_uint_base0(&plain).is_some() {
                return INT_TAG;
            }
            if yaml_style_float(&plain) && go_parse_float(&plain).is_some() {
                return FLOAT_TAG;
            }
            if let Some(rest) = plain.strip_prefix("0b") {
                if parse_digits(rest, 2).is_some() {
                    return INT_TAG;
                }
            } else if let Some(rest) = plain.strip_prefix("-0b")
                && parse_digits(rest, 2).is_some_and(|value| value <= i64::MAX as u128 + 1)
            {
                return INT_TAG;
            }
            if let Some(rest) = plain.strip_prefix("0o") {
                if parse_digits(rest, 8).is_some() {
                    return INT_TAG;
                }
            } else if let Some(rest) = plain.strip_prefix("-0o")
                && parse_digits(rest, 8).is_some_and(|value| value <= i64::MAX as u128 + 1)
            {
                return INT_TAG;
            }
        }
        _ => {}
    }
    STR_TAG
}

/// Digits in `radix` without sign or prefix, fitting in u64.
fn parse_digits(digits: &str, radix: u32) -> Option<u128> {
    if digits.is_empty() {
        return None;
    }
    let value = u128::from_str_radix(digits, radix).ok()?;
    (value <= u64::MAX as u128 && digits.bytes().all(|byte| (byte as char).is_digit(radix)))
        .then_some(value)
}

/// Go `strconv.ParseUint(s, 0, 64)` without underscores.
fn go_parse_uint_base0(value: &str) -> Option<u64> {
    let (digits, radix) = base_prefix(value);
    parse_digits(digits, radix).map(|value| value as u64)
}

fn base_prefix(value: &str) -> (&str, u32) {
    let bytes = value.as_bytes();
    if bytes.len() >= 2 && bytes[0] == b'0' {
        match bytes[1] {
            b'x' | b'X' => return (&value[2..], 16),
            b'o' | b'O' => return (&value[2..], 8),
            b'b' | b'B' => return (&value[2..], 2),
            _ => return (&value[1..], 8),
        }
    }
    (value, 10)
}

/// Go `strconv.ParseInt(s, 0, 64)` without underscores.
fn go_parse_int_base0(value: &str) -> Option<i64> {
    let (negative, rest) = match value.as_bytes().first() {
        Some(b'+') => (false, &value[1..]),
        Some(b'-') => (true, &value[1..]),
        _ => (false, value),
    };
    let magnitude = go_parse_uint_base0(rest)? as i128;
    let signed = if negative { -magnitude } else { magnitude };
    i64::try_from(signed).ok()
}

/// Go `strconv.ParseFloat(s, 64)` for decimal input: rejects overflow.
pub(crate) fn go_parse_float(value: &str) -> Option<f64> {
    let bytes = value.as_bytes();
    let body = value.trim_start_matches(['+', '-']);
    if bytes.is_empty()
        || value.len() - body.len() > 1
        || body.eq_ignore_ascii_case("inf")
        || body.eq_ignore_ascii_case("infinity")
        || body.eq_ignore_ascii_case("nan")
    {
        return None;
    }
    let parsed: f64 = value.parse().ok()?;
    parsed.is_finite().then_some(parsed)
}

fn yaml_style_float(value: &str) -> bool {
    // ^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$
    let bytes = value.as_bytes();
    let mut index = 0;
    if matches!(bytes.first(), Some(b'+' | b'-')) {
        index += 1;
    }
    let digits = |index: &mut usize| {
        let start = *index;
        while *index < bytes.len() && bytes[*index].is_ascii_digit() {
            *index += 1;
        }
        *index - start
    };
    if bytes.get(index) == Some(&b'.') {
        index += 1;
        if digits(&mut index) == 0 {
            return false;
        }
    } else {
        if digits(&mut index) == 0 {
            return false;
        }
        if bytes.get(index) == Some(&b'.') {
            index += 1;
            digits(&mut index);
        }
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

/// `parseTimestamp` for the layouts yaml.v3 accepts.
fn parse_timestamp(value: &str) -> bool {
    let bytes = value.as_bytes();
    let year_digits = bytes
        .iter()
        .take_while(|byte| byte.is_ascii_digit())
        .count();
    if year_digits != 4 || year_digits == bytes.len() || bytes[4] != b'-' {
        return false;
    }
    let mut index = 5;
    let number = |index: &mut usize, max_digits: usize| -> Option<u32> {
        let start = *index;
        while *index < bytes.len() && *index - start < max_digits && bytes[*index].is_ascii_digit()
        {
            *index += 1;
        }
        if *index == start {
            return None;
        }
        value[start..*index].parse().ok()
    };
    let year: u32 = value[..4].parse().unwrap_or(0);
    let Some(month) = number(&mut index, 2) else {
        return false;
    };
    if bytes.get(index) != Some(&b'-') || !(1..=12).contains(&month) {
        return false;
    }
    index += 1;
    let Some(day) = number(&mut index, 2) else {
        return false;
    };
    let leap = year.is_multiple_of(4) && (!year.is_multiple_of(100) || year.is_multiple_of(400));
    let days = [
        31,
        if leap { 29 } else { 28 },
        31,
        30,
        31,
        30,
        31,
        31,
        30,
        31,
        30,
        31,
    ][month as usize - 1];
    if day < 1 || day > days {
        return false;
    }
    if index == bytes.len() {
        return true;
    }
    let separator = bytes[index];
    if !matches!(separator, b'T' | b't' | b' ') {
        return false;
    }
    index += 1;
    let Some(hour) = number(&mut index, 2) else {
        return false;
    };
    if bytes.get(index) != Some(&b':') || hour > 23 {
        return false;
    }
    index += 1;
    let Some(minute) = number(&mut index, 2) else {
        return false;
    };
    if bytes.get(index) != Some(&b':') || minute > 59 {
        return false;
    }
    index += 1;
    let Some(second) = number(&mut index, 2) else {
        return false;
    };
    if second > 59 {
        return false;
    }
    if index + 1 < bytes.len()
        && matches!(bytes[index], b'.' | b',')
        && bytes[index + 1].is_ascii_digit()
    {
        index += 1;
        while index < bytes.len() && bytes[index].is_ascii_digit() {
            index += 1;
        }
    }
    if separator == b' ' {
        return index == bytes.len();
    }
    match bytes.get(index) {
        Some(b'Z') => index + 1 == bytes.len(),
        Some(b'+' | b'-') => {
            let zone = &bytes[index + 1..];
            zone.len() == 5
                && zone[2] == b':'
                && zone[..2].iter().chain(&zone[3..]).all(u8::is_ascii_digit)
        }
        _ => false,
    }
}

/// Go `strconv.FormatFloat(value, 'g', -1, 64)` as yaml.v3 renders floats.
pub(crate) fn format_float(value: f64) -> String {
    if value.is_nan() {
        return ".nan".to_owned();
    }
    if value.is_infinite() {
        return if value > 0.0 { ".inf" } else { "-.inf" }.to_owned();
    }
    if value == 0.0 {
        return if value.is_sign_negative() { "-0" } else { "0" }.to_owned();
    }
    // Shortest round-trip digits and decimal exponent from Rust's formatter.
    let scientific = format!("{:e}", value.abs());
    let (mantissa, exponent) = scientific.split_once('e').unwrap_or((&scientific, "0"));
    let exponent: i32 = exponent.parse().unwrap_or(0);
    let digits: String = mantissa.chars().filter(char::is_ascii_digit).collect();
    let sign = if value < 0.0 { "-" } else { "" };
    // Shortest formatting decides on %e with precision 6.
    if !(-4..6).contains(&exponent) {
        let mut out = String::from(sign);
        out.push_str(&digits[..1]);
        if digits.len() > 1 {
            out.push('.');
            out.push_str(&digits[1..]);
        }
        out.push('e');
        out.push(if exponent < 0 { '-' } else { '+' });
        let magnitude = exponent.unsigned_abs();
        if magnitude < 10 {
            out.push('0');
        }
        out.push_str(&magnitude.to_string());
        return out;
    }
    let mut out = String::from(sign);
    if exponent < 0 {
        out.push_str("0.");
        for _ in 0..(-exponent - 1) {
            out.push('0');
        }
        out.push_str(&digits);
    } else {
        let point = exponent as usize + 1;
        if digits.len() <= point {
            out.push_str(&digits);
            for _ in digits.len()..point {
                out.push('0');
            }
        } else {
            out.push_str(&digits[..point]);
            out.push('.');
            out.push_str(&digits[point..]);
        }
    }
    out
}

// ---------------------------------------------------------------------------
// encode.go: node → events

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ScalarStyle {
    Plain,
    SingleQuoted,
    DoubleQuoted,
    Literal,
}

#[derive(Clone, Debug)]
enum Event {
    StreamStart,
    StreamEnd,
    DocumentStart,
    DocumentEnd,
    Scalar {
        tag: String,
        value: String,
        implicit: bool,
        style: ScalarStyle,
    },
    SequenceStart {
        tag: String,
        implicit: bool,
        flow: bool,
    },
    SequenceEnd,
    MappingStart {
        tag: String,
        implicit: bool,
        flow: bool,
    },
    MappingEnd,
}

fn node_events(node: &Node, events: &mut Vec<Event>) {
    let mut tag = node.tag.clone();
    let stag = short_tag(&tag);
    let mut force_quoting = false;
    if !tag.is_empty() {
        match node.kind {
            NodeKind::Scalar => {
                if stag == STR_TAG && node.literal {
                    tag.clear();
                } else {
                    let rtag = resolve(&node.value);
                    if rtag == stag {
                        tag.clear();
                    } else if stag == STR_TAG {
                        tag.clear();
                        force_quoting = true;
                    }
                }
            }
            NodeKind::Mapping => {
                if stag == MAP_TAG {
                    tag.clear();
                }
            }
            NodeKind::Sequence => {
                if stag == SEQ_TAG {
                    tag.clear();
                }
            }
        }
    }
    match node.kind {
        NodeKind::Sequence => {
            events.push(Event::SequenceStart {
                tag: long_tag(&tag),
                implicit: tag.is_empty(),
                flow: node.flow,
            });
            for child in &node.content {
                node_events(child, events);
            }
            events.push(Event::SequenceEnd);
        }
        NodeKind::Mapping => {
            events.push(Event::MappingStart {
                tag: long_tag(&tag),
                implicit: tag.is_empty(),
                flow: node.flow,
            });
            for child in &node.content {
                node_events(child, events);
            }
            events.push(Event::MappingEnd);
        }
        NodeKind::Scalar => {
            let style = if node.literal || node.value.contains('\n') {
                ScalarStyle::Literal
            } else if force_quoting {
                ScalarStyle::DoubleQuoted
            } else {
                ScalarStyle::Plain
            };
            let implicit = tag.is_empty();
            events.push(Event::Scalar {
                tag: if implicit {
                    String::new()
                } else {
                    long_tag(&tag)
                },
                value: node.value.clone(),
                implicit,
                style,
            });
        }
    }
}

/// `yaml.NewEncoder` + `SetIndent(indent)` + `Encode(node)` + `Close()`.
pub(crate) fn encode(node: &Node, indent: usize) -> String {
    let mut events = vec![Event::StreamStart, Event::DocumentStart];
    node_events(node, &mut events);
    events.push(Event::DocumentEnd);
    events.push(Event::StreamEnd);
    let mut emitter = Emitter::new(indent as i32, events);
    emitter.run();
    String::from_utf8(emitter.out).unwrap_or_default()
}

// ---------------------------------------------------------------------------
// emitterc.go

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum State {
    StreamStart,
    FirstDocumentStart,
    DocumentStart,
    DocumentContent,
    DocumentEnd,
    FlowSequenceFirstItem,
    FlowSequenceItem,
    FlowMappingFirstKey,
    FlowMappingKey,
    FlowMappingSimpleValue,
    FlowMappingValue,
    BlockSequenceFirstItem,
    BlockSequenceItem,
    BlockMappingFirstKey,
    BlockMappingKey,
    BlockMappingSimpleValue,
    BlockMappingValue,
    End,
}

#[derive(Default)]
struct ScalarData {
    value: Vec<u8>,
    multiline: bool,
    flow_plain_allowed: bool,
    block_plain_allowed: bool,
    single_quoted_allowed: bool,
    block_allowed: bool,
}

struct Emitter {
    events: Vec<Event>,
    head: usize,
    out: Vec<u8>,
    best_indent: i32,
    state: State,
    states: Vec<State>,
    indents: Vec<i32>,
    indent: i32,
    flow_level: i32,
    root_context: bool,
    simple_key_context: bool,
    column: i32,
    whitespace: bool,
    indention: bool,
    open_ended: bool,
    foot_indent: i32,
    tag_handle: Vec<u8>,
    tag_suffix: Vec<u8>,
    scalar: ScalarData,
    style: ScalarStyle,
}

fn byte_at(value: &[u8], index: usize) -> u8 {
    value.get(index).copied().unwrap_or(0)
}

fn width(byte: u8) -> usize {
    if byte & 0x80 == 0 {
        1
    } else if byte & 0xE0 == 0xC0 {
        2
    } else if byte & 0xF0 == 0xE0 {
        3
    } else if byte & 0xF8 == 0xF0 {
        4
    } else {
        0
    }
}

fn is_printable(b: &[u8], i: usize) -> bool {
    let c = b[i];
    let n1 = byte_at(b, i + 1);
    let n2 = byte_at(b, i + 2);
    c == 0x0A
        || (0x20..=0x7E).contains(&c)
        || (c == 0xC2 && n1 >= 0xA0)
        || (c > 0xC2 && c < 0xED)
        || (c == 0xED && n1 < 0xA0)
        || c == 0xEE
        || (c == 0xEF && !(n1 == 0xBB && n2 == 0xBF) && !(n1 == 0xBF && (n2 == 0xBE || n2 == 0xBF)))
}

fn is_break(b: &[u8], i: usize) -> bool {
    let c = byte_at(b, i);
    c == b'\r'
        || c == b'\n'
        || (c == 0xC2 && byte_at(b, i + 1) == 0x85)
        || (c == 0xE2 && byte_at(b, i + 1) == 0x80 && matches!(byte_at(b, i + 2), 0xA8 | 0xA9))
}

fn is_blank(b: &[u8], i: usize) -> bool {
    matches!(byte_at(b, i), b' ' | b'\t')
}

fn is_blankz(b: &[u8], i: usize) -> bool {
    is_blank(b, i) || is_break(b, i) || byte_at(b, i) == 0
}

fn is_bom(b: &[u8]) -> bool {
    b.len() >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF
}

fn is_alpha(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-'
}

impl Emitter {
    fn new(best_indent: i32, events: Vec<Event>) -> Self {
        Emitter {
            events,
            head: 0,
            out: Vec::new(),
            best_indent,
            state: State::StreamStart,
            states: Vec::new(),
            indents: Vec::new(),
            indent: -1,
            flow_level: 0,
            root_context: false,
            simple_key_context: false,
            column: 0,
            whitespace: true,
            indention: true,
            open_ended: false,
            foot_indent: -1,
            tag_handle: Vec::new(),
            tag_suffix: Vec::new(),
            scalar: ScalarData::default(),
            style: ScalarStyle::Plain,
        }
    }

    fn run(&mut self) {
        while self.head < self.events.len() {
            let event = self.events[self.head].clone();
            self.analyze_event(&event);
            self.state_machine(&event);
            self.head += 1;
        }
    }

    fn put(&mut self, byte: u8) {
        self.out.push(byte);
        self.column += 1;
    }

    fn put_break(&mut self) {
        self.out.push(b'\n');
        self.column = 0;
        self.indention = true;
    }

    fn write(&mut self, value: &[u8], index: &mut usize) {
        let w = width(value[*index]).max(1);
        self.out.extend_from_slice(&value[*index..*index + w]);
        self.column += 1;
        *index += w;
    }

    fn write_all(&mut self, value: &[u8]) {
        let mut index = 0;
        while index < value.len() {
            self.write(value, &mut index);
        }
    }

    fn write_break(&mut self, value: &[u8], index: &mut usize) {
        if value[*index] == b'\n' {
            self.put_break();
            *index += 1;
        } else {
            self.write(value, index);
            self.column = 0;
            self.indention = true;
        }
    }

    fn pop_state(&mut self) {
        self.state = self.states.pop().unwrap_or(State::End);
    }

    fn pop_indent(&mut self) {
        self.indent = self.indents.pop().unwrap_or(-1);
    }

    fn increase_indent(&mut self, flow: bool, indentless: bool) {
        self.indents.push(self.indent);
        if self.indent < 0 {
            self.indent = if flow { self.best_indent } else { 0 };
        } else if !indentless {
            if self.states.last() == Some(&State::BlockSequenceItem) {
                self.indent += 2;
            } else {
                self.indent =
                    self.best_indent * ((self.indent + self.best_indent) / self.best_indent);
            }
        }
    }

    fn write_indent(&mut self) {
        let indent = self.indent.max(0);
        if !self.indention || self.column > indent || (self.column == indent && !self.whitespace) {
            self.put_break();
        }
        if self.foot_indent == indent {
            self.put_break();
        }
        while self.column < indent {
            self.put(b' ');
        }
        self.whitespace = true;
        self.foot_indent = -1;
    }

    fn write_indicator(
        &mut self,
        indicator: &[u8],
        need_whitespace: bool,
        is_whitespace: bool,
        is_indention: bool,
    ) {
        if need_whitespace && !self.whitespace {
            self.put(b' ');
        }
        self.write_all(indicator);
        self.whitespace = is_whitespace;
        self.indention = self.indention && is_indention;
        self.open_ended = false;
    }

    fn analyze_event(&mut self, event: &Event) {
        self.tag_handle.clear();
        self.tag_suffix.clear();
        self.scalar.value.clear();
        match event {
            Event::Scalar {
                tag,
                value,
                implicit,
                ..
            } => {
                if !tag.is_empty() && !implicit {
                    self.analyze_tag(tag);
                }
                self.analyze_scalar(value.as_bytes());
            }
            Event::SequenceStart { tag, implicit, .. }
            | Event::MappingStart { tag, implicit, .. }
                if !tag.is_empty() && !implicit =>
            {
                self.analyze_tag(tag);
            }
            _ => {}
        }
    }

    fn analyze_tag(&mut self, tag: &str) {
        for (handle, prefix) in [("!", "!"), ("!!", LONG_TAG_PREFIX)] {
            if let Some(suffix) = tag.strip_prefix(prefix) {
                self.tag_handle = handle.as_bytes().to_vec();
                self.tag_suffix = suffix.as_bytes().to_vec();
                return;
            }
        }
        self.tag_suffix = tag.as_bytes().to_vec();
    }

    fn analyze_scalar(&mut self, value: &[u8]) {
        let scalar = &mut self.scalar;
        scalar.value = value.to_vec();
        if value.is_empty() {
            scalar.multiline = false;
            scalar.flow_plain_allowed = false;
            scalar.block_plain_allowed = true;
            scalar.single_quoted_allowed = true;
            scalar.block_allowed = false;
            return;
        }
        let mut block_indicators = false;
        let mut flow_indicators = false;
        let mut line_breaks = false;
        let mut special_characters = false;
        let mut tab_characters = false;
        let mut leading_space = false;
        let mut leading_break = false;
        let mut trailing_space = false;
        let mut trailing_break = false;
        let mut break_space = false;
        let mut space_break = false;
        let mut previous_space = false;
        let mut previous_break = false;
        if value.len() >= 3 && (value.starts_with(b"---") || value.starts_with(b"...")) {
            block_indicators = true;
            flow_indicators = true;
        }
        let mut preceded_by_whitespace = true;
        let mut i = 0;
        while i < value.len() {
            let w = width(value[i]).max(1);
            let followed_by_whitespace = i + w >= value.len() || is_blank(value, i + w);
            if i == 0 {
                match value[i] {
                    b'#' | b',' | b'[' | b']' | b'{' | b'}' | b'&' | b'*' | b'!' | b'|' | b'>'
                    | b'\'' | b'"' | b'%' | b'@' | b'`' => {
                        flow_indicators = true;
                        block_indicators = true;
                    }
                    b'?' | b':' => {
                        flow_indicators = true;
                        if followed_by_whitespace {
                            block_indicators = true;
                        }
                    }
                    b'-' if followed_by_whitespace => {
                        flow_indicators = true;
                        block_indicators = true;
                    }
                    _ => {}
                }
            } else {
                match value[i] {
                    b',' | b'?' | b'[' | b']' | b'{' | b'}' => flow_indicators = true,
                    b':' => {
                        flow_indicators = true;
                        if followed_by_whitespace {
                            block_indicators = true;
                        }
                    }
                    b'#' if preceded_by_whitespace => {
                        flow_indicators = true;
                        block_indicators = true;
                    }
                    _ => {}
                }
            }
            if value[i] == b'\t' {
                tab_characters = true;
            } else if !is_printable(value, i) {
                special_characters = true;
            }
            if value[i] == b' ' {
                if i == 0 {
                    leading_space = true;
                }
                if i + w == value.len() {
                    trailing_space = true;
                }
                if previous_break {
                    break_space = true;
                }
                previous_space = true;
                previous_break = false;
            } else if is_break(value, i) {
                line_breaks = true;
                if i == 0 {
                    leading_break = true;
                }
                if i + w == value.len() {
                    trailing_break = true;
                }
                if previous_space {
                    space_break = true;
                }
                previous_space = false;
                previous_break = true;
            } else {
                previous_space = false;
                previous_break = false;
            }
            preceded_by_whitespace = is_blankz(value, i);
            i += w;
        }
        scalar.multiline = line_breaks;
        scalar.flow_plain_allowed = true;
        scalar.block_plain_allowed = true;
        scalar.single_quoted_allowed = true;
        scalar.block_allowed = true;
        if leading_space || leading_break || trailing_space || trailing_break {
            scalar.flow_plain_allowed = false;
            scalar.block_plain_allowed = false;
        }
        if trailing_space {
            scalar.block_allowed = false;
        }
        if break_space {
            scalar.flow_plain_allowed = false;
            scalar.block_plain_allowed = false;
            scalar.single_quoted_allowed = false;
        }
        if space_break || tab_characters || special_characters {
            scalar.flow_plain_allowed = false;
            scalar.block_plain_allowed = false;
            scalar.single_quoted_allowed = false;
        }
        if space_break || special_characters {
            scalar.block_allowed = false;
        }
        if line_breaks {
            scalar.flow_plain_allowed = false;
            scalar.block_plain_allowed = false;
        }
        if flow_indicators {
            scalar.flow_plain_allowed = false;
        }
        if block_indicators {
            scalar.block_plain_allowed = false;
        }
    }

    fn state_machine(&mut self, event: &Event) {
        match self.state {
            State::StreamStart => {
                self.indent = -1;
                self.column = 0;
                self.whitespace = true;
                self.indention = true;
                self.foot_indent = -1;
                self.state = State::FirstDocumentStart;
            }
            State::FirstDocumentStart | State::DocumentStart => {
                if let Event::DocumentStart = event {
                    let implicit = self.state == State::FirstDocumentStart;
                    if !implicit {
                        self.write_indent();
                        self.write_indicator(b"---", true, false, false);
                        self.write_indent();
                    }
                    self.state = State::DocumentContent;
                } else {
                    // yaml.v3 clears open_ended before STREAM-END.
                    self.state = State::End;
                }
            }
            State::DocumentContent => {
                self.states.push(State::DocumentEnd);
                self.emit_node(event, true, false, false, false);
            }
            State::DocumentEnd => {
                self.foot_indent = -1;
                self.write_indent();
                self.state = State::DocumentStart;
            }
            State::FlowSequenceFirstItem => self.emit_flow_sequence_item(event, true),
            State::FlowSequenceItem => self.emit_flow_sequence_item(event, false),
            State::FlowMappingFirstKey => self.emit_flow_mapping_key(event, true),
            State::FlowMappingKey => self.emit_flow_mapping_key(event, false),
            State::FlowMappingSimpleValue => self.emit_flow_mapping_value(event, true),
            State::FlowMappingValue => self.emit_flow_mapping_value(event, false),
            State::BlockSequenceFirstItem => self.emit_block_sequence_item(event, true),
            State::BlockSequenceItem => self.emit_block_sequence_item(event, false),
            State::BlockMappingFirstKey => self.emit_block_mapping_key(event, true),
            State::BlockMappingKey => self.emit_block_mapping_key(event, false),
            State::BlockMappingSimpleValue => self.emit_block_mapping_value(event, true),
            State::BlockMappingValue => self.emit_block_mapping_value(event, false),
            State::End => {}
        }
    }

    fn emit_flow_sequence_item(&mut self, event: &Event, first: bool) {
        if first {
            self.write_indicator(b"[", true, true, false);
            self.increase_indent(true, false);
            self.flow_level += 1;
        }
        if let Event::SequenceEnd = event {
            self.flow_level -= 1;
            self.pop_indent();
            if self.column == 0 {
                self.write_indent();
            }
            self.write_indicator(b"]", false, false, false);
            self.pop_state();
            return;
        }
        if !first {
            self.write_indicator(b",", false, false, false);
        }
        if self.column == 0 {
            self.write_indent();
        }
        self.states.push(State::FlowSequenceItem);
        self.emit_node(event, false, true, false, false);
    }

    fn emit_flow_mapping_key(&mut self, event: &Event, first: bool) {
        if first {
            self.write_indicator(b"{", true, true, false);
            self.increase_indent(true, false);
            self.flow_level += 1;
        }
        if let Event::MappingEnd = event {
            self.flow_level -= 1;
            self.pop_indent();
            self.write_indicator(b"}", false, false, false);
            self.pop_state();
            return;
        }
        if !first {
            self.write_indicator(b",", false, false, false);
        }
        if self.column == 0 {
            self.write_indent();
        }
        if self.check_simple_key() {
            self.states.push(State::FlowMappingSimpleValue);
            self.emit_node(event, false, false, true, true);
            return;
        }
        self.write_indicator(b"?", true, false, false);
        self.states.push(State::FlowMappingValue);
        self.emit_node(event, false, false, true, false);
    }

    fn emit_flow_mapping_value(&mut self, event: &Event, simple: bool) {
        if simple {
            self.write_indicator(b":", false, false, false);
        } else {
            self.write_indicator(b":", true, false, false);
        }
        self.states.push(State::FlowMappingKey);
        self.emit_node(event, false, false, true, false);
    }

    fn emit_block_sequence_item(&mut self, event: &Event, first: bool) {
        if first {
            self.increase_indent(false, false);
        }
        if let Event::SequenceEnd = event {
            self.pop_indent();
            self.pop_state();
            return;
        }
        self.write_indent();
        self.write_indicator(b"-", true, false, true);
        self.states.push(State::BlockSequenceItem);
        self.emit_node(event, false, true, false, false);
    }

    fn emit_block_mapping_key(&mut self, event: &Event, first: bool) {
        if first {
            self.increase_indent(false, false);
        }
        if let Event::MappingEnd = event {
            self.pop_indent();
            self.pop_state();
            return;
        }
        self.write_indent();
        if self.check_simple_key() {
            self.states.push(State::BlockMappingSimpleValue);
            self.emit_node(event, false, false, true, true);
            return;
        }
        self.write_indicator(b"?", true, false, true);
        self.states.push(State::BlockMappingValue);
        self.emit_node(event, false, false, true, false);
    }

    fn emit_block_mapping_value(&mut self, event: &Event, simple: bool) {
        if simple {
            self.write_indicator(b":", false, false, false);
        } else {
            self.write_indent();
            self.write_indicator(b":", true, false, true);
        }
        self.states.push(State::BlockMappingKey);
        self.emit_node(event, false, false, true, false);
    }

    fn emit_node(
        &mut self,
        event: &Event,
        root: bool,
        _sequence: bool,
        _mapping: bool,
        simple_key: bool,
    ) {
        self.root_context = root;
        self.simple_key_context = simple_key;
        match event {
            Event::Scalar { .. } => self.emit_scalar(event),
            Event::SequenceStart { flow, .. } => {
                self.process_tag();
                self.state = if self.flow_level > 0 || *flow || self.check_empty_sequence() {
                    State::FlowSequenceFirstItem
                } else {
                    State::BlockSequenceFirstItem
                };
            }
            Event::MappingStart { flow, .. } => {
                self.process_tag();
                self.state = if self.flow_level > 0 || *flow || self.check_empty_mapping() {
                    State::FlowMappingFirstKey
                } else {
                    State::BlockMappingFirstKey
                };
            }
            _ => {}
        }
    }

    fn emit_scalar(&mut self, event: &Event) {
        self.select_scalar_style(event);
        self.process_tag();
        self.increase_indent(true, false);
        self.process_scalar();
        self.pop_indent();
        self.pop_state();
    }

    fn check_empty_sequence(&self) -> bool {
        matches!(
            self.events.get(self.head),
            Some(Event::SequenceStart { .. })
        ) && matches!(self.events.get(self.head + 1), Some(Event::SequenceEnd))
    }

    fn check_empty_mapping(&self) -> bool {
        matches!(self.events.get(self.head), Some(Event::MappingStart { .. }))
            && matches!(self.events.get(self.head + 1), Some(Event::MappingEnd))
    }

    fn check_simple_key(&self) -> bool {
        let tag_length = self.tag_handle.len() + self.tag_suffix.len();
        let length = match &self.events[self.head] {
            Event::Scalar { .. } => {
                if self.scalar.multiline {
                    return false;
                }
                tag_length + self.scalar.value.len()
            }
            Event::SequenceStart { .. } => {
                if !self.check_empty_sequence() {
                    return false;
                }
                tag_length
            }
            Event::MappingStart { .. } => {
                if !self.check_empty_mapping() {
                    return false;
                }
                tag_length
            }
            _ => return false,
        };
        length <= 128
    }

    fn select_scalar_style(&mut self, event: &Event) {
        let Event::Scalar {
            implicit, style, ..
        } = event
        else {
            return;
        };
        let no_tag = self.tag_handle.is_empty() && self.tag_suffix.is_empty();
        let mut style = *style;
        if self.simple_key_context && self.scalar.multiline {
            style = ScalarStyle::DoubleQuoted;
        }
        if style == ScalarStyle::Plain {
            if (self.flow_level > 0 && !self.scalar.flow_plain_allowed)
                || (self.flow_level == 0 && !self.scalar.block_plain_allowed)
            {
                style = ScalarStyle::SingleQuoted;
            }
            if self.scalar.value.is_empty() && (self.flow_level > 0 || self.simple_key_context) {
                style = ScalarStyle::SingleQuoted;
            }
            if no_tag && !implicit {
                style = ScalarStyle::SingleQuoted;
            }
        }
        if style == ScalarStyle::SingleQuoted && !self.scalar.single_quoted_allowed {
            style = ScalarStyle::DoubleQuoted;
        }
        if style == ScalarStyle::Literal
            && (!self.scalar.block_allowed || self.flow_level > 0 || self.simple_key_context)
        {
            style = ScalarStyle::DoubleQuoted;
        }
        if no_tag && !implicit && style != ScalarStyle::Plain {
            self.tag_handle = b"!".to_vec();
        }
        self.style = style;
    }

    fn process_tag(&mut self) {
        if self.tag_handle.is_empty() && self.tag_suffix.is_empty() {
            return;
        }
        if !self.tag_handle.is_empty() {
            let handle = self.tag_handle.clone();
            if !self.whitespace {
                self.put(b' ');
            }
            self.write_all(&handle);
            self.whitespace = false;
            self.indention = false;
            if !self.tag_suffix.is_empty() {
                let suffix = self.tag_suffix.clone();
                self.write_tag_content(&suffix, false);
            }
        } else {
            let suffix = self.tag_suffix.clone();
            self.write_indicator(b"!<", true, false, false);
            self.write_tag_content(&suffix, false);
            self.write_indicator(b">", false, false, false);
        }
    }

    fn write_tag_content(&mut self, value: &[u8], need_whitespace: bool) {
        if need_whitespace && !self.whitespace {
            self.put(b' ');
        }
        let mut i = 0;
        while i < value.len() {
            let must_write = matches!(
                value[i],
                b';' | b'/'
                    | b'?'
                    | b':'
                    | b'@'
                    | b'&'
                    | b'='
                    | b'+'
                    | b'$'
                    | b','
                    | b'_'
                    | b'.'
                    | b'~'
                    | b'*'
                    | b'\''
                    | b'('
                    | b')'
                    | b'['
                    | b']'
            ) || is_alpha(value[i]);
            if must_write {
                self.write(value, &mut i);
            } else {
                let w = width(value[i]).max(1);
                for _ in 0..w {
                    let octet = value[i];
                    i += 1;
                    self.put(b'%');
                    for nibble in [octet >> 4, octet & 0x0F] {
                        self.put(if nibble < 10 {
                            b'0' + nibble
                        } else {
                            b'A' + nibble - 10
                        });
                    }
                }
            }
        }
        self.whitespace = false;
        self.indention = false;
    }

    fn process_scalar(&mut self) {
        let value = std::mem::take(&mut self.scalar.value);
        match self.style {
            ScalarStyle::Plain => self.write_plain_scalar(&value),
            ScalarStyle::SingleQuoted => self.write_single_quoted_scalar(&value),
            ScalarStyle::DoubleQuoted => self.write_double_quoted_scalar(&value),
            ScalarStyle::Literal => self.write_literal_scalar(&value),
        }
        self.scalar.value = value;
    }

    fn write_plain_scalar(&mut self, value: &[u8]) {
        if !value.is_empty() && !self.whitespace {
            self.put(b' ');
        }
        let mut breaks = false;
        let mut i = 0;
        while i < value.len() {
            if value[i] == b' ' {
                self.write(value, &mut i);
            } else if is_break(value, i) {
                if !breaks && value[i] == b'\n' {
                    self.put_break();
                }
                self.write_break(value, &mut i);
                breaks = true;
            } else {
                if breaks {
                    self.write_indent();
                }
                self.write(value, &mut i);
                self.indention = false;
                breaks = false;
            }
        }
        if !value.is_empty() {
            self.whitespace = false;
        }
        self.indention = false;
        if self.root_context {
            self.open_ended = true;
        }
    }

    fn write_single_quoted_scalar(&mut self, value: &[u8]) {
        self.write_indicator(b"'", true, false, false);
        let mut breaks = false;
        let mut i = 0;
        while i < value.len() {
            if value[i] == b' ' {
                self.write(value, &mut i);
            } else if is_break(value, i) {
                if !breaks && value[i] == b'\n' {
                    self.put_break();
                }
                self.write_break(value, &mut i);
                breaks = true;
            } else {
                if breaks {
                    self.write_indent();
                }
                if value[i] == b'\'' {
                    self.put(b'\'');
                }
                self.write(value, &mut i);
                self.indention = false;
                breaks = false;
            }
        }
        self.write_indicator(b"'", false, false, false);
        self.whitespace = false;
        self.indention = false;
    }

    fn write_double_quoted_scalar(&mut self, value: &[u8]) {
        self.write_indicator(b"\"", true, false, false);
        let bom = is_bom(value);
        let mut i = 0;
        while i < value.len() {
            if !is_printable(value, i)
                || bom
                || is_break(value, i)
                || value[i] == b'"'
                || value[i] == b'\\'
            {
                let octet = value[i];
                let (w, mut v): (usize, u32) = if octet & 0x80 == 0 {
                    (1, u32::from(octet & 0x7F))
                } else if octet & 0xE0 == 0xC0 {
                    (2, u32::from(octet & 0x1F))
                } else if octet & 0xF0 == 0xE0 {
                    (3, u32::from(octet & 0x0F))
                } else {
                    (4, u32::from(octet & 0x07))
                };
                for k in 1..w {
                    v = (v << 6) + u32::from(byte_at(value, i + k) & 0x3F);
                }
                i += w;
                self.put(b'\\');
                let short = match v {
                    0x00 => Some(b'0'),
                    0x07 => Some(b'a'),
                    0x08 => Some(b'b'),
                    0x09 => Some(b't'),
                    0x0A => Some(b'n'),
                    0x0B => Some(b'v'),
                    0x0C => Some(b'f'),
                    0x0D => Some(b'r'),
                    0x1B => Some(b'e'),
                    0x22 => Some(b'"'),
                    0x5C => Some(b'\\'),
                    0x85 => Some(b'N'),
                    0xA0 => Some(b'_'),
                    0x2028 => Some(b'L'),
                    0x2029 => Some(b'P'),
                    _ => None,
                };
                match short {
                    Some(byte) => self.put(byte),
                    None => {
                        let (marker, digits) = if v <= 0xFF {
                            (b'x', 2)
                        } else if v <= 0xFFFF {
                            (b'u', 4)
                        } else {
                            (b'U', 8)
                        };
                        self.put(marker);
                        for k in (0..digits).rev() {
                            let digit = ((v >> (k * 4)) & 0x0F) as u8;
                            self.put(if digit < 10 {
                                b'0' + digit
                            } else {
                                b'A' + digit - 10
                            });
                        }
                    }
                }
            } else {
                self.write(value, &mut i);
            }
        }
        self.write_indicator(b"\"", false, false, false);
        self.whitespace = false;
        self.indention = false;
    }

    fn write_block_scalar_hints(&mut self, value: &[u8]) {
        if byte_at(value, 0) == b' ' || is_break(value, 0) {
            let hint = [b'0' + self.best_indent as u8];
            self.write_indicator(&hint, false, false, false);
        }
        self.open_ended = false;
        let mut chomp = 0u8;
        if value.is_empty() {
            chomp = b'-';
        } else {
            let mut i = value.len() - 1;
            while value[i] & 0xC0 == 0x80 {
                i -= 1;
            }
            if !is_break(value, i) {
                chomp = b'-';
            } else if i == 0 {
                chomp = b'+';
                self.open_ended = true;
            } else {
                i -= 1;
                while value[i] & 0xC0 == 0x80 {
                    i -= 1;
                }
                if is_break(value, i) {
                    chomp = b'+';
                    self.open_ended = true;
                }
            }
        }
        if chomp != 0 {
            self.write_indicator(&[chomp], false, false, false);
        }
    }

    fn write_literal_scalar(&mut self, value: &[u8]) {
        self.write_indicator(b"|", true, false, false);
        self.write_block_scalar_hints(value);
        self.whitespace = true;
        let mut breaks = true;
        let mut i = 0;
        while i < value.len() {
            if is_break(value, i) {
                self.write_break(value, &mut i);
                breaks = true;
            } else {
                if breaks {
                    self.write_indent();
                }
                self.write(value, &mut i);
                self.indention = false;
                breaks = false;
            }
        }
    }
}
