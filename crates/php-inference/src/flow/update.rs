//! `unset()` and flow-sensitive array field updates (port of
//! `flow_update.go`).

use swlsp_parser_php::query;
use swlsp_php_semantic::TypeSource;
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::expr::ArrayAccessSegment;
use crate::syntax::trim_quotes;
use crate::types_util::MAX_SPECIAL_TYPE_DEPTH;

impl Fs<'_, '_, '_> {
    pub(crate) fn apply_unset(&mut self, call: &SyntaxNode, env: &Env) {
        let count = query::arguments(call).count();
        for index in 0..count {
            let expression = query::argument_expression(call, index);
            let Some((base, path)) = self.array_access_path(expression.as_ref(), env) else {
                continue;
            };
            let name = query::variable_key(&base);
            let Some(existing) = env.get(&name) else {
                continue;
            };
            let updated =
                self.update_flow_array_path(&existing, &path, &Type::UNKNOWN, true, true, 0);
            if updated.is_unknown() || updated == existing {
                continue;
            }
            env.set(&name, updated.clone());
            self.record(&base, &updated, TypeSource::Assignment, "unset array field");
        }
    }

    pub(crate) fn update_flow_array_path(
        &self,
        existing: &Type,
        path: &[ArrayAccessSegment],
        value: &Type,
        optional: bool,
        remove: bool,
        depth: usize,
    ) -> Type {
        if path.is_empty() || depth >= MAX_SPECIAL_TYPE_DEPTH {
            return Type::UNKNOWN;
        }
        if path.len() == 1 {
            if !path[0].literal {
                return existing.clone();
            }
            return self.update_flow_array_field(
                existing,
                &path[0].field_name,
                value,
                optional,
                remove,
                depth + 1,
            );
        }
        if let Some(resolved) = self.snapshot.resolve_type_alias(existing)
            && !resolved.is_unknown()
            && resolved != *existing
        {
            return self.update_flow_array_path(
                &resolved,
                path,
                value,
                optional,
                remove,
                depth + 1,
            );
        }
        if existing.kind() == Kind::Union {
            let alternatives: Vec<Type> = existing
                .arguments()
                .iter()
                .map(|member| {
                    self.update_flow_array_path(member, path, value, optional, remove, depth + 1)
                })
                .filter(|updated| !updated.is_unknown())
                .collect();
            return Type::union(alternatives);
        }
        let head = &path[0];
        let rest = &path[1..];
        match existing.kind() {
            Kind::Array | Kind::NonEmptyArray => {
                if existing.argument_count() != 2 {
                    return Type::UNKNOWN;
                }
                let updated = self.update_flow_array_path(
                    &existing.argument(1),
                    rest,
                    value,
                    optional,
                    remove,
                    depth + 1,
                );
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                if existing.kind() == Kind::NonEmptyArray && !remove {
                    return Type::non_empty_array(existing.argument(0), updated);
                }
                Type::array(existing.argument(0), updated)
            }
            Kind::List | Kind::NonEmptyList => {
                if existing.argument_count() != 1 {
                    return Type::UNKNOWN;
                }
                let updated = self.update_flow_array_path(
                    &existing.argument(0),
                    rest,
                    value,
                    optional,
                    remove,
                    depth + 1,
                );
                if updated.is_unknown() {
                    return Type::UNKNOWN;
                }
                if existing.kind() == Kind::NonEmptyList && !remove {
                    return Type::non_empty_list(updated);
                }
                Type::list(updated)
            }
            Kind::ArrayShape => {
                let mut fields = existing.fields().to_vec();
                let mut matched = false;
                for field in &mut fields {
                    if head.literal && trim_quotes(&field.name) != head.field_name {
                        continue;
                    }
                    let updated = self.update_flow_array_path(
                        &field.ty,
                        rest,
                        value,
                        optional,
                        remove,
                        depth + 1,
                    );
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

    fn update_flow_array_field(
        &self,
        existing: &Type,
        field_name: &str,
        value: &Type,
        optional: bool,
        remove: bool,
        depth: usize,
    ) -> Type {
        if depth >= MAX_SPECIAL_TYPE_DEPTH {
            return Type::UNKNOWN;
        }
        if let Some(resolved) = self.snapshot.resolve_type_alias(existing)
            && !resolved.is_unknown()
            && resolved != *existing
        {
            return self.update_flow_array_field(
                &resolved,
                field_name,
                value,
                optional,
                remove,
                depth + 1,
            );
        }
        if existing.kind() == Kind::Union {
            let alternatives: Vec<Type> = existing
                .arguments()
                .iter()
                .map(|member| {
                    self.update_flow_array_field(
                        member,
                        field_name,
                        value,
                        optional,
                        remove,
                        depth + 1,
                    )
                })
                .filter(|updated| !updated.is_unknown())
                .collect();
            return Type::union(alternatives);
        }
        if existing.kind() != Kind::ArrayShape {
            return existing.clone();
        }
        let mut fields = Vec::with_capacity(existing.field_count());
        let mut found = false;
        for field in existing.fields() {
            if trim_quotes(&field.name) != field_name {
                fields.push(field.clone());
                continue;
            }
            found = true;
            if remove {
                continue;
            }
            if optional
                && value.kind() == Kind::Null
                && field.optional
                && self.relations.without(&field.ty, &Type::NULL) == field.ty
            {
                // Keep absence in the optional bit instead of inventing null.
                continue;
            }
            if value.kind() == Kind::Never {
                if field.optional {
                    continue;
                }
                return Type::UNKNOWN;
            }
            let mut field = field.clone();
            field.ty = value.clone();
            field.optional = optional;
            fields.push(field);
        }
        if !found {
            if !optional && !remove {
                return Type::UNKNOWN;
            }
            return existing.clone();
        }
        Type::array_shape(fields, existing.is_open_shape())
    }
}
