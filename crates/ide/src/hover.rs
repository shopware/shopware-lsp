//! Hover providers.
//!
//! Every provider builds Markdown from byte ranges and converts byte
//! ranges to UTF-16 positions only when building the protocol range.

use swlsp_language::LanguageId;
use swlsp_lsp::{HoverRequest, SyntaxContext};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{TK_STRING as JS_TK_STRING, TK_TEMPLATE as JS_TK_TEMPLATE};
use swlsp_parser_twig::ast::{AstNode, TwigLiteralString};
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::filepath;

pub fn hover_protocol_range(range: TextRange, line_index: &LineIndex) -> protocol::Range {
    let (start_line, start_character) = line_index.position_utf16(u32::from(range.start()));
    let (end_line, end_character) = line_index.position_utf16(u32::from(range.end()));
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

pub fn markdown_hover(value: String, range: Option<protocol::Range>) -> protocol::Hover {
    protocol::Hover {
        contents: protocol::MarkupContent {
            kind: protocol::MarkupKind::Markdown,
            value,
        },
        range,
    }
}

/// Escapes backticks so `value` can sit inside Markdown inline code.
pub fn escape_markdown(value: &str) -> String {
    value.replace('`', "\\`")
}

/// The lowercase extension of `path`, including the leading dot.
pub fn lower_ext(path: &str) -> String {
    filepath::ext(path).to_lowercase()
}

/// `target` relative to `base`, or `target` unchanged when no relative path
/// exists.
pub fn rel_or(base: &str, target: &str) -> String {
    filepath::rel(base, target).unwrap_or_else(|_| target.to_owned())
}

/// The request document's URI.
pub fn request_uri(request: &HoverRequest) -> &str {
    &request.params.text_document.uri
}

/// The byte offset of the hover position.
pub fn request_offset(request: &HoverRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    let position = request.params.position;
    Some(line_index.offset_utf16(position.line, position.character))
}

/// The request's PHP semantic context for PHP documents.
pub fn php_context(php: Option<&PhpIndex>, syntax: &SyntaxContext) -> Option<PhpContext> {
    if syntax.language != Some(LanguageId::Php) {
        return None;
    }
    let php = php?;
    let document = syntax.document.as_ref()?;
    let state = swlsp_lsp_phpanalysis::for_document(php, document)
        .ok()
        .flatten()?;
    Some(php.analyzed_snapshot_context(
        syntax.node.as_ref(),
        state.document.clone(),
        Some(state.snapshot.clone()),
    ))
}

/// The protocol range of a CST byte range, converted to UTF-16 against the
/// request's line index. `None` without a range or a line index.
pub fn request_byte_range(
    request: &HoverRequest,
    range: Option<TextRange>,
) -> Option<protocol::Range> {
    Some(hover_protocol_range(range?, request.line_index()?))
}

/// The content range of a string whose trivia-trimmed `range` spells `text`:
/// one leading quote and a matching trailing quote are excluded.
pub fn unquoted_range(range: TextRange, text: &str) -> TextRange {
    let trimmed = text.trim_end_matches([' ', '\t', '\r', '\n', '\x0c', '\x0b']);
    let mut start = range.start();
    let mut end = range.end() - TextSize::of(&text[trimmed.len()..]);
    let bytes = trimmed.as_bytes();
    if let Some(&quote @ (b'\'' | b'"' | b'`')) = bytes.first() {
        start += TextSize::from(1);
        if bytes.len() >= 2 && bytes[bytes.len() - 1] == quote {
            end -= TextSize::from(1);
        }
    }
    TextRange::new(start, end)
}

/// The byte range of a Twig string literal's unquoted contents.
pub fn twig_string_content_range(literal: &SyntaxNode) -> Option<TextRange> {
    let string = TwigLiteralString::cast(literal.clone())?;
    if let Some(inner) = string.inner() {
        return Some(inner.syntax().text_range());
    }
    let range = literal.range_trimmed_trivia();
    let text = literal.text().to_string();
    let skip = usize::from(range.start() - literal.text_range().start());
    Some(unquoted_range(range, &text[skip..]))
}

/// The byte range of the unquoted contents of the JavaScript string at
/// `node`, matching `jsquery::string_value`.
pub fn js_string_content_range(node: &SyntaxNode) -> Option<TextRange> {
    let token = jsquery::string_at(node)?
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| matches!(token.kind(), JS_TK_STRING | JS_TK_TEMPLATE))?;
    Some(unquoted_range(token.text_range(), token.text()))
}
