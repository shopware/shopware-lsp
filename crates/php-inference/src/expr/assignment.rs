//! Assignments, destructuring and array element updates.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Kind, ShapeField, Type};
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::flow::boolean_alias_prefix;
use crate::syntax::{direct_nodes, direct_operator, flow_expression_key, start, trim_quotes};
use crate::types_util::{
    MAX_INFERRED_TUPLE_FIELDS, MAX_SPECIAL_TYPE_DEPTH, array_literal_key, array_shape_is_list,
    array_shape_iterable_types,
};

/// One `[key]` segment of an array access path.
#[derive(Clone)]
pub(crate) struct ArrayAccessSegment {
    pub(crate) key: Type,
    pub(crate) field_name: String,
    pub(crate) literal: bool,
    pub(crate) append: bool,
}

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_assignment(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let nodes = direct_nodes(node);
        if nodes.len() < 2 {
            return Type::UNKNOWN;
        }
        let right = &nodes[nodes.len() - 1];
        let mut value = self.infer(right, env);
        let left = &nodes[0];
        if direct_operator(node) == "??=" {
            let existing = self.infer(left, env);
            let non_null = self.relations.without(&existing, &Type::NULL);
            value = self.relations.join(&non_null, &value);
        }
        self.assign_target(Some(left), &value, Some(right), env);
        value
    }

    pub(crate) fn assign_target(
        &mut self,
        left: Option<&SyntaxNode>,
        value: &Type,
        source: Option<&SyntaxNode>,
        env: &Env,
    ) {
        let Some(left) = left else {
            return;
        };
        if left.kind() == PHP_VARIABLE {
            let name = query::variable_key(left);
            env.delete_prefix(&boolean_alias_prefix(&name));
            env.set(&name, value.clone());
            self.record(left, value, TypeSource::Assignment, "assignment");
            self.update_local_type(&name, start(left), value);
            if matches!(value.kind(), Kind::Bool | Kind::True | Kind::False) {
                self.capture_boolean_alias(&name, source, env);
            }
        } else if left.kind() == PHP_ARRAY {
            self.assign_destructured(left, value, env);
        } else if left.kind() == PHP_ARRAY_ACCESS {
            self.infer(left, env);
            self.update_array_assignment(left, value, env);
            let key = flow_expression_key(self.src(), Some(left));
            if !key.is_empty() {
                env.set(&key, value.clone());
                self.record(left, value, TypeSource::Assignment, "assignment");
            }
        } else {
            let key = flow_expression_key(self.src(), Some(left));
            self.infer(left, env);
            if !key.is_empty() {
                self.record_readonly_property_assignment(left, value);
                env.set(&key, value.clone());
                self.record(left, value, TypeSource::Assignment, "assignment");
            }
        }
    }

    fn assign_destructured(&mut self, left: &SyntaxNode, value: &Type, env: &Env) {
        for (position, item) in left.children().enumerate() {
            if item.kind() != PHP_ARRAY_ITEM {
                continue;
            }
            let parts = direct_nodes(&item);
            if parts.is_empty() {
                continue;
            }
            let mut key = Type::literal_int(&position.to_string());
            let target = &parts[parts.len() - 1];
            if parts.len() > 1 {
                key = self.infer(&parts[0], env);
            }
            let element = self.array_access_type(value, &key);
            if element.is_unknown() {
                continue;
            }
            self.assign_target(Some(target), &element, None, env);
        }
    }

    fn update_array_assignment(&mut self, left: &SyntaxNode, value: &Type, env: &Env) {
        let Some((base, path)) = self.array_access_path(Some(left), env) else {
            return;
        };
        let name = query::variable_key(&base);
        let existing = match env.get(&name) {
            Some(existing) => existing,
            None => self.infer(&base, env),
        };
        let updated = self.array_path_assignment_type(&existing, &path, value, 0);
        if updated.is_unknown() {
            return;
        }
        env.set(&name, updated.clone());
        self.record(&base, &updated, TypeSource::Assignment, "array assignment");
        self.update_local_type(&name, start(&base), &updated);
    }

    /// The base variable and key segments of `$base[a][b]...`.
    pub(crate) fn array_access_path(
        &mut self,
        node: Option<&SyntaxNode>,
        env: &Env,
    ) -> Option<(SyntaxNode, Vec<ArrayAccessSegment>)> {
        let mut reversed = Vec::new();
        let mut current = node.cloned();
        while let Some(node) = current
            .as_ref()
            .filter(|node| node.kind() == PHP_ARRAY_ACCESS)
        {
            let nodes = direct_nodes(node);
            if nodes.is_empty() {
                return None;
            }
            let mut segment = ArrayAccessSegment {
                key: Type::INT,
                field_name: String::new(),
                literal: false,
                append: nodes.len() == 1,
            };
            if !segment.append {
                let key_node = &nodes[nodes.len() - 1];
                segment.key = self.infer(key_node, env);
                if let Some(name) = self.array_field_name(key_node, env) {
                    segment.field_name = name;
                    segment.literal = true;
                    // Foo::class is a single literal array key.
                    segment.key = Type::literal_string(&segment.field_name);
                }
            }
            reversed.push(segment);
            current = Some(nodes[0].clone());
        }
        let base = current.filter(|node| node.kind() == PHP_VARIABLE)?;
        if reversed.is_empty() {
            return None;
        }
        reversed.reverse();
        Some((base, reversed))
    }

    fn array_path_assignment_type(
        &self,
        existing: &Type,
        path: &[ArrayAccessSegment],
        value: &Type,
        depth: usize,
    ) -> Type {
        if path.is_empty() || depth >= MAX_SPECIAL_TYPE_DEPTH {
            return Type::UNKNOWN;
        }
        if path.len() == 1 {
            return self.array_assignment_type(
                existing,
                &path[0].key,
                value,
                path[0].append,
                depth + 1,
            );
        }
        if let Some(resolved) = self.snapshot.resolve_type_alias(existing)
            && !resolved.is_unknown()
            && resolved != *existing
        {
            return self.array_path_assignment_type(&resolved, path, value, depth + 1);
        }
        let relations = self.relations;
        if existing.kind() == Kind::Union {
            let mut alternatives = Vec::with_capacity(existing.argument_count());
            for member in existing.arguments() {
                let updated = self.array_path_assignment_type(member, path, value, depth + 1);
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                alternatives.push(updated);
            }
            return Type::union(alternatives);
        }
        let head = &path[0];
        match existing.kind() {
            Kind::Array | Kind::NonEmptyArray => {
                if existing.argument_count() != 2 {
                    return Type::UNKNOWN;
                }
                if existing.argument(1).kind() == Kind::Never {
                    let updated = self.array_path_assignment_type(
                        &Type::array(Type::ARRAY_KEY, Type::NEVER),
                        &path[1..],
                        value,
                        depth + 1,
                    );
                    if updated.is_unknown() {
                        return Type::UNKNOWN;
                    }
                    if head.append {
                        return Type::non_empty_list(updated);
                    }
                    return Type::non_empty_array(head.key.clone(), updated);
                }
                let updated = self.array_path_assignment_type(
                    &existing.argument(1),
                    &path[1..],
                    value,
                    depth + 1,
                );
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                Type::non_empty_array(relations.join(&existing.argument(0), &head.key), updated)
            }
            Kind::List | Kind::NonEmptyList => {
                if existing.argument_count() != 1 {
                    return Type::UNKNOWN;
                }
                let updated = self.array_path_assignment_type(
                    &existing.argument(0),
                    &path[1..],
                    value,
                    depth + 1,
                );
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                if existing.kind() == Kind::NonEmptyList {
                    Type::non_empty_list(updated)
                } else {
                    Type::list(updated)
                }
            }
            Kind::ArrayShape => {
                let mut fields = existing.fields().to_vec();
                let mut matched = false;
                for field in &mut fields {
                    if head.literal && trim_quotes(&field.name) != head.field_name {
                        continue;
                    }
                    let updated =
                        self.array_path_assignment_type(&field.ty, &path[1..], value, depth + 1);
                    if updated.is_unknown() {
                        continue;
                    }
                    field.ty = updated;
                    matched = true;
                }
                if matched {
                    return Type::array_shape(fields, existing.is_open_shape());
                }
                Type::UNKNOWN
            }
            _ => Type::UNKNOWN,
        }
    }

    fn array_assignment_type(
        &self,
        existing: &Type,
        key: &Type,
        value: &Type,
        append_value: bool,
        depth: usize,
    ) -> Type {
        if depth >= MAX_SPECIAL_TYPE_DEPTH {
            return Type::UNKNOWN;
        }
        if let Some(resolved) = self.snapshot.resolve_type_alias(existing)
            && !resolved.is_unknown()
            && resolved != *existing
        {
            return self.array_assignment_type(&resolved, key, value, append_value, depth + 1);
        }
        let relations = self.relations;
        if existing.kind() == Kind::Union {
            let mut alternatives = Vec::with_capacity(existing.argument_count());
            for member in existing.arguments() {
                let updated =
                    self.array_assignment_type(member, key, value, append_value, depth + 1);
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                alternatives.push(updated);
            }
            return Type::union(alternatives);
        }
        match existing.kind() {
            Kind::Array | Kind::NonEmptyArray => {
                if existing.argument_count() != 2 {
                    return Type::UNKNOWN;
                }
                let key_type = existing.argument(0);
                let value_type = existing.argument(1);
                if value_type.kind() == Kind::Never {
                    if append_value {
                        Type::non_empty_list(value.clone())
                    } else if let Some(field_name) = array_literal_key(key) {
                        Type::array_shape(vec![ShapeField::new(field_name, value.clone())], false)
                    } else {
                        Type::non_empty_array(key.clone(), value.clone())
                    }
                } else {
                    Type::non_empty_array(
                        relations.join(&key_type, key),
                        relations.join(&value_type, value),
                    )
                }
            }
            Kind::List | Kind::NonEmptyList => {
                if existing.argument_count() != 1 {
                    return Type::UNKNOWN;
                }
                let element = existing.argument(0);
                if append_value {
                    Type::non_empty_list(relations.join(&element, value))
                } else {
                    Type::non_empty_array(
                        relations.join(&Type::INT, key),
                        relations.join(&element, value),
                    )
                }
            }
            Kind::ArrayShape => {
                if !append_value && let Some(field_name) = array_literal_key(key) {
                    let mut fields = Vec::with_capacity(existing.field_count() + 1);
                    let mut replaced = false;
                    for field in existing.fields() {
                        let mut field = field.clone();
                        if trim_quotes(&field.name) == field_name {
                            field.ty = value.clone();
                            field.optional = false;
                            replaced = true;
                        }
                        fields.push(field);
                    }
                    if replaced || fields.len() < MAX_INFERRED_TUPLE_FIELDS {
                        if !replaced {
                            fields.push(ShapeField::new(field_name, value.clone()));
                        }
                        return Type::array_shape(fields, false);
                    }
                }
                let (key_type, element_type) = array_shape_iterable_types(existing, relations);
                if append_value && array_shape_is_list(existing) {
                    Type::non_empty_list(relations.join(&element_type, value))
                } else {
                    Type::array(
                        relations.join(&key_type, key),
                        relations.join(&element_type, value),
                    )
                }
            }
            _ => Type::array(key.clone(), value.clone()),
        }
    }
}
