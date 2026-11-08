//! Object creation and member access (port of `expression_object.go`).

use rustc_hash::FxHashMap;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::{TemplateBindings, resolve_signature};
use swlsp_php_semantic::{Flags, Str, Symbol, SymbolKind, Visibility, anonymous_class_name};
use swlsp_php_types::{Kind, Type, equal_fold, go_to_lower};
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{direct_nodes, start, trim_backslash, trimmed_start};
use crate::types_util::{dynamic_object_type, join_types};

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_object_creation(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let Some(name_node) = query::direct_child(node, PHP_NAME) else {
            if let Some(anonymous) = query::direct_child(node, PHP_ANONYMOUS_CLASS) {
                return Type::named(
                    &anonymous_class_name(&self.st.document.path, trimmed_start(&anonymous)),
                    Vec::new(),
                );
            }
            return Type::OBJECT;
        };
        let context = self.name_context_at(start(node));
        let name = query::name_value(&name_node);
        if name.starts_with('$') {
            return match env.get(&name) {
                Some(value) => dynamic_object_type(&value),
                None => Type::OBJECT,
            };
        }
        let mut class_name = context.resolve_class(&name);
        let mut preserve_current_templates = false;
        match go_to_lower(trim_backslash(&name)).as_str() {
            "self" | "static" => {
                if self.current_class.kind() == Kind::Object {
                    class_name = self.current_class.name().to_owned();
                    preserve_current_templates = true;
                }
            }
            "parent" if self.current_class.kind() == Kind::Object => {
                self.snapshot
                    .visit_class_views(self.current_class.name(), |class| {
                        let class = class.materialize();
                        if let Some(first) = class.extends().first() {
                            class_name = first.to_string();
                        }
                        false
                    });
            }
            _ => {}
        }
        let result = self.named_type(&class_name);
        let (arguments, uncertain_unpack) = self.infer_arguments(node, env);

        let relations = self.relations;
        let snapshot = self.snapshot;
        let members = self.members();
        let mut inferred = Vec::new();
        let mut has_constructor = false;
        let mut permissive_constructor = false;
        let mut found_class = false;
        snapshot.visit_class_views(&class_name, |class_view| {
            found_class = true;
            let class = class_view.materialize();
            let current_templates = map_current_class_templates(
                &class,
                &self.current_class,
                preserve_current_templates,
            );
            let class_templates = class.templates();
            let constructor_receiver = if class_templates.is_empty() {
                self.named_type(&class.fully_qualified)
            } else {
                Type::named(
                    &class.fully_qualified,
                    class_templates
                        .iter()
                        .map(|template| Type::template(&template.name))
                        .collect(),
                )
            };
            let mut found_constructor = false;
            members.visit_methods(&constructor_receiver, "__construct", |constructor| {
                found_constructor = true;
                has_constructor = true;
                let resolved = resolve_signature(relations, constructor.symbol.clone(), &arguments);
                if resolved.compatible {
                    self.apply_by_reference_arguments(node, &constructor.symbol, &arguments, env);
                    let templates = merge_missing_templates(resolved.templates, &current_templates);
                    inferred.push(self.generic_object_type(&class, &templates));
                } else if constructor.symbol.flags.has(Flags::GENERATED_STUB) {
                    permissive_constructor = true;
                }
                true
            });
            if !found_constructor && arguments.is_empty() {
                inferred.push(self.generic_object_type(&class, &current_templates));
            }
            true
        });
        if !found_class {
            return result;
        }
        if !inferred.is_empty() {
            return join_types(relations, &inferred);
        }
        if permissive_constructor {
            return result;
        }
        if !uncertain_unpack && (has_constructor || !arguments.is_empty()) {
            self.report(
                node,
                "php.arguments",
                format!("No matching constructor for {class_name}"),
            );
        }
        result
    }

    fn generic_object_type(&mut self, class: &Symbol, inferred: &TemplateBindings) -> Type {
        let class_templates = class.templates();
        if class_templates.is_empty() {
            return self.named_type(&class.fully_qualified);
        }
        let mut arguments = Vec::with_capacity(class_templates.len());
        for template in class_templates {
            let value = inferred
                .get(&template.name)
                .cloned()
                .unwrap_or_else(|| template.default.clone());
            if value.is_unknown() {
                return self.named_type(&class.fully_qualified);
            }
            arguments.push(value);
        }
        Type::named(&class.fully_qualified, arguments)
    }

    pub(crate) fn infer_member(&mut self, node: &SyntaxNode, env: &Env, is_static: bool) -> Type {
        let nodes = direct_nodes(node);
        if nodes.len() < 2 {
            return Type::UNKNOWN;
        }
        let receiver = self.infer_receiver(&nodes[0], env, is_static);
        let name = query::name_value(&nodes[nodes.len() - 1]);
        if is_static && equal_fold(&name, "class") {
            return Type::class_string(receiver);
        }
        let members = self.members();
        let values = if is_static {
            if name.starts_with('$') {
                members.property_types(&receiver, &name)
            } else {
                members.constant_types(&receiver, &name)
            }
        } else {
            let values = members.property_types(&receiver, &name);
            if let Some(refined) = self.readonly_property_type(&receiver, &name) {
                return refined;
            }
            values
        };
        self.join_members(&values, &receiver)
    }

    pub(crate) fn record_readonly_property_assignment(
        &mut self,
        target: &SyntaxNode,
        value: &Type,
    ) {
        if target.kind() != PHP_MEMBER_ACCESS
            || !equal_fold(&self.symbol.name, "__construct")
            || value.is_unknown()
        {
            return;
        }
        let nodes = direct_nodes(target);
        if nodes.len() < 2
            || nodes[0].kind() != PHP_VARIABLE
            || query::variable_key(&nodes[0]) != "$this"
        {
            return;
        }
        let name = query::name_value(&nodes[nodes.len() - 1]);
        let relations = self.relations;
        for id in self.members().property_ids(&self.current_class, &name) {
            let Some(property) = self.snapshot.symbol(&id) else {
                continue;
            };
            if property.kind != SymbolKind::Property
                || property.visibility != Visibility::Private
                || !property.flags.has(Flags::READONLY)
                || !relations.is_assignable_to(value, &property.ty)
            {
                continue;
            }
            let entry = self.st.readonly_property_types.entry(id);
            entry
                .and_modify(|existing| *existing = relations.join(existing, value))
                .or_insert_with(|| value.clone());
        }
    }

    fn readonly_property_type(&self, receiver: &Type, name: &str) -> Option<Type> {
        let ids = self.members().property_ids(receiver, name);
        if ids.is_empty() {
            return None;
        }
        let mut values = Vec::with_capacity(ids.len());
        for id in ids {
            values.push(self.st.readonly_property_types.get(&id)?.clone());
        }
        Some(join_types(self.relations, &values))
    }
}

fn map_current_class_templates(class: &Symbol, current: &Type, enabled: bool) -> TemplateBindings {
    let mut result = TemplateBindings::default();
    if !enabled
        || current.kind() != Kind::Object
        || !equal_fold(&class.fully_qualified, current.name())
    {
        return result;
    }
    for (template, value) in class.templates().iter().zip(current.arguments()) {
        result.insert(template.name.clone(), value.clone());
    }
    result
}

fn merge_missing_templates(
    inferred: TemplateBindings,
    fallback: &TemplateBindings,
) -> TemplateBindings {
    if fallback.is_empty() {
        return inferred;
    }
    let mut result: FxHashMap<Str, Type> = fallback.clone();
    result.extend(inferred);
    result
}
