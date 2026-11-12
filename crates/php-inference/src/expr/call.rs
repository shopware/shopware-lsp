//! Function, method and static calls.

use rustc_hash::FxHashSet;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::resolve_signature;
use swlsp_php_semantic::{Flags, Symbol};
use swlsp_php_types::{CallableParameter, Kind, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::analyzer::Fs;
use crate::env::Env;
use crate::extensions::CallArgument;
use crate::syntax::{
    direct_nodes, has_direct_token, last_node, start, trim_backslash, trim_quotes,
};
use crate::types_util::{
    array_shape_is_list, callable_result, dynamic_object_type, iterable_types, join_types,
    regex_callback_matches_type, resolve_special_type,
};

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_call(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
        is_static: bool,
        function: bool,
    ) -> Type {
        let nodes = direct_nodes(node);
        if nodes.is_empty() {
            return Type::UNKNOWN;
        }
        if is_first_class_callable(node) {
            return self.infer_first_class_callable(node, &nodes, env, is_static, function);
        }
        let (arguments, uncertain_unpack) = self.infer_arguments(node, env);
        if function {
            let callee = &nodes[0];
            if callee.kind() != PHP_NAME {
                let callable = self.infer(callee, env);
                return callable_result(&callable).unwrap_or_default();
            }
            let name = query::name_value(callee);
            if let Some(extension) =
                self.extension_type(node, &name, &Type::UNKNOWN, &arguments, false)
            {
                return extension;
            }
            let context = self.name_context_at(start(node));
            let snapshot = self.snapshot;
            let relations = self.relations;
            let mut results = Vec::new();
            let mut inferred_results = Vec::new();
            let mut generated_fallbacks = Vec::new();
            let mut mismatches = Vec::new();
            let mut candidate_count = 0usize;
            context.visit_function_names(&name, |candidate_name| {
                snapshot.visit_function_views(candidate_name, |candidate| {
                    candidate_count += 1;
                    let symbol = candidate.materialize();
                    let resolved = resolve_signature(relations, symbol.clone(), &arguments);
                    if resolved.compatible {
                        self.apply_by_reference_arguments(node, &symbol, &arguments, env);
                        self.record_return_dependency(&symbol);
                        if symbol.flags.has(Flags::INFERRED_RETURN) {
                            inferred_results.push(resolved.return_type);
                        } else {
                            results.push(resolved.return_type);
                        }
                    } else if symbol.flags.has(Flags::GENERATED_STUB) {
                        generated_fallbacks.push(resolved.return_type);
                    } else {
                        mismatches.push(resolved.mismatch);
                    }
                    true
                });
                results.is_empty() && inferred_results.is_empty()
            });
            // Several declarations of one function are conditional
            // alternatives (polyfills behind `function_exists`); the ones
            // that declare their return type outweigh body-inferred ones.
            if results.is_empty() {
                results = inferred_results;
            }
            if results.is_empty() && !generated_fallbacks.is_empty() {
                return join_types(relations, &generated_fallbacks);
            }
            if candidate_count > 0 && results.is_empty() && !uncertain_unpack {
                self.report(
                    node,
                    "php.arguments",
                    no_matching_signature_message(&format!("{name}()"), &mismatches),
                );
            }
            if candidate_count == 0 {
                // An unknown function (an extension that is not loaded) may
                // write to any variable it is passed.
                self.declare_unknown_call_outputs(node, env);
            }
            return join_types(relations, &results);
        }

        if nodes.len() < 2 {
            return Type::UNKNOWN;
        }
        let receiver = self.infer_receiver(&nodes[0], env, is_static);
        let name = query::name_value(&nodes[1]);
        if is_static && nodes[0].kind() == PHP_OBJECT_CREATION && name.starts_with('$') {
            let property_types = self.members().property_types(&receiver, &name);
            return dynamic_object_type(&self.join_members(&property_types, &receiver));
        }
        let mut late_static_receiver = receiver.clone();
        if is_static
            && nodes[0].kind() == PHP_NAME
            && self.current_class.kind() == Kind::Object
            && matches!(
                lowercase(&query::name_value(&nodes[0])).as_str(),
                "self" | "static" | "parent"
            )
        {
            late_static_receiver = self.current_class.clone();
        }
        if let Some(extension) = self.extension_type(node, &name, &receiver, &arguments, is_static)
        {
            return extension;
        }
        let relations = self.relations;
        let mut has_members = false;
        let mut results = Vec::new();
        let mut generated_fallbacks = Vec::new();
        let mut mismatches = Vec::new();
        let members = self.members();
        members.visit_methods(&receiver, &name, |member| {
            has_members = true;
            let self_type = self.member_self_type(&member.symbol, &receiver);
            let symbol = resolve_member_special_types(
                member.symbol.clone(),
                &late_static_receiver,
                &self_type,
            );
            let resolved = resolve_signature(relations, symbol.clone(), &arguments);
            if resolved.compatible {
                self.apply_by_reference_arguments(node, &symbol, &arguments, env);
                self.record_return_dependency(&member.symbol);
                results.push(resolved.return_type);
            } else if member.symbol.flags.has(Flags::GENERATED_STUB) {
                generated_fallbacks.push(resolved.return_type);
            } else {
                mismatches.push(resolved.mismatch);
            }
            true
        });
        if results.is_empty() && !generated_fallbacks.is_empty() {
            results = generated_fallbacks;
        }
        if has_members && results.is_empty() && !uncertain_unpack {
            self.report(
                node,
                "php.arguments",
                no_matching_signature_message(&format!("{name}()"), &mismatches),
            );
        }
        let mut result = join_types(relations, &results);
        if has_direct_token(node, TK_NULLSAFE_OBJECT_OPERATOR) {
            result = Type::nullable(result);
        }
        result
    }

    fn infer_first_class_callable(
        &mut self,
        node: &SyntaxNode,
        nodes: &[SyntaxNode],
        env: &Env,
        is_static: bool,
        function: bool,
    ) -> Type {
        let mut callables = Vec::new();
        let relations = self.relations;
        if function {
            let callee = &nodes[0];
            if callee.kind() != PHP_NAME {
                let value = self.infer(callee, env);
                if value.kind() == Kind::Callable {
                    return value;
                }
                return Type::callable(Vec::new(), Type::MIXED);
            }
            let name = query::name_value(callee);
            let context = self.name_context_at(start(node));
            let snapshot = self.snapshot;
            context.visit_function_names(&name, |candidate_name| {
                snapshot.visit_function_views(candidate_name, |candidate| {
                    callables.push(callable_from_symbol(&candidate.materialize()));
                    true
                });
                callables.is_empty()
            });
            return join_types(relations, &callables);
        }
        if nodes.len() < 2 {
            return Type::callable(Vec::new(), Type::MIXED);
        }
        let receiver = self.infer_receiver(&nodes[0], env, is_static);
        let name = query::name_value(&nodes[1]);
        let members = self.members();
        members.visit_methods(&receiver, &name, |member| {
            let self_type = self.member_self_type(&member.symbol, &receiver);
            let symbol = resolve_member_special_types(member.symbol, &receiver, &self_type);
            callables.push(callable_from_symbol(&symbol));
            true
        });
        join_types(relations, &callables)
    }

    /// The declaring class type of a member, projected for `receiver`.
    pub(crate) fn member_self_type(&mut self, member: &Symbol, receiver: &Type) -> Type {
        let mut container = member.container.clone();
        while !container.is_empty() {
            let Some(symbol) = self.snapshot.symbol(&container) else {
                break;
            };
            if symbol.is_class_like() {
                if symbol.kind == swlsp_php_semantic::SymbolKind::Trait {
                    return receiver.clone();
                }
                if receiver.kind() == Kind::Object {
                    if eq_ignore_case(
                        trim_backslash(receiver.name()),
                        trim_backslash(&symbol.fully_qualified),
                    ) {
                        return receiver.clone();
                    }
                    if let Some(hierarchy) = self.relations.hierarchy
                        && let Some(projected) =
                            hierarchy.as_supertype(receiver, &symbol.fully_qualified)
                    {
                        return projected;
                    }
                }
                return self.named_type(&symbol.fully_qualified);
            }
            container = symbol.container.clone();
        }
        receiver.clone()
    }

    pub(crate) fn record_return_dependency(&mut self, symbol: &Symbol) {
        if !symbol.return_type.is_unknown() || symbol.path != self.st.document.path {
            return;
        }
        if !self.st.function_indices.contains_key(&symbol.id) {
            return;
        }
        self.dependencies
            .get_or_insert_with(FxHashSet::default)
            .insert(symbol.id.clone());
    }

    pub(crate) fn infer_arguments(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
    ) -> (Vec<CallArgument>, bool) {
        let arguments: Vec<SyntaxNode> = query::arguments(node).collect();
        let mut result = Vec::with_capacity(arguments.len());
        let mut uncertain_unpack = false;
        for (index, argument) in arguments.iter().enumerate() {
            let expression = last_node(argument);
            let mut value = Type::UNKNOWN;
            let parameter_types = self.contextual_callback_parameter_types(node, index, env);
            if !parameter_types.is_empty()
                && let Some(expression) = &expression
            {
                match expression.kind() {
                    PHP_ARROW_FUNCTION => {
                        value = self.infer_arrow_function(expression, env, &parameter_types);
                    }
                    PHP_CLOSURE => {
                        value = self.infer_closure(expression, env, &parameter_types);
                    }
                    _ => {}
                }
            }
            if value.is_unknown() {
                value = self.infer_opt(expression.as_ref(), env);
            }
            if has_direct_token(argument, TK_ELLIPSIS) {
                if let Some(expanded) = positional_tuple_arguments(&value) {
                    result.extend(expanded);
                    continue;
                }
                let (_, element) = iterable_types(&value, self.relations);
                if !element.is_unknown() {
                    value = element;
                }
                uncertain_unpack = true;
            }
            result.push(CallArgument {
                name: query::argument_name(argument),
                ty: value,
                expression: self.src().opt_text(expression.as_ref()).to_owned(),
            });
        }
        (result, uncertain_unpack)
    }

    fn contextual_callback_parameter_types(
        &mut self,
        call: &SyntaxNode,
        argument_index: usize,
        env: &Env,
    ) -> Vec<Type> {
        if call.kind() != PHP_FUNCTION_CALL {
            return Vec::new();
        }
        let name = lowercase(trim_backslash(&query::call_method_name(call)));
        match name.as_str() {
            "preg_replace_callback" if argument_index == 1 => vec![regex_callback_matches_type()],
            "array_map" => {
                if argument_index != 0 {
                    return Vec::new();
                }
                let count = query::arguments(call).count();
                let mut parameter_types = Vec::with_capacity(count.saturating_sub(1));
                for index in 1..count {
                    let source = query::argument_expression(call, index);
                    let value = self.infer_opt(source.as_ref(), env);
                    let (_, element) = iterable_types(&value, self.relations);
                    if element.is_unknown() {
                        return Vec::new();
                    }
                    parameter_types.push(element);
                }
                parameter_types
            }
            "array_filter" if argument_index == 1 => {
                let source = query::argument_expression(call, 0);
                let value = self.infer_opt(source.as_ref(), env);
                let (key, element) = iterable_types(&value, self.relations);
                // The mode decides what the callback receives.
                let mode = query::argument_expression(call, 2)
                    .map(|mode| trim_backslash(self.src().text(&mode).trim()).to_owned());
                let types = match mode.as_deref() {
                    Some("ARRAY_FILTER_USE_KEY") => vec![key],
                    Some("ARRAY_FILTER_USE_BOTH") => vec![element, key],
                    None => vec![element],
                    Some(_) => Vec::new(),
                };
                if types.iter().any(Type::is_unknown) {
                    Vec::new()
                } else {
                    types
                }
            }
            _ => Vec::new(),
        }
    }

    pub(crate) fn infer_receiver(&mut self, node: &SyntaxNode, env: &Env, is_static: bool) -> Type {
        if is_static && node.kind() == PHP_NAME {
            let name = query::name_value(node);
            return match lowercase(&name).as_str() {
                "self" | "static" => self.current_class.clone(),
                "parent" => {
                    if self.current_class.kind() == Kind::Object {
                        let mut parent = None;
                        let snapshot = self.snapshot;
                        snapshot.visit_class_views(self.current_class.name(), |class| {
                            let class = class.materialize();
                            if let Some(first) = class.extends().first() {
                                parent = Some(first.to_string());
                            }
                            false
                        });
                        if let Some(parent) = parent {
                            return self.named_type(&parent);
                        }
                    }
                    Type::UNKNOWN
                }
                _ => {
                    let resolved = self.name_context_at(start(node)).resolve_class(&name);
                    self.named_type(&resolved)
                }
            };
        }
        self.infer(node, env)
    }
}

pub(crate) fn no_matching_signature_message(callable: &str, mismatches: &[String]) -> String {
    let mut message = format!("No matching signature for {}", trim_backslash(callable));
    if mismatches.len() == 1 && !mismatches[0].is_empty() {
        message.push_str(": ");
        message.push_str(&mismatches[0]);
    }
    message
}

pub(crate) fn is_first_class_callable(node: &SyntaxNode) -> bool {
    let mut arguments = query::arguments(node);
    let Some(argument) = arguments.next() else {
        return false;
    };
    arguments.next().is_none()
        && has_direct_token(&argument, TK_ELLIPSIS)
        && last_node(&argument).is_none()
}

pub(crate) fn resolve_member_special_types(
    mut symbol: Symbol,
    receiver: &Type,
    self_type: &Type,
) -> Symbol {
    symbol.return_type = resolve_special_type(&symbol.return_type, receiver, self_type);
    for parameter in &mut symbol.parameters {
        parameter.ty = resolve_special_type(&parameter.ty, receiver, self_type);
        parameter.native_type = resolve_special_type(&parameter.native_type, receiver, self_type);
        parameter.doc_type = resolve_special_type(&parameter.doc_type, receiver, self_type);
    }
    symbol
}

pub(crate) fn callable_from_symbol(symbol: &Symbol) -> Type {
    let parameters = symbol
        .parameters
        .iter()
        .map(|parameter| CallableParameter {
            name: parameter.name.as_str().into(),
            ty: parameter.ty.clone(),
            optional: parameter.optional,
            variadic: parameter.flags.has(Flags::VARIADIC),
            by_reference: parameter.flags.has(Flags::BY_REFERENCE),
        })
        .collect();
    let result = if symbol.return_type.is_unknown() {
        Type::MIXED
    } else {
        symbol.return_type.clone()
    };
    Type::callable(parameters, result)
}

fn positional_tuple_arguments(value: &Type) -> Option<Vec<CallArgument>> {
    if value.kind() != Kind::ArrayShape || !array_shape_is_list(value) {
        return None;
    }
    let fields = value.fields();
    let mut result = Vec::with_capacity(fields.len());
    for index in 0..fields.len() {
        let expected = index.to_string();
        let field = fields
            .iter()
            .find(|field| trim_quotes(&field.name) == expected)?;
        result.push(CallArgument::of(field.ty.clone()));
    }
    Some(result)
}
