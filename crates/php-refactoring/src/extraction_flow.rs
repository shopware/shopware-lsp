//! Structured data-flow analysis of statements selected for method
//! extraction.

use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_syntax::SyntaxNode;

use crate::{Cancelled, child_tokens, descendant_tokens};

/// Variables (with `$`) definitely assigned at a program point.
pub(crate) type Env = HashSet<String>;

pub(crate) struct ExtractionFlow<'a> {
    pub retained: Vec<String>,
    retained_set: HashSet<String>,
    pub returns: Vec<SyntaxNode>,
    cancelled: Cancelled<'a>,
    pub base: HashSet<String>,
    pub references: HashSet<String>,
    pub inputs: Vec<String>,
    input_set: HashSet<String>,
    pub writes: HashSet<String>,
    is_static: bool,
}

impl<'a> ExtractionFlow<'a> {
    pub fn new(
        base: HashSet<String>,
        references: HashSet<String>,
        is_static: bool,
        cancelled: Cancelled<'a>,
    ) -> Self {
        Self {
            retained: Vec::new(),
            retained_set: HashSet::new(),
            returns: Vec::new(),
            cancelled,
            base,
            references,
            inputs: Vec::new(),
            input_set: HashSet::new(),
            writes: HashSet::new(),
            is_static,
        }
    }

    pub fn read(&mut self, name: &str, env: &mut Env) -> bool {
        if name == "$this" {
            return !self.is_static;
        }
        if env.contains(name) {
            return true;
        }
        if !self.base.contains(name) {
            return false;
        }
        if self.input_set.insert(name.to_owned()) {
            self.inputs.push(name.to_owned());
        }
        env.insert(name.to_owned());
        true
    }

    fn write(&mut self, name: &str, env: &mut Env) -> bool {
        if name == "$this" {
            return false;
        }
        if self.base.contains(name) {
            self.references.insert(name.to_owned());
        }
        if self.references.contains(name) && !self.read(name, env) {
            return false;
        }
        self.writes.insert(name.to_owned());
        env.insert(name.to_owned());
        true
    }

    fn expression(&mut self, node: &SyntaxNode, env: &mut Env) -> bool {
        if (self.cancelled)() {
            return false;
        }
        let children = query::expression_children(node);
        match node.kind() {
            PHP_ASSIGNMENT_EXPRESSION => {
                let [lhs, rhs] = children.as_slice() else {
                    return false;
                };
                if query::expression_operator(node) != "=" && !self.expression(lhs, env) {
                    return false;
                }
                if !self.expression(rhs, env) {
                    return false;
                }
                if lhs.kind() == PHP_VARIABLE && extraction_may_retain_object(rhs) {
                    let name = query::variable_key(lhs);
                    if self.retained_set.insert(name.clone()) {
                        self.retained.push(name);
                    }
                }
                return self.target(lhs, env);
            }
            PHP_UNARY_EXPRESSION => {
                let operator = query::expression_operator(node);
                if (operator == "++" || operator == "--") && children.len() == 1 {
                    return self.expression(&children[0], env) && self.target(&children[0], env);
                }
            }
            PHP_VARIABLE => return self.read(&query::variable_key(node), env),
            PHP_CLOSURE | PHP_ARROW_FUNCTION | PHP_ANONYMOUS_CLASS | PHP_YIELD_EXPRESSION => {
                return false;
            }
            // Branch-local writes require expression-level CFG handling. Reads
            // and pure branch expressions are supported without treating
            // writes as definite.
            PHP_BINARY_EXPRESSION | PHP_TERNARY_EXPRESSION | PHP_MATCH_EXPRESSION
                if node
                    .descendants()
                    .any(|child| child.kind() == PHP_ASSIGNMENT_EXPRESSION) =>
            {
                return false;
            }
            _ => {}
        }
        for child in &children {
            if !self.expression(child, env) {
                return false;
            }
        }
        for token in child_tokens(node) {
            if token.kind() == TK_VARIABLE && !self.read(token.text(), env) {
                return false;
            }
            if token.kind() == TK_STRING {
                let Some(names) = query::string_variables(&token) else {
                    return false;
                };
                for name in &names {
                    if !self.read(name, env) {
                        return false;
                    }
                }
            }
        }
        if matches!(node.kind(), PHP_ARGUMENT | PHP_NAMED_ARGUMENT) {
            for token in descendant_tokens(node) {
                if token.kind() == TK_VARIABLE && token.text() != "$this" {
                    // A call may write through a reference argument, but
                    // cannot make an otherwise undefined input safe to read.
                    self.writes.insert(token.text().to_owned());
                    if self.base.contains(token.text()) {
                        self.references.insert(token.text().to_owned());
                    }
                }
            }
        }
        true
    }

    pub fn target(&mut self, node: &SyntaxNode, env: &mut Env) -> bool {
        match node.kind() {
            PHP_VARIABLE => self.write(&query::variable_key(node), env),
            PHP_ARRAY_ACCESS => {
                if !self.expression(node, env) {
                    return false;
                }
                node.first_child()
                    .is_some_and(|child| self.target(&child, env))
            }
            PHP_MEMBER_ACCESS | PHP_SCOPED_ACCESS => self.expression(node, env),
            PHP_ARRAY => {
                for item in query::array_items(node) {
                    if let Some(key) = query::array_item_key(&item)
                        && !self.expression(&key, env)
                    {
                        return false;
                    }
                    let Some(value) = query::array_item_value(&item) else {
                        return false;
                    };
                    if value.kind() != PHP_VARIABLE
                        || !self.write(&query::variable_key(&value), env)
                    {
                        return false;
                    }
                }
                true
            }
            _ => false,
        }
    }

    /// `(returns, ok)`: whether all paths return from the enclosing callable,
    /// and whether the statements are supported at all.
    pub fn statements(
        &mut self,
        nodes: &[SyntaxNode],
        env: &mut Env,
        loops: usize,
    ) -> (bool, bool) {
        for (index, node) in nodes.iter().enumerate() {
            let (returns, ok) = self.statement(node, env, loops);
            if !ok {
                return (false, false);
            }
            if returns {
                return (true, index == nodes.len() - 1);
            }
            if matches!(node.kind(), PHP_BREAK_STATEMENT | PHP_CONTINUE_STATEMENT) {
                return (false, true);
            }
        }
        (false, true)
    }

    fn statement(&mut self, node: &SyntaxNode, env: &mut Env, loops: usize) -> (bool, bool) {
        if (self.cancelled)() {
            return (false, false);
        }
        match node.kind() {
            PHP_BLOCK => self.statements(&query::expression_children(node), env, loops),
            PHP_EXPRESSION_STATEMENT | PHP_ECHO_STATEMENT | PHP_THROW_STATEMENT => {
                (false, self.expression(node, env))
            }
            PHP_RETURN_STATEMENT => {
                self.returns.push(node.clone());
                (true, self.expression(node, env))
            }
            PHP_IF_STATEMENT => self.conditional(node, env, loops),
            PHP_SWITCH_STATEMENT => (false, self.switch_statement(node, env, loops)),
            PHP_FOR_STATEMENT
            | PHP_FOREACH_STATEMENT
            | PHP_WHILE_STATEMENT
            | PHP_DO_WHILE_STATEMENT => (false, self.loop_statement(node, env, loops)),
            PHP_BREAK_STATEMENT | PHP_CONTINUE_STATEMENT => {
                let mut level: i64 = 1;
                for child in query::expression_children(node) {
                    if child.kind() != PHP_NUMBER {
                        return (false, false);
                    }
                    match child.text().to_string().parse::<i64>() {
                        Ok(parsed) => level = parsed,
                        Err(_) => return (false, false),
                    }
                }
                (false, level > 0 && level <= loops as i64)
            }
            _ => (false, false),
        }
    }

    fn conditional(&mut self, node: &SyntaxNode, env: &mut Env, loops: usize) -> (bool, bool) {
        let mut before = env.clone();
        let mut states = Vec::new();
        let mut all_return = true;
        let mut has_else = false;
        for branch in query::conditional_branches(node) {
            match &branch.condition {
                Some(condition) => {
                    if !self.expression(condition, &mut before) {
                        return (false, false);
                    }
                }
                None => has_else = true,
            }
            let Some(body) = &branch.body else {
                return (false, false);
            };
            let mut local = before.clone();
            let (returns, ok) = self.statement(body, &mut local, loops);
            if !ok {
                return (false, false);
            }
            all_return &= returns;
            if !returns {
                states.push(local);
            }
        }
        if !has_else {
            states.push(before);
            all_return = false;
        }
        intersect_flow(env, &states);
        (all_return, true)
    }

    fn loop_statement(&mut self, node: &SyntaxNode, env: &mut Env, loops: usize) -> bool {
        let parts = query::extraction_loop_parts(node);
        for expression in &parts.init {
            if !self.expression(expression, env) {
                return false;
            }
        }
        if node.kind() != PHP_DO_WHILE_STATEMENT {
            for expression in &parts.condition {
                if !self.expression(expression, env) {
                    return false;
                }
            }
        }
        let mut local = env.clone();
        for target in &parts.targets {
            if !self.target(target, &mut local) {
                return false;
            }
        }
        let Some(body) = &parts.body else {
            return false;
        };
        if !self.statement(body, &mut local, loops + 1).1 {
            return false;
        }
        for expression in &parts.update {
            if !self.expression(expression, &mut local) {
                return false;
            }
        }
        if node.kind() == PHP_DO_WHILE_STATEMENT {
            for expression in &parts.condition {
                if !self.expression(expression, &mut local) {
                    return false;
                }
            }
        }
        true // The body may execute zero times or exit before a write.
    }

    fn switch_statement(&mut self, node: &SyntaxNode, env: &mut Env, loops: usize) -> bool {
        let mut states = Vec::new();
        let mut has_default = false;
        for child in node.children() {
            if child.kind() != PHP_CASE_CLAUSE {
                if !self.expression(&child, env) {
                    return false;
                }
                continue;
            }
            let mut local = env.clone();
            let is_default = child_tokens(&child).any(|token| token.text() == "default");
            has_default |= is_default;
            let mut nodes = query::expression_children(&child);
            if !is_default {
                if nodes.is_empty() || !self.expression(&nodes[0], &mut local) {
                    return false;
                }
                nodes.remove(0);
            }
            if !self.statements(&nodes, &mut local, loops + 1).1 {
                return false;
            }
            states.push(local);
        }
        if !has_default {
            states.push(env.clone());
        }
        intersect_flow(env, &states);
        true
    }
}

/// Replaces `target` with the names present in every state.
pub(crate) fn intersect_flow(target: &mut Env, states: &[Env]) {
    target.clear();
    let Some((first, rest)) = states.split_first() else {
        return;
    };
    target.extend(
        first
            .iter()
            .filter(|name| rest.iter().all(|state| state.contains(*name)))
            .cloned(),
    );
}

/// Locals holding an object (including one nested in an array) must remain
/// in the caller until their original lifetime ends, even without a later
/// read.
fn extraction_may_retain_object(node: &SyntaxNode) -> bool {
    match node.kind() {
        PHP_NUMBER
        | PHP_BOOLEAN
        | PHP_NULL
        | PHP_STRING
        | PHP_BINARY_EXPRESSION
        | PHP_UNARY_EXPRESSION => false,
        PHP_PARENTHESIZED => {
            let children = query::expression_children(node);
            children.len() != 1 || extraction_may_retain_object(&children[0])
        }
        _ => true,
    }
}
