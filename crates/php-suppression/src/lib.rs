//! Parses source-level diagnostic suppression directives and matches them to
//! Shopware LSP's stable PHP diagnostic identifiers.

use std::collections::HashMap;

use swlsp_textutil::lowercase;

#[cfg(test)]
mod tests;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
struct Directive {
    identifiers: Vec<String>,
    wildcard: bool,
}

/// An immutable, line-indexed collection of suppression directives. Sources
/// without supported markers yield an empty set.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Set {
    line_starts: Vec<usize>,
    lines: HashMap<usize, Vec<Directive>>,
}

const IGNORE_NEXT_LINE: &str = "@phpstan-ignore-next-line";
const IGNORE_LINE: &str = "@phpstan-ignore-line";
const IGNORE: &str = "@phpstan-ignore";

/// Recognizes PHPStan's ignore, ignore-line, and ignore-next-line forms, plus
/// JetBrains' noinspection form. Ordinary ignore/noinspection comments apply
/// to their inline statement or the next non-comment source line.
pub fn parse(source: &str) -> Set {
    if !contains_suppression_marker(source) {
        return Set::default();
    }
    let (text_lines, starts) = source_lines(source);
    let mut result = Set {
        line_starts: starts,
        lines: HashMap::new(),
    };
    let line_count = text_lines.len();
    for (line_index, line) in text_lines.iter().enumerate() {
        // Markers are found in the lowercased copy and the original line is
        // sliced at the same byte offsets; `slice_from`/`slice_to` move them
        // to a character boundary.
        let lower = lowercase(line);
        if let Some(marker) = lower.find(IGNORE_NEXT_LINE) {
            let directive =
                parse_directive(slice_from(line, marker + IGNORE_NEXT_LINE.len()), false);
            result.add(line_index + 1, directive, line_count);
            continue;
        }
        if let Some(marker) = lower.find(IGNORE_LINE) {
            let directive = parse_directive(slice_from(line, marker + IGNORE_LINE.len()), false);
            result.add(line_index, directive, line_count);
            continue;
        }
        if let Some(marker) = lower.find(IGNORE) {
            let target = directive_target(&text_lines, line_index, marker);
            let directive = parse_directive(slice_from(line, marker + IGNORE.len()), false);
            result.add(target, directive, line_count);
            continue;
        }
        if let Some((marker, length)) = no_inspection_marker(&lower) {
            let target = directive_target(&text_lines, line_index, marker);
            let directive = parse_directive(slice_from(line, marker + length), true);
            result.add(target, directive, line_count);
        }
    }
    if result.lines.is_empty() {
        return Set::default();
    }
    result
}

impl Set {
    fn add(&mut self, line: usize, value: Directive, line_count: usize) {
        if line >= line_count {
            return;
        }
        self.lines.entry(line).or_default().push(value);
    }

    /// Reports whether no directive was recognized.
    pub fn is_empty(&self) -> bool {
        self.lines.is_empty()
    }

    /// Reports whether a directive targeting the line containing the byte
    /// `offset` accepts the diagnostic `code`.
    pub fn suppresses(&self, offset: u32, code: &str) -> bool {
        let Some(&last_start) = self.line_starts.last() else {
            return false;
        };
        if self.lines.is_empty() || code.is_empty() {
            return false;
        }
        let position = (offset as usize).min(last_start);
        let line = self
            .line_starts
            .partition_point(|&start| start <= position)
            .saturating_sub(1);
        self.lines.get(&line).is_some_and(|directives| {
            directives.iter().any(|candidate| {
                candidate.wildcard
                    || candidate
                        .identifiers
                        .iter()
                        .any(|identifier| identifier_matches(identifier, code))
            })
        })
    }
}

fn source_lines(source: &str) -> (Vec<&str>, Vec<usize>) {
    let lines: Vec<&str> = source.split('\n').collect();
    let mut starts = Vec::with_capacity(lines.len());
    let mut position = 0;
    for line in &lines {
        starts.push(position);
        position += line.len() + 1;
    }
    (lines, starts)
}

fn directive_target(lines: &[&str], line_index: usize, marker: usize) -> usize {
    let line = lines[line_index];
    let prefix = slice_to(line, marker).trim();
    if !prefix.is_empty() && !comment_only_prefix(prefix) {
        return line_index;
    }
    let from_marker = slice_from(line, marker);
    if let Some(close) = from_marker.find("*/")
        && !from_marker[close + 2..].trim().is_empty()
    {
        return line_index;
    }
    next_code_line(lines, line_index)
}

fn comment_only_prefix(prefix: &str) -> bool {
    let prefix = prefix.trim();
    prefix.starts_with("//")
        || prefix.starts_with('#')
        || prefix.starts_with("/*")
        || prefix.starts_with('*')
}

fn next_code_line(lines: &[&str], start: usize) -> usize {
    let mut in_block_comment = lines[start]
        .find("/*")
        .is_some_and(|open| !lines[start][open..].contains("*/"));
    for (index, line) in lines.iter().enumerate().skip(start + 1) {
        let mut line = line.trim();
        if in_block_comment {
            let Some(close) = line.find("*/") else {
                continue;
            };
            in_block_comment = false;
            line = line[close + 2..].trim();
            if line.is_empty() {
                continue;
            }
        }
        if line.is_empty()
            || line.starts_with("//")
            || line.starts_with('#')
            || line.starts_with('*')
        {
            continue;
        }
        if let Some(body) = line.strip_prefix("/*") {
            if let Some(close) = body.find("*/") {
                if !body[close + 2..].trim().is_empty() {
                    return index;
                }
                continue;
            }
            in_block_comment = true;
            continue;
        }
        return index;
    }
    start + 1
}

fn no_inspection_marker(lower: &str) -> Option<(usize, usize)> {
    if let Some(marker) = lower.find("@noinspection") {
        return Some((marker, "@noinspection".len()));
    }
    lower
        .find("noinspection")
        .map(|marker| (marker, "noinspection".len()))
}

fn parse_directive(source: &str, inspection: bool) -> Directive {
    let source = source.trim();
    if source.is_empty()
        || source.starts_with("*/")
        || source.starts_with("//")
        || source.starts_with('(')
        || source.starts_with('-')
    {
        return Directive {
            identifiers: Vec::new(),
            wildcard: true,
        };
    }
    let source = match source.find('(') {
        Some(reason) => &source[..reason],
        None => source,
    };
    let mut identifiers = Vec::new();
    for field in source
        .split(|character: char| character.is_whitespace() || character == ',')
        .filter(|field| !field.is_empty())
    {
        let field = field.trim_matches(['*', '/', ';', ':', '[', ']', '{', '}']);
        if field.is_empty() {
            continue;
        }
        if !inspection && !field.contains('.') && field != "*" {
            break;
        }
        identifiers.push(lowercase(field));
    }
    let wildcard = identifiers.is_empty();
    Directive {
        identifiers,
        wildcard,
    }
}

fn identifier_matches(identifier: &str, code: &str) -> bool {
    let identifier = lowercase(identifier.trim());
    let identifier = identifier.as_str();
    let code = lowercase(code.trim());
    if identifier == "*" || identifier == "all" || identifier == code {
        return true;
    }
    match code.as_str() {
        "php.unusedimport" => {
            identifier == "phpunusedaliasinspection" || identifier == "phpunusedimportinspection"
        }
        "php.returntype" => {
            identifier.starts_with("return.")
                || identifier == "cast.string"
                || identifier == "vartag.type"
                || identifier == "phpreturndoctypemismatchinspection"
                || identifier == "phpinconsistentreturnpointsinspection"
        }
        "php.arguments" => {
            identifier.starts_with("argument.")
                || identifier.starts_with("arguments.")
                || identifier == "phpparamsinspection"
                || identifier == "phpmethodparameterscountmismatchinspection"
        }
        "php.deprecated" => {
            identifier.contains("deprecated") || identifier == "phpdeprecationinspection"
        }
        "php.undefined" | "php.undefinedvariable" => {
            identifier.contains("notfound")
                || identifier.contains("undefined")
                || identifier.starts_with("phpundefined")
        }
        "php.override" => identifier.contains("override"),
        "php.inheritance" => {
            identifier.contains("inheritance")
                || identifier.contains("extends")
                || identifier.contains("implements")
        }
        "php.abstract" => identifier.contains("abstract"),
        "php.visibility" => {
            identifier.contains("visibility")
                || identifier.contains("access")
                || identifier == "phpaccessmodifiersinspection"
        }
        "php.parse" => identifier.contains("syntax") || identifier.contains("parse"),
        "php.version" => {
            identifier.contains("languagelevel")
                || identifier.contains("phpversion")
                || identifier == "version"
        }
        _ => false,
    }
}

/// Cheap prefilter: whether `source` contains a marker candidate. Both
/// markers are ASCII without Unicode case-fold equivalents of equal byte
/// length, so an ASCII case-insensitive window comparison finds every
/// case-insensitive occurrence.
fn contains_suppression_marker(source: &str) -> bool {
    let bytes = source.as_bytes();
    bytes.iter().enumerate().any(|(index, &byte)| {
        let marker: &[u8] = match byte {
            b'@' => IGNORE.as_bytes(),
            b'n' | b'N' => b"noinspection",
            _ => return false,
        };
        bytes.len() - index >= marker.len()
            && bytes[index..index + marker.len()].eq_ignore_ascii_case(marker)
    })
}

/// `&line[start..]`, clamped to the line and moved forward to a character
/// boundary.
fn slice_from(line: &str, start: usize) -> &str {
    let mut start = start.min(line.len());
    while !line.is_char_boundary(start) {
        start += 1;
    }
    &line[start..]
}

/// `&line[..end]`, clamped to the line and moved back to a character
/// boundary.
fn slice_to(line: &str, end: usize) -> &str {
    let mut end = end.min(line.len());
    while !line.is_char_boundary(end) {
        end -= 1;
    }
    &line[..end]
}
