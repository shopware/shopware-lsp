//! Literal, null, instanceof and class predicate narrowing (port of
//! `flow_narrow.go`).

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Kind, Type, equal_fold, go_to_lower};
use swlsp_syntax::SyntaxNode;

use super::Branches;
use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{
    compact, condition_flow_expression_key, flow_expression_key, start, trim_backslash,
};
use crate::types_util::without_falsy_literals;

impl Fs<'_, '_, '_> {
    pub(crate) fn narrow_class_predicate(
        &mut self,
        call: &SyntaxNode,
        name: &str,
        env: &Env,
    ) -> Option<Branches> {
        let argument_count = query::arguments(call).count();
        if argument_count == 0 {
            return None;
        }
        let expression = query::argument_expression(call, 0)?;
        let key = flow_expression_key(self.src(), Some(&expression));
        if key.is_empty() {
            return None;
        }
        let original = self.flow_value(&key, &expression, env);
        let relations = self.relations;
        let constraint = match name {
            "class_exists" | "interface_exists" | "trait_exists" | "enum_exists" => {
                Type::class_string(Type::OBJECT)
            }
            "is_a" | "is_subclass_of" => {
                if argument_count < 2 {
                    return None;
                }
                let target_expression = query::argument_expression(call, 1);
                let target = self.infer_opt(target_expression.as_ref(), env);
                let mut target_object = Type::OBJECT;
                if target.kind() == Kind::ClassString && target.argument_count() == 1 {
                    target_object = target.argument(0);
                }
                let mut allow_string = name == "is_subclass_of";
                if argument_count > 2 {
                    let allow = query::argument_expression(call, 2);
                    let allow_type = self.infer_opt(allow.as_ref(), env);
                    if allow_type.kind() == Kind::True {
                        allow_string = true;
                    } else if allow_type.kind() == Kind::False {
                        allow_string = false;
                    }
                }
                if allow_string {
                    if relations.is_subtype(&original, &Type::STRING) {
                        Type::class_string(target_object)
                    } else if relations.is_subtype(&original, &Type::OBJECT) {
                        target_object
                    } else {
                        Type::union([target_object.clone(), Type::class_string(target_object)])
                    }
                } else {
                    target_object
                }
            }
            _ => return None,
        };
        let (true_env, false_env) = (env.fork(), env.fork());
        let true_value = relations.narrow(&original, &constraint);
        true_env.set(&key, true_value.clone());
        false_env.set(&key, relations.without(&original, &constraint));
        self.record(&expression, &true_value, TypeSource::Flow, name.to_owned());
        Some((true_env, false_env))
    }

    pub(crate) fn narrow_literal(
        &mut self,
        value_node: Option<&SyntaxNode>,
        constraint: &Type,
        operator: &str,
        env: &Env,
    ) -> Branches {
        let key = condition_flow_expression_key(self.src(), value_node);
        let Some(value_node) = value_node.filter(|_| !key.is_empty()) else {
            return (env.fork(), env.fork());
        };
        let (true_env, false_env) = (env.fork(), env.fork());
        let original = self.flow_value(&key, value_node, env);
        let relations = self.relations;
        if operator == "===" {
            true_env.set(&key, relations.narrow(&original, constraint));
            false_env.set(&key, relations.without(&original, constraint));
        } else {
            true_env.set(&key, relations.without(&original, constraint));
            false_env.set(&key, relations.narrow(&original, constraint));
        }
        let true_value = true_env.get(&key).unwrap_or_default();
        self.record(
            value_node,
            &true_value,
            TypeSource::Flow,
            "literal comparison",
        );
        (true_env, false_env)
    }

    pub(crate) fn truthiness_environments(
        &mut self,
        expression: Option<&SyntaxNode>,
        env: &Env,
    ) -> Branches {
        let key = flow_expression_key(self.src(), expression);
        let Some(expression) = expression.filter(|_| !key.is_empty()) else {
            return (env.fork(), env.fork());
        };
        let original = self.flow_value(&key, expression, env);
        let truthy = without_falsy_literals(self.relations, &original);
        let true_env = env.fork();
        true_env.set(&key, truthy.clone());
        self.record(expression, &truthy, TypeSource::Flow, "truthiness");
        (true_env, env.fork())
    }

    pub(crate) fn narrow_instanceof(
        &mut self,
        value_node: &SyntaxNode,
        type_node: &SyntaxNode,
        env: &Env,
    ) -> Branches {
        let key = condition_flow_expression_key(self.src(), Some(value_node));
        if key.is_empty() {
            return (env.fork(), env.fork());
        }
        let class_name = if type_node.kind() == PHP_NAME {
            self.name_context_at(start(type_node))
                .resolve_class(&query::name_value(type_node))
        } else {
            compact(self.src().text(type_node)).into_owned()
        };
        let constraint = Type::named(&class_name, Vec::new());
        let (true_env, false_env) = (env.fork(), env.fork());
        let original = self.flow_value(&key, value_node, env);
        let true_value = self.relations.narrow(&original, &constraint);
        let false_value = self.without_instanceof_constraint(&original, &constraint);
        true_env.set(&key, true_value.clone());
        false_env.set(&key, false_value);
        self.record(value_node, &true_value, TypeSource::Flow, "instanceof");
        (true_env, false_env)
    }

    fn without_instanceof_constraint(&self, original: &Type, constraint: &Type) -> Type {
        if original.kind() == Kind::Union {
            let mut result = Type::NEVER;
            for member in original.arguments() {
                result = self.relations.join(
                    &result,
                    &self.without_instanceof_constraint(member, constraint),
                );
            }
            return result;
        }
        if original.kind() == Kind::Object
            && constraint.kind() == Kind::Object
            && equal_fold(trim_backslash(original.name()), "DateTimeInterface")
        {
            // DateTimeInterface is engine-only; excluding one implementation
            // leaves exactly the other.
            match go_to_lower(trim_backslash(constraint.name())).as_str() {
                "datetimeimmutable" => return Type::named("DateTime", Vec::new()),
                "datetime" => return Type::named("DateTimeImmutable", Vec::new()),
                _ => {}
            }
        }
        self.relations.without(original, constraint)
    }

    pub(crate) fn narrow_null(
        &mut self,
        value_node: &SyntaxNode,
        operator: &str,
        env: &Env,
    ) -> Branches {
        let key = condition_flow_expression_key(self.src(), Some(value_node));
        if key.is_empty() {
            return (env.fork(), env.fork());
        }
        let equal = operator == "===" || operator == "==";
        let (true_env, false_env) = (env.fork(), env.fork());
        let original = self.flow_value(&key, value_node, env);
        let relations = self.relations;
        let null_value = relations.narrow(&original, &Type::NULL);
        let non_null = relations.without(&original, &Type::NULL);
        let (true_value, false_value) = if equal {
            (null_value, non_null)
        } else {
            (non_null, null_value)
        };
        true_env.set(&key, true_value.clone());
        false_env.set(&key, false_value.clone());
        self.narrow_array_access_base(value_node, &true_env, &true_value, equal);
        self.narrow_array_access_base(value_node, &false_env, &false_value, !equal);
        let true_value = true_env.get(&key).unwrap_or_default();
        self.record(value_node, &true_value, TypeSource::Flow, "null comparison");
        (true_env, false_env)
    }

    pub(crate) fn narrow_array_access_base(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
        value: &Type,
        optional: bool,
    ) {
        let Some((base, path)) = self.array_access_path(Some(node), env) else {
            return;
        };
        let name = query::variable_key(&base);
        let Some(existing) = env.get(&name) else {
            return;
        };
        let updated = self.update_flow_array_path(&existing, &path, value, optional, false, 0);
        if updated.is_unknown() || updated == existing {
            return;
        }
        env.set(&name, updated.clone());
        self.record(
            &base,
            &updated,
            TypeSource::Flow,
            "array element null comparison",
        );
    }
}
