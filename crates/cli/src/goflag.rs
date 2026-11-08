//! A port of the subset of Go's `flag` package the CLI uses: single- or
//! double-dash flags, `-name=value` and `-name value`, bool flags, parsing
//! that stops at the first positional argument or `--`, and Go's exact error
//! and usage text (printed to the error writer before the error returns).

use std::collections::BTreeMap;
use std::io::Write;
use std::time::Duration;

use crate::gofmt::{format_duration, go_quote, parse_duration};

#[derive(Debug, Clone)]
enum Value {
    Bool(bool),
    Int(i64),
    Str(String),
    Duration(Duration),
}

impl Value {
    fn type_name(&self) -> &'static str {
        match self {
            Value::Bool(_) => "",
            Value::Int(_) => "int",
            Value::Str(_) => "string",
            Value::Duration(_) => "duration",
        }
    }

    fn render(&self) -> String {
        match self {
            Value::Bool(value) => value.to_string(),
            Value::Int(value) => value.to_string(),
            Value::Str(value) => value.clone(),
            Value::Duration(value) => format_duration(value.as_nanos() as i64),
        }
    }

    fn is_zero(&self) -> bool {
        match self {
            Value::Bool(value) => !value,
            Value::Int(value) => *value == 0,
            Value::Str(value) => value.is_empty(),
            Value::Duration(value) => value.is_zero(),
        }
    }

    fn set(&mut self, input: &str) -> Result<(), &'static str> {
        match self {
            Value::Bool(value) => {
                *value = parse_bool(input).ok_or("parse error")?;
            }
            Value::Int(value) => {
                *value = parse_int(input)?;
            }
            Value::Str(value) => *value = input.to_owned(),
            Value::Duration(value) => {
                let nanos = parse_duration(input).ok_or("parse error")?;
                *value = Duration::from_nanos(nanos.max(0) as u64);
            }
        }
        Ok(())
    }
}

#[derive(Debug, Clone)]
struct Flag {
    usage: String,
    default: Value,
    value: Value,
}

/// The error a failed parse returns. Its text is Go's error text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FlagError(pub String);

impl std::fmt::Display for FlagError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

/// A Go `flag.FlagSet` with `ContinueOnError`.
#[derive(Debug, Clone)]
pub struct FlagSet {
    name: String,
    flags: BTreeMap<String, Flag>,
    args: Vec<String>,
}

impl FlagSet {
    pub fn new(name: &str) -> Self {
        Self {
            name: name.to_owned(),
            flags: BTreeMap::new(),
            args: Vec::new(),
        }
    }

    fn define(&mut self, name: &str, value: Value, usage: &str) {
        self.flags.insert(
            name.to_owned(),
            Flag {
                usage: usage.to_owned(),
                default: value.clone(),
                value,
            },
        );
    }

    pub fn bool(&mut self, name: &str, default: bool, usage: &str) {
        self.define(name, Value::Bool(default), usage);
    }

    pub fn int(&mut self, name: &str, default: i64, usage: &str) {
        self.define(name, Value::Int(default), usage);
    }

    pub fn string(&mut self, name: &str, default: &str, usage: &str) {
        self.define(name, Value::Str(default.to_owned()), usage);
    }

    pub fn duration(&mut self, name: &str, default: Duration, usage: &str) {
        self.define(name, Value::Duration(default), usage);
    }

    pub fn get_bool(&self, name: &str) -> bool {
        matches!(
            self.flags.get(name).map(|flag| &flag.value),
            Some(Value::Bool(true))
        )
    }

    pub fn get_int(&self, name: &str) -> i64 {
        match self.flags.get(name).map(|flag| &flag.value) {
            Some(Value::Int(value)) => *value,
            _ => 0,
        }
    }

    pub fn get_string(&self, name: &str) -> String {
        match self.flags.get(name).map(|flag| &flag.value) {
            Some(Value::Str(value)) => value.clone(),
            _ => String::new(),
        }
    }

    pub fn get_duration(&self, name: &str) -> Duration {
        match self.flags.get(name).map(|flag| &flag.value) {
            Some(Value::Duration(value)) => *value,
            _ => Duration::ZERO,
        }
    }

    /// Remaining positional arguments after `parse`.
    pub fn args(&self) -> &[String] {
        &self.args
    }

    /// Parses `arguments`; on failure the error and the usage text are
    /// written to `output`, as Go does.
    pub fn parse(&mut self, arguments: &[String], output: &mut dyn Write) -> Result<(), FlagError> {
        self.args = arguments.to_vec();
        loop {
            match self.parse_one(output) {
                Ok(true) => {}
                Ok(false) => return Ok(()),
                Err(error) => return Err(error),
            }
        }
    }

    fn fail(&self, message: String, output: &mut dyn Write) -> FlagError {
        let _ = writeln!(output, "{message}");
        self.usage(output);
        FlagError(message)
    }

    fn parse_one(&mut self, output: &mut dyn Write) -> Result<bool, FlagError> {
        let Some(first) = self.args.first().cloned() else {
            return Ok(false);
        };
        let bytes = first.as_bytes();
        if bytes.len() < 2 || bytes[0] != b'-' {
            return Ok(false);
        }
        let mut minuses = 1;
        if bytes[1] == b'-' {
            minuses = 2;
            if bytes.len() == 2 {
                self.args.remove(0);
                return Ok(false);
            }
        }
        let mut name = first[minuses..].to_owned();
        if name.is_empty() || name.starts_with('-') || name.starts_with('=') {
            return Err(self.fail(format!("bad flag syntax: {first}"), output));
        }
        self.args.remove(0);
        let mut value = None;
        if let Some(index) = name[1..].find('=') {
            let index = index + 1;
            value = Some(name[index + 1..].to_owned());
            name.truncate(index);
        }
        let Some(flag) = self.flags.get(&name) else {
            if name == "help" || name == "h" {
                self.usage(output);
                return Err(FlagError("flag: help requested".into()));
            }
            return Err(self.fail(format!("flag provided but not defined: -{name}"), output));
        };
        let mut current = flag.value.clone();
        if matches!(current, Value::Bool(_)) {
            match value {
                Some(value) => {
                    if let Err(error) = current.set(&value) {
                        return Err(self.fail(
                            format!(
                                "invalid boolean value {} for -{name}: {error}",
                                go_quote(&value)
                            ),
                            output,
                        ));
                    }
                }
                None => {
                    let _ = current.set("true");
                }
            }
        } else {
            let value = match value {
                Some(value) => value,
                None if !self.args.is_empty() => self.args.remove(0),
                None => {
                    return Err(self.fail(format!("flag needs an argument: -{name}"), output));
                }
            };
            if let Err(error) = current.set(&value) {
                return Err(self.fail(
                    format!(
                        "invalid value {} for flag -{name}: {error}",
                        go_quote(&value)
                    ),
                    output,
                ));
            }
        }
        if let Some(flag) = self.flags.get_mut(&name) {
            flag.value = current;
        }
        Ok(true)
    }

    /// Go `defaultUsage`.
    pub fn usage(&self, output: &mut dyn Write) {
        if self.name.is_empty() {
            let _ = writeln!(output, "Usage:");
        } else {
            let _ = writeln!(output, "Usage of {}:", self.name);
        }
        self.print_defaults(output);
    }

    pub fn print_defaults(&self, output: &mut dyn Write) {
        for (name, flag) in &self.flags {
            let mut line = format!("  -{name}");
            let (type_name, usage) = unquote_usage(&flag.usage, &flag.default);
            if !type_name.is_empty() {
                line.push(' ');
                line.push_str(&type_name);
            }
            if line.len() <= 4 {
                line.push('\t');
            } else {
                line.push_str("\n    \t");
            }
            line.push_str(&usage.replace('\n', "\n    \t"));
            if !flag.default.is_zero() {
                match &flag.default {
                    Value::Str(value) => line.push_str(&format!(" (default {})", go_quote(value))),
                    other => line.push_str(&format!(" (default {})", other.render())),
                }
            }
            let _ = writeln!(output, "{line}");
        }
    }
}

fn unquote_usage(usage: &str, value: &Value) -> (String, String) {
    if let Some(start) = usage.find('`')
        && let Some(length) = usage[start + 1..].find('`')
    {
        let end = start + 1 + length;
        let name = usage[start + 1..end].to_owned();
        let unquoted = format!("{}{}{}", &usage[..start], name, &usage[end + 1..]);
        return (name, unquoted);
    }
    (value.type_name().to_owned(), usage.to_owned())
}

fn parse_bool(input: &str) -> Option<bool> {
    match input {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

/// Go `strconv.ParseInt(s, 0, 64)` mapped to flag's error text.
fn parse_int(input: &str) -> Result<i64, &'static str> {
    let (negative, rest) = match input.as_bytes().first() {
        Some(b'+') => (false, &input[1..]),
        Some(b'-') => (true, &input[1..]),
        _ => (false, input),
    };
    let lower = rest.to_ascii_lowercase();
    let (base, digits, prefixed) = if let Some(digits) = lower.strip_prefix("0x") {
        (16, digits.to_owned(), true)
    } else if let Some(digits) = lower.strip_prefix("0b") {
        (2, digits.to_owned(), true)
    } else if let Some(digits) = lower.strip_prefix("0o") {
        (8, digits.to_owned(), true)
    } else if lower.len() > 1 && lower.starts_with('0') {
        (8, lower[1..].to_owned(), true)
    } else {
        (10, lower.clone(), false)
    };
    if digits.is_empty() {
        return Err("parse error");
    }
    // Underscores are allowed only with a base prefix, between digits.
    if digits.contains('_') {
        if !prefixed && base == 10 {
            return Err("parse error");
        }
        if digits.starts_with('_') && base == 8 && !lower.starts_with("0o") {
            // "0_1" style is valid in Go; keep it simple and accept.
        }
        if digits.ends_with('_') || digits.contains("__") {
            return Err("parse error");
        }
    }
    let cleaned: String = digits.chars().filter(|c| *c != '_').collect();
    if cleaned.is_empty() {
        return Err("parse error");
    }
    let mut value: u128 = 0;
    for character in cleaned.chars() {
        let digit = character.to_digit(base).ok_or("parse error")?;
        value = value * u128::from(base) + u128::from(digit);
        if value > u128::from(u64::MAX) + 1 {
            // Keep scanning for syntax errors, but saturate.
            value = u128::from(u64::MAX) + 1;
        }
    }
    if negative {
        if value > (i64::MAX as u128) + 1 {
            return Err("value out of range");
        }
        Ok((value as i128).wrapping_neg() as i64)
    } else {
        if value > i64::MAX as u128 {
            return Err("value out of range");
        }
        Ok(value as i64)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(values: &[&str]) -> Vec<String> {
        values.iter().map(|value| (*value).to_owned()).collect()
    }

    #[test]
    fn parses_go_flag_syntax() {
        let mut flags = FlagSet::new("check");
        flags.string("severity", "", "minimum severity");
        flags.int("workers", 4, "workers");
        flags.bool("w", false, "write");
        let mut output = Vec::new();
        flags
            .parse(
                &args(&["--severity=error", "-workers", "2", "-w", "a", "-x"]),
                &mut output,
            )
            .unwrap();
        assert_eq!(flags.get_string("severity"), "error");
        assert_eq!(flags.get_int("workers"), 2);
        assert!(flags.get_bool("w"));
        assert_eq!(flags.args(), &args(&["a", "-x"])[..]);
    }

    #[test]
    fn reports_go_errors_and_usage() {
        let mut flags = FlagSet::new("check");
        flags.int("workers", 4, "maximum concurrent diagnostic requests");
        flags.bool("w", false, "write resulting edits");
        let mut output = Vec::new();
        let error = flags
            .parse(&args(&["-workers", "x"]), &mut output)
            .unwrap_err();
        assert_eq!(
            error.0,
            "invalid value \"x\" for flag -workers: parse error"
        );
        assert_eq!(
            String::from_utf8(output).unwrap(),
            "invalid value \"x\" for flag -workers: parse error\nUsage of check:\n  -w\twrite resulting edits\n  -workers int\n    \tmaximum concurrent diagnostic requests (default 4)\n"
        );
        let mut output = Vec::new();
        let error = flags.parse(&args(&["-h"]), &mut output).unwrap_err();
        assert_eq!(error.0, "flag: help requested");
        assert!(
            String::from_utf8(output)
                .unwrap()
                .starts_with("Usage of check:\n")
        );
    }

    #[test]
    fn parses_go_integers() {
        assert_eq!(parse_int("0x10"), Ok(16));
        assert_eq!(parse_int("010"), Ok(8));
        assert_eq!(parse_int("-3"), Ok(-3));
        assert_eq!(parse_int("1_000"), Err("parse error"));
        assert_eq!(parse_int("99999999999999999999"), Err("value out of range"));
    }
}
