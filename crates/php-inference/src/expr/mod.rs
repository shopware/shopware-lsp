//! Expression evaluation (port of `expression_*.go`).

mod array;
mod assignment;
mod call;
mod closure;
mod matching;
mod object;
mod operator;

use std::borrow::Cow;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::{Confidence, TypeSource, node_identity};
use swlsp_php_types::{Type, go_to_lower};
use swlsp_syntax::SyntaxNode;

pub(crate) use array::inspect_array_literal;
pub(crate) use assignment::ArrayAccessSegment;
pub(crate) use call::{is_first_class_callable, resolve_member_special_types};

#[cfg(test)]
pub(crate) use array::{SHAPE_FIELD_LINEAR_LIMIT, is_implicit_list_array, upsert_shape_field};

use crate::analyzer::Fs;
use crate::env::Env;
use crate::extensions::{CallArgument, CallContext};
use crate::syntax::{
    cast_type, direct_nodes, first_node, flow_expression_key, has_direct_token, last_node,
};
use crate::types_util::{join_types, resolve_special_type};

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_opt(&mut self, node: Option<&SyntaxNode>, env: &Env) -> Type {
        match node {
            Some(node) => self.infer(node, env),
            None => Type::UNKNOWN,
        }
    }

    pub(crate) fn infer(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        if let Some(fact) = self.st.type_fact(node_identity(node)) {
            // Literal expressions are independent of the flow environment.
            if fact.confidence == Confidence::Declared || fact.source == TypeSource::Literal {
                return fact.ty;
            }
        }
        if let Some(value) = swlsp_php_literal::type_of(node) {
            return value;
        }
        let key = flow_expression_key(self.src(), Some(node));
        if !key.is_empty()
            && let Some(value) = env.get(&key)
        {
            match node.kind() {
                PHP_MEMBER_ACCESS | PHP_MEMBER_CALL | PHP_SCOPED_ACCESS | PHP_SCOPED_CALL => {
                    // The member-linking pass also needs the receiver's fact.
                    let receiver = first_node(node);
                    self.infer_opt(receiver.as_ref(), env);
                }
                PHP_ARRAY_ACCESS => {
                    // Key expressions may contain calls that need receiver
                    // facts for member linking.
                    let nodes = direct_nodes(node);
                    if let Some(first) = nodes.first() {
                        self.infer(first, env);
                    }
                    if nodes.len() > 1 {
                        self.infer(&nodes[nodes.len() - 1], env);
                    }
                }
                _ => {}
            }
            self.record(node, &value, TypeSource::Flow, "flow expression");
            return value;
        }
        let mut confidence = Confidence::Inferred;
        let mut type_source = TypeSource::Assignment;
        let result = match node.kind() {
            PHP_VARIABLE => env.get(&query::variable_key(node)).unwrap_or_default(),
            PHP_PARENTHESIZED => self.infer_opt(first_node(node).as_ref(), env),
            PHP_ASSIGNMENT_EXPRESSION => self.infer_assignment(node, env),
            PHP_BINARY_EXPRESSION => self.infer_binary(node, env),
            PHP_UNARY_EXPRESSION => self.infer_unary(node, env),
            PHP_TERNARY_EXPRESSION => self.infer_ternary(node, env),
            PHP_ARRAY => self.infer_array(node, env),
            PHP_ARRAY_ACCESS => self.infer_array_access(node, env),
            PHP_OBJECT_CREATION => {
                type_source = TypeSource::Signature;
                self.infer_object_creation(node, env)
            }
            PHP_MEMBER_ACCESS => {
                type_source = TypeSource::Signature;
                let is_static = has_direct_token(node, TK_SCOPE_RESOLUTION);
                self.infer_member(node, env, is_static)
            }
            PHP_SCOPED_ACCESS => {
                type_source = TypeSource::Signature;
                self.infer_member(node, env, true)
            }
            PHP_MEMBER_CALL => {
                type_source = TypeSource::Signature;
                self.infer_call(node, env, false, false)
            }
            PHP_SCOPED_CALL => {
                type_source = TypeSource::Signature;
                self.infer_call(node, env, true, false)
            }
            PHP_FUNCTION_CALL => {
                type_source = TypeSource::Signature;
                self.infer_call(node, env, false, true)
            }
            PHP_ARROW_FUNCTION => self.infer_arrow_function(node, env, &[]),
            PHP_CLOSURE => self.infer_closure(node, env, &[]),
            PHP_MATCH_EXPRESSION => self.infer_match(node, env),
            PHP_THROW_EXPRESSION => {
                if let Some(expression) = last_node(node) {
                    self.infer(&expression, env);
                }
                Type::NEVER
            }
            PHP_CLONE_EXPRESSION => self.infer_opt(last_node(node).as_ref(), env),
            PHP_YIELD_EXPRESSION => {
                let mut value = Type::MIXED;
                for expression in node.children() {
                    value = self.infer(&expression, env);
                }
                Type::iterable(Type::ARRAY_KEY, value)
            }
            PHP_CAST_EXPRESSION => {
                self.infer_opt(last_node(node).as_ref(), env);
                cast_type(self.src(), node)
            }
            PHP_NAME => infer_name(node),
            PHP_ARGUMENT | PHP_NAMED_ARGUMENT | PHP_ARRAY_ITEM | PHP_MATCH_ARM => {
                self.infer_opt(last_node(node).as_ref(), env)
            }
            _ => Type::UNKNOWN,
        };
        if result.is_unknown() {
            type_source = TypeSource::Unknown;
            confidence = Confidence::Unknown;
        }
        self.st
            .record_fact(node, &result, confidence, type_source, Cow::Borrowed(""));
        result
    }

    pub(crate) fn join_members(&self, members: &[Type], receiver: &Type) -> Type {
        let values: Vec<Type> = members
            .iter()
            .map(|member| resolve_special_type(member, receiver, &self.current_class))
            .collect();
        join_types(self.relations, &values)
    }

    /// Runs the registered extensions; the first match wins and is stored as
    /// the call's fact.
    pub(crate) fn extension_type(
        &mut self,
        node: &SyntaxNode,
        name: &str,
        receiver: &Type,
        arguments: &[CallArgument],
        is_static: bool,
    ) -> Option<Type> {
        let extensions = self.st.extensions();
        if extensions.is_empty() {
            return None;
        }
        let fact = {
            let context = CallContext {
                snapshot: Some(self.snapshot),
                document: Some(&self.st.document),
                node: Some(node),
                source: self.src().0,
                name,
                receiver: receiver.clone(),
                arguments,
                current_class: self.current_class.clone(),
                is_static,
            };
            extensions
                .iter()
                .find_map(|extension| extension.infer_call(&context))
        }?;
        let value = fact.ty.clone();
        self.st.document.set_type_fact(node_identity(node), fact);
        Some(value)
    }
}

fn infer_name(node: &SyntaxNode) -> Type {
    match go_to_lower(&query::name_value(node)).as_str() {
        "null" => Type::NULL,
        "true" => Type::TRUE,
        "false" => Type::FALSE,
        _ => Type::UNKNOWN,
    }
}
