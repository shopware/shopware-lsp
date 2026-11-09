//! Empty-array comparisons and `has*()`/`get*()` accessor conventions.

use swlsp_parser_php::query;
use swlsp_php_resolver::resolve_signature;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Joiner, Kind, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use super::Branches;
use crate::analyzer::Fs;
use crate::env::Env;
use crate::expr::resolve_member_special_types;
use crate::syntax::{flow_expression_key, normalize_flow_expression};
use crate::types_util::{MAX_SPECIAL_TYPE_DEPTH, array_shape_iterable_types, join_types};

impl Fs<'_, '_, '_> {
    pub(crate) fn narrow_empty_array(
        &mut self,
        expression: Option<&SyntaxNode>,
        operator: &str,
        env: &Env,
    ) -> Option<Branches> {
        let key = flow_expression_key(self.src(), expression);
        let expression = expression.filter(|_| !key.is_empty())?;
        let original = self.flow_value(&key, expression, env);
        let (non_empty, empty) = self.array_emptiness_types(&original, 0)?;
        let (true_value, false_value) = if operator == "===" {
            (empty, non_empty)
        } else {
            (non_empty, empty)
        };
        let (true_env, false_env) = (env.fork(), env.fork());
        true_env.set(&key, true_value.clone());
        false_env.set(&key, false_value);
        self.record(
            expression,
            &true_value,
            TypeSource::Flow,
            "empty array comparison",
        );
        Some((true_env, false_env))
    }

    /// The non-empty and empty projections of `value`; `None` when no array
    /// is possible.
    fn array_emptiness_types(&self, value: &Type, depth: usize) -> Option<(Type, Type)> {
        if depth >= MAX_SPECIAL_TYPE_DEPTH {
            return None;
        }
        if let Some(resolved) = self.snapshot.resolve_type_alias(value)
            && !resolved.is_unknown()
            && resolved != *value
        {
            return self.array_emptiness_types(&resolved, depth + 1);
        }
        let empty_array = || Type::array(Type::ARRAY_KEY, Type::NEVER);
        let relations = self.relations;
        match value.kind() {
            Kind::Array => {
                if value.argument_count() != 2 {
                    return None;
                }
                if value.argument(1).kind() == Kind::Never {
                    return Some((Type::NEVER, empty_array()));
                }
                Some((
                    Type::non_empty_array(value.argument(0), value.argument(1)),
                    empty_array(),
                ))
            }
            Kind::NonEmptyArray | Kind::NonEmptyList => Some((value.clone(), Type::NEVER)),
            Kind::List => {
                if value.argument_count() != 1 {
                    return None;
                }
                Some((Type::non_empty_list(value.argument(0)), empty_array()))
            }
            Kind::ArrayShape => {
                if value.fields().iter().any(|field| !field.optional) {
                    return Some((value.clone(), Type::NEVER));
                }
                let (mut key, mut element) = array_shape_iterable_types(value, relations);
                if value.is_open_shape() {
                    key = relations.join(&key, &Type::ARRAY_KEY);
                    element = relations.join(&element, &Type::MIXED);
                }
                if element.kind() == Kind::Never {
                    return Some((Type::NEVER, empty_array()));
                }
                Some((Type::non_empty_array(key, element), empty_array()))
            }
            Kind::Union => {
                let mut non_empty = Joiner::new(relations, Type::NEVER);
                let mut empty = Joiner::new(relations, Type::NEVER);
                let mut found_array = false;
                for member in value.arguments() {
                    match self.array_emptiness_types(member, depth + 1) {
                        Some((member_non_empty, member_empty)) => {
                            found_array = true;
                            non_empty.add(member_non_empty);
                            empty.add(member_empty);
                        }
                        // A non-array value always differs from [].
                        None => non_empty.add(member.clone()),
                    }
                }
                found_array.then(|| (non_empty.value(), empty.value()))
            }
            _ => None,
        }
    }

    /// `has()`/`hasX()` returning true proves the matching getter non-null.
    pub(crate) fn narrow_has_accessor(&mut self, call: &SyntaxNode, env: &Env) -> Option<Branches> {
        let name = query::call_method_name(call);
        let (target_name, reuse_arguments) = if eq_ignore_case(&name, "has") {
            ("get".to_owned(), true)
        } else if name.len() > 3 && name.is_char_boundary(3) && eq_ignore_case(&name[..3], "has") {
            if query::arguments(call).next().is_some() {
                return None;
            }
            (format!("get{}", &name[3..]), false)
        } else {
            return None;
        };
        let receiver_node = query::call_receiver(call)?;
        let receiver = self.infer_receiver(&receiver_node, env, false);
        let (arguments, uncertain) = self.infer_arguments(call, env);
        if uncertain {
            return None;
        }
        let relations = self.relations;
        let members = self.members();
        let mut has_compatible = false;
        members.visit_methods(&receiver, &name, |member| {
            let self_type = self.member_self_type(&member.symbol, &receiver);
            let symbol = resolve_member_special_types(member.symbol, &receiver, &self_type);
            let resolved = resolve_signature(relations, symbol, &arguments);
            if resolved.compatible && relations.is_assignable_to(&resolved.return_type, &Type::BOOL)
            {
                has_compatible = true;
                return false;
            }
            true
        });
        if !has_compatible {
            return None;
        }
        let target_arguments: &[_] = if reuse_arguments { &arguments } else { &[] };
        let mut returns = Vec::new();
        members.visit_methods(&receiver, &target_name, |member| {
            let self_type = self.member_self_type(&member.symbol, &receiver);
            let symbol = resolve_member_special_types(member.symbol, &receiver, &self_type);
            let resolved = resolve_signature(relations, symbol, target_arguments);
            if resolved.compatible {
                returns.push(resolved.return_type);
            }
            true
        });
        if returns.is_empty() {
            return None;
        }
        let original = join_types(relations, &returns);
        let non_null = relations.without(&original, &Type::NULL);
        if non_null.kind() == Kind::Never || non_null == original {
            return None;
        }
        let receiver_key = flow_expression_key(self.src(), Some(&receiver_node));
        if receiver_key.is_empty() {
            return None;
        }
        let mut target = String::new();
        target.push_str(&receiver_key);
        target.push_str("->");
        target.push_str(&target_name);
        target.push('(');
        if reuse_arguments {
            let count = query::arguments(call).count();
            for index in 0..count {
                if index > 0 {
                    target.push(',');
                }
                let expression = query::argument_expression(call, index)?;
                target.push_str(self.src().text(&expression));
            }
        }
        target.push(')');
        let key = normalize_flow_expression(&target).into_owned();
        let true_env = env.fork();
        true_env.set(&key, non_null);
        Some((true_env, env.fork()))
    }
}
