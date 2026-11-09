//! Go `admin_i18n_deprecation.go`.

use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_twig::ast::{AstNode, TwigFunctionCall, TwigLiteralName};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use super::{go_ext, payload, text};
use crate::ProblemAnalyzer;

const DEPRECATED_ADMIN_I18N_TC_CODE: &str = "admin.vue-i18n.tc-deprecated";

/// Mirrors Shopware Administration's `no-tc-translation` ESLint rule for the
/// native JavaScript, Twig and mixed Vue CSTs. It targets calls only, so
/// declarations and references to a property named `$tc` are left untouched
/// (Go `AdminI18nDeprecationAnalyzer`).
#[derive(Clone, Copy, Debug, Default)]
pub struct AdminI18nDeprecationAnalyzer;

impl AdminI18nDeprecationAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for AdminI18nDeprecationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !document.uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        let ext = go_to_lower(go_ext(&document.uri));
        if !matches!(ext.as_str(), ".js" | ".ts" | ".twig" | ".vue") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        if matches!(ext.as_str(), ".js" | ".ts" | ".vue") {
            for call in jsquery::iter_calls(&root, &[]) {
                let Some(name) = jsquery::call_callee(&call)
                    .and_then(|callee| jsquery::member_name_node(&callee))
                else {
                    continue;
                };
                if name.text().to_string().trim() != "$tc" {
                    continue;
                }
                result.push(deprecated_tc_problem(SyntaxElement::Node(name)));
            }
        }
        if matches!(ext.as_str(), ".twig" | ".vue") {
            for call in twigquery::iterate_nodes(&root, &[twigsyntax::TWIG_FUNCTION_CALL]) {
                if twigquery::function_name(&call).as_deref() != Some("$tc") {
                    continue;
                }
                let Some(name_operand) =
                    TwigFunctionCall::cast(call.clone()).and_then(|typed| typed.name_operand())
                else {
                    continue;
                };
                for name in twigquery::iterate_nodes(
                    name_operand.syntax(),
                    &[twigsyntax::TWIG_LITERAL_NAME],
                ) {
                    let Some(token) =
                        TwigLiteralName::cast(name).and_then(|literal| literal.name())
                    else {
                        continue;
                    };
                    if token.text() == "$tc" {
                        result.push(deprecated_tc_problem(SyntaxElement::Token(token)));
                        break;
                    }
                }
            }
        }
        Ok(result)
    }
}

fn deprecated_tc_problem(name: SyntaxElement) -> Problem {
    let range = match &name {
        SyntaxElement::Node(node) => node.range_trimmed_trivia(),
        SyntaxElement::Token(token) => token.text_range(),
    };
    Problem {
        id: DEPRECATED_ADMIN_I18N_TC_CODE.into(),
        range,
        element: Some(name),
        message: "Use $t() instead of deprecated $tc(); $t() handles pluralization natively"
            .to_owned(),
        severity: Some(DiagnosticSeverity::WARNING),
        source: "shopware-lsp".into(),
        payload: Some(payload(vec![("replacement", text("$t"))])),
        ..Problem::default()
    }
}
