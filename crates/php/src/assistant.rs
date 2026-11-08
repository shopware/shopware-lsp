//! PHPDoc assistant tags on resolved call parameters (port of
//! `assistant.go`), such as `#Route` or `#Service`.

use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_NAME, PHP_OBJECT_CREATION, PHP_SCOPED_CALL,
};
use swlsp_php_resolver::{MemberResolver, NameContext};
use swlsp_php_semantic::{Document, Flags, Parameter, Snapshot, Symbol, SymbolKind};
use swlsp_php_types::{Type, equal_fold, go_to_lower};
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::PhpContext;

/// The content range of a direct string argument whose resolved function,
/// constructor or method parameter carries `tag` (including tags declared on
/// inherited interfaces, parents and traits).
pub fn assistant_argument_reference(
    context: &PhpContext,
    node: &SyntaxNode,
    tag: &str,
) -> Option<TextRange> {
    assistant_argument_tags(context, node, &[tag]).map(|(range, _)| range)
}

/// The content range of a direct string call argument and the requested
/// tags its resolved parameter carries. `None` when no tag matches.
pub fn assistant_argument_tags(
    context: &PhpContext,
    node: &SyntaxNode,
    tags: &[&str],
) -> Option<(TextRange, Vec<String>)> {
    if tags.is_empty() {
        return None;
    }
    let literal = query::string_at(node)?;
    let (container, candidates) = call_container(context, &literal)?;
    if candidates.is_empty() {
        return None;
    }
    let argument_index = query::argument_index(&container, &literal)?;
    if query::argument_expression(&container, argument_index).as_ref() != Some(&literal) {
        return None;
    }
    let argument_name = query::argument_name(&literal);
    let mut seen = HashSet::with_capacity(tags.len());
    let mut matched = Vec::new();
    for candidate in &candidates {
        let Some(parameter_index) =
            parameter_index(&candidate.parameters, argument_index, &argument_name)
        else {
            continue;
        };
        for tag in tags {
            let key = go_to_lower(tag.trim());
            if key.is_empty() || seen.contains(&key) {
                continue;
            }
            if parameter_has_tag(
                &context.snapshot,
                candidate,
                parameter_index,
                tag,
                &mut HashSet::new(),
            ) {
                seen.insert(key);
                matched.push((*tag).to_owned());
            }
        }
    }
    if matched.is_empty() {
        return None;
    }
    Some((query::string_content_range(&literal), matched))
}

/// The direct string value passed to a parameter carrying `tag` on the same
/// resolved call, e.g. the `#TranslationDomain` sibling of a
/// `#TranslationKey` argument.
pub fn assistant_sibling_string_argument(
    context: &PhpContext,
    node: &SyntaxNode,
    tag: &str,
) -> Option<String> {
    if tag.is_empty() {
        return None;
    }
    let literal = query::string_at(node)?;
    let (container, candidates) = call_container(context, &literal)?;
    for candidate in &candidates {
        for (argument_index, argument) in query::arguments(&container).enumerate() {
            let Some(parameter_index) = parameter_index(
                &candidate.parameters,
                argument_index,
                &query::argument_name(&argument),
            ) else {
                continue;
            };
            if !parameter_has_tag(
                &context.snapshot,
                candidate,
                parameter_index,
                tag,
                &mut HashSet::new(),
            ) {
                continue;
            }
            let Some(expression) = query::argument_expression(&container, argument_index) else {
                continue;
            };
            if query::string_at(&expression).as_ref() != Some(&expression) {
                continue;
            }
            return Some(query::string_value(&expression));
        }
    }
    None
}

/// The call or object creation the literal is an argument of, and the
/// callables it resolves to.
fn call_container(context: &PhpContext, literal: &SyntaxNode) -> Option<(SyntaxNode, Vec<Symbol>)> {
    if let Some(call) = query::call_at(literal) {
        let candidates = call_candidates(context, &call);
        return Some((call, candidates));
    }
    let object = literal
        .ancestors()
        .find(|current| current.kind() == PHP_OBJECT_CREATION)?;
    let name = name_context(&context.document, u32::from(object.text_range().start()))
        .resolve_class(&query::object_class_name(&object));
    let candidates = MemberResolver::new(&context.snapshot)
        .methods(&Type::named(&name, Vec::new()), "__construct")
        .into_iter()
        .map(|member| member.symbol)
        .collect();
    Some((object, candidates))
}

fn call_candidates(context: &PhpContext, call: &SyntaxNode) -> Vec<Symbol> {
    match call.kind() {
        PHP_MEMBER_CALL | PHP_SCOPED_CALL => {
            let receiver_node = query::call_receiver(call);
            let mut receiver = receiver_node
                .as_ref()
                .map_or(Type::UNKNOWN, |node| context.document.type_of(node).ty);
            if receiver.is_unknown()
                && call.kind() == PHP_SCOPED_CALL
                && let Some(node) = receiver_node
                    .as_ref()
                    .filter(|node| node.kind() == PHP_NAME)
            {
                let name = name_context(&context.document, u32::from(node.text_range().start()))
                    .resolve_class(query::node_text(node).trim());
                receiver = Type::named(&name, Vec::new());
            }
            MemberResolver::new(&context.snapshot)
                .methods(&receiver, &query::call_method_name(call))
                .into_iter()
                .map(|member| member.symbol)
                .collect()
        }
        PHP_FUNCTION_CALL => {
            let names = name_context(&context.document, u32::from(call.text_range().start()));
            let mut result = Vec::new();
            names.visit_function_names(&query::call_name(call), |name| {
                let functions = context.snapshot.functions(name);
                if functions.is_empty() {
                    return true;
                }
                result = functions;
                false
            });
            result
        }
        _ => Vec::new(),
    }
}

/// The name resolution context of the scope containing `offset`.
pub(crate) fn name_context(document: &Document, offset: u32) -> NameContext {
    match document.scope_at(offset) {
        Some(scope) => NameContext::with_imports(scope.namespace.clone(), &scope.imports),
        None => NameContext::new(&document.namespace),
    }
}

fn parameter_index(
    parameters: &[Parameter],
    argument_index: usize,
    argument_name: &str,
) -> Option<usize> {
    if !argument_name.is_empty() {
        let argument_name = argument_name.strip_prefix('$').unwrap_or(argument_name);
        return parameters.iter().position(|parameter| {
            equal_fold(
                parameter.name.strip_prefix('$').unwrap_or(&parameter.name),
                argument_name,
            )
        });
    }
    if argument_index < parameters.len() {
        return Some(argument_index);
    }
    parameters
        .last()
        .filter(|parameter| parameter.flags.has(Flags::VARIADIC))
        .map(|_| parameters.len() - 1)
}

fn parameter_has_tag(
    snapshot: &Snapshot,
    method: &Symbol,
    parameter_index: usize,
    tag: &str,
    visited: &mut HashSet<String>,
) -> bool {
    if !visited.insert(method.id.as_str().to_owned()) {
        return false;
    }
    if let Some(parameter) = method.parameters.get(parameter_index)
        && parameter
            .assistant_tags
            .iter()
            .any(|candidate| equal_fold(candidate, tag))
    {
        return true;
    }
    let Some(owner) = snapshot.symbol(method.container.as_str()) else {
        return false;
    };
    let parents = owner
        .extends()
        .iter()
        .chain(owner.implements())
        .chain(owner.traits());
    for parent_name in parents {
        for parent in snapshot.classes(parent_name) {
            for inherited in snapshot.members(parent.id.as_str(), &method.name) {
                if inherited.kind == SymbolKind::Method
                    && parameter_has_tag(snapshot, &inherited, parameter_index, tag, visited)
                {
                    return true;
                }
            }
        }
    }
    false
}
