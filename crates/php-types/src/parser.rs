//! Parser for native PHP and common PHPDoc type syntax.

use std::fmt;

use crate::gostr::{go_quote, go_unquote, is_space_byte};
use crate::render::is_type_name_byte;
use crate::ty::{CallableParameter, Name, ShapeField, Type};

/// A type syntax error, rendered like Go's
/// `parse PHP type at byte N: message`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParseError {
    pub position: usize,
    pub message: String,
}

impl fmt::Display for ParseError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "parse PHP type at byte {}: {}",
            self.position, self.message
        )
    }
}

impl std::error::Error for ParseError {}

type ParseResult = Result<Type, ParseError>;

/// Parses native PHP and common PHPDoc type syntax into a canonical type.
/// Empty input yields `unknown`.
pub fn parse(source: &str) -> ParseResult {
    parse_with(source, true)
}

/// Parses a language-level PHP type. Unlike PHPDoc parsing, names such as
/// `T` or `TValue` are treated as class names.
pub fn parse_native(source: &str) -> ParseResult {
    parse_with(source, false)
}

/// Parses a type and panics on invalid syntax. Intended for declarations and
/// tests where invalid syntax is a programmer error.
pub fn must_parse(source: &str) -> Type {
    match parse(source) {
        Ok(value) => value,
        Err(error) => panic!("{error}"),
    }
}

fn parse_with(source: &str, allow_templates: bool) -> ParseResult {
    let mut parser = TypeParser {
        source: source.trim(),
        position: 0,
        allow_templates,
    };
    if parser.source.is_empty() {
        return Ok(Type::UNKNOWN);
    }
    let result = parser.parse_conditional()?;
    parser.skip_space();
    if !parser.at_end() {
        let rest = parser.slice(parser.position, parser.source.len());
        return Err(parser.error(format!("unexpected {}", go_quote(&rest))));
    }
    Ok(result)
}

struct TypeParser<'a> {
    source: &'a str,
    position: usize,
    allow_templates: bool,
}

impl<'a> TypeParser<'a> {
    fn bytes(&self) -> &'a [u8] {
        self.source.as_bytes()
    }

    /// Slices the source; never panics on non-boundary offsets.
    fn slice(&self, start: usize, end: usize) -> std::borrow::Cow<'a, str> {
        match self.source.get(start..end) {
            Some(value) => std::borrow::Cow::Borrowed(value),
            None => String::from_utf8_lossy(&self.bytes()[start..end]),
        }
    }

    fn error(&self, message: impl Into<String>) -> ParseError {
        ParseError {
            position: self.position,
            message: message.into(),
        }
    }

    fn parse_conditional(&mut self) -> ParseResult {
        let subject = self.parse_union()?;
        if !self.consume_word("is") {
            return Ok(subject);
        }
        let negated = self.consume_word("not");
        let target = self.parse_union()?;
        if !self.consume("?") {
            return Err(self.error("expected ?"));
        }
        let mut if_true = self.parse_conditional()?;
        if !self.consume(":") {
            return Err(self.error("expected :"));
        }
        let mut if_false = self.parse_conditional()?;
        if negated {
            std::mem::swap(&mut if_true, &mut if_false);
        }
        Ok(Type::conditional(subject, target, if_true, if_false))
    }

    fn parse_union(&mut self) -> ParseResult {
        let left = self.parse_intersection()?;
        if !self.consume("|") {
            return Ok(left);
        }
        let mut values = vec![left];
        loop {
            values.push(self.parse_intersection()?);
            if !self.consume("|") {
                break;
            }
        }
        Ok(Type::union(values))
    }

    fn parse_intersection(&mut self) -> ParseResult {
        let left = self.parse_postfix()?;
        if !self.consume("&") {
            return Ok(left);
        }
        let mut values = vec![left];
        loop {
            values.push(self.parse_postfix()?);
            if !self.consume("&") {
                break;
            }
        }
        Ok(Type::intersection(values))
    }

    fn parse_postfix(&mut self) -> ParseResult {
        let mut value = self.parse_primary()?;
        while self.consume("[") {
            if !self.consume("]") {
                return Err(self.error("expected ]"));
            }
            value = Type::array(Type::ARRAY_KEY, value);
        }
        Ok(value)
    }

    fn parse_primary(&mut self) -> ParseResult {
        if self.consume("?") {
            let value = self.parse_postfix()?;
            return Ok(Type::nullable(value));
        }
        if self.consume("(") {
            let value = self.parse_conditional()?;
            if !self.consume(")") {
                return Err(self.error("expected )"));
            }
            return Ok(value);
        }
        self.skip_space();
        if self.at_end() {
            return Err(self.error("expected type"));
        }
        if self.peek() == b'\'' || self.peek() == b'"' {
            let value = self.parse_quoted()?;
            return Ok(Type::literal_string(&value));
        }
        if is_number_start(self.peek()) {
            let (number, floating_point) = self.parse_number();
            if floating_point {
                return Ok(Type::literal_float(&number));
            }
            return Ok(Type::literal_int(&number));
        }

        let name = self.parse_name();
        if name.is_empty() {
            return Err(self.error("expected type"));
        }
        let lower_is = |word: &str| name.eq_ignore_ascii_case(word);
        if (lower_is("array") || lower_is("object")) && self.consume("{") {
            return self.parse_shape(lower_is("object"));
        }
        let normalized = name.strip_prefix('\\').unwrap_or(&name);
        if (normalized.eq_ignore_ascii_case("callable")
            || normalized.eq_ignore_ascii_case("closure"))
            && self.consume("(")
        {
            return self.parse_callable();
        }

        let mut arguments = Vec::new();
        if self.consume("<") {
            while !self.consume(">") {
                // PHPStan call-site variance affects compatibility checks, but
                // the argument still carries the same value type.
                self.consume_word("covariant");
                self.consume_word("contravariant");
                let argument = if self.consume("*") {
                    Type::MIXED
                } else {
                    self.parse_union()?
                };
                arguments.push(argument);
                if self.consume(">") {
                    break;
                }
                if !self.consume(",") {
                    return Err(self.error("expected , or >"));
                }
            }
        }
        Ok(type_from_name(&name, arguments, self.allow_templates))
    }

    fn parse_callable(&mut self) -> ParseResult {
        let mut parameters = Vec::new();
        while !self.consume(")") {
            let mut parameter = CallableParameter {
                by_reference: self.consume("&"),
                ..Default::default()
            };
            parameter.variadic = self.consume("...");
            parameter.ty = self.parse_union()?;
            self.skip_space();
            if !self.at_end() && self.peek() == b'$' {
                parameter.name = Name::from(self.parse_name().as_ref());
            }
            parameter.optional = self.consume("=");
            parameters.push(parameter);
            if self.consume(")") {
                break;
            }
            if !self.consume(",") {
                return Err(self.error("expected , or )"));
            }
        }
        let result = if self.consume(":") {
            self.parse_union()?
        } else {
            Type::MIXED
        };
        Ok(Type::callable(parameters, result))
    }

    fn parse_shape(&mut self, object: bool) -> ParseResult {
        let mut fields = Vec::new();
        let mut open = false;
        while !self.consume("}") {
            if self.consume("...") {
                open = true;
                if !self.consume("}") {
                    return Err(self.error("expected }"));
                }
                break;
            }
            let name = self.parse_shape_key();
            if name.is_empty() {
                return Err(self.error("expected shape key"));
            }
            let optional = self.consume("?");
            if !self.consume(":") {
                return Err(self.error("expected :"));
            }
            let ty = self.parse_union()?;
            fields.push(ShapeField {
                name: Name::from(name),
                ty,
                optional,
            });
            if self.consume("}") {
                break;
            }
            if !self.consume(",") {
                return Err(self.error("expected , or }"));
            }
        }
        if object {
            Ok(Type::object_shape(fields, open))
        } else {
            Ok(Type::array_shape(fields, open))
        }
    }

    fn parse_shape_key(&mut self) -> String {
        self.skip_space();
        if self.at_end() {
            return String::new();
        }
        if self.peek() == b'\'' || self.peek() == b'"' {
            return match self.parse_quoted() {
                Ok(value) => go_quote(&value),
                Err(_) => String::new(),
            };
        }
        let start = self.position;
        let name = self.parse_name().into_owned();
        // Older persisted inferred shapes rendered dotted string keys without
        // quotes. Keep accepting that legacy representation.
        while !self.at_end() && self.peek() == b'.' {
            self.position += 1;
            if self.parse_name().is_empty() {
                self.position = start;
                return String::new();
            }
        }
        if self.position > start {
            return self.slice(start, self.position).into_owned();
        }
        name
    }

    fn parse_name(&mut self) -> std::borrow::Cow<'a, str> {
        self.skip_space();
        let start = self.position;
        if !self.at_end() && self.peek() == b'\\' {
            self.position += 1;
        }
        if !self.at_end() && self.peek() == b'$' {
            self.position += 1;
        }
        while !self.at_end() && is_type_name_byte(self.peek()) {
            self.position += 1;
        }
        self.slice(start, self.position)
    }

    fn parse_number(&mut self) -> (String, bool) {
        let bytes = self.bytes();
        let start = self.position;
        if !self.at_end() && (self.peek() == b'+' || self.peek() == b'-') {
            self.position += 1;
        }
        if self.position + 2 <= bytes.len() && bytes[self.position] == b'0' {
            let valid_digit: Option<fn(u8) -> bool> = match bytes[self.position + 1] {
                b'x' | b'X' => Some(|value: u8| value.is_ascii_hexdigit()),
                b'b' | b'B' => Some(|value: u8| value == b'0' || value == b'1'),
                b'o' | b'O' => Some(|value: u8| (b'0'..=b'7').contains(&value)),
                _ => None,
            };
            if let Some(valid_digit) = valid_digit {
                self.position += 2;
                while !self.at_end() && (valid_digit(self.peek()) || self.peek() == b'_') {
                    self.position += 1;
                }
                return (self.slice(start, self.position).into_owned(), false);
            }
        }

        let mut floating_point = false;
        self.skip_digits();
        if !self.at_end() && self.peek() == b'.' {
            floating_point = true;
            self.position += 1;
            self.skip_digits();
        }
        if !self.at_end() && (self.peek() == b'e' || self.peek() == b'E') {
            floating_point = true;
            self.position += 1;
            if !self.at_end() && (self.peek() == b'+' || self.peek() == b'-') {
                self.position += 1;
            }
            self.skip_digits();
        }
        (
            self.slice(start, self.position).into_owned(),
            floating_point,
        )
    }

    fn skip_digits(&mut self) {
        while !self.at_end() && (self.peek().is_ascii_digit() || self.peek() == b'_') {
            self.position += 1;
        }
    }

    fn parse_quoted(&mut self) -> Result<String, ParseError> {
        let quote = self.peek();
        let start = self.position;
        self.position += 1;
        let mut escaped = false;
        while !self.at_end() {
            let value = self.peek();
            self.position += 1;
            if escaped {
                escaped = false;
                continue;
            }
            if value == b'\\' {
                escaped = true;
                continue;
            }
            if value == quote {
                let raw = self.slice(start, self.position);
                return Ok(match go_unquote(&raw) {
                    Some(decoded) => decoded,
                    None => raw[1..raw.len() - 1].to_owned(),
                });
            }
        }
        Err(self.error("unterminated string"))
    }

    fn consume(&mut self, text: &str) -> bool {
        self.skip_space();
        if !self.bytes()[self.position..].starts_with(text.as_bytes()) {
            return false;
        }
        self.position += text.len();
        true
    }

    fn consume_word(&mut self, word: &str) -> bool {
        self.skip_space();
        let rest = &self.bytes()[self.position..];
        if rest.len() < word.len() || !rest[..word.len()].eq_ignore_ascii_case(word.as_bytes()) {
            return false;
        }
        if rest.len() > word.len() {
            let next = rest[word.len()];
            if is_type_name_byte(next) && next != b'-' && next != b'$' {
                return false;
            }
        }
        self.position += word.len();
        true
    }

    fn skip_space(&mut self) {
        while !self.at_end() && is_space_byte(self.peek()) {
            self.position += 1;
        }
    }

    fn at_end(&self) -> bool {
        self.position >= self.source.len()
    }

    fn peek(&self) -> u8 {
        self.bytes()[self.position]
    }
}

fn is_number_start(value: u8) -> bool {
    value.is_ascii_digit() || value == b'+' || value == b'-'
}

fn type_from_name(name: &str, arguments: Vec<Type>, allow_templates: bool) -> Type {
    let builtin = builtin_type_name(name.strip_prefix('\\').unwrap_or(name));
    if allow_templates && name.starts_with('$') && !crate::gostr::equal_fold(name, "$this") {
        return Type::template(name);
    }
    let mut arguments = arguments;
    let take = |arguments: &mut Vec<Type>, index: usize| std::mem::take(&mut arguments[index]);
    match builtin {
        "unknown" => Type::UNKNOWN,
        "error" => Type::ERROR,
        "never" => Type::NEVER,
        "mixed" => Type::MIXED,
        "void" => Type::VOID,
        "null" => Type::NULL,
        "bool" | "boolean" => Type::BOOL,
        "true" => Type::TRUE,
        "false" => Type::FALSE,
        "int" | "integer" | "positive-int" | "negative-int" | "non-negative-int" => Type::INT,
        "float" | "double" | "number" => Type::FLOAT,
        "scalar" => Type::union([Type::BOOL, Type::INT, Type::FLOAT, Type::STRING]),
        "string" | "non-empty-string" | "numeric-string" | "literal-string" => Type::STRING,
        "object" => Type::OBJECT,
        "resource" => Type::RESOURCE,
        "array-key" => Type::ARRAY_KEY,
        "array" | "non-empty-array" | "iterable" => {
            let constructor = match builtin {
                "array" => Type::array,
                "non-empty-array" => Type::non_empty_array,
                _ => Type::iterable,
            };
            match arguments.len() {
                0 => constructor(Type::MIXED, Type::MIXED),
                1 => constructor(Type::ARRAY_KEY, take(&mut arguments, 0)),
                _ => {
                    let value = take(&mut arguments, 1);
                    constructor(take(&mut arguments, 0), value)
                }
            }
        }
        "list" | "non-empty-list" => {
            let value = if arguments.is_empty() {
                Type::MIXED
            } else {
                take(&mut arguments, 0)
            };
            if builtin == "list" {
                Type::list(value)
            } else {
                Type::non_empty_list(value)
            }
        }
        "callable" | "closure" => Type::callable(Vec::new(), Type::MIXED),
        "class-string" | "interface-string" | "trait-string" | "enum-string" => {
            if arguments.is_empty() {
                Type::class_string(Type::OBJECT)
            } else {
                Type::class_string(take(&mut arguments, 0))
            }
        }
        "self" => Type::self_type(arguments),
        "static" | "$this" => Type::static_type(arguments),
        "parent" => Type::parent_type(arguments),
        _ => {
            if allow_templates && is_template_name(name) {
                return Type::template(name);
            }
            Type::named(name, arguments)
        }
    }
}

const BUILTIN_NAMES: &[&str] = &[
    "int",
    "void",
    "null",
    "bool",
    "true",
    "list",
    "self",
    "error",
    "never",
    "mixed",
    "false",
    "float",
    "array",
    "$this",
    "double",
    "number",
    "scalar",
    "string",
    "object",
    "static",
    "parent",
    "unknown",
    "boolean",
    "integer",
    "closure",
    "resource",
    "iterable",
    "callable",
    "array-key",
    "enum-string",
    "positive-int",
    "negative-int",
    "class-string",
    "trait-string",
    "numeric-string",
    "literal-string",
    "non-empty-list",
    "non-empty-array",
    "non-negative-int",
    "non-empty-string",
    "interface-string",
];

/// Classifies a builtin pseudo-type name case-insensitively without
/// allocating. Returns the lowercase canonical spelling or `""`.
pub(crate) fn builtin_type_name(name: &str) -> &'static str {
    if name.len() < 3 || name.len() > 16 {
        return "";
    }
    BUILTIN_NAMES
        .iter()
        .find(|candidate| candidate.len() == name.len() && candidate.eq_ignore_ascii_case(name))
        .copied()
        .unwrap_or("")
}

fn is_template_name(name: &str) -> bool {
    if name.contains('\\') || name.starts_with('$') {
        return false;
    }
    let bytes = name.as_bytes();
    if bytes.len() == 1 {
        return bytes[0].is_ascii_uppercase();
    }
    bytes.len() > 1 && bytes[0] == b'T' && bytes[1].is_ascii_uppercase()
}
