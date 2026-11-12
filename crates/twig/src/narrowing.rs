//! What the conditions around an expression say about it: inside
//! `{% if product.manufacturer %}` the manufacturer is not null.
//!
//! Facts are kept per expression path (`product.manufacturer`); a truthy or
//! non-null path makes every prefix of it non-null as well, because Twig
//! yields null for an attribute of null.

use std::collections::HashSet;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt as _, SyntaxToken};

use crate::macros::direct_child;
use crate::php_access::{direct_twig_children, first_twig_child};
use crate::template_variable::is_descendant_node;

/// The path of a name, an attribute chain or a call with literal arguments
/// on one: `product`, `product.manufacturer`, `product.getCover()`,
/// `measurements.type('weight')`.
pub(crate) fn expression_path(node: &SyntaxNode) -> Option<String> {
    match node.kind() {
        TWIG_EXPRESSION | TWIG_OPERAND | TWIG_PARENTHESES_EXPRESSION => {
            let inner = node
                .children()
                .find(|child| !matches!(child.kind(), TWIG_ARGUMENTS))?;
            expression_path(&inner)
        }
        TWIG_LITERAL_NAME => {
            let text = node.text_string();
            let text = text.trim();
            (!text.is_empty()).then(|| text.to_owned())
        }
        TWIG_ACCESSOR => {
            let operands = direct_twig_children(node, TWIG_OPERAND);
            let receiver = expression_path(operands.first()?)?;
            let member = first_twig_child(operands.get(1)?)
                .filter(|member| member.kind() == TWIG_LITERAL_NAME)?;
            Some(format!("{receiver}.{}", member.text_string().trim()))
        }
        TWIG_FUNCTION_CALL => {
            let arguments = direct_child(node, TWIG_ARGUMENTS)?;
            // The same call with the same literal arguments is taken to
            // return the same value: `measurements.type('weight')`.
            let mut literals = Vec::new();
            for argument in arguments.children() {
                let value = unwrap(&argument);
                if argument.kind() != TWIG_EXPRESSION
                    || !matches!(
                        value.kind(),
                        TWIG_LITERAL_STRING
                            | TWIG_LITERAL_NUMBER
                            | TWIG_LITERAL_BOOLEAN
                            | TWIG_LITERAL_NULL
                    )
                    || (value.kind() == TWIG_LITERAL_STRING && !twigquery::string_is_static(&value))
                {
                    return None;
                }
                literals.push(value.text_string().trim().replace('.', "\u{b7}"));
            }
            let callee = direct_child(node, TWIG_OPERAND)?;
            Some(format!(
                "{}({})",
                expression_path(&callee)?,
                literals.join(",")
            ))
        }
        _ => None,
    }
}

/// The paths known to be non-null when a condition holds, and when it does
/// not.
#[derive(Default)]
struct Facts {
    when_true: HashSet<String>,
    when_false: HashSet<String>,
}

impl Facts {
    fn swapped(self) -> Facts {
        Facts {
            when_true: self.when_false,
            when_false: self.when_true,
        }
    }
}

/// `path` and every prefix of it.
fn with_prefixes(path: &str) -> HashSet<String> {
    let mut result = HashSet::new();
    let mut end = path.len();
    loop {
        result.insert(path[..end].to_owned());
        match path[..end].rfind('.') {
            Some(dot) => end = dot,
            None => return result,
        }
    }
}

fn operator(binary: &SyntaxNode) -> Option<SyntaxToken> {
    binary
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| !matches!(token.kind(), TK_WHITESPACE | TK_LINE_BREAK))
}

/// The two sides of a binary expression. A nested binary expression is a
/// direct child, anything else is wrapped in an expression node.
pub(crate) fn binary_operands(binary: &SyntaxNode) -> Vec<SyntaxNode> {
    binary.children().collect()
}

fn unwrap(node: &SyntaxNode) -> SyntaxNode {
    let mut current = node.clone();
    while matches!(
        current.kind(),
        TWIG_EXPRESSION | TWIG_OPERAND | TWIG_PARENTHESES_EXPRESSION
    ) {
        match current.children().next() {
            Some(inner) => current = inner,
            None => break,
        }
    }
    current
}

fn is_null_literal(node: &SyntaxNode) -> bool {
    unwrap(node).kind() == TWIG_LITERAL_NULL
}

/// The name of a test on the right of `is`: `null`, `defined`, `same as`.
fn test_name(node: &SyntaxNode) -> String {
    let node = unwrap(node);
    match node.kind() {
        TWIG_LITERAL_NULL => "null".to_owned(),
        TWIG_FUNCTION_CALL => direct_child(&node, TWIG_OPERAND)
            .map(|name| name.text_string().trim().to_owned())
            .unwrap_or_default(),
        _ => node.text_string().trim().to_owned(),
    }
}

/// How many assignments a condition is followed through: `{% set guarded =
/// a.b is not null %}` and then `{% if guarded %}`.
const MAX_GUARD_DEPTH: u32 = 4;

fn facts(condition: &SyntaxNode) -> Facts {
    facts_at(condition, 0)
}

/// The condition a boolean variable holds where `name_node` reads it: the
/// value of the one `{% set %}` that always reaches the read.
fn assigned_condition(name_node: &SyntaxNode) -> Option<SyntaxNode> {
    let name = name_node.text_string();
    let name = name.trim();
    let root = name_node.ancestors().last()?;
    let sets = crate::inference::set_index(&root);
    let mut reaching = sets.get(name)?.iter().rev().filter(|binding| {
        crate::inference::reach(&binding.set, name_node) != crate::inference::Reach::Never
    });
    let last = reaching.next()?;
    if crate::inference::reach(&last.set, name_node) != crate::inference::Reach::Always {
        return None;
    }
    last.value.clone()
}

fn facts_at(condition: &SyntaxNode, depth: u32) -> Facts {
    let node = unwrap(condition);
    if node.kind() == TWIG_LITERAL_NAME
        && depth < MAX_GUARD_DEPTH
        && let Some(value) = assigned_condition(&node)
    {
        // The variable itself is truthy, and so is what it was set from.
        let own = facts_at(&node.clone(), MAX_GUARD_DEPTH);
        let held = facts_at(&value, depth + 1);
        return Facts {
            when_true: own.when_true.union(&held.when_true).cloned().collect(),
            when_false: own.when_false.union(&held.when_false).cloned().collect(),
        };
    }
    let truthy = |subject: &SyntaxNode| Facts {
        when_true: expression_path(subject)
            .map(|path| with_prefixes(&path))
            .unwrap_or_default(),
        when_false: HashSet::new(),
    };
    match node.kind() {
        TWIG_UNARY_EXPRESSION => {
            let negation = node
                .children_with_tokens()
                .filter_map(|element| element.into_token())
                .any(|token| token.kind() == TK_NOT);
            match direct_child(&node, TWIG_EXPRESSION) {
                Some(operand) if negation => facts(&operand).swapped(),
                _ => Facts::default(),
            }
        }
        TWIG_BINARY_EXPRESSION => {
            let operands = binary_operands(&node);
            let (Some(operator), [left, right]) = (operator(&node), operands.as_slice()) else {
                return Facts::default();
            };
            match operator.text() {
                "and" | "&&" => {
                    let (left, right) = (facts(left), facts(right));
                    Facts {
                        when_true: left.when_true.union(&right.when_true).cloned().collect(),
                        when_false: left
                            .when_false
                            .intersection(&right.when_false)
                            .cloned()
                            .collect(),
                    }
                }
                "or" | "||" => {
                    let (left, right) = (facts(left), facts(right));
                    Facts {
                        when_true: left
                            .when_true
                            .intersection(&right.when_true)
                            .cloned()
                            .collect(),
                        when_false: left.when_false.union(&right.when_false).cloned().collect(),
                    }
                }
                "is" | "is not" => {
                    let result = match test_name(right).as_str() {
                        // Null when the test holds.
                        "null" | "none" | "empty" => truthy(left).swapped(),
                        "same as" => match direct_child(&unwrap(right), TWIG_ARGUMENTS)
                            .and_then(|arguments| direct_child(&arguments, TWIG_EXPRESSION))
                        {
                            Some(argument) if is_null_literal(&argument) => truthy(left).swapped(),
                            _ => return Facts::default(),
                        },
                        // Used as a null guard throughout; an attribute of
                        // null is never defined.
                        "defined" | "iterable" => truthy(left),
                        _ => return Facts::default(),
                    };
                    if operator.text() == "is not" {
                        result.swapped()
                    } else {
                        result
                    }
                }
                "==" | "!=" | "===" | "!==" => {
                    let subject = match (is_null_literal(left), is_null_literal(right)) {
                        (false, true) => left,
                        (true, false) => right,
                        _ => return Facts::default(),
                    };
                    if operator.text().starts_with('!') {
                        truthy(subject)
                    } else {
                        truthy(subject).swapped()
                    }
                }
                // A comparison with a number holds for no null value worth
                // guarding: `items|length > 0`, `product.stock > 0`.
                ">" | ">=" => truthy(left),
                _ => Facts::default(),
            }
        }
        TWIG_FILTER if twigquery::filter_name(&node).as_deref() == Some("length") => {
            match direct_child(&node, TWIG_OPERAND) {
                Some(input) => truthy(&input),
                None => Facts::default(),
            }
        }
        _ => truthy(&node),
    }
}

/// The paths that are not null where `node` is evaluated, by the conditions
/// around it: `if`/`elseif`/`else` branches, ternaries, and the left side
/// of `and` and `or`.
pub(crate) fn non_null_paths(node: &SyntaxNode) -> HashSet<String> {
    let mut result = HashSet::new();
    let mut inner = node.clone();
    for ancestor in node.ancestors().skip(1) {
        match ancestor.kind() {
            TWIG_IF if inner.kind() == BODY => branch_facts(&ancestor, &inner, &mut result),
            TWIG_CONDITIONAL_EXPRESSION => {
                let parts = direct_twig_children(&ancestor, TWIG_EXPRESSION);
                // `a ?: c` has no part between `?` and `:`.
                let elvis = ancestor
                    .children_with_tokens()
                    .filter_map(|element| element.into_token())
                    .skip_while(|token| token.kind() != TK_QUESTION_MARK)
                    .nth(1)
                    .is_some_and(|token| token.kind() == TK_COLON);
                if let Some(position) = parts.iter().position(|part| *part == inner)
                    && position > 0
                {
                    let condition = facts(&parts[0]);
                    if position == 1 && !elvis {
                        result.extend(condition.when_true);
                    } else {
                        result.extend(condition.when_false);
                    }
                }
            }
            TWIG_BINARY_EXPRESSION => {
                let operands = binary_operands(&ancestor);
                if let (Some(operator), [left, right]) = (operator(&ancestor), operands.as_slice())
                    && *right == inner
                {
                    match operator.text() {
                        "and" | "&&" => result.extend(facts(left).when_true),
                        "or" | "||" => result.extend(facts(left).when_false),
                        _ => {}
                    }
                }
            }
            _ => {}
        }
        inner = ancestor;
    }
    result
}

/// The facts of the branch `body` of `if_node`: its own condition holds and
/// every earlier one does not.
fn branch_facts(if_node: &SyntaxNode, body: &SyntaxNode, result: &mut HashSet<String>) {
    let mut earlier: Vec<SyntaxNode> = Vec::new();
    let mut own: Option<SyntaxNode> = None;
    for child in if_node.children() {
        match child.kind() {
            TWIG_IF_BLOCK | TWIG_ELSE_IF_BLOCK => {
                earlier.extend(own.take());
                own = direct_child(&child, TWIG_EXPRESSION);
            }
            TWIG_ELSE_BLOCK => earlier.extend(own.take()),
            BODY if child == *body => break,
            _ => {}
        }
    }
    for condition in earlier {
        result.extend(facts(&condition).when_false);
    }
    if let Some(condition) = own {
        result.extend(facts(&condition).when_true);
    }
}

/// Whether `node` is evaluated where a null receiver is expected and
/// harmless: the tested side of `is defined`, the left of `??` and the
/// input of `default`.
pub(crate) fn tolerates_null(node: &SyntaxNode) -> bool {
    if in_null_safe_chain(node) {
        return true;
    }
    let mut inner = node.clone();
    for ancestor in node.ancestors().skip(1) {
        match ancestor.kind() {
            TWIG_BINARY_EXPRESSION => {
                let operands = binary_operands(&ancestor);
                if let (Some(operator), [left, right]) = (operator(&ancestor), operands.as_slice())
                    && *left == inner
                {
                    match operator.text() {
                        "??" => return true,
                        "is" | "is not" if test_name(right) == "defined" => return true,
                        _ => {}
                    }
                }
            }
            TWIG_FILTER => {
                if twigquery::filter_name(&ancestor).as_deref() == Some("default")
                    && direct_child(&ancestor, TWIG_OPERAND)
                        .is_some_and(|input| is_descendant_node(node, &input))
                {
                    return true;
                }
            }
            TWIG_VAR | BODY | TWIG_IF_BLOCK | TWIG_ELSE_IF_BLOCK | TWIG_SET_BLOCK => return false,
            _ => {}
        }
        inner = ancestor;
    }
    false
}

/// Whether an accessor is null-safe (`a?.b`).
pub(crate) fn is_null_safe(accessor: &SyntaxNode) -> bool {
    accessor.kind() == TWIG_ACCESSOR
        && accessor
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .any(|token| token.kind() == TK_QUESTION_MARK)
}

/// Whether `accessor` or an accessor it reads from is null-safe: `?.`
/// short-circuits the rest of its chain.
fn in_null_safe_chain(accessor: &SyntaxNode) -> bool {
    let mut current = accessor.clone();
    loop {
        match current.kind() {
            TWIG_ACCESSOR if is_null_safe(&current) => return true,
            TWIG_ACCESSOR | TWIG_FUNCTION_CALL | TWIG_INDEX_LOOKUP | TWIG_OPERAND
            | TWIG_EXPRESSION => match current.children().next() {
                Some(receiver) => current = receiver,
                None => return false,
            },
            _ => return false,
        }
    }
}
