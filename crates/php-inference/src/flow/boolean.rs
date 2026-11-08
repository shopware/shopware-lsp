//! Boolean aliases, isset/empty and nullsafe narrowing (port of
//! `flow_boolean.go`).

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::SyntaxNode;

use super::Branches;
use crate::analyzer::Fs;
use crate::env::{BOOLEAN_ALIAS_BINDING_PREFIX, Env, IMPOSSIBLE_ENVIRONMENT_BINDING};
use crate::syntax::{first_node, flow_expression_key, has_direct_token};

pub(crate) fn boolean_alias_prefix(name: &str) -> String {
    format!("{BOOLEAN_ALIAS_BINDING_PREFIX}{name}:")
}

fn boolean_alias_branch_prefix(name: &str, truth: bool) -> String {
    let branch = if truth { "true:" } else { "false:" };
    format!("{}{branch}", boolean_alias_prefix(name))
}

impl Fs<'_, '_, '_> {
    pub(crate) fn merge_refined_environment(&self, base: &Env, refinements: &Env) -> Env {
        let result = base.fork();
        refinements.visit(|name, value| {
            if base.get(name).is_none_or(|original| original != *value) {
                result.set(name, value.clone());
            }
        });
        result
    }

    /// Records how a boolean variable's truth refines other variables.
    pub(crate) fn capture_boolean_alias(
        &mut self,
        name: &str,
        condition: Option<&SyntaxNode>,
        env: &Env,
    ) {
        let Some(condition) = condition else {
            return;
        };
        if condition.kind() == PHP_VARIABLE && query::variable_key(condition) == name {
            return;
        }
        let (true_env, false_env) = self.condition_environments(Some(condition), env);
        for (truth, branch) in [(true, true_env), (false, false_env)] {
            let prefix = boolean_alias_branch_prefix(name, truth);
            let mut refinements = Vec::new();
            branch.visit(|key, value| {
                if key.starts_with(BOOLEAN_ALIAS_BINDING_PREFIX) || key == name {
                    return;
                }
                match env.get(key) {
                    Some(original) if original != *value => {
                        refinements.push((format!("{prefix}{key}"), value.clone()));
                    }
                    _ => {}
                }
            });
            for (key, value) in refinements {
                env.set(&key, value);
            }
        }
    }

    pub(crate) fn boolean_alias_environments(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
    ) -> Option<Branches> {
        let name = query::variable_key(node);
        if name.is_empty() {
            return None;
        }
        let true_prefix = boolean_alias_branch_prefix(&name, true);
        let false_prefix = boolean_alias_branch_prefix(&name, false);
        let mut refinements = Vec::new();
        env.visit(|key, value| {
            if let Some(stripped) = key.strip_prefix(&true_prefix) {
                refinements.push((stripped.to_owned(), value.clone(), true));
            } else if let Some(stripped) = key.strip_prefix(&false_prefix) {
                refinements.push((stripped.to_owned(), value.clone(), false));
            }
        });
        if refinements.is_empty() {
            return None;
        }
        let (true_env, false_env) = (env.fork(), env.fork());
        for (key, value, truth) in refinements {
            if truth {
                true_env.set(&key, value);
            } else {
                false_env.set(&key, value);
            }
        }
        Some((true_env, false_env))
    }

    /// A literal comparison operand: a scalar literal or a class constant
    /// whose inferred type is literal.
    pub(crate) fn condition_literal_type(
        &mut self,
        node: Option<&SyntaxNode>,
        env: &Env,
    ) -> Option<Type> {
        let node = node?;
        if let Some(value) = swlsp_php_literal::type_of(node) {
            return Some(value);
        }
        if (node.kind() != PHP_MEMBER_ACCESS && node.kind() != PHP_SCOPED_ACCESS)
            || !has_direct_token(node, TK_SCOPE_RESOLUTION)
        {
            return None;
        }
        let value = self.infer(node, env);
        matches!(
            value.kind(),
            Kind::LiteralString
                | Kind::LiteralInt
                | Kind::LiteralFloat
                | Kind::True
                | Kind::False
                | Kind::Null
        )
        .then_some(value)
    }

    pub(crate) fn narrow_nullsafe_receiver(
        &mut self,
        expression: &SyntaxNode,
        true_env: &Env,
        original_env: &Env,
    ) -> bool {
        if !has_direct_token(expression, TK_NULLSAFE_OBJECT_OPERATOR) {
            return false;
        }
        let receiver = query::call_receiver(expression).or_else(|| first_node(expression));
        let key = flow_expression_key(self.src(), receiver.as_ref());
        let Some(receiver) = receiver.filter(|_| !key.is_empty()) else {
            return false;
        };
        let original = self.flow_value(&key, &receiver, original_env);
        let value = self.relations.without(&original, &Type::NULL);
        true_env.set(&key, value.clone());
        self.record(&receiver, &value, TypeSource::Flow, "nullsafe truthiness");
        true
    }

    pub(crate) fn narrow_isset(&mut self, call: &SyntaxNode, env: &Env) -> Option<Branches> {
        let (true_env, false_env) = (env.fork(), env.fork());
        let argument_count = query::arguments(call).count();
        let mut narrowed = false;
        for index in 0..argument_count {
            let Some(expression) = query::argument_expression(call, index) else {
                continue;
            };
            let key = flow_expression_key(self.src(), Some(&expression));
            if key.is_empty() {
                continue;
            }
            let original = self.flow_value(&key, &expression, env);
            if original.kind() == Kind::Never {
                true_env.set(IMPOSSIBLE_ENVIRONMENT_BINDING, Type::NEVER);
                if argument_count == 1 {
                    false_env.set(&key, Type::NULL);
                }
                self.record(&expression, &Type::NEVER, TypeSource::Flow, "isset");
                narrowed = true;
                continue;
            }
            let true_value = self.relations.without(&original, &Type::NULL);
            true_env.set(&key, true_value.clone());
            self.narrow_array_access_base(&expression, &true_env, &true_value, false);
            // With one operand a false isset() proves it null or absent.
            if argument_count == 1 {
                let false_value = self.relations.narrow(&original, &Type::NULL);
                false_env.set(&key, false_value.clone());
                self.narrow_array_access_base(&expression, &false_env, &false_value, true);
            }
            self.record(&expression, &true_value, TypeSource::Flow, "isset");
            narrowed = true;
        }
        narrowed.then_some((true_env, false_env))
    }

    pub(crate) fn narrow_empty(&mut self, call: &SyntaxNode, env: &Env) -> Option<Branches> {
        let expression = query::argument_expression(call, 0)?;
        if flow_expression_key(self.src(), Some(&expression)).is_empty() {
            return None;
        }
        let (truthy, falsy) = self.truthiness_environments(Some(&expression), env);
        // empty() is the inverse of the value's truthiness.
        Some((falsy, truthy))
    }
}
