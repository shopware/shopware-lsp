//! Condition narrowing (port of `flow_condition_dispatch.go`).

mod array;
mod assertion;
mod boolean;
mod narrow;
mod update;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Type, go_to_lower};
use swlsp_syntax::SyntaxNode;

pub(crate) use boolean::boolean_alias_prefix;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{
    direct_nodes, direct_operator, first_node, flow_expression_key, has_direct_token, is_null_node,
    last_node, lower_call_name, predicate_flow_target,
};
use crate::types_util::predicate_type;

pub(crate) type Branches = (Env, Env);

impl Fs<'_, '_, '_> {
    /// The environments in which `node` is true and false.
    pub(crate) fn condition_environments(
        &mut self,
        node: Option<&SyntaxNode>,
        env: &Env,
    ) -> Branches {
        let Some(node) = node else {
            return (env.fork(), env.fork());
        };
        if node.kind() == PHP_PARENTHESIZED {
            return self.condition_environments(first_node(node).as_ref(), env);
        }
        if node.kind() == PHP_UNARY_EXPRESSION && direct_operator(node) == "!" {
            let (false_env, true_env) = self.condition_environments(last_node(node).as_ref(), env);
            self.record(node, &Type::BOOL, TypeSource::Flow, "logical condition");
            return (true_env, false_env);
        }
        if node.kind() == PHP_BINARY_EXPRESSION {
            if let Some(branches) = self.binary_condition_environments(node, env) {
                return branches;
            }
        } else {
            self.infer(node, env);
        }
        match node.kind() {
            PHP_MEMBER_CALL => {
                if let Some(branches) = self.member_call_condition_environments(node, env) {
                    return branches;
                }
            }
            PHP_MEMBER_ACCESS => {
                if has_direct_token(node, TK_NULLSAFE_OBJECT_OPERATOR) {
                    let (true_env, false_env) = self.truthiness_environments(Some(node), env);
                    if self.narrow_nullsafe_receiver(node, &true_env, env) {
                        return (true_env, false_env);
                    }
                }
            }
            PHP_FUNCTION_CALL => {
                if let Some(branches) = self.function_call_condition_environments(node, env) {
                    return branches;
                }
            }
            _ => {}
        }
        if !flow_expression_key(self.src(), Some(node)).is_empty() {
            if node.kind() == PHP_VARIABLE
                && let Some(branches) = self.boolean_alias_environments(node, env)
            {
                return branches;
            }
            return self.truthiness_environments(Some(node), env);
        }
        if node.kind() == PHP_ASSIGNMENT_EXPRESSION
            && let Some(left) = first_node(node)
            && left.kind() == PHP_VARIABLE
        {
            return self.truthiness_environments(Some(&left), env);
        }
        (env.fork(), env.fork())
    }

    fn binary_condition_environments(&mut self, node: &SyntaxNode, env: &Env) -> Option<Branches> {
        let nodes = direct_nodes(node);
        if nodes.len() < 2 {
            return None;
        }
        let left = &nodes[0];
        let right = &nodes[nodes.len() - 1];
        let operator = go_to_lower(&direct_operator(node));
        match operator.as_str() {
            "&&" | "and" => {
                let (left_true, left_false) = self.condition_environments(Some(left), env);
                let (right_true, right_false) =
                    self.condition_environments(Some(right), &left_true);
                self.record(node, &Type::BOOL, TypeSource::Flow, "logical condition");
                let false_env = self.join(&left_false, &right_false);
                return Some((right_true, false_env));
            }
            "||" | "or" => {
                let (left_true, left_false) = self.condition_environments(Some(left), env);
                let (right_true, right_false) =
                    self.condition_environments(Some(right), &left_false);
                self.record(node, &Type::BOOL, TypeSource::Flow, "logical condition");
                let true_env = self.join(&left_true, &right_true);
                return Some((true_env, right_false));
            }
            _ => {}
        }
        self.infer(node, env);
        if operator == "instanceof" {
            return Some(self.narrow_instanceof(left, right, env));
        }
        if matches!(operator.as_str(), "===" | "!==" | "==" | "!=") {
            if is_null_node(self.src(), Some(left)) {
                return Some(self.narrow_null(right, &operator, env));
            }
            if is_null_node(self.src(), Some(right)) {
                return Some(self.narrow_null(left, &operator, env));
            }
        }
        if operator != "===" && operator != "!==" {
            return None;
        }
        self.strict_comparison_environments(left, right, &operator, env)
    }

    fn strict_comparison_environments(
        &mut self,
        left: &SyntaxNode,
        right: &SyntaxNode,
        operator: &str,
        env: &Env,
    ) -> Option<Branches> {
        if is_empty_array_literal(left)
            && let Some(branches) = self.narrow_empty_array(Some(right), operator, env)
        {
            return Some(branches);
        }
        if is_empty_array_literal(right)
            && let Some(branches) = self.narrow_empty_array(Some(left), operator, env)
        {
            return Some(branches);
        }
        if let Some(branches) = self.narrow_class_identity(Some(left), Some(right), operator, env) {
            return Some(branches);
        }
        if let Some(constraint) = self.condition_literal_type(Some(left), env) {
            return Some(self.narrow_literal(Some(right), &constraint, operator, env));
        }
        if let Some(constraint) = self.condition_literal_type(Some(right), env) {
            return Some(self.narrow_literal(Some(left), &constraint, operator, env));
        }
        None
    }

    fn member_call_condition_environments(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
    ) -> Option<Branches> {
        let (mut true_env, mut false_env, mut narrowed) = self.narrow_call_assertions(node, env);
        if let Some((convention_true, convention_false)) = self.narrow_has_accessor(node, env) {
            if narrowed {
                true_env = self.merge_refined_environment(&true_env, &convention_true);
                false_env = self.merge_refined_environment(&false_env, &convention_false);
            } else {
                true_env = convention_true;
                false_env = convention_false;
            }
            narrowed = true;
        }
        if !narrowed {
            (true_env, false_env) = self.truthiness_environments(Some(node), env);
        }
        if self.narrow_nullsafe_receiver(node, &true_env, env) {
            narrowed = true;
        }
        narrowed.then_some((true_env, false_env))
    }

    fn function_call_condition_environments(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
    ) -> Option<Branches> {
        let name = lower_call_name(node);
        match name.as_str() {
            "empty" => {
                if let Some(branches) = self.narrow_empty(node, env) {
                    return Some(branches);
                }
            }
            "isset" => {
                if let Some(branches) = self.narrow_isset(node, env) {
                    return Some(branches);
                }
            }
            _ => {}
        }
        if let Some(branches) = self.narrow_class_predicate(node, &name, env) {
            return Some(branches);
        }
        if name == "is_numeric"
            && let Some(branches) = self.numeric_predicate_environments(node, env)
        {
            return Some(branches);
        }
        self.typed_predicate_environments(node, &name, env)
    }

    fn numeric_predicate_environments(&mut self, node: &SyntaxNode, env: &Env) -> Option<Branches> {
        let expression = query::argument_expression(node, 0)?;
        let key = flow_expression_key(self.src(), Some(&expression));
        if key.is_empty() {
            return None;
        }
        let original = self.flow_value(&key, &expression, env);
        let relations = self.relations;
        let true_value = relations.narrow(
            &original,
            &Type::union([Type::INT, Type::FLOAT, Type::STRING]),
        );
        let false_value =
            relations.without(&relations.without(&original, &Type::INT), &Type::FLOAT);
        let (true_env, false_env) = (env.fork(), env.fork());
        true_env.set(&key, true_value.clone());
        false_env.set(&key, false_value);
        self.record(&expression, &true_value, TypeSource::Flow, "is_numeric");
        Some((true_env, false_env))
    }

    fn typed_predicate_environments(
        &mut self,
        node: &SyntaxNode,
        name: &str,
        env: &Env,
    ) -> Option<Branches> {
        let narrowed = predicate_type(name);
        if narrowed.is_unknown() {
            return None;
        }
        let first_argument = query::arguments(node).next()?;
        let expression = predicate_flow_target(self.src(), first_node(&first_argument));
        let key = flow_expression_key(self.src(), expression.as_ref());
        if key.is_empty() {
            return None;
        }
        let expression = expression?;
        let original = self.flow_value(&key, &expression, env);
        let true_value = self.relations.narrow(&original, &narrowed);
        let false_value = self.relations.without(&original, &narrowed);
        let (true_env, false_env) = (env.fork(), env.fork());
        true_env.set(&key, true_value.clone());
        false_env.set(&key, false_value);
        self.record(&expression, &true_value, TypeSource::Flow, name.to_owned());
        Some((true_env, false_env))
    }

    /// The current flow value of `key`, inferring `node` when unbound.
    pub(crate) fn flow_value(&mut self, key: &str, node: &SyntaxNode, env: &Env) -> Type {
        match env.get(key) {
            Some(value) => value,
            None => self.infer(node, env),
        }
    }
}

pub(crate) fn is_empty_array_literal(node: &SyntaxNode) -> bool {
    node.kind() == PHP_ARRAY && crate::expr::inspect_array_literal(node).item_count == 0
}
