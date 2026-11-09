//! Go `embedded_{css,json,xpath,language}_diagnostics.go`: static PHP
//! strings embedding JSON, CSS selectors or XPath, validated by the native
//! parsers with ranges mapped back into the PHP document.

use std::borrow::Cow;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, InspectionScope, InspectionSpec, Problem, TextDocument,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_php::{
    EmbeddedLanguage, EmbeddedPhpString, PhpIndex, embedded_css_selectors, embedded_json_literals,
    embedded_language_strings, embedded_xpath_expressions,
};
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const INVALID_EMBEDDED_CSS_CODE: &str = "symfony.php.embedded_css.invalid";
pub const INVALID_EMBEDDED_JSON_CODE: &str = "symfony.php.embedded_json.invalid";
pub const INVALID_EMBEDDED_XPATH_CODE: &str = "symfony.php.embedded_xpath.invalid";

const EMBEDDED_CSS_PREFIX: &str = "@media all { ";
const EMBEDDED_CSS_SUFFIX: &str = " {} }";

type Extract = fn(&PhpIndex, &str, i64, &str, &SyntaxNode) -> Vec<EmbeddedPhpString>;

/// The shared guard and scan of the single-language analyzers.
fn analyze_strings(
    php_index: Option<&PhpIndex>,
    ctx: &Context,
    document: &TextDocument,
    extract: Extract,
    diagnose: fn(&EmbeddedPhpString) -> Vec<Problem>,
) -> Vec<Problem> {
    let Some((php_index, root, path)) = php_document(php_index, document) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for string in extract(
        php_index,
        &path,
        i64::from(document.version),
        document.source(),
        &root,
    ) {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        result.extend(diagnose(&string));
    }
    result
}

fn php_document<'a>(
    php_index: Option<&'a PhpIndex>,
    document: &TextDocument,
) -> Option<(&'a PhpIndex, SyntaxNode, String)> {
    let php_index = php_index?;
    let root = document.root()?;
    if !go_to_lower(&document.uri).ends_with(".php") {
        return None;
    }
    let path = swlsp_uriutil::path(&document.uri).ok()?;
    Some((php_index, root, path))
}

/// Validates typed DomCrawler CSS selector arguments through the SCSS
/// parser inside a synthetic `@media` rule.
pub struct EmbeddedCssAnalyzer {
    php_index: Option<PhpIndex>,
}

impl EmbeddedCssAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for EmbeddedCssAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        Ok(analyze_strings(
            self.php_index.as_ref(),
            ctx,
            document,
            embedded_css_selectors,
            embedded_css_diagnostics,
        ))
    }
}

/// Validates `JsonResponse` JSON strings with the JSON parser.
pub struct EmbeddedJsonAnalyzer {
    php_index: Option<PhpIndex>,
}

impl EmbeddedJsonAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for EmbeddedJsonAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        Ok(analyze_strings(
            self.php_index.as_ref(),
            ctx,
            document,
            embedded_json_literals,
            embedded_json_diagnostics,
        ))
    }
}

/// Validates typed DomCrawler `filterXPath()`/`evaluate()` strings.
pub struct EmbeddedXPathAnalyzer {
    php_index: Option<PhpIndex>,
}

impl EmbeddedXPathAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for EmbeddedXPathAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        Ok(analyze_strings(
            self.php_index.as_ref(),
            ctx,
            document,
            embedded_xpath_expressions,
            embedded_xpath_diagnostics,
        ))
    }
}

/// Validates every supported injection signature in one CST scan and one
/// semantic analysis.
pub struct EmbeddedLanguageAnalyzer {
    php_index: Option<PhpIndex>,
}

impl EmbeddedLanguageAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for EmbeddedLanguageAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.embedded_language",
        description: "Invalid CSS selectors, JSON and XPath expressions in PHP string arguments that expect them.",
        domain: "symfony",
        scope: InspectionScope::Language,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[
            INVALID_EMBEDDED_CSS_CODE,
            INVALID_EMBEDDED_JSON_CODE,
            INVALID_EMBEDDED_XPATH_CODE,
        ],
    };
}

impl ProblemAnalyzer for EmbeddedLanguageAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some((php_index, root, path)) = php_document(self.php_index.as_ref(), document) else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        for literal in embedded_language_strings(
            php_index,
            &path,
            i64::from(document.version),
            document.source(),
            &root,
        ) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            result.extend(match literal.language {
                EmbeddedLanguage::Json => embedded_json_diagnostics(&literal.string),
                EmbeddedLanguage::Css => embedded_css_diagnostics(&literal.string),
                EmbeddedLanguage::XPath => embedded_xpath_diagnostics(&literal.string),
            });
        }
        Ok(result)
    }
}

/// The decoded value as parser input. Go parses the raw bytes; escapes that
/// decode to invalid UTF-8 are replaced here, which may shift ranges after
/// the replaced bytes.
fn embedded_text(string: &EmbeddedPhpString) -> Cow<'_, str> {
    string.text()
}

fn embedded_problem(id: &str, range: TextRange, message: String) -> Problem {
    Problem {
        id: DiagnosticId::from(id),
        range,
        message,
        severity: Some(DiagnosticSeverity::ERROR),
        source: "symfony".to_owned(),
        ..Problem::default()
    }
}

fn embedded_css_diagnostics(selector: &EmbeddedPhpString) -> Vec<Problem> {
    let value = embedded_text(selector);
    let parsed = swlsp_parser_scss::parse(&format!(
        "{EMBEDDED_CSS_PREFIX}{value}{EMBEDDED_CSS_SUFFIX}"
    ));
    for error in &parsed.errors {
        let Some(embedded) = embedded_css_error_range(error.range, value.len() as u32) else {
            continue;
        };
        // The synthetic rule suffix can cascade one unbalanced selector
        // delimiter into additional closing-block errors. Report the first
        // selector error only.
        return vec![embedded_problem(
            INVALID_EMBEDDED_CSS_CODE,
            embedded_diagnostic_host_range(selector, embedded),
            format!("Invalid CSS selector: {}", error.message()),
        )];
    }
    Vec::new()
}

fn embedded_css_error_range(wrapped: TextRange, selector_length: u32) -> Option<TextRange> {
    let prefix_length = EMBEDDED_CSS_PREFIX.len() as u32;
    let selector_end = prefix_length + selector_length;
    let (start, end) = (u32::from(wrapped.start()), u32::from(wrapped.end()));
    if end < prefix_length {
        return None;
    }
    let start = start.max(prefix_length).min(selector_end);
    let end = end.max(prefix_length).min(selector_end);
    Some(TextRange::new(
        TextSize::from(start - prefix_length),
        TextSize::from(end - prefix_length),
    ))
}

fn embedded_json_diagnostics(literal: &EmbeddedPhpString) -> Vec<Problem> {
    let parsed = swlsp_parser_json::parse(&embedded_text(literal));
    parsed
        .errors
        .iter()
        .map(|error| {
            embedded_problem(
                INVALID_EMBEDDED_JSON_CODE,
                embedded_diagnostic_host_range(literal, error.range),
                format!("Invalid JSON: {}", error.message()),
            )
        })
        .collect()
}

fn embedded_xpath_diagnostics(expression: &EmbeddedPhpString) -> Vec<Problem> {
    let parsed = swlsp_parser_xpath::parse(&embedded_text(expression));
    let Some(error) = parsed.errors.first() else {
        return Vec::new();
    };
    vec![embedded_problem(
        INVALID_EMBEDDED_XPATH_CODE,
        embedded_diagnostic_host_range(expression, error.range),
        format!("Invalid XPath expression: {}", error.message()),
    )]
}

/// Go `embeddedDiagnosticHostRange`: maps an embedded range into the PHP
/// document and widens an empty range by one byte inside the content.
fn embedded_diagnostic_host_range(literal: &EmbeddedPhpString, embedded: TextRange) -> TextRange {
    let host = literal.source_range(embedded);
    if !host.is_empty() {
        return host;
    }
    let one = TextSize::from(1);
    if host.end() < literal.content_range.end() {
        TextRange::new(host.start(), host.end() + one)
    } else if host.start() > literal.content_range.start() {
        TextRange::new(host.start() - one, host.end())
    } else {
        host
    }
}
