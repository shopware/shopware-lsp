//! `Request::get()`: deprecated since Symfony 7.4, and vague on every
//! version. It looks a key up in the route attributes, then the query
//! string, then the body, so the caller does not say where a value may come
//! from.

use std::sync::Arc;

use serde_json::json;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ATTRIBUTE_GROUP, PHP_CLASS_DECLARATION, PHP_MEMBER_CALL, PHP_METHOD_DECLARATION, PHP_NAME,
    PHP_STRING,
};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use super::{lower_ext, problem};
use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::php_type_is_subtype;

pub const REQUEST_GET_CODE: &str = "symfony.request.get";

const REQUEST_CLASS: &str = "Symfony\\Component\\HttpFoundation\\Request";

pub struct RequestGetAnalyzer {
    php_index: Option<PhpIndex>,
}

impl RequestGetAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for RequestGetAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if lower_ext(&document.uri) != ".php" || !document.source().contains("get(") {
            return Ok(Vec::new());
        }
        let calls: Vec<SyntaxNode> = phpquery::nodes(&root, &[PHP_MEMBER_CALL])
            .into_iter()
            .filter(|call| eq_ignore_case(&phpquery::call_method_name(call), "get"))
            .collect();
        if calls.is_empty() {
            return Ok(Vec::new());
        }
        let Some(analysis) = swlsp_lsp_phpanalysis::for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let snapshot = Some(analysis.snapshot.as_ref());
        let mut result = Vec::new();
        for call in calls {
            ctx.check()?;
            let (Some(receiver), Some(name)) = (
                phpquery::call_receiver(&call),
                phpquery::direct_child(&call, PHP_NAME),
            ) else {
                continue;
            };
            if !php_type_is_subtype(
                &analysis.document.type_of(&receiver).ty,
                snapshot,
                REQUEST_CLASS,
            ) {
                continue;
            }
            let mut found = problem(
                REQUEST_GET_CODE,
                name.range_trimmed_trivia(),
                "Request::get() looks in the route attributes, the query string and the body \
                 in turn, and is deprecated since Symfony 7.4; read the value from \
                 ->attributes, ->query or ->request"
                    .to_owned(),
                DiagnosticSeverity::WARNING,
            );
            found.payload = Some(json!({
                "method": name.text().to_string().trim(),
                "routeParameter": is_route_parameter(&call),
            }));
            found.element = Some(SyntaxElement::Node(name));
            result.push(found);
        }
        Ok(result)
    }
}

/// Whether the call reads a key that the route of the enclosing action (or
/// of its controller) has in its path: `get()` then returns the attribute.
fn is_route_parameter(call: &SyntaxNode) -> bool {
    let Some(key) = phpquery::argument(call, 0)
        .and_then(|argument| argument.children().last())
        .filter(|value| value.kind() == PHP_STRING)
        .map(|value| phpquery::string_value(&value))
        .filter(|key| !key.is_empty())
    else {
        return false;
    };
    let placeholders = [
        format!("{{{key}}}"),
        format!("{{{key}<"),
        format!("{{{key}?"),
        format!("{{{key}:"),
        format!("{{!{key}}}"),
        format!("{{!{key}<"),
    ];
    call.ancestors()
        .filter(|node| matches!(node.kind(), PHP_METHOD_DECLARATION | PHP_CLASS_DECLARATION))
        .flat_map(|declaration| {
            declaration
                .children()
                .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
                .collect::<Vec<_>>()
        })
        .flat_map(|group| phpquery::nodes(&group, &[PHP_STRING]))
        .any(|string| {
            let path = phpquery::string_value(&string);
            placeholders
                .iter()
                .any(|placeholder| path.contains(placeholder.as_str()))
        })
}
