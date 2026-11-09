//! Missing Storefront and Administration snippets.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_BINARY_EXPRESSION, TWIG_LITERAL_STRING, TWIG_OPERAND};
use swlsp_snippet::SnippetIndexer;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_translation::Index as TranslationIndex;

use super::lower_ext;
use crate::LookupError;
use crate::ProblemAnalyzer;

const ADMIN_SNIPPET_MISSING: &str = "admin.snippet.missing";
const FRONTEND_SNIPPET_MISSING: &str = "frontend.snippet.missing";

pub struct SnippetAnalyzer {
    snippet_index: Option<Arc<SnippetIndexer>>,
    translation_index: Option<Arc<TranslationIndex>>,
}

impl SnippetAnalyzer {
    pub fn new(
        snippet_index: Option<Arc<SnippetIndexer>>,
        translation_index: Option<Arc<TranslationIndex>>,
    ) -> Self {
        Self {
            snippet_index,
            translation_index,
        }
    }
}

fn snippet_problem(id: &str, range: TextRange, message: String, key: &str) -> Problem {
    let mut payload = Map::new();
    payload.insert("snippetText".into(), Value::String(key.to_owned()));
    Problem {
        id: id.into(),
        range,
        message,
        source: "shopware".into(),
        severity: Some(protocol::DiagnosticSeverity::ERROR),
        payload: Some(Value::Object(payload)),
        ..Problem::default()
    }
}

impl ProblemAnalyzer for SnippetAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.snippet_index) + crate::inputs(&self.translation_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.snippet_index, document.root()) else {
            return Ok(Vec::new());
        };
        if is_test_fixture(&document.uri) {
            return Ok(Vec::new());
        }
        match lower_ext(&document.uri).as_str() {
            ".twig" => self.twig_diagnostics(ctx, document, index, &root),
            ".js" | ".ts" => js_diagnostics(ctx, index, &root),
            _ => Ok(Vec::new()),
        }
    }
}

impl SnippetAnalyzer {
    fn twig_diagnostics(
        &self,
        ctx: &Context,
        document: &TextDocument,
        index: &SnippetIndexer,
        root: &SyntaxNode,
    ) -> Result<Vec<Problem>, BoxError> {
        let mut diagnostics = Vec::new();
        if document.uri.contains("/Resources/app/administration/") {
            for reference in swlsp_snippet::admin_twig_references(root) {
                if ctx.is_cancelled() {
                    return Ok(Vec::new());
                }
                let key = reference.key;
                if key.is_empty() {
                    continue;
                }
                let snippets = index.admin_snippet(&key).map_err(|error| {
                    LookupError::new(format!("query admin snippet {:?}", key), error)
                })?;
                if snippets.is_empty() {
                    diagnostics.push(snippet_problem(
                        ADMIN_SNIPPET_MISSING,
                        reference.range,
                        format!("Admin snippet '{key}' not found"),
                        &key,
                    ));
                }
            }
            return Ok(diagnostics);
        }
        let mut matches = Vec::new();
        for candidate in twigquery::iterate_nodes(root, &[TWIG_LITERAL_STRING]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if twigquery::string_in_filter(&candidate, &["trans"]) && is_whole_key(&candidate) {
                matches.push(candidate);
            }
        }
        for candidate in matches {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let key = twigquery::string_value(&candidate).unwrap_or_default();
            if key.is_empty() {
                continue;
            }
            let snippets = index.frontend_snippet(&key).map_err(|error| {
                LookupError::new(format!("query frontend snippet {:?}", key), error)
            })?;
            if snippets.is_empty()
                && let Some(translations) = &self.translation_index
            {
                let exists = translations
                    .has_message("messages", &key)
                    .map_err(|error| {
                        LookupError::new(format!("query Symfony translation {:?}", key), error)
                    })?;
                if exists {
                    continue;
                }
            }
            if snippets.is_empty() {
                diagnostics.push(snippet_problem(
                    FRONTEND_SNIPPET_MISSING,
                    candidate.range_trimmed_trivia(),
                    format!("Snippet '{key}' not found"),
                    &key,
                ));
            }
        }
        Ok(diagnostics)
    }
}

/// Test input under a `__fixtures__` or `__mocks__` directory: data for a
/// test (a codemod's legacy component, a mocked module), not shipped code
/// whose snippets must exist.
fn is_test_fixture(uri: &str) -> bool {
    uri.contains("/__fixtures__/") || uri.contains("/__mocks__/")
}

/// Whether the literal is the complete translation key: not interpolated
/// (`"a.#{b}"`) and not one operand of a computed key (`'a.' ~ b`).
fn is_whole_key(literal: &SyntaxNode) -> bool {
    twigquery::string_is_static(literal)
        && !literal
            .ancestors()
            .skip(1)
            .take_while(|node| node.kind() != TWIG_OPERAND)
            .any(|node| node.kind() == TWIG_BINARY_EXPRESSION)
}

fn js_diagnostics(
    ctx: &Context,
    index: &SnippetIndexer,
    root: &SyntaxNode,
) -> Result<Vec<Problem>, BoxError> {
    let mut diagnostics = Vec::new();
    for node in swlsp_snippet::admin_javascript_string_references(root) {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let key = swlsp_parser_js::query::string_value(&node);
        if key.is_empty() {
            continue;
        }
        let snippets = index
            .admin_snippet(&key)
            .map_err(|error| LookupError::new(format!("query admin snippet {:?}", key), error))?;
        if snippets.is_empty() {
            diagnostics.push(snippet_problem(
                ADMIN_SNIPPET_MISSING,
                node.range_trimmed_trivia(),
                format!("Admin snippet '{key}' not found"),
                &key,
            ));
        }
    }
    Ok(diagnostics)
}
