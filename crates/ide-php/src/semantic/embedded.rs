//! Embedded JSON, CSS selector and XPath tokens in PHP string arguments.

use std::borrow::Cow;
use std::collections::HashSet;

use swlsp_lsp::{
    Context, ProviderResult, SemanticToken, SemanticTokensProvider, SemanticTokensRequest,
    TextDocument,
};
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_CLASS, SEMANTIC_TOKEN_FUNCTION, SEMANTIC_TOKEN_KEYWORD, SEMANTIC_TOKEN_NUMBER,
    SEMANTIC_TOKEN_OPERATOR, SEMANTIC_TOKEN_PROPERTY, SEMANTIC_TOKEN_STRING, SEMANTIC_TOKEN_TYPE,
    SEMANTIC_TOKEN_VARIABLE,
};
use swlsp_php::{
    EmbeddedLanguage, EmbeddedPhpString, PhpIndex, embedded_css_selectors, embedded_json_literals,
    embedded_language_strings, embedded_xpath_expressions,
};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize, Token};

use crate::semantic::{has_suffix_fold, sort_tokens, token};

/// The PHP document a provider should analyze: `(path, version, source,
/// root)`, or `None` for unsupported requests.
fn php_document(request: &SemanticTokensRequest) -> Option<(String, &TextDocument, SyntaxNode)> {
    let document: &TextDocument = &request.document;
    let root = document.root()?;
    if !has_suffix_fold(&document.uri, ".php") {
        return None;
    }
    let path = swlsp_uriutil::path(&document.uri).ok()?;
    Some((path, document, root))
}

fn collect(
    ctx: &Context,
    strings: impl IntoIterator<Item = (EmbeddedPhpString, EmbeddedLanguage)>,
) -> Vec<SemanticToken> {
    let mut result = Vec::new();
    for (string, language) in strings {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        match language {
            EmbeddedLanguage::Json => result.extend(embedded_json_semantic_tokens(&string)),
            EmbeddedLanguage::Css => result.extend(embedded_css_semantic_tokens(&string)),
            EmbeddedLanguage::XPath => result.extend(embedded_xpath_semantic_tokens(&string)),
        }
    }
    sort_tokens(&mut result);
    result
}

/// Colors every supported Symfony language-injection signature in one PHP
/// CST scan and one semantic analysis.
pub struct EmbeddedLanguageProvider {
    php_index: Option<PhpIndex>,
}

impl EmbeddedLanguageProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl SemanticTokensProvider for EmbeddedLanguageProvider {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        let Some((path, document, root)) = php_document(request) else {
            return Ok(Vec::new());
        };
        let strings = embedded_language_strings(
            php_index,
            &path,
            i64::from(document.version),
            document.source(),
            &root,
        );
        Ok(collect(
            ctx,
            strings
                .into_iter()
                .map(|string| (string.string, string.language)),
        ))
    }
}

type Extractor = fn(&PhpIndex, &str, i64, &str, &SyntaxNode) -> Vec<EmbeddedPhpString>;

fn single_language(
    index: &PhpIndex,
    ctx: &Context,
    request: &SemanticTokensRequest,
    extract: Extractor,
    language: EmbeddedLanguage,
) -> Vec<SemanticToken> {
    let Some((path, document, root)) = php_document(request) else {
        return Vec::new();
    };
    let strings = extract(
        index,
        &path,
        i64::from(document.version),
        document.source(),
        &root,
    );
    collect(ctx, strings.into_iter().map(|string| (string, language)))
}

/// Colors decoded `JsonResponse` JSON arguments with standard semantic token
/// kinds.
pub struct EmbeddedJsonProvider {
    php_index: Option<PhpIndex>,
}

impl EmbeddedJsonProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl SemanticTokensProvider for EmbeddedJsonProvider {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        Ok(single_language(
            php_index,
            ctx,
            request,
            embedded_json_literals,
            EmbeddedLanguage::Json,
        ))
    }
}

/// Colors typed DomCrawler selector arguments with the native SCSS lexer.
pub struct EmbeddedCssProvider {
    php_index: Option<PhpIndex>,
}

impl EmbeddedCssProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl SemanticTokensProvider for EmbeddedCssProvider {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        Ok(single_language(
            php_index,
            ctx,
            request,
            embedded_css_selectors,
            EmbeddedLanguage::Css,
        ))
    }
}

/// Colors typed DomCrawler XPath expressions from the native XPath lexer.
pub struct EmbeddedXPathProvider {
    php_index: Option<PhpIndex>,
}

impl EmbeddedXPathProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl SemanticTokensProvider for EmbeddedXPathProvider {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        Ok(single_language(
            php_index,
            ctx,
            request,
            embedded_xpath_expressions,
            EmbeddedLanguage::XPath,
        ))
    }
}

/// The decoded value as lexer input. PHP escapes can produce invalid UTF-8,
/// whose bytes are each replaced with U+FFFD, so the lexers treat a raw byte >=
/// 0x80 like any other non-ASCII character; `raw_offsets` maps every text byte
/// offset back to the decoded value.
pub(crate) struct EmbeddedText<'a> {
    pub(crate) text: Cow<'a, str>,
    raw_offsets: Option<Vec<u32>>,
}

impl EmbeddedText<'_> {
    pub(crate) fn new(value: &[u8]) -> EmbeddedText<'_> {
        if let Ok(text) = std::str::from_utf8(value) {
            return EmbeddedText {
                text: Cow::Borrowed(text),
                raw_offsets: None,
            };
        }
        let mut text = String::with_capacity(value.len() + 8);
        let mut offsets = Vec::with_capacity(value.len() + 8);
        let mut raw = 0usize;
        while raw < value.len() {
            let rest = &value[raw..];
            let (valid, invalid) = match std::str::from_utf8(rest) {
                Ok(_) => (rest.len(), 0),
                Err(error) => (
                    error.valid_up_to(),
                    error
                        .error_len()
                        .unwrap_or(rest.len() - error.valid_up_to()),
                ),
            };
            text.push_str(std::str::from_utf8(&rest[..valid]).unwrap_or_default());
            offsets.extend((0..valid).map(|index| (raw + index) as u32));
            raw += valid;
            for _ in 0..invalid {
                text.push('\u{FFFD}');
                offsets.extend([raw as u32; 3]);
                raw += 1;
            }
        }
        offsets.push(raw as u32);
        EmbeddedText {
            text: Cow::Owned(text),
            raw_offsets: Some(offsets),
        }
    }

    /// Maps a range of [`Self::text`] to the decoded value.
    pub(crate) fn raw(&self, range: TextRange) -> TextRange {
        let Some(offsets) = &self.raw_offsets else {
            return range;
        };
        let at = |offset: TextSize| {
            let offset = (u32::from(offset) as usize).min(offsets.len() - 1);
            TextSize::from(offsets[offset])
        };
        TextRange::new(at(range.start()), at(range.end()))
    }
}

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end.max(start)))
}

fn push_mapped(
    result: &mut Vec<SemanticToken>,
    string: &EmbeddedPhpString,
    text: &EmbeddedText<'_>,
    embedded: TextRange,
    token_type: u32,
) {
    result.push(token(string.source_range(text.raw(embedded)), token_type));
}

// --- JSON -----------------------------------------------------------------

pub(crate) fn embedded_json_semantic_tokens(literal: &EmbeddedPhpString) -> Vec<SemanticToken> {
    use swlsp_parser_json::syntax::{
        JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_PAIR, JSON_STRING,
    };
    let embedded_text = EmbeddedText::new(&literal.value);
    let text = &embedded_text.text;
    let parsed = swlsp_parser_json::parse(text);
    let root = parsed.root();
    let mut result = Vec::new();
    let mut keys = HashSet::new();
    let append = |result: &mut Vec<SemanticToken>, embedded: TextRange, token_type: u32| {
        let host = literal.source_range(embedded_text.raw(embedded));
        if host.start() >= host.end() {
            return;
        }
        result.push(token(host, token_type));
    };
    for pair in root.descendants().filter(|node| node.kind() == JSON_PAIR) {
        let Some(key) = pair.children().find(|child| child.kind() == JSON_STRING) else {
            continue;
        };
        keys.insert(key.text_range());
        append(
            &mut result,
            json_string_content_range(&key),
            SEMANTIC_TOKEN_PROPERTY,
        );
    }
    for node in root.descendants() {
        let kind = node.kind();
        if kind != JSON_STRING
            && kind != JSON_NUMBER
            && kind != JSON_BOOLEAN
            && kind != JSON_NULL_VALUE
        {
            continue;
        }
        if keys.contains(&node.text_range()) {
            continue;
        }
        let (token_type, embedded) = if kind == JSON_STRING {
            (SEMANTIC_TOKEN_STRING, json_string_content_range(&node))
        } else if kind == JSON_NUMBER {
            (SEMANTIC_TOKEN_NUMBER, node.text_range())
        } else {
            (SEMANTIC_TOKEN_KEYWORD, node.text_range())
        };
        append(&mut result, embedded, token_type);
    }
    result
}

fn json_string_content_range(node: &SyntaxNode) -> TextRange {
    let trimmed = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim();
    let bytes = text.as_bytes();
    if bytes.len() >= 2 && bytes[0] == b'"' && bytes[bytes.len() - 1] == b'"' {
        return range(
            u32::from(trimmed.start()) + 1,
            u32::from(trimmed.end()).saturating_sub(1),
        );
    }
    trimmed
}

// --- shared lexer helpers -------------------------------------------------

fn previous_significant<'a>(tokens: &'a [Token<'a>], index: usize) -> (Kind, &'a str) {
    tokens[..index]
        .iter()
        .rev()
        .find(|token| !token.kind.is_trivia())
        .map_or((Kind::NONE, ""), |token| (token.kind, token.text))
}

fn next_significant<'a>(tokens: &'a [Token<'a>], index: usize) -> (Kind, &'a str) {
    tokens[index + 1..]
        .iter()
        .find(|token| !token.kind.is_trivia())
        .map_or((Kind::NONE, ""), |token| (token.kind, token.text))
}

/// The token range, shrunk to the content when it is quoted on both ends.
fn unquoted_range(token: &Token<'_>) -> (u32, u32) {
    let start = token.start;
    let end = start + token.text.len() as u32;
    let bytes = token.text.as_bytes();
    if bytes.len() >= 2 && bytes[0] == bytes[bytes.len() - 1] {
        (start + 1, end - 1)
    } else {
        (start, end)
    }
}

// --- CSS ------------------------------------------------------------------

pub(crate) fn embedded_css_semantic_tokens(selector: &EmbeddedPhpString) -> Vec<SemanticToken> {
    use swlsp_parser_scss::syntax::{
        TK_CLOSE_BRACKET, TK_COLON, TK_DOUBLE_QUOTED_STRING, TK_HASH, TK_IDENTIFIER, TK_NUMBER,
        TK_OPEN_BRACKET, TK_OPEN_PAREN, TK_OPERATOR, TK_SINGLE_QUOTED_STRING,
    };
    let embedded_text = EmbeddedText::new(&selector.value);
    let text = &embedded_text.text;
    let tokens = swlsp_parser_scss::lexer::lex(text);
    let mut result = Vec::new();
    let mut bracket_depth = 0usize;
    for (index, current) in tokens.iter().enumerate() {
        if current.kind.is_trivia() {
            continue;
        }
        let (previous_kind, previous_text) = previous_significant(&tokens, index);
        let (next_kind, _) = next_significant(&tokens, index);
        let mut start = current.start;
        let mut end = start + current.text.len() as u32;
        let token_type = match current.kind {
            kind if kind == TK_OPEN_BRACKET => {
                bracket_depth += 1;
                None
            }
            kind if kind == TK_CLOSE_BRACKET => {
                bracket_depth = bracket_depth.saturating_sub(1);
                None
            }
            kind if kind == TK_IDENTIFIER => Some(if previous_text == "." {
                SEMANTIC_TOKEN_CLASS
            } else if previous_kind == TK_HASH {
                SEMANTIC_TOKEN_VARIABLE
            } else if previous_kind == TK_COLON && next_kind == TK_OPEN_PAREN {
                SEMANTIC_TOKEN_FUNCTION
            } else if previous_kind == TK_COLON {
                SEMANTIC_TOKEN_KEYWORD
            } else if bracket_depth > 0 && previous_kind == TK_OPERATOR {
                SEMANTIC_TOKEN_STRING
            } else if bracket_depth > 0 {
                SEMANTIC_TOKEN_PROPERTY
            } else {
                SEMANTIC_TOKEN_TYPE
            }),
            kind if kind == TK_SINGLE_QUOTED_STRING || kind == TK_DOUBLE_QUOTED_STRING => {
                (start, end) = unquoted_range(current);
                Some(SEMANTIC_TOKEN_STRING)
            }
            kind if kind == TK_NUMBER => Some(SEMANTIC_TOKEN_NUMBER),
            kind if kind == TK_OPERATOR && current.text != "." => Some(SEMANTIC_TOKEN_OPERATOR),
            _ => None,
        };
        let Some(token_type) = token_type else {
            continue;
        };
        if start >= end {
            continue;
        }
        push_mapped(
            &mut result,
            selector,
            &embedded_text,
            range(start, end),
            token_type,
        );
    }
    result
}

// --- XPath ----------------------------------------------------------------

pub(crate) fn embedded_xpath_semantic_tokens(expression: &EmbeddedPhpString) -> Vec<SemanticToken> {
    use swlsp_parser_xpath::syntax::{
        TK_AT, TK_DOLLAR, TK_DOUBLE_COLON, TK_NAME, TK_NUMBER, TK_OPEN_PAREN, TK_STRING,
    };
    let embedded_text = EmbeddedText::new(&expression.value);
    let text = &embedded_text.text;
    let tokens = swlsp_parser_xpath::lexer::lex(text);
    let mut result = Vec::new();
    for (index, current) in tokens.iter().enumerate() {
        if current.kind.is_trivia() {
            continue;
        }
        let (previous_kind, _) = previous_significant(&tokens, index);
        let (next_kind, _) = next_significant(&tokens, index);
        let mut start = current.start;
        let mut end = start + current.text.len() as u32;
        let token_type = match current.kind {
            kind if kind == TK_NAME => Some(if previous_kind == TK_DOLLAR {
                SEMANTIC_TOKEN_VARIABLE
            } else if previous_kind == TK_AT {
                SEMANTIC_TOKEN_PROPERTY
            } else if next_kind == TK_DOUBLE_COLON {
                SEMANTIC_TOKEN_KEYWORD
            } else if next_kind == TK_OPEN_PAREN {
                SEMANTIC_TOKEN_FUNCTION
            } else if xpath_operator_name(current.text)
                && xpath_word_operator_context(previous_kind, next_kind)
            {
                SEMANTIC_TOKEN_OPERATOR
            } else {
                SEMANTIC_TOKEN_TYPE
            }),
            kind if kind == TK_STRING => {
                (start, end) = unquoted_range(current);
                Some(SEMANTIC_TOKEN_STRING)
            }
            kind if kind == TK_NUMBER => Some(SEMANTIC_TOKEN_NUMBER),
            kind if xpath_operator_token(kind) => Some(SEMANTIC_TOKEN_OPERATOR),
            _ => None,
        };
        let Some(token_type) = token_type else {
            continue;
        };
        if start >= end {
            continue;
        }
        push_mapped(
            &mut result,
            expression,
            &embedded_text,
            range(start, end),
            token_type,
        );
    }
    result
}

fn xpath_word_operator_context(previous: Kind, next: Kind) -> bool {
    use swlsp_parser_xpath::syntax::*;
    let previous_operand = [
        TK_NAME,
        TK_NUMBER,
        TK_STRING,
        TK_CLOSE_PAREN,
        TK_CLOSE_BRACKET,
        TK_DOT,
        TK_DOUBLE_DOT,
        TK_STAR,
    ]
    .contains(&previous);
    previous_operand
        && [
            TK_NAME,
            TK_NUMBER,
            TK_STRING,
            TK_OPEN_PAREN,
            TK_DOT,
            TK_DOUBLE_DOT,
            TK_AT,
            TK_DOLLAR,
            TK_MINUS,
            TK_STAR,
        ]
        .contains(&next)
}

fn xpath_operator_name(value: &str) -> bool {
    matches!(value.to_lowercase().as_str(), "and" | "or" | "mod" | "div")
}

fn xpath_operator_token(kind: Kind) -> bool {
    use swlsp_parser_xpath::syntax::*;
    [
        TK_SLASH,
        TK_DOUBLE_SLASH,
        TK_DOT,
        TK_DOUBLE_DOT,
        TK_AT,
        TK_COLON,
        TK_DOUBLE_COLON,
        TK_PIPE,
        TK_PLUS,
        TK_MINUS,
        TK_STAR,
        TK_EQUALS,
        TK_NOT_EQUALS,
        TK_LESS,
        TK_LESS_EQUALS,
        TK_GREATER,
        TK_GREATER_EQUALS,
    ]
    .contains(&kind)
}
