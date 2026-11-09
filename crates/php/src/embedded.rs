//! Static PHP string arguments that embed JSON, CSS selectors or XPath
//!.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_MEMBER_CALL, PHP_NAME, PHP_SCOPED_CALL};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_textutil::lowercase;

use crate::PhpIndex;
use crate::assistant::name_context;

const SYMFONY_JSON_RESPONSE: &str = "Symfony\\Component\\HttpFoundation\\JsonResponse";
const SYMFONY_DOM_CRAWLER: &str = "Symfony\\Component\\DomCrawler\\Crawler";
const SYMFONY_CSS_CONVERTER: &str = "Symfony\\Component\\CssSelector\\CssSelectorConverter";

/// A decoded static PHP string. `source_offsets` maps each decoded byte
/// boundary back to the absolute byte offset in the PHP document, so
/// embedded parser ranges stay exact through PHP escapes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct EmbeddedPhpString {
    pub value: Vec<u8>,
    pub content_range: TextRange,
    pub source_offsets: Vec<u32>,
}

pub type EmbeddedJsonLiteral = EmbeddedPhpString;
pub type EmbeddedCssSelector = EmbeddedPhpString;
pub type EmbeddedXPathExpression = EmbeddedPhpString;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum EmbeddedLanguage {
    Json = 1,
    Css,
    XPath,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct EmbeddedLanguageString {
    pub string: EmbeddedPhpString,
    pub language: EmbeddedLanguage,
}

impl EmbeddedPhpString {
    /// The decoded value as text; escapes may produce invalid UTF-8, which is
    /// replaced.
    pub fn text(&self) -> std::borrow::Cow<'_, str> {
        String::from_utf8_lossy(&self.value)
    }

    /// Maps a byte range of the decoded value back into the PHP document.
    pub fn source_range(&self, embedded: TextRange) -> TextRange {
        if self.source_offsets.is_empty() {
            return self.content_range;
        }
        let last = self.source_offsets.len() as u32 - 1;
        let start = u32::from(embedded.start()).min(last);
        let end = u32::from(embedded.end()).min(last).max(start);
        TextRange::new(
            TextSize::from(self.source_offsets[start as usize]),
            TextSize::from(self.source_offsets[end as usize]),
        )
    }
}

fn of_language(
    index: &PhpIndex,
    path: &str,
    version: i64,
    source: &str,
    root: &SyntaxNode,
    language: EmbeddedLanguage,
) -> Vec<EmbeddedPhpString> {
    embedded_language_strings(index, path, version, source, root)
        .into_iter()
        .filter(|string| string.language == language)
        .map(|string| string.string)
        .collect()
}

/// Static string arguments of `JsonResponse::fromJsonString()` and
/// `JsonResponse::setJson()` on semantically resolved receivers.
pub fn embedded_json_literals(
    index: &PhpIndex,
    path: &str,
    version: i64,
    source: &str,
    root: &SyntaxNode,
) -> Vec<EmbeddedJsonLiteral> {
    of_language(index, path, version, source, root, EmbeddedLanguage::Json)
}

/// CSS selectors of `Crawler::filter()`, `Crawler::children()` and
/// `CssSelectorConverter::toXPath()`.
pub fn embedded_css_selectors(
    index: &PhpIndex,
    path: &str,
    version: i64,
    source: &str,
    root: &SyntaxNode,
) -> Vec<EmbeddedCssSelector> {
    of_language(index, path, version, source, root, EmbeddedLanguage::Css)
}

/// XPath expressions of `Crawler::filterXPath()` and `Crawler::evaluate()`.
pub fn embedded_xpath_expressions(
    index: &PhpIndex,
    path: &str,
    version: i64,
    source: &str,
    root: &SyntaxNode,
) -> Vec<EmbeddedXPathExpression> {
    of_language(index, path, version, source, root, EmbeddedLanguage::XPath)
}

/// Every embedded-language string of a document. The CST is scanned and the
/// document analyzed once.
pub fn embedded_language_strings(
    index: &PhpIndex,
    path: &str,
    version: i64,
    source: &str,
    root: &SyntaxNode,
) -> Vec<EmbeddedLanguageString> {
    if source.is_empty() {
        return Vec::new();
    }
    struct Candidate {
        call: SyntaxNode,
        literal: SyntaxNode,
        language: EmbeddedLanguage,
        target: &'static str,
    }
    let mut candidates = Vec::new();
    for call in query::nodes(root, &[PHP_MEMBER_CALL, PHP_SCOPED_CALL]) {
        let (language, target) = match lowercase(&query::call_method_name(&call)).as_str() {
            "fromjsonstring" | "setjson" => (EmbeddedLanguage::Json, SYMFONY_JSON_RESPONSE),
            "filter" | "children" => (EmbeddedLanguage::Css, SYMFONY_DOM_CRAWLER),
            "toxpath" => (EmbeddedLanguage::Css, SYMFONY_CSS_CONVERTER),
            "filterxpath" | "evaluate" => (EmbeddedLanguage::XPath, SYMFONY_DOM_CRAWLER),
            _ => continue,
        };
        let Some(literal) = query::string_argument(&call, 0) else {
            continue;
        };
        if query::argument_expression(&call, 0).as_ref() != Some(&literal) {
            continue;
        }
        candidates.push(Candidate {
            call,
            literal,
            language,
            target,
        });
    }
    if candidates.is_empty() {
        return Vec::new();
    }
    let document = index.analyze_document(path, version, root, source);
    let snapshot = index.semantic_snapshot().with_document(&document);
    let relations = snapshot.relations();

    let mut result = Vec::new();
    for candidate in candidates {
        let Some(receiver) = query::call_receiver(&candidate.call) else {
            continue;
        };
        let mut receiver_type = document.type_of(&receiver).ty;
        if receiver_type.is_unknown()
            && candidate.call.kind() == PHP_SCOPED_CALL
            && receiver.kind() == PHP_NAME
        {
            let name = name_context(&document, u32::from(receiver.text_range().start()))
                .resolve_class(query::node_text(&receiver).trim());
            receiver_type = Type::named(&name, Vec::new());
        }
        if receiver_type.is_unknown()
            || !relations.is_subtype(&receiver_type, &Type::named(candidate.target, Vec::new()))
        {
            continue;
        }
        let Some(string) = decode_static_php_string(source, &candidate.literal) else {
            continue;
        };
        result.push(EmbeddedLanguageString {
            string,
            language: candidate.language,
        });
    }
    result
}

fn decode_static_php_string(source: &str, literal: &SyntaxNode) -> Option<EmbeddedPhpString> {
    let content_range = query::string_content_range(literal);
    let (start, end) = (
        u32::from(content_range.start()),
        u32::from(content_range.end()),
    );
    let bytes = source.as_bytes();
    if start == 0 || start > end || end as usize >= bytes.len() {
        return None;
    }
    let quote = bytes[start as usize - 1];
    if (quote != b'\'' && quote != b'"') || bytes[end as usize] != quote {
        return None;
    }
    let raw = &bytes[start as usize..end as usize];
    let mut output: Vec<u8> = Vec::with_capacity(raw.len());
    let mut offsets: Vec<u32> = Vec::with_capacity(raw.len() + 1);
    offsets.push(start);
    let mut append_mapped = |output: &mut Vec<u8>, value: &[u8], from: u32, to: u32| {
        output.extend_from_slice(value);
        for index in 0..value.len() {
            offsets.push(if index == value.len() - 1 { to } else { from });
        }
    };

    let mut position = 0usize;
    while position < raw.len() {
        if quote == b'"' && php_string_interpolation_at(raw, position) {
            return None;
        }
        let at = start + position as u32;
        if raw[position] != b'\\' || position + 1 >= raw.len() {
            append_mapped(&mut output, &raw[position..=position], at, at + 1);
            position += 1;
            continue;
        }
        let next = raw[position + 1];
        if quote == b'\'' {
            if next == b'\\' || next == b'\'' {
                append_mapped(&mut output, &[next], at, at + 2);
                position += 2;
            } else {
                append_mapped(&mut output, &raw[position..=position], at, at + 1);
                position += 1;
            }
            continue;
        }

        let escaped = match next {
            b'n' => Some(b'\n'),
            b'r' => Some(b'\r'),
            b't' => Some(b'\t'),
            b'v' => Some(0x0b),
            b'e' => Some(0x1b),
            b'f' => Some(0x0c),
            b'\\' | b'$' | b'"' => Some(next),
            _ => None,
        };
        if let Some(escaped) = escaped {
            append_mapped(&mut output, &[escaped], at, at + 2);
            position += 2;
            continue;
        }

        if next == b'x' || next == b'X' {
            let mut stop = position + 2;
            while stop < raw.len() && stop < position + 4 && raw[stop].is_ascii_hexdigit() {
                stop += 1;
            }
            if stop > position + 2
                && let Some(value) = std::str::from_utf8(&raw[position + 2..stop])
                    .ok()
                    .and_then(|digits| u8::from_str_radix(digits, 16).ok())
            {
                append_mapped(&mut output, &[value], at, start + stop as u32);
                position = stop;
                continue;
            }
        }
        if (b'0'..=b'7').contains(&next) {
            let mut stop = position + 1;
            while stop < raw.len() && stop < position + 4 && (b'0'..=b'7').contains(&raw[stop]) {
                stop += 1;
            }
            if let Some(value) = std::str::from_utf8(&raw[position + 1..stop])
                .ok()
                .and_then(|digits| u8::from_str_radix(digits, 8).ok())
            {
                append_mapped(&mut output, &[value], at, start + stop as u32);
                position = stop;
                continue;
            }
        }
        if next == b'u'
            && position + 3 < raw.len()
            && raw[position + 2] == b'{'
            && let Some(close) = raw[position + 3..].iter().position(|byte| *byte == b'}')
        {
            let stop = position + 3 + close;
            // One or more hex digits (no sign) naming a valid character.
            let value = std::str::from_utf8(&raw[position + 3..stop])
                .ok()
                .filter(|digits| {
                    !digits.is_empty() && digits.bytes().all(|b| b.is_ascii_hexdigit())
                })
                .and_then(|digits| u32::from_str_radix(digits, 16).ok())
                .and_then(char::from_u32);
            if let Some(value) = value {
                let mut buffer = [0u8; 4];
                let encoded = value.encode_utf8(&mut buffer);
                append_mapped(&mut output, encoded.as_bytes(), at, start + stop as u32 + 1);
                position = stop + 1;
                continue;
            }
        }

        // PHP keeps the backslash of unknown double-quoted escapes.
        append_mapped(&mut output, &raw[position..=position], at, at + 1);
        position += 1;
    }
    Some(EmbeddedPhpString {
        value: output,
        content_range,
        source_offsets: offsets,
    })
}

fn php_string_interpolation_at(source: &[u8], position: usize) -> bool {
    if position >= source.len() {
        return false;
    }
    if source[position] == b'{' {
        return position + 1 < source.len() && source[position + 1] == b'$';
    }
    if source[position] != b'$' || position + 1 >= source.len() {
        return false;
    }
    let next = source[position + 1];
    next == b'{' || next == b'_' || next.is_ascii_alphabetic() || next >= 0x80
}
