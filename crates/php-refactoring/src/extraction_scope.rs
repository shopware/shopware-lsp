//! Variables definitely assigned before the selected statements.

use std::collections::HashSet;

use swlsp_parser_php::query::{self, MethodSelection};
use swlsp_parser_php::syntax::*;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::child_tokens;
use crate::extraction_flow::{Env, intersect_flow};

/// `(defined, references)`: parameters and locals definitely assigned before
/// the selection, and by-reference parameters.
pub(crate) fn extraction_available(
    selected: &MethodSelection,
) -> (HashSet<String>, HashSet<String>) {
    let mut defined = HashSet::new();
    let mut references = HashSet::new();
    for parameter in query::parameters(&selected.method) {
        let name = query::parameter_name(&parameter);
        if child_tokens(&parameter).any(|token| token.kind() == TK_AMPERSAND) {
            references.insert(name.clone());
        }
        defined.insert(name);
    }
    let path: Vec<SyntaxNode> = selected
        .block
        .ancestors()
        .take_while(|node| *node != selected.method)
        .collect();
    for index in (0..path.len()).rev() {
        let node = &path[index];
        let limit = if index > 0 {
            path[index - 1].range_trimmed_trivia().start()
        } else {
            selected.range.start()
        };
        match node.kind() {
            PHP_BLOCK => {
                for child in node.children() {
                    if child.range_trimmed_trivia().start() >= limit {
                        break;
                    }
                    add_definite_writes(&child, &mut defined);
                }
            }
            PHP_IF_STATEMENT | PHP_ELSE_IF_CLAUSE => {
                if let Some(condition) = query::direct_child(node, PHP_PARENTHESIZED) {
                    add_definite_writes(&condition, &mut defined);
                }
            }
            PHP_FOR_STATEMENT | PHP_FOREACH_STATEMENT | PHP_WHILE_STATEMENT => {
                let parts = query::extraction_loop_parts(node);
                for expression in parts.init.iter().chain(&parts.condition) {
                    add_definite_writes(expression, &mut defined);
                }
                for target in parts
                    .targets
                    .iter()
                    .filter(|target| target.kind() == PHP_VARIABLE)
                {
                    defined.insert(query::variable_key(target));
                }
            }
            _ => {}
        }
    }
    (defined, references)
}

fn add_definite_writes(node: &SyntaxNode, defined: &mut Env) {
    match node.kind() {
        PHP_EXPRESSION_STATEMENT | PHP_BLOCK | PHP_PARENTHESIZED => {
            for child in node.children() {
                add_definite_writes(&child, defined);
            }
        }
        PHP_BINARY_EXPRESSION => {
            // Only the left operand is evaluated unconditionally.
            if let Some(child) = node.first_child() {
                add_definite_writes(&child, defined);
            }
        }
        PHP_ASSIGNMENT_EXPRESSION => {
            let children = query::expression_children(node);
            if children.len() == 2
                && children[0].kind() == PHP_VARIABLE
                && query::expression_operator(node) == "="
            {
                defined.insert(query::variable_key(&children[0]));
            }
        }
        PHP_IF_STATEMENT => {
            let mut branches = Vec::new();
            let mut has_else = false;
            for branch in query::conditional_branches(node) {
                let mut local = defined.clone();
                if let Some(body) = &branch.body {
                    add_definite_writes(body, &mut local);
                }
                branches.push(local);
                has_else |= branch.condition.is_none();
            }
            if !has_else {
                branches.push(defined.clone());
            }
            intersect_flow(defined, &branches);
        }
        PHP_FOR_STATEMENT => {
            for expression in query::extraction_loop_parts(node).init {
                add_definite_writes(&expression, defined);
            }
        }
        _ => {}
    }
}
