//! PHPDoc assertion contracts and class identity narrowing (port of
//! `flow_assertion.go`).

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::resolve_signature;
use swlsp_php_semantic::{Symbol, SymbolKind, TypeSource};
use swlsp_php_types::{Relations, Type, contains_uncertain, substitute};
use swlsp_syntax::SyntaxNode;

use super::Branches;
use crate::analyzer::Fs;
use crate::env::Env;
use crate::expr::resolve_member_special_types;
use crate::syntax::{
    Source, class_constant_receiver, first_node, flow_expression_key, normalize_flow_expression,
    start,
};
use crate::types_util::join_types;

impl Fs<'_, '_, '_> {
    /// Conditional (`@phpstan-assert-if-true/false`) assertions of a method
    /// call used as a condition.
    pub(crate) fn narrow_call_assertions(
        &mut self,
        call: &SyntaxNode,
        env: &Env,
    ) -> (Env, Env, bool) {
        let Some(receiver_node) = first_node(call) else {
            return (env.fork(), env.fork(), false);
        };
        let receiver = self.infer(&receiver_node, env);
        let method_name = query::call_method_name(call);
        if method_name.is_empty() {
            return (env.fork(), env.fork(), false);
        }
        let (true_env, false_env) = (env.fork(), env.fork());
        let mut narrowed = false;
        let (arguments, _) = self.infer_arguments(call, env);
        let relations = self.relations;
        let members = self.members();
        members.visit_methods(&receiver, &method_name, |member| {
            let resolved = resolve_signature(relations, member.symbol.clone(), &arguments);
            if !resolved.compatible {
                return true;
            }
            for assertion in member.symbol.assertions() {
                if !assertion.conditional {
                    continue;
                }
                let target =
                    assertion_target_node(call, &receiver_node, &member.symbol, &assertion.target);
                let assertion_type = substitute(&assertion.ty, &resolved.templates);
                let mut key = flow_expression_key(self.src(), target.as_ref());
                let mut original = Type::UNKNOWN;
                if key.is_empty() {
                    let Some((member_key, member_type)) = self.assertion_receiver_member_target(
                        &receiver_node,
                        &receiver,
                        &assertion.target,
                    ) else {
                        continue;
                    };
                    key = member_key;
                    original = member_type;
                }
                if contains_uncertain(&assertion_type) {
                    continue;
                }
                if let Some(existing) = env.get(&key) {
                    original = existing;
                } else if original.is_unknown() {
                    original = self.infer_opt(target.as_ref(), env);
                }
                let true_value = apply_type_assertion(
                    relations,
                    &original,
                    &assertion_type,
                    assertion.negated == assertion.when_true,
                );
                let false_value = apply_type_assertion(
                    relations,
                    &original,
                    &assertion_type,
                    assertion.negated != assertion.when_true,
                );
                true_env.set(&key, true_value.clone());
                false_env.set(&key, false_value);
                self.record_opt(
                    target.as_ref(),
                    &true_value,
                    TypeSource::Flow,
                    "conditional assertion",
                );
                narrowed = true;
            }
            true
        });
        (true_env, false_env, narrowed)
    }

    /// Applies unconditional `@phpstan-assert` contracts of a call statement.
    pub(crate) fn apply_unconditional_call_assertions(
        &mut self,
        call: &SyntaxNode,
        env: &Env,
    ) -> bool {
        if !matches!(
            call.kind(),
            PHP_FUNCTION_CALL | PHP_MEMBER_CALL | PHP_SCOPED_CALL
        ) {
            return false;
        }
        let receiver_node = query::call_receiver(call);
        let method_name = query::call_method_name(call);
        let mut symbols: Vec<Symbol> = Vec::new();
        let members = self.members();
        if let Some(receiver_node) = &receiver_node {
            let receiver = self.infer_receiver(receiver_node, env, call.kind() == PHP_SCOPED_CALL);
            members.visit_methods(&receiver, &method_name, |member| {
                symbols.push(member.symbol);
                true
            });
            if symbols.is_empty() && self.current_class_is_trait() {
                // A trait does not know its consumer; use PHPUnit's Assert
                // declarations as the implicit contract.
                members.visit_methods(
                    &Type::named("PHPUnit\\Framework\\Assert", Vec::new()),
                    &method_name,
                    |member| {
                        symbols.push(member.symbol);
                        true
                    },
                );
            }
        } else if call.kind() == PHP_FUNCTION_CALL {
            let context = self.name_context_at(start(call));
            let snapshot = self.snapshot;
            context.visit_function_names(&method_name, |candidate| {
                snapshot.visit_function_views(candidate, |view| {
                    symbols.push(view.materialize());
                    true
                });
                true
            });
        }
        let has_assertions = symbols.iter().any(|symbol| {
            symbol
                .assertions()
                .iter()
                .any(|assertion| !assertion.conditional)
        });
        if !has_assertions {
            return false;
        }
        let (arguments, _) = self.infer_arguments(call, env);
        let relations = self.relations;
        let mut applied = false;
        for symbol in symbols {
            let resolved = resolve_signature(relations, symbol.clone(), &arguments);
            if !resolved.compatible {
                continue;
            }
            for assertion in symbol.assertions() {
                if assertion.conditional {
                    continue;
                }
                let target = assertion_target_node(
                    call,
                    receiver_node.as_ref().unwrap_or(call),
                    &symbol,
                    &assertion.target,
                )
                .filter(|_| receiver_node.is_some() || assertion.target.as_str() != "$this");
                let key = flow_expression_key(self.src(), target.as_ref());
                let assertion_type = substitute(&assertion.ty, &resolved.templates);
                if key.is_empty() || contains_uncertain(&assertion_type) {
                    continue;
                }
                let Some(target) = target else {
                    continue;
                };
                let original = self.flow_value(&key, &target, env);
                let value =
                    apply_type_assertion(relations, &original, &assertion_type, assertion.negated);
                env.set(&key, value.clone());
                self.record(&target, &value, TypeSource::Flow, "unconditional assertion");
                applied = true;
            }
        }
        applied
    }

    fn current_class_is_trait(&self) -> bool {
        let container = &self.symbol.container;
        if container.is_empty() {
            return false;
        }
        if let Some(symbol) = self.snapshot.symbol(container) {
            return symbol.kind == SymbolKind::Trait;
        }
        self.st
            .document
            .symbols
            .iter()
            .find(|symbol| symbol.id == *container)
            .is_some_and(|symbol| symbol.kind == SymbolKind::Trait)
    }

    /// Resolves an assertion target such as `$this->value` or
    /// `$this->getValue()` against the call receiver.
    fn assertion_receiver_member_target(
        &mut self,
        receiver_node: &SyntaxNode,
        receiver: &Type,
        target: &str,
    ) -> Option<(String, Type)> {
        let member = target.strip_prefix("$this->")?;
        let method_target = member.ends_with("()");
        let member_name = member.strip_suffix("()").unwrap_or(member);
        if member_name.is_empty() || member_name.contains(['(', ')', '$', '>', ',', ' ']) {
            return None;
        }
        let receiver_key = flow_expression_key(self.src(), Some(receiver_node));
        if receiver_key.is_empty() {
            return None;
        }
        let key = normalize_flow_expression(&format!(
            "{receiver_key}{}",
            target.strip_prefix("$this").unwrap_or(target)
        ))
        .into_owned();
        let relations = self.relations;
        let members = self.members();
        let mut returns = Vec::new();
        if method_target {
            members.visit_methods(receiver, member_name, |member| {
                let self_type = self.member_self_type(&member.symbol, receiver);
                let symbol = resolve_member_special_types(member.symbol, receiver, &self_type);
                let resolved = resolve_signature(relations, symbol, &[]);
                if resolved.compatible {
                    returns.push(resolved.return_type);
                }
                true
            });
        } else {
            returns.extend(members.property_types(receiver, member_name));
        }
        if returns.is_empty() {
            return None;
        }
        Some((key, join_types(relations, &returns)))
    }

    /// `Foo::class === $value::class` style class identity checks.
    pub(crate) fn narrow_class_identity(
        &mut self,
        left: Option<&SyntaxNode>,
        right: Option<&SyntaxNode>,
        operator: &str,
        env: &Env,
    ) -> Option<Branches> {
        let source = self.src();
        let (value_node, class_node) = class_identity_operands(source, left, right)
            .or_else(|| class_identity_operands(source, right, left))?;
        let key = flow_expression_key(source, Some(&value_node));
        if key.is_empty() {
            return None;
        }
        let class_name = self
            .name_context_at(start(&class_node))
            .resolve_class(&query::name_value(&class_node));
        let constraint = Type::named(&class_name, Vec::new());
        if constraint.is_unknown() {
            return None;
        }
        let original = self.flow_value(&key, &value_node, env);
        let (equal_env, not_equal_env) = (env.fork(), env.fork());
        equal_env.set(&key, self.relations.narrow(&original, &constraint));
        not_equal_env.set(&key, self.relations.without(&original, &constraint));
        let equal_value = equal_env.get(&key).unwrap_or_default();
        self.record(
            &value_node,
            &equal_value,
            TypeSource::Flow,
            "class identity",
        );
        if operator == "!==" {
            return Some((not_equal_env, equal_env));
        }
        Some((equal_env, not_equal_env))
    }
}

fn apply_type_assertion(
    relations: Relations<'_>,
    original: &Type,
    asserted: &Type,
    negated: bool,
) -> Type {
    if negated {
        relations.without(original, asserted)
    } else {
        relations.narrow(original, asserted)
    }
}

fn assertion_target_node(
    call: &SyntaxNode,
    receiver: &SyntaxNode,
    method: &Symbol,
    target: &str,
) -> Option<SyntaxNode> {
    if target == "$this" {
        return Some(receiver.clone());
    }
    let index = method
        .parameters
        .iter()
        .position(|parameter| &*parameter.name == target)?;
    query::argument_expression(call, index)
}

fn class_identity_operands(
    source: Source<'_>,
    value_access: Option<&SyntaxNode>,
    class_access: Option<&SyntaxNode>,
) -> Option<(SyntaxNode, SyntaxNode)> {
    let value = class_constant_receiver(source, value_access)?;
    let class = class_constant_receiver(source, class_access)?;
    if flow_expression_key(source, Some(&value)).is_empty() || class.kind() != PHP_NAME {
        return None;
    }
    Some((value, class))
}
