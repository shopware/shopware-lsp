//! Declaration, control-flow, write-target and extraction queries.

use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use super::{
    array_item_value, child_tokens, class_at, class_body, direct_child, function_like_at, nodes,
    variable_name,
};
use crate::eq_ignore_case;
use crate::syntax::*;

/// Declared constant names of a (class) const declaration, excluding names in
/// initializers and native types.
pub fn constant_names(node: &SyntaxNode) -> Vec<SyntaxNode> {
    if node.kind() != PHP_CONST_DECLARATION && node.kind() != PHP_CLASS_CONST_DECLARATION {
        return Vec::new();
    }
    let mut names = Vec::new();
    let mut expect_name = false;
    for child in node.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) => {
                if eq_ignore_case(token.text(), "const") || token.kind() == TK_COMMA {
                    expect_name = true;
                }
                if token.kind() == TK_EQUALS {
                    expect_name = false;
                }
            }
            NodeOrToken::Node(child) => {
                if expect_name && child.kind() == PHP_NAME {
                    names.push(child);
                    expect_name = false;
                }
            }
        }
    }
    names
}

/// Distinguishes expression names from declaration names, member names,
/// named-argument labels, and class/function references.
pub fn is_constant_reference(node: &SyntaxNode) -> bool {
    if node.kind() != PHP_NAME {
        return false;
    }
    let Some(parent) = node.parent() else {
        return false;
    };
    match parent.kind() {
        PHP_NAMED_ARGUMENT | PHP_ENUM_CASE_DECLARATION => {
            direct_child(&parent, PHP_NAME).as_ref() != Some(node)
        }
        PHP_CONST_DECLARATION | PHP_CLASS_CONST_DECLARATION => {
            !constant_names(&parent).contains(node)
        }
        PHP_MEMBER_ACCESS | PHP_MEMBER_CALL | PHP_SCOPED_ACCESS | PHP_SCOPED_CALL => {
            // A braced member expression can contain a constant; a literal
            // member name does not consume a namespace import.
            let start = node.range_trimmed_trivia().start();
            let mut previous = None;
            for token in child_tokens(&parent) {
                if token.text_range().start() >= start {
                    break;
                }
                if !token.kind().is_trivia() {
                    previous = Some(token);
                }
            }
            previous.is_some_and(|token| token.text() == "{")
        }
        PHP_FOR_STATEMENT
        | PHP_FOREACH_STATEMENT
        | PHP_ARROW_FUNCTION
        | PHP_THROW_STATEMENT
        | PHP_BREAK_STATEMENT
        | PHP_CONTINUE_STATEMENT
        | PHP_STATIC_STATEMENT
        | PHP_ARGUMENT
        | PHP_ECHO_STATEMENT
        | PHP_RETURN_STATEMENT
        | PHP_EXPRESSION_STATEMENT
        | PHP_ARRAY_ITEM
        | PHP_ARRAY_ACCESS
        | PHP_PARENTHESIZED
        | PHP_ASSIGNMENT_EXPRESSION
        | PHP_BINARY_EXPRESSION
        | PHP_UNARY_EXPRESSION
        | PHP_TERNARY_EXPRESSION
        | PHP_MATCH_ARM
        | PHP_CASE_CLAUSE
        | PHP_THROW_EXPRESSION
        | PHP_YIELD_EXPRESSION
        | PHP_CLONE_EXPRESSION
        | PHP_CAST_EXPRESSION
        | PHP_PARAMETER
        | PHP_PROPERTY_DECLARATION => true,
        _ => false,
    }
}

/// One `if`/`elseif`/`else` branch.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ConditionalBranch {
    pub condition: Option<SyntaxNode>,
    pub body: Option<SyntaxNode>,
}

/// Branches of an if statement, flattening `elseif`/`else` clauses.
pub fn conditional_branches(node: &SyntaxNode) -> Vec<ConditionalBranch> {
    let mut result = Vec::new();
    let mut branch = ConditionalBranch::default();
    for child in node.children() {
        match child.kind() {
            PHP_ELSE_IF_CLAUSE | PHP_ELSE_CLAUSE => result.extend(conditional_branches(&child)),
            kind => {
                if branch.condition.is_none() && kind == PHP_PARENTHESIZED {
                    branch.condition = Some(child);
                } else if branch.body.is_none() {
                    branch.body = Some(child);
                    result.push(branch.clone());
                }
            }
        }
    }
    result
}

/// Header and body parts of a loop statement.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct LoopParts {
    pub init: Vec<SyntaxNode>,
    pub condition: Vec<SyntaxNode>,
    pub update: Vec<SyntaxNode>,
    pub targets: Vec<SyntaxNode>,
    pub body: Option<SyntaxNode>,
}

pub fn extraction_loop_parts(node: &SyntaxNode) -> LoopParts {
    let mut parts = LoopParts::default();
    let mut phase = 0;
    let mut in_header = node.kind() == PHP_DO_WHILE_STATEMENT;
    for child in node.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) => {
                match token.kind() {
                    TK_OPEN_PAREN => in_header = true,
                    TK_CLOSE_PAREN => in_header = false,
                    TK_SEMICOLON if in_header => phase += 1,
                    _ => {}
                }
                if token.text() == "as" {
                    phase = 1;
                }
            }
            NodeOrToken::Node(child) => {
                if node.kind() == PHP_FOR_STATEMENT && in_header {
                    match phase {
                        0 => parts.init.push(child),
                        1 => parts.condition.push(child),
                        _ => parts.update.push(child),
                    }
                } else if node.kind() == PHP_FOREACH_STATEMENT && in_header {
                    if phase == 0 {
                        parts.condition.push(child);
                    } else {
                        parts.targets.push(child);
                    }
                } else if child.kind() == PHP_PARENTHESIZED {
                    parts.condition.push(child);
                } else {
                    parts.body = Some(child);
                }
            }
        }
    }
    parts
}

/// Concatenated non-trivia direct tokens (the operator of an expression).
pub fn expression_operator(node: &SyntaxNode) -> String {
    child_tokens(node)
        .filter(|token| !token.kind().is_trivia())
        .map(|token| token.text().to_owned())
        .collect()
}

/// Direct child nodes (the operands of an expression).
pub fn expression_children(node: &SyntaxNode) -> Vec<SyntaxNode> {
    node.children().collect()
}

/// Excludes write targets, bare names and variables.
pub fn extractable_expression(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        PHP_FUNCTION_CALL
            | PHP_MEMBER_CALL
            | PHP_SCOPED_CALL
            | PHP_OBJECT_CREATION
            | PHP_BINARY_EXPRESSION
            | PHP_UNARY_EXPRESSION
            | PHP_PARENTHESIZED
            | PHP_TERNARY_EXPRESSION
            | PHP_MATCH_EXPRESSION
            | PHP_ARRAY
            | PHP_ARRAY_ACCESS
            | PHP_MEMBER_ACCESS
            | PHP_SCOPED_ACCESS
            | PHP_STRING
            | PHP_NUMBER
    )
}

/// The statement evaluating `expression` first and unconditionally; excludes
/// argument passing, reference contexts, loop headers, conditional branches
/// and unbraced bodies.
pub fn extraction_statement(expression: &SyntaxNode) -> Option<SyntaxNode> {
    let mut node = expression.clone();
    while let Some(parent) = node.parent() {
        let children = expression_children(&parent);
        match parent.kind() {
            PHP_PARENTHESIZED => {}
            PHP_BINARY_EXPRESSION => {
                if children.len() != 2
                    || children[0] != node
                    || expression_operator(&parent) == "??"
                {
                    return None;
                }
            }
            PHP_UNARY_EXPRESSION => {
                if !matches!(expression_operator(&parent).as_str(), "!" | "~" | "+" | "-") {
                    return None;
                }
            }
            PHP_ASSIGNMENT_EXPRESSION => {
                if children.len() != 2
                    || children[1] != node
                    || children[0].kind() != PHP_VARIABLE
                    || expression_operator(&parent) != "="
                {
                    return None;
                }
            }
            PHP_RETURN_STATEMENT | PHP_ECHO_STATEMENT | PHP_EXPRESSION_STATEMENT => {
                if children.first() != Some(&node) {
                    return None;
                }
                return match parent.parent()?.kind() {
                    PHP_BLOCK | PHP_PROGRAM => Some(parent),
                    _ => None,
                };
            }
            _ => return None,
        }
        node = parent;
    }
    None
}

fn has_extraction_token(node: &SyntaxNode, text: &str) -> bool {
    child_tokens(node).any(|token| token.text() == text)
}

/// The owner permitting an inline assignment at the original evaluation
/// point, preserving short-circuiting, loop frequency and argument order.
pub fn inline_extraction_owner(
    expression: &SyntaxNode,
    value_call: Option<&dyn Fn(&SyntaxNode) -> bool>,
) -> Option<SyntaxNode> {
    let mut node = expression.clone();
    while let Some(parent) = node.parent() {
        let children = expression_children(&parent);
        match parent.kind() {
            PHP_PARENTHESIZED
            | PHP_TERNARY_EXPRESSION
            | PHP_MATCH_ARM
            | PHP_MATCH_EXPRESSION
            | PHP_ARRAY_ACCESS
            | PHP_ARRAY
            | PHP_MEMBER_ACCESS
            | PHP_SCOPED_ACCESS
            | PHP_MEMBER_CALL
            | PHP_SCOPED_CALL
            | PHP_FUNCTION_CALL
            | PHP_OBJECT_CREATION
            | PHP_ARGUMENT_LIST
            | PHP_CAST_EXPRESSION
            | PHP_CLONE_EXPRESSION => {}
            PHP_ARRAY_ITEM => {
                if has_extraction_token(&parent, "&") {
                    return None;
                }
            }
            PHP_ARGUMENT | PHP_NAMED_ARGUMENT => {
                if !value_only_expression(&node, value_call) {
                    return None;
                }
            }
            PHP_BINARY_EXPRESSION => {
                if expression_operator(&parent) == "??" && children.first() == Some(&node) {
                    return None;
                }
            }
            PHP_UNARY_EXPRESSION => {
                if !matches!(
                    expression_operator(&parent).as_str(),
                    "!" | "~" | "+" | "-" | "@"
                ) {
                    return None;
                }
            }
            PHP_ASSIGNMENT_EXPRESSION => {
                if children.len() != 2 || children[1] != node || has_extraction_token(&parent, "&")
                {
                    return None;
                }
            }
            PHP_FOREACH_STATEMENT => {
                if children.first() != Some(&node) {
                    return None;
                }
                return Some(parent);
            }
            PHP_EXPRESSION_STATEMENT
            | PHP_RETURN_STATEMENT
            | PHP_ECHO_STATEMENT
            | PHP_THROW_STATEMENT
            | PHP_IF_STATEMENT
            | PHP_ELSE_IF_CLAUSE
            | PHP_WHILE_STATEMENT
            | PHP_DO_WHILE_STATEMENT
            | PHP_FOR_STATEMENT
            | PHP_SWITCH_STATEMENT
            | PHP_CASE_CLAUSE
            | PHP_ARROW_FUNCTION => return Some(parent),
            _ => return None,
        }
        node = parent;
    }
    None
}

fn value_only_expression(
    node: &SyntaxNode,
    value_call: Option<&dyn Fn(&SyntaxNode) -> bool>,
) -> bool {
    match node.kind() {
        PHP_PARENTHESIZED => {
            let children = expression_children(node);
            children.len() == 1 && value_only_expression(&children[0], value_call)
        }
        PHP_FUNCTION_CALL | PHP_MEMBER_CALL | PHP_SCOPED_CALL => {
            value_call.is_some_and(|call| call(node))
        }
        PHP_BINARY_EXPRESSION
        | PHP_UNARY_EXPRESSION
        | PHP_TERNARY_EXPRESSION
        | PHP_MATCH_EXPRESSION
        | PHP_ARRAY
        | PHP_STRING
        | PHP_NUMBER
        | PHP_OBJECT_CREATION
        | PHP_CAST_EXPRESSION
        | PHP_CLONE_EXPRESSION => true,
        _ => false,
    }
}

/// Expressions without calls, object/property access, interpolation or
/// writes, which can be shared between occurrences.
pub fn pure_extraction_expression(node: &SyntaxNode) -> bool {
    match node.kind() {
        PHP_VARIABLE | PHP_NUMBER | PHP_BOOLEAN | PHP_NULL => true,
        PHP_STRING => child_tokens(node).all(|token| {
            let text = token.text();
            !text.contains(['$', '\r', '\n']) && !text.starts_with("<<<")
        }),
        PHP_PARENTHESIZED | PHP_BINARY_EXPRESSION | PHP_UNARY_EXPRESSION => {
            !has_extraction_token(node, "++")
                && !has_extraction_token(node, "--")
                && !has_extraction_token(node, "@")
                && node
                    .children()
                    .all(|child| pure_extraction_expression(&child))
        }
        _ => false,
    }
}

/// The argument list in which `expression` is always evaluated when its
/// argument is evaluated.
pub fn eager_argument_list(expression: &SyntaxNode) -> Option<SyntaxNode> {
    let mut node = expression.clone();
    while let Some(parent) = node.parent() {
        match parent.kind() {
            PHP_ARGUMENT | PHP_NAMED_ARGUMENT => return parent.parent(),
            PHP_PARENTHESIZED | PHP_UNARY_EXPRESSION => {}
            PHP_BINARY_EXPRESSION => {
                if matches!(
                    expression_operator(&parent).as_str(),
                    "&&" | "||" | "and" | "or" | "??"
                ) && parent.children().next().as_ref() != Some(&node)
                {
                    return None;
                }
            }
            _ => return None,
        }
        node = parent;
    }
    None
}

/// Consecutive top-level statements of one method or function body selected
/// for extraction.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct MethodSelection {
    pub method: SyntaxNode,
    pub class: Option<SyntaxNode>,
    pub block: SyntaxNode,
    pub statements: Vec<SyntaxNode>,
    pub range: TextRange,
}

/// Accepts complete consecutive top-level method statements; a caret selects
/// the containing statement. `cancelled` is polled per candidate body.
pub fn select_method_statements(
    root: &SyntaxNode,
    source: &str,
    mut selected: TextRange,
    cancelled: impl Fn() -> bool,
) -> Option<MethodSelection> {
    if u32::from(selected.end()) as usize > source.len() {
        return None;
    }
    let caret = selected.is_empty();
    if !caret {
        let bytes = source.as_bytes();
        let space = |offset: u32| matches!(bytes[offset as usize], b' ' | b'\t' | b'\r' | b'\n');
        let (mut start, mut end) = (u32::from(selected.start()), u32::from(selected.end()));
        while start < end && space(start) {
            start += 1;
        }
        while end > start && space(end - 1) {
            end -= 1;
        }
        selected = TextRange::new(start.into(), end.into());
    }

    let mut best: Option<MethodSelection> = None;
    for body in nodes(root, &[PHP_BLOCK]) {
        if cancelled() {
            return None;
        }
        let Some(owner) = function_like_at(&body) else {
            continue;
        };
        if owner.kind() != PHP_METHOD_DECLARATION && owner.kind() != PHP_FUNCTION_DECLARATION {
            continue;
        }
        let mut class = class_at(&owner);
        if owner.kind() == PHP_METHOD_DECLARATION
            && class
                .as_ref()
                .is_none_or(|class| class.kind() != PHP_CLASS_DECLARATION)
        {
            continue;
        }
        if owner.kind() == PHP_FUNCTION_DECLARATION {
            if owner
                .parent()
                .and_then(|parent| function_like_at(&parent))
                .is_some()
            {
                continue;
            }
            class = None;
        } else if owner.parent() != class.as_ref().and_then(class_body) {
            continue;
        }
        let statements: Vec<SyntaxNode> = body
            .children()
            .filter(|statement| {
                let range = statement.range_trimmed_trivia();
                if caret {
                    range.start() <= selected.start() && selected.start() < range.end()
                } else {
                    range.start() >= selected.start() && range.end() <= selected.end()
                }
            })
            .collect();
        let (Some(first), Some(last)) = (statements.first(), statements.last()) else {
            continue;
        };
        let range = TextRange::new(
            first.range_trimmed_trivia().start(),
            last.range_trimmed_trivia().end(),
        );
        if (caret || range == selected)
            && best
                .as_ref()
                .is_none_or(|best| range.len() < best.range.len())
        {
            best = Some(MethodSelection {
                method: owner,
                class,
                block: body,
                statements,
                range,
            });
        }
    }
    best
}

/// An unconditional assignment to one local: the variable (with `$`) and
/// the assigned value.
pub fn simple_assignment(statement: &SyntaxNode) -> Option<(String, SyntaxNode)> {
    if statement.kind() != PHP_EXPRESSION_STATEMENT {
        return None;
    }
    let assignment = direct_child(statement, PHP_ASSIGNMENT_EXPRESSION)?;
    if expression_operator(&assignment) != "=" {
        return None;
    }
    let children = expression_children(&assignment);
    if children.len() != 2 || children[0].kind() != PHP_VARIABLE {
        return None;
    }
    Some((
        format!("${}", variable_name(&children[0])),
        children[1].clone(),
    ))
}

pub fn declaration_is_static(node: &SyntaxNode) -> bool {
    child_tokens(node).any(|token| eq_ignore_case(token.text(), "static"))
}

/// Syntactic local-variable write targets, including array/destructuring
/// assignments, increments, and foreach targets.
pub fn variable_is_write(node: &SyntaxNode) -> bool {
    if node.kind() != PHP_VARIABLE {
        return false;
    }
    let mut current = node.clone();
    while let Some(parent) = current.parent() {
        match parent.kind() {
            PHP_ASSIGNMENT_EXPRESSION => {
                return parent.children().next().as_ref() == Some(&current);
            }
            PHP_UNARY_EXPRESSION => {
                return child_tokens(&parent)
                    .any(|token| token.text() == "++" || token.text() == "--");
            }
            PHP_FOREACH_STATEMENT => {
                let mut after_as = false;
                for element in parent.children_with_tokens() {
                    match element {
                        NodeOrToken::Token(token) => after_as |= eq_ignore_case(token.text(), "as"),
                        NodeOrToken::Node(child) if child == current => return after_as,
                        NodeOrToken::Node(_) => {}
                    }
                }
                return false;
            }
            PHP_ARRAY_ACCESS => {
                // Writing $array[$index] changes the array, but only reads the index.
                if parent.children().next().as_ref() != Some(&current) {
                    return false;
                }
            }
            PHP_ARRAY_ITEM => {
                if array_item_value(&parent).as_ref() != Some(&current) {
                    return false;
                }
            }
            PHP_ARRAY | PHP_PARENTHESIZED => {}
            _ => return false,
        }
        current = parent;
    }
    false
}
