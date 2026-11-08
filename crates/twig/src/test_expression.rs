//! Twig `is` / `is not` test expressions (port of `test_expression.go`).

use swlsp_parser_twig::ast::{AstNode, TwigBinaryExpression};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange};

use crate::DocumentMemo;

/// One name on the right-hand side of Twig's `is` or `is not` operator.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TestExpression {
    pub name: String,
    pub node: Option<SyntaxNode>,
    pub range: TextRange,
}

/// Exact test-name occurrences, bare or with arguments.
pub fn test_expressions(root: &SyntaxNode) -> Vec<TestExpression> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[TWIG_BINARY_EXPRESSION]) {
        let Some(expression) = TwigBinaryExpression::cast(node) else {
            continue;
        };
        if !twig_is_operator(expression.operator().as_ref()) {
            continue;
        }
        let Some(rhs) = expression.rhs_expression() else {
            continue;
        };
        let Some(name_node) = twigquery::iterate_nodes(rhs.syntax(), &[TWIG_LITERAL_NAME]).next()
        else {
            continue;
        };
        let name = name_node.text().to_string().trim().to_owned();
        let range = name_node.range_trimmed_trivia();
        if name.is_empty() || range.is_empty() {
            continue;
        }
        result.push(TestExpression {
            name,
            node: Some(name_node),
            range,
        });
    }
    result
}

/// The test name under `offset`.
///
/// The document scan is memoized per tree snapshot; the matching name node
/// is recovered from its range.
pub fn test_expression_at(root: &SyntaxNode, offset: u32) -> Option<TestExpression> {
    // (name, trimmed range, name node range)
    static MEMO: DocumentMemo<Vec<(String, TextRange, TextRange)>> = DocumentMemo::new(4);
    let expressions = MEMO.get_or_compute(root, "", || {
        test_expressions(root)
            .into_iter()
            .map(|expression| {
                let node_range = expression
                    .node
                    .as_ref()
                    .map(SyntaxNode::text_range)
                    .unwrap_or_default();
                (expression.name, expression.range, node_range)
            })
            .collect()
    });
    let (name, range, node_range) = expressions
        .iter()
        .find(|(_, range, _)| range.contains(offset.into()) || offset == u32::from(range.end()))?;
    Some(TestExpression {
        name: name.clone(),
        node: literal_name_node(root, *node_range),
        range: *range,
    })
}

/// The outermost `TWIG_LITERAL_NAME` node spanning exactly `range` (the first
/// in preorder, as `test_expressions` finds it).
fn literal_name_node(root: &SyntaxNode, range: TextRange) -> Option<SyntaxNode> {
    let start = match root.covering_element(range) {
        rowan::NodeOrToken::Node(node) => node,
        rowan::NodeOrToken::Token(token) => token.parent()?,
    };
    start
        .ancestors()
        .take_while(|node| node.text_range() == range)
        .filter(|node| node.kind() == TWIG_LITERAL_NAME)
        .last()
}

/// Whether a function-call-shaped node is the right-hand test operand
/// rather than a regular Twig function invocation.
pub fn is_test_function_call(call: &SyntaxNode) -> bool {
    if call.kind() != TWIG_FUNCTION_CALL {
        return false;
    }
    let Some(binary) = twigquery::closest_node_of_kind(call, &[TWIG_BINARY_EXPRESSION]) else {
        return false;
    };
    test_expressions(&binary).iter().any(|expression| {
        expression
            .node
            .as_ref()
            .is_some_and(|node| node.ancestors().any(|current| current == *call))
    })
}

/// A cursor in a test name or immediately after an incomplete `is` /
/// `is not` operator.
pub fn test_completion_at(
    root: &SyntaxNode,
    content: &[u8],
    offset: u32,
) -> Option<TestExpression> {
    if offset as usize > content.len() {
        return None;
    }
    for mut expression in test_expressions(root) {
        let start = u32::from(expression.range.start());
        if offset < start || offset > u32::from(expression.range.end()) {
            continue;
        }
        let prefix_end = (offset as usize).min(content.len());
        expression.name =
            String::from_utf8_lossy(&content[start as usize..prefix_end]).into_owned();
        return Some(expression);
    }

    let mut closest: Option<SyntaxToken> = None;
    for node in twigquery::iterate_nodes(root, &[TWIG_BINARY_EXPRESSION]) {
        let Some(expression) = TwigBinaryExpression::cast(node) else {
            continue;
        };
        let Some(operator) = expression.operator() else {
            continue;
        };
        let operator_end = u32::from(operator.text_range().end());
        if !twig_is_operator(Some(&operator)) || operator_end > offset {
            continue;
        }
        if expression.rhs_expression().is_some() {
            continue;
        }
        if !content[operator_end as usize..offset as usize]
            .iter()
            .all(|byte| matches!(byte, b' ' | b'\t' | b'\r' | b'\n'))
        {
            continue;
        }
        if closest
            .as_ref()
            .is_none_or(|current| operator.text_range().end() > current.text_range().end())
        {
            closest = Some(operator);
        }
    }
    closest.map(|_| TestExpression {
        name: String::new(),
        node: None,
        range: TextRange::empty(offset.into()),
    })
}

fn twig_is_operator(token: Option<&SyntaxToken>) -> bool {
    token.is_some_and(|token| token.kind() == TK_IS || token.kind() == TK_IS_NOT)
}
