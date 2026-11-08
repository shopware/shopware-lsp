//! Go `twig_deprecation_diagnostics.go`.

use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_FILTER, TWIG_FUNCTION_CALL, TWIG_LITERAL_NAME};
use swlsp_php_types::{go_quote, go_to_lower};
use swlsp_syntax::SyntaxNode;
use swlsp_twig::TwigIndexer;

use crate::ProblemAnalyzer;
use crate::common::value_node_text_range;

const DEPRECATED_TWIG_FUNCTION_CODE: &str = "twig.function.deprecated";
const DEPRECATED_TWIG_FILTER_CODE: &str = "twig.filter.deprecated";
const DEPRECATED_TWIG_TEST_CODE: &str = "twig.test.deprecated";
const DEPRECATED_TWIG_TAG_CODE: &str = "twig.tag.deprecated";

/// Go `TwigDeprecationAnalyzer`.
pub struct TwigDeprecationAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
}

impl TwigDeprecationAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>) -> Self {
        Self { twig_index }
    }
}

impl ProblemAnalyzer for TwigDeprecationAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(index) = &self.twig_index else {
            return Ok(Vec::new());
        };
        if !go_to_lower(&document.uri).ends_with(".twig") {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        for call in twigquery::iterate_nodes(&root, &[TWIG_FUNCTION_CALL]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if swlsp_twig::is_test_function_call(&call) {
                continue;
            }
            let name = twigquery::function_name(&call).unwrap_or_default();
            let Some(message) = index.twig_function_deprecation(&name).map_err(|error| {
                format!(
                    "query Twig function {} deprecation: {error}",
                    go_quote(&name)
                )
            })?
            else {
                continue;
            };
            let node = twig_callable_name_node(&call, &name, false);
            result.push(callable_deprecation(
                Some(&node),
                &name,
                "function",
                DEPRECATED_TWIG_FUNCTION_CODE,
                &message,
            ));
        }
        for filter in twigquery::iterate_nodes(&root, &[TWIG_FILTER]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let name = twigquery::filter_name(&filter).unwrap_or_default();
            let Some(message) = index.twig_filter_deprecation(&name).map_err(|error| {
                format!("query Twig filter {} deprecation: {error}", go_quote(&name))
            })?
            else {
                continue;
            };
            let node = twig_callable_name_node(&filter, &name, true);
            result.push(callable_deprecation(
                Some(&node),
                &name,
                "filter",
                DEPRECATED_TWIG_FILTER_CODE,
                &message,
            ));
        }
        for expression in swlsp_twig::test_expressions(&root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(message) = index
                .twig_test_deprecation(&expression.name)
                .map_err(|error| {
                    format!(
                        "query Twig test {} deprecation: {error}",
                        go_quote(&expression.name)
                    )
                })?
            else {
                continue;
            };
            result.push(callable_deprecation(
                expression.node.as_ref(),
                &expression.name,
                "test",
                DEPRECATED_TWIG_TEST_CODE,
                &message,
            ));
        }
        for usage in swlsp_twig::twig_tag_usages(document.source().as_bytes()) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(message) = index.twig_tag_deprecation(&usage.name).map_err(|error| {
                format!(
                    "query Twig tag {} deprecation: {error}",
                    go_quote(&usage.name)
                )
            })?
            else {
                continue;
            };
            let message = if message.is_empty() {
                "Deprecated Twig tag".to_owned()
            } else {
                format!("Deprecated Twig tag: {message}")
            };
            result.push(Problem {
                id: DEPRECATED_TWIG_TAG_CODE.into(),
                range: usage.range,
                message,
                source: "twig".into(),
                severity: Some(protocol::DiagnosticSeverity::HINT),
                tags: vec![protocol::DiagnosticTag::DEPRECATED],
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

/// Go `twigCallableNameNode`.
fn twig_callable_name_node(container: &SyntaxNode, name: &str, last: bool) -> SyntaxNode {
    let mut result = None;
    for candidate in twigquery::iterate_nodes(container, &[TWIG_LITERAL_NAME]) {
        if candidate.text().to_string().trim() != name {
            continue;
        }
        if container.kind() == TWIG_FUNCTION_CALL
            && twigquery::function_call_at(&candidate).as_ref() != Some(container)
        {
            continue;
        }
        if container.kind() == TWIG_FILTER
            && twigquery::closest_node_of_kind(&candidate, &[TWIG_FILTER]).as_ref()
                != Some(container)
        {
            continue;
        }
        result = Some(candidate);
        if !last {
            break;
        }
    }
    result.unwrap_or_else(|| container.clone())
}

fn callable_deprecation(
    node: Option<&SyntaxNode>,
    name: &str,
    kind: &str,
    code: &str,
    message: &str,
) -> Problem {
    let message = if message.is_empty() {
        format!("Deprecated Twig {kind}")
    } else {
        format!("Deprecated Twig {kind}: {message}")
    };
    Problem {
        id: code.into(),
        range: value_node_text_range(node, name),
        message,
        source: "twig".into(),
        severity: Some(protocol::DiagnosticSeverity::HINT),
        tags: vec![protocol::DiagnosticTag::DEPRECATED],
        ..Problem::default()
    }
}
