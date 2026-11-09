//! Standalone PHPDoc annotation edits.

use swlsp_parser_php::syntax::TK_BLOCK_COMMENT;
use swlsp_syntax::{SyntaxNode, SyntaxToken, TextRange};
use swlsp_textutil::eq_ignore_case;

use crate::editor::{Editor, Result, invalid, range, start};

struct AnnotationSpan {
    start: usize,
    end: usize,
    line_start: usize,
    line_end: usize,
}

/// One standalone annotation found in a declaration's docblock.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PhpDocAnnotation {
    pub text: String,
    pub range: TextRange,
}

/// One complete standalone annotation owned by a declaration. Short names
/// also match qualified annotations.
pub fn find_phpdoc_annotation(
    owner: &SyntaxNode,
    annotation_name: &str,
) -> Option<PhpDocAnnotation> {
    let token = leading_phpdoc(owner)?;
    let text = token.text();
    let annotation = find_annotation(text, annotation_name)?;
    let base = start(token.text_range());
    Some(PhpDocAnnotation {
        text: text[annotation.start..annotation.end].to_owned(),
        range: range(base + annotation.start as u32, base + annotation.end as u32),
    })
}

impl Editor<'_> {
    /// Replaces one complete, standalone annotation while preserving the
    /// surrounding docblock. Nested parentheses and quoted strings are
    /// balanced before a rewrite is considered safe. Returns whether the
    /// annotation was found.
    pub fn replace_phpdoc_annotation(
        &mut self,
        owner: &SyntaxNode,
        annotation_name: &str,
        replacement: &str,
    ) -> Result<bool> {
        let Some(token) = leading_phpdoc(owner) else {
            return Ok(false);
        };
        let Some(annotation) = find_annotation(token.text(), annotation_name) else {
            return Ok(false);
        };
        let replacement = replacement.trim();
        if replacement.is_empty() {
            return invalid("replace PHPDoc annotation: replacement is empty");
        }
        let replacement = if replacement.starts_with('@') {
            replacement.to_owned()
        } else {
            format!("@{replacement}")
        };
        let base = start(token.text_range());
        self.replace_range(
            range(base + annotation.start as u32, base + annotation.end as u32),
            replacement,
        )?;
        Ok(true)
    }

    /// Removes the whole line(s) of one standalone annotation. Returns
    /// whether the annotation was found.
    pub fn remove_phpdoc_annotation(
        &mut self,
        owner: &SyntaxNode,
        annotation_name: &str,
    ) -> Result<bool> {
        let Some(token) = leading_phpdoc(owner) else {
            return Ok(false);
        };
        let Some(annotation) = find_annotation(token.text(), annotation_name) else {
            return Ok(false);
        };
        let base = start(token.text_range());
        self.replace_range(
            range(
                base + annotation.line_start as u32,
                base + annotation.line_end as u32,
            ),
            "",
        )?;
        Ok(true)
    }
}

/// The last docblock in the leading trivia of `owner`.
pub(crate) fn leading_phpdoc(owner: &SyntaxNode) -> Option<SyntaxToken> {
    let mut candidate = None;
    for token in owner
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
    {
        if !token.kind().is_trivia() {
            break;
        }
        if token.kind() == TK_BLOCK_COMMENT && token.text().trim().starts_with("/**") {
            candidate = Some(token);
        }
    }
    candidate
}

fn find_annotation(source: &str, annotation_name: &str) -> Option<AnnotationSpan> {
    let annotation_name = annotation_name.trim();
    let annotation_name = annotation_name.strip_prefix('@').unwrap_or(annotation_name);
    let annotation_name = annotation_name
        .strip_prefix('\\')
        .unwrap_or(annotation_name);
    if annotation_name.is_empty() {
        return None;
    }
    let bytes = source.as_bytes();
    let mut line_begin = 0;
    while line_begin < bytes.len() {
        let line_end = bytes[line_begin..]
            .iter()
            .position(|&byte| byte == b'\n')
            .map_or(bytes.len(), |index| line_begin + index);
        let line = &bytes[line_begin..line_end];
        let mut prefix = 0;
        while prefix < line.len() && matches!(line[prefix], b' ' | b'\t' | b'\r') {
            prefix += 1;
        }
        if prefix < line.len() && line[prefix] == b'*' {
            prefix += 1;
            while prefix < line.len() && matches!(line[prefix], b' ' | b'\t') {
                prefix += 1;
            }
            if prefix < line.len() && line[prefix] == b'@' {
                let name_start = prefix + 1;
                let mut name_end = name_start;
                while name_end < line.len() && is_name_byte(line[name_end]) {
                    name_end += 1;
                }
                // Bytes are classified as Latin-1 code points, so a name may
                // end inside a multi-byte character; such names never match.
                if let Ok(actual) = std::str::from_utf8(&line[name_start..name_end]) {
                    let actual = actual.strip_prefix('\\').unwrap_or(actual);
                    if names_equal(actual, annotation_name) {
                        return scan_annotation(source, line_begin + prefix, line_begin + name_end);
                    }
                }
            }
        }
        if line_end == bytes.len() {
            break;
        }
        line_begin = line_end + 1;
    }
    None
}

fn names_equal(actual: &str, requested: &str) -> bool {
    if eq_ignore_case(actual, requested) {
        return true;
    }
    if requested.contains('\\') {
        return false;
    }
    actual
        .rfind('\\')
        .is_some_and(|separator| eq_ignore_case(&actual[separator + 1..], requested))
}

fn scan_annotation(
    source: &str,
    annotation_start: usize,
    name_end: usize,
) -> Option<AnnotationSpan> {
    let bytes = source.as_bytes();
    let mut position = name_end;
    while position < bytes.len() && matches!(bytes[position], b' ' | b'\t') {
        position += 1;
    }
    let mut annotation_end = name_end;
    if position < bytes.len() && bytes[position] == b'(' {
        let mut depth = 0i32;
        let mut quote = 0u8;
        let mut escaped = false;
        let mut closed = false;
        while position < bytes.len() {
            let character = bytes[position];
            position += 1;
            if quote != 0 {
                if escaped {
                    escaped = false;
                } else if character == b'\\' {
                    escaped = true;
                } else if character == quote {
                    quote = 0;
                }
                continue;
            }
            match character {
                b'\'' | b'"' => quote = character,
                b'(' => depth += 1,
                b')' => {
                    depth -= 1;
                    if depth == 0 {
                        annotation_end = position;
                        closed = true;
                        break;
                    }
                }
                _ => {}
            }
        }
        if !closed {
            return None;
        }
    }
    let end_of_line = bytes[annotation_end..]
        .iter()
        .position(|&byte| byte == b'\n')
        .map_or(bytes.len(), |index| annotation_end + index);
    if !source[annotation_end..end_of_line].trim().is_empty() {
        return None;
    }
    let line_start = bytes[..annotation_start]
        .iter()
        .rposition(|&byte| byte == b'\n')
        .map_or(0, |index| index + 1);
    let line_end = if end_of_line < bytes.len() {
        end_of_line + 1
    } else {
        end_of_line
    };
    Some(AnnotationSpan {
        start: annotation_start,
        end: annotation_end,
        line_start,
        line_end,
    })
}

/// Whether `byte`, read as a Latin-1 code point, can be part of an annotation
/// name.
fn is_name_byte(byte: u8) -> bool {
    byte == b'\\' || byte == b'_' || byte.is_ascii_alphanumeric() || {
        let character = char::from(byte);
        byte >= 0x80 && character.is_alphabetic()
    }
}
