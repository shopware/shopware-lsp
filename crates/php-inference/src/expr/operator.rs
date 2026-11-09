//! Binary, unary and ternary operators.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::lowercase;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{
    compact, direct_nodes, direct_operator, first_node, has_direct_token_text, last_node,
    lower_call_name,
};
use crate::types_util::{is_float_like, is_int_like, predicate_type, without_falsy_literals};

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_binary(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let nodes = direct_nodes(node);
        if nodes.len() < 2 {
            return Type::UNKNOWN;
        }
        let operator = direct_operator(node);
        let normalized = lowercase(&operator);
        let left_node = &nodes[0];
        let right_node = &nodes[nodes.len() - 1];
        match normalized.as_str() {
            "&&" | "and" => {
                let (left_true, _) = self.condition_environments(Some(left_node), env);
                self.infer(right_node, &left_true);
                return Type::BOOL;
            }
            "||" | "or" => {
                let (_, left_false) = self.condition_environments(Some(left_node), env);
                self.infer(right_node, &left_false);
                return Type::BOOL;
            }
            _ => {}
        }
        let left = self.infer(left_node, env);
        let right = self.infer(right_node, env);
        match normalized.as_str() {
            "==" | "!=" | "===" | "!==" | "<" | ">" | "<=" | ">=" | "instanceof" | "xor" => {
                Type::BOOL
            }
            "<=>" => Type::INT,
            "." => Type::STRING,
            "??" => self
                .relations
                .join(&self.relations.without(&left, &Type::NULL), &right),
            "+" | "-" | "*" | "/" | "%" | "**" => {
                let is_array =
                    |value: &Type| matches!(value.kind(), Kind::Array | Kind::NonEmptyArray);
                if is_array(&left) && is_array(&right) && operator == "+" {
                    return self.relations.join(&left, &right);
                }
                if is_float_like(&left) || is_float_like(&right) || operator == "/" {
                    return Type::FLOAT;
                }
                if is_int_like(&left) && is_int_like(&right) {
                    return Type::INT;
                }
                Type::UNKNOWN
            }
            "|" | "&" | "^" | "<<" | ">>" => Type::INT,
            _ => Type::UNKNOWN,
        }
    }

    pub(crate) fn infer_unary(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let value = self.infer_opt(last_node(node).as_ref(), env);
        for keyword in ["include", "include_once", "require", "require_once"] {
            if has_direct_token_text(node, keyword) {
                // Included files may return any value.
                return Type::MIXED;
            }
        }
        match direct_operator(node).as_str() {
            "!" => Type::BOOL,
            "+" | "-" | "++" | "--" => {
                if is_float_like(&value) {
                    Type::FLOAT
                } else if is_int_like(&value) {
                    Type::INT
                } else {
                    Type::UNKNOWN
                }
            }
            "~" => {
                if matches!(value.kind(), Kind::String | Kind::LiteralString) {
                    Type::STRING
                } else {
                    Type::INT
                }
            }
            _ => value,
        }
    }

    pub(crate) fn infer_ternary(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let nodes = direct_nodes(node);
        if nodes.len() < 2 {
            return Type::UNKNOWN;
        }
        let condition = &nodes[0];
        let (true_env, false_env) = self.condition_environments(Some(condition), env);
        if nodes.len() == 2 {
            let condition_value = self.infer(&nodes[0], &true_env);
            let truthy = without_falsy_literals(self.relations, &condition_value);
            let other = self.infer(&nodes[1], &false_env);
            return self.relations.join(&truthy, &other);
        }
        let true_node = &nodes[nodes.len() - 2];
        let false_node = &nodes[nodes.len() - 1];
        let true_value = self.infer(true_node, &true_env);
        let true_value =
            self.narrow_repeated_condition_expression(Some(condition), true_node, true_value, true);
        let false_value = self.infer(false_node, &false_env);
        let false_value = self.narrow_repeated_condition_expression(
            Some(condition),
            false_node,
            false_value,
            false,
        );
        self.relations.join(&true_value, &false_value)
    }

    fn narrow_repeated_condition_expression(
        &mut self,
        condition: Option<&SyntaxNode>,
        branch: &SyntaxNode,
        value: Type,
        truth: bool,
    ) -> Type {
        let Some(condition) = condition else {
            return value;
        };
        if condition.kind() == PHP_PARENTHESIZED {
            return self.narrow_repeated_condition_expression(
                first_node(condition).as_ref(),
                branch,
                value,
                truth,
            );
        }
        if condition.kind() == PHP_UNARY_EXPRESSION && direct_operator(condition) == "!" {
            return self.narrow_repeated_condition_expression(
                last_node(condition).as_ref(),
                branch,
                value,
                !truth,
            );
        }
        if condition.kind() != PHP_FUNCTION_CALL {
            return value;
        }
        let constraint = predicate_type(&lower_call_name(condition));
        if constraint.is_unknown() {
            return value;
        }
        let Some(first_argument) = query::arguments(condition).next() else {
            return value;
        };
        let source = self.src();
        let Some(tested) = last_node(&first_argument) else {
            return value;
        };
        if compact(source.text(&tested)) != compact(source.text(branch)) {
            return value;
        }
        let value = if truth {
            self.relations.narrow(&value, &constraint)
        } else {
            self.relations.without(&value, &constraint)
        };
        self.record(branch, &value, TypeSource::Flow, "conditional predicate");
        value
    }
}
