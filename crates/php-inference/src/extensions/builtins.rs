//! Stable core PHP signatures that are useful even before a versioned stub
//! package has been loaded.

use std::borrow::Cow;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Confidence, TypeFact, TypeSource};
use swlsp_php_types::{Joiner, Kind, Relations, Type, contains_uncertain};
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::{CallContext, Extension};
use crate::syntax::{
    Source, compact, direct_nodes, direct_operator, first_node, flow_expression_key, is_null_node,
    last_node, lower_call_name, trim_backslash, trim_quotes,
};
use crate::types_util::{
    array_type_known_non_empty, iterable_types, predicate_type, type_definitely_truthy,
    without_falsy_literals,
};

/// Core PHP builtin return types.
#[derive(Clone, Copy, Debug, Default)]
pub struct Builtins;

impl Extension for Builtins {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        let value = builtin_type(context)?;
        if value.is_unknown() {
            return None;
        }
        Some(TypeFact {
            ty: value,
            confidence: Confidence::Declared,
            source: TypeSource::Signature,
            origin: TextRange::default(),
            reason: Cow::Borrowed("PHP builtin"),
        })
    }
}

/// `None` means the extension does not apply; `Some(unknown)` that it
/// applies without a better type.
fn builtin_type(context: &CallContext<'_>) -> Option<Type> {
    let name = lowercase(trim_backslash(context.name));
    let relations = context.relations();
    let arguments = context.arguments;
    let source = Source(context.source);
    let argument_type = |index: usize| arguments[index].ty.clone();
    let value = match name.as_str() {
        "count" | "strlen" | "mb_strlen" | "array_push" | "array_unshift" => Type::INT,
        "strtolower" | "strtoupper" | "mb_strtolower" | "mb_strtoupper" => Type::STRING,
        // Internal false-return unions are benevolent in PHPStan.
        "tempnam" => Type::STRING,
        "is_string" | "is_int" | "is_integer" | "is_float" | "is_double" | "is_bool"
        | "is_array" | "is_callable" | "is_iterable" | "is_object" | "is_resource" | "is_null"
        | "isset" | "empty" => Type::BOOL,
        "array_values" => {
            if arguments.is_empty() {
                Type::UNKNOWN
            } else {
                let (_, element) = iterable_types(&argument_type(0), relations);
                if array_type_known_non_empty(&argument_type(0)) {
                    Type::non_empty_list(element)
                } else {
                    Type::list(element)
                }
            }
        }
        "array_reverse" => arguments
            .first()
            .map_or(Type::UNKNOWN, |argument| argument.ty.clone()),
        "array_column" => {
            let mut value = Type::UNKNOWN;
            if arguments.len() > 1 {
                let (_, row) = iterable_types(&argument_type(0), relations);
                let (column, always_present) =
                    array_column_type(&row, &argument_type(1), relations);
                if !column.is_unknown() {
                    value = if arguments.len() == 2 {
                        if always_present && array_type_known_non_empty(&argument_type(0)) {
                            Type::non_empty_list(column)
                        } else {
                            Type::list(column)
                        }
                    } else {
                        Type::array(Type::ARRAY_KEY, column)
                    };
                }
            }
            value
        }
        "array_keys" => {
            if arguments.is_empty() {
                Type::UNKNOWN
            } else {
                let (key, _) = iterable_types(&argument_type(0), relations);
                if array_type_known_non_empty(&argument_type(0)) {
                    Type::non_empty_list(key)
                } else {
                    Type::list(key)
                }
            }
        }
        "array_flip" => {
            let mut value = Type::UNKNOWN;
            if !arguments.is_empty() {
                let (key, element) = iterable_types(&argument_type(0), relations);
                if !key.is_unknown()
                    && !element.is_unknown()
                    && relations.is_subtype(&element, &Type::ARRAY_KEY)
                {
                    value = Type::array(element, key);
                }
            }
            value
        }
        "iterator_to_array" => {
            if arguments.is_empty() {
                Type::UNKNOWN
            } else {
                let (key, element) = iterable_types(&argument_type(0), relations);
                let preserve_keys =
                    !(arguments.len() > 1 && argument_type(1).kind() == Kind::False);
                if preserve_keys {
                    Type::array(key, element)
                } else {
                    Type::list(element)
                }
            }
        }
        "array_map" => {
            let mut value = Type::UNKNOWN;
            if arguments.len() > 1 {
                let mapped = array_map_result_type(context);
                if !mapped.is_unknown() {
                    if arguments.len() > 2 {
                        value = Type::list(mapped);
                    } else {
                        let (key, _) = iterable_types(&argument_type(1), relations);
                        value = if !key.is_unknown() && relations.is_subtype(&key, &Type::INT) {
                            Type::list(mapped)
                        } else {
                            Type::array(key, mapped)
                        };
                    }
                }
            }
            value
        }
        "array_sum" => {
            let mut value = Type::UNKNOWN;
            if !arguments.is_empty() {
                let (_, element) = iterable_types(&argument_type(0), relations);
                if !element.is_unknown() && relations.is_subtype(&element, &Type::INT) {
                    value = Type::INT;
                }
            }
            value
        }
        "array_reduce" => {
            let mut value = Type::UNKNOWN;
            if arguments.len() > 1 && argument_type(1).kind() == Kind::Callable {
                value = argument_type(1).result().clone();
                value = if arguments.len() > 2 {
                    relations.join(&value, &argument_type(2))
                } else {
                    Type::nullable(value)
                };
            }
            value
        }
        "array_search" => {
            if arguments.len() > 1 {
                let (key, _) = iterable_types(&argument_type(1), relations);
                Type::union([key, Type::FALSE])
            } else {
                Type::UNKNOWN
            }
        }
        "explode" => {
            if arguments.len() > 1 {
                Type::list(Type::STRING)
            } else {
                Type::UNKNOWN
            }
        }
        "ini_get" => {
            if !arguments.is_empty() && known_core_ini_option(&argument_type(0)) {
                Type::STRING
            } else {
                Type::UNKNOWN
            }
        }
        "parse_url" => parse_url_component_return_type(context),
        "pathinfo" => {
            if pathinfo_returns_string(context) {
                Type::STRING
            } else {
                Type::UNKNOWN
            }
        }
        "array_first" | "array_last" | "array_shift" | "array_pop" => {
            let mut value = Type::UNKNOWN;
            if !arguments.is_empty() {
                let (_, element) = iterable_types(&argument_type(0), relations);
                if !element.is_unknown() {
                    value = if argument_type(0).kind() == Kind::NonEmptyList
                        || array_source_known_non_empty(source, context.node)
                    {
                        element
                    } else {
                        Type::nullable(element)
                    };
                }
            }
            value
        }
        "version_compare" => {
            if arguments.len() >= 3 {
                Type::BOOL
            } else {
                Type::INT
            }
        }
        "str_replace" | "str_ireplace" | "substr_replace" => {
            let subject = if name == "substr_replace" { 0 } else { 2 };
            if arguments.len() > subject {
                replacement_subject_type(&argument_type(subject), relations)
            } else {
                Type::UNKNOWN
            }
        }
        "preg_replace" | "preg_filter" | "preg_replace_callback" => {
            let mut value = Type::UNKNOWN;
            if arguments.len() > 2 {
                let result = replacement_subject_type(&argument_type(2), relations);
                if !result.is_unknown() {
                    value = Type::nullable(result);
                }
            }
            value
        }
        "preg_split" => Type::union([Type::list(Type::STRING), Type::FALSE]),
        "preg_replace_callback_array" => {
            let mut value = Type::UNKNOWN;
            if arguments.len() > 1 {
                let result = replacement_subject_type(&argument_type(1), relations);
                if !result.is_unknown() {
                    value = Type::nullable(result);
                }
            }
            value
        }
        "array_filter" => {
            if arguments.is_empty() {
                Type::UNKNOWN
            } else {
                let callback = context.argument_expression(1);
                if let Some(shape) =
                    array_filter_shape_result(context, callback.as_ref(), relations)
                {
                    shape
                } else {
                    let (key, mut element) = iterable_types(&argument_type(0), relations);
                    match callback.as_ref() {
                        None => element = without_falsy_literals(relations, &element),
                        Some(callback) if callback.kind() == PHP_NULL => {
                            element = without_falsy_literals(relations, &element);
                        }
                        Some(callback)
                            if callback.kind() == PHP_FUNCTION_CALL
                                && crate::expr::is_first_class_callable(callback) =>
                        {
                            let constraint = predicate_type(&lower_call_name(callback));
                            if !constraint.is_unknown() {
                                element = Relations::default().narrow(&element, &constraint);
                            }
                        }
                        Some(callback) => {
                            let constraint = array_filter_arrow_constraint(context, Some(callback));
                            if !constraint.is_unknown() {
                                element = relations.narrow(&element, &constraint);
                            }
                        }
                    }
                    Type::array(key, element)
                }
            }
        }
        "filter" => {
            let mut value = Type::UNKNOWN;
            let receiver = &context.receiver;
            if receiver.kind() == Kind::Object
                && receiver.argument_count() > 0
                && relations.is_subtype(
                    receiver,
                    &Type::named("Shopware\\Core\\Framework\\Struct\\Collection", Vec::new()),
                )
            {
                let callback = context.argument_expression(0);
                let constraint = array_filter_arrow_constraint(context, callback.as_ref());
                if !constraint.is_unknown() {
                    let mut arguments = receiver.arguments().to_vec();
                    arguments[0] = if contains_uncertain(&arguments[0]) {
                        constraint
                    } else {
                        relations.narrow(&arguments[0], &constraint)
                    };
                    value = Type::named(receiver.name(), arguments);
                }
            }
            value
        }
        "json_encode" => {
            let flags = context.argument_expression(1);
            let throws = flags.is_some_and(|flags| {
                compact(context.text(&flags))
                    .to_uppercase()
                    .contains("JSON_THROW_ON_ERROR")
            });
            if !throws {
                return None;
            }
            Type::STRING
        }
        "var_export" | "print_r" => {
            if arguments.len() > 1 && argument_type(1).kind() == Kind::True {
                Type::STRING
            } else {
                Type::UNKNOWN
            }
        }
        "modify" => {
            if date_time_receiver(&context.receiver) {
                context.receiver.clone()
            } else {
                Type::UNKNOWN
            }
        }
        "getrealpath" => {
            // Finder yields resolved file entries; PHPStan treats their
            // inherited getRealPath() result benevolently.
            if relations.is_subtype(&context.receiver, &Type::named("SplFileInfo", Vec::new()))
                || relations.is_subtype(
                    &context.receiver,
                    &Type::named("Symfony\\Component\\Finder\\SplFileInfo", Vec::new()),
                )
            {
                Type::STRING
            } else {
                Type::UNKNOWN
            }
        }
        "get_class" => Type::class_string(Type::OBJECT),
        _ => return None,
    };
    Some(value)
}

fn array_column_type(row: &Type, column_key: &Type, relations: Relations<'_>) -> (Type, bool) {
    if row.kind() == Kind::Union {
        let mut values = Joiner::new(relations, Type::NEVER);
        let mut always_present = true;
        for member in row.arguments() {
            let (value, present) = array_column_type(member, column_key, relations);
            if value.is_unknown() {
                return (Type::UNKNOWN, false);
            }
            values.add(value);
            always_present = always_present && present;
        }
        return (values.value(), always_present);
    }
    if row.kind() != Kind::ArrayShape
        || !matches!(column_key.kind(), Kind::LiteralString | Kind::LiteralInt)
    {
        return (Type::UNKNOWN, false);
    }
    for field in row.fields() {
        if trim_quotes(&field.name) == column_key.name() {
            return (field.ty.clone(), !field.optional);
        }
    }
    (Type::UNKNOWN, false)
}

fn array_filter_shape_result(
    context: &CallContext<'_>,
    callback: Option<&SyntaxNode>,
    relations: Relations<'_>,
) -> Option<Type> {
    let source = &context.arguments[0].ty;
    if source.kind() != Kind::ArrayShape {
        return None;
    }
    let mut constraint = Type::UNKNOWN;
    let remove_falsy = callback.is_none_or(|callback| callback.kind() == PHP_NULL);
    let mut remove_null = false;
    if let Some(callback) = callback.filter(|_| !remove_falsy) {
        if callback.kind() == PHP_FUNCTION_CALL && crate::expr::is_first_class_callable(callback) {
            constraint = predicate_type(&lower_call_name(callback));
        } else {
            constraint = array_filter_arrow_constraint(context, Some(callback));
            remove_null = array_filter_arrow_excludes_null(Source(context.source), Some(callback));
        }
        if constraint.is_unknown() && !remove_null {
            return None;
        }
    }
    let mut fields = Vec::with_capacity(source.field_count());
    for field in source.fields() {
        let narrowed = if remove_falsy {
            without_falsy_literals(relations, &field.ty)
        } else if remove_null {
            relations.without(&field.ty, &Type::NULL)
        } else {
            relations.narrow(&field.ty, &constraint)
        };
        if narrowed.kind() == Kind::Never {
            continue;
        }
        let may_remove = if remove_falsy {
            !type_definitely_truthy(&field.ty)
        } else if remove_null {
            relations.without(&field.ty, &Type::NULL) != field.ty
        } else {
            !relations.is_subtype(&field.ty, &constraint)
        };
        let mut field = field.clone();
        if may_remove {
            field.optional = true;
        }
        field.ty = narrowed;
        fields.push(field);
    }
    Some(Type::array_shape(fields, source.is_open_shape()))
}

fn array_filter_arrow_excludes_null(source: Source<'_>, callback: Option<&SyntaxNode>) -> bool {
    let Some(callback) = callback.filter(|callback| callback.kind() == PHP_ARROW_FUNCTION) else {
        return false;
    };
    let Some(first_parameter) = query::parameters(callback).next() else {
        return false;
    };
    let Some(predicate) =
        last_node(callback).filter(|predicate| predicate.kind() == PHP_BINARY_EXPRESSION)
    else {
        return false;
    };
    let operator = direct_operator(&predicate);
    if operator != "!==" && operator != "!=" {
        return false;
    }
    let operands = direct_nodes(&predicate);
    if operands.len() < 2 {
        return false;
    }
    let parameter = query::parameter_name(&first_parameter);
    let left = &operands[0];
    let right = &operands[operands.len() - 1];
    left.kind() == PHP_VARIABLE
        && query::variable_key(left) == parameter
        && is_null_node(source, Some(right))
        || right.kind() == PHP_VARIABLE
            && query::variable_key(right) == parameter
            && is_null_node(source, Some(left))
}

fn array_map_result_type(context: &CallContext<'_>) -> Type {
    let callback = &context.arguments[0].ty;
    if callback.kind() == Kind::Callable {
        return callback.result().clone();
    }
    let Some(expression) = context
        .argument_expression(0)
        .filter(|expression| expression.kind() == PHP_STRING)
    else {
        return Type::UNKNOWN;
    };
    match lowercase(&diagnostic_string_value(context, &expression)).as_str() {
        "count" | "strlen" | "mb_strlen" => Type::INT,
        _ => Type::UNKNOWN,
    }
}

fn date_time_receiver(value: &Type) -> bool {
    match value.kind() {
        Kind::Object => {
            let name = trim_backslash(value.name());
            eq_ignore_case(name, "DateTime") || eq_ignore_case(name, "DateTimeImmutable")
        }
        Kind::Union | Kind::Intersection => {
            value.argument_count() > 0 && value.arguments().iter().all(date_time_receiver)
        }
        _ => false,
    }
}

fn diagnostic_string_value(context: &CallContext<'_>, node: &SyntaxNode) -> String {
    let value = query::string_value(node);
    let text = context.text(node).trim();
    if text.len() < 2 {
        return value;
    }
    let quote = match text.as_bytes()[0] {
        b'\'' => '\'',
        b'"' => '"',
        _ => return value,
    };
    let mut result = String::with_capacity(value.len());
    let mut characters = value.chars().peekable();
    while let Some(character) = characters.next() {
        if character == '\\'
            && let Some(&next) = characters.peek()
            && (next == '\\' || next == quote)
        {
            result.push(next);
            characters.next();
            continue;
        }
        result.push(character);
    }
    result
}

fn array_removal_is_guarded_non_empty(source: Source<'_>, call: &SyntaxNode) -> bool {
    let argument = query::argument_expression(call, 0);
    let key = flow_expression_key(source, argument.as_ref());
    if argument.is_none() || key.is_empty() {
        return false;
    }
    let call_start = call.text_range().start();
    for current in call.ancestors() {
        if current.kind() == PHP_TERNARY_EXPRESSION {
            let parts = direct_nodes(&current);
            if parts.len() >= 3 {
                let condition = &parts[0];
                let true_branch = &parts[parts.len() - 2];
                let false_branch = &parts[parts.len() - 1];
                if true_branch.text_range().contains(call_start)
                    && condition_guarantees_non_empty_array(source, Some(condition), &key, true)
                {
                    return true;
                }
                if false_branch.text_range().contains(call_start)
                    && condition_guarantees_non_empty_array(source, Some(condition), &key, false)
                {
                    return true;
                }
            }
        }
        if current.kind() == PHP_BLOCK
            && let Some(parent) = current.parent()
            && parent.kind() == PHP_IF_STATEMENT
        {
            let parts = direct_nodes(&parent);
            if parts.len() >= 2
                && parts[1] == current
                && condition_guarantees_non_empty_array(source, Some(&parts[0]), &key, true)
            {
                return true;
            }
        }
        if current.kind() == PHP_WHILE_STATEMENT {
            let nodes = direct_nodes(&current);
            if nodes.len() >= 2
                && nodes[nodes.len() - 1].text_range().contains(call_start)
                && condition_guarantees_non_empty_array(source, Some(&nodes[0]), &key, true)
            {
                return true;
            }
        }
    }
    false
}

fn array_source_known_non_empty(source: Source<'_>, call: Option<&SyntaxNode>) -> bool {
    let Some(call) = call else {
        return false;
    };
    array_removal_is_guarded_non_empty(source, call)
        || array_removal_source_known_non_empty(source, call)
        || array_call_follows_terminating_empty_guard(source, call)
}

/// The statement directly inside a block that contains `call`.
fn enclosing_block_statement(call: &SyntaxNode) -> Option<(SyntaxNode, SyntaxNode)> {
    let mut statement = call.clone();
    while let Some(parent) = statement.parent() {
        if parent.kind() == PHP_BLOCK {
            return Some((statement, parent));
        }
        statement = parent;
    }
    None
}

fn array_call_follows_terminating_empty_guard(source: Source<'_>, call: &SyntaxNode) -> bool {
    let argument = query::argument_expression(call, 0);
    let key = flow_expression_key(source, argument.as_ref());
    if key.is_empty() {
        return false;
    }
    let Some((statement, block)) = enclosing_block_statement(call) else {
        return false;
    };
    let statements = direct_nodes(&block);
    for index in 1..statements.len() {
        if statements[index] != statement {
            continue;
        }
        let guard = &statements[index - 1];
        if guard.kind() != PHP_IF_STATEMENT {
            return false;
        }
        let parts = direct_nodes(guard);
        if parts.len() < 2 {
            return false;
        }
        let body = &parts[1];
        return body.kind() == PHP_BLOCK
            && block_ends_execution(body)
            && condition_guarantees_non_empty_array(source, Some(&parts[0]), &key, false);
    }
    false
}

fn block_ends_execution(block: &SyntaxNode) -> bool {
    last_node(block)
        .is_some_and(|last| matches!(last.kind(), PHP_RETURN_STATEMENT | PHP_THROW_STATEMENT))
}

fn array_removal_source_known_non_empty(source: Source<'_>, call: &SyntaxNode) -> bool {
    let argument = query::argument_expression(call, 0);
    if non_empty_array_source(argument.as_ref()) {
        return true;
    }
    let key = flow_expression_key(source, argument.as_ref());
    if key.is_empty() {
        return false;
    }
    let Some((statement, block)) = enclosing_block_statement(call) else {
        return false;
    };
    let statements = direct_nodes(&block);
    if let Some(index) = statements
        .iter()
        .position(|candidate| *candidate == statement)
    {
        for previous in statements[..index].iter().rev() {
            if let Some(assignment) = assignment_to_expression(source, previous, &key) {
                return non_empty_array_source(Some(&assignment));
            }
        }
    }
    false
}

fn assignment_to_expression(
    source: Source<'_>,
    node: &SyntaxNode,
    key: &str,
) -> Option<SyntaxNode> {
    if key.is_empty() {
        return None;
    }
    for assignment in query::nodes(node, &[PHP_ASSIGNMENT_EXPRESSION]) {
        let nodes = direct_nodes(&assignment);
        if nodes.len() >= 2 && flow_expression_key(source, Some(&nodes[0])) == key {
            return nodes.last().cloned();
        }
    }
    None
}

fn non_empty_array_source(node: Option<&SyntaxNode>) -> bool {
    let Some(node) = node else {
        return false;
    };
    if node.kind() == PHP_ARRAY {
        return !query::array_items(node).is_empty();
    }
    node.kind() == PHP_FUNCTION_CALL
        && eq_ignore_case(trim_backslash(&query::call_method_name(node)), "explode")
}

fn condition_guarantees_non_empty_array(
    source: Source<'_>,
    node: Option<&SyntaxNode>,
    key: &str,
    truth: bool,
) -> bool {
    let Some(node) = node else {
        return false;
    };
    if key.is_empty() {
        return false;
    }
    if node.kind() == PHP_PARENTHESIZED {
        return first_node(node).is_some_and(|inner| {
            condition_guarantees_non_empty_array(source, Some(&inner), key, truth)
        });
    }
    if node.kind() == PHP_UNARY_EXPRESSION && direct_operator(node) == "!" {
        return last_node(node).is_some_and(|inner| {
            condition_guarantees_non_empty_array(source, Some(&inner), key, !truth)
        });
    }
    if flow_expression_key(source, Some(node)) == key {
        return truth;
    }
    if node.kind() != PHP_BINARY_EXPRESSION {
        return false;
    }
    let nodes = direct_nodes(node);
    if nodes.len() < 2 {
        return false;
    }
    let left = &nodes[0];
    let right = &nodes[nodes.len() - 1];
    let operator = lowercase(&direct_operator(node));
    match operator.as_str() {
        "&&" | "and" => {
            let left = condition_guarantees_non_empty_array(source, Some(left), key, truth);
            let right = condition_guarantees_non_empty_array(source, Some(right), key, truth);
            if truth { left || right } else { left && right }
        }
        "||" | "or" => {
            let left = condition_guarantees_non_empty_array(source, Some(left), key, truth);
            let right = condition_guarantees_non_empty_array(source, Some(right), key, truth);
            if truth { left && right } else { left || right }
        }
        "!==" | "!=" | "===" | "==" => {
            let negated = operator == "!==" || operator == "!=";
            if compared_count_is_positive(source, left, right, key)
                || compared_count_is_positive(source, right, left, key)
            {
                return if negated { !truth } else { truth };
            }
            let compares_empty = flow_expression_key(source, Some(left)) == key
                && empty_array_literal(right)
                || flow_expression_key(source, Some(right)) == key && empty_array_literal(left);
            if !compares_empty {
                return false;
            }
            if negated { truth } else { !truth }
        }
        _ => false,
    }
}

fn compared_count_is_positive(
    source: Source<'_>,
    call: &SyntaxNode,
    literal: &SyntaxNode,
    key: &str,
) -> bool {
    if call.kind() != PHP_FUNCTION_CALL
        || !eq_ignore_case(trim_backslash(&query::call_method_name(call)), "count")
    {
        return false;
    }
    let argument = query::argument_expression(call, 0);
    if flow_expression_key(source, argument.as_ref()) != key {
        return false;
    }
    let text = source.text(literal).trim();
    !text.is_empty() && text != "0" && (b'1'..=b'9').contains(&text.as_bytes()[0])
}

fn empty_array_literal(node: &SyntaxNode) -> bool {
    node.kind() == PHP_ARRAY && query::array_items(node).is_empty()
}

pub(crate) fn array_filter_arrow_constraint(
    context: &CallContext<'_>,
    callback: Option<&SyntaxNode>,
) -> Type {
    let Some(callback) = callback.filter(|callback| callback.kind() == PHP_ARROW_FUNCTION) else {
        return Type::UNKNOWN;
    };
    let Some(first_parameter) = query::parameters(callback).next() else {
        return Type::UNKNOWN;
    };
    let mut name_context = NameContext::default();
    if let Some(document) = context.document
        && let Some(scope) = document.scope_at(callback.text_range().start().into())
    {
        name_context = NameContext::with_imports(scope.namespace.clone(), &scope.imports);
    }
    arrow_predicate_constraint(
        last_node(callback).as_ref(),
        &query::parameter_name(&first_parameter),
        &name_context,
    )
}

fn arrow_predicate_constraint(
    predicate: Option<&SyntaxNode>,
    parameter: &str,
    name_context: &NameContext,
) -> Type {
    let Some(predicate) = predicate else {
        return Type::UNKNOWN;
    };
    if predicate.kind() == PHP_PARENTHESIZED {
        return arrow_predicate_constraint(first_node(predicate).as_ref(), parameter, name_context);
    }
    if predicate.kind() != PHP_BINARY_EXPRESSION {
        return Type::UNKNOWN;
    }
    let operands = direct_nodes(predicate);
    if operands.len() < 2 {
        return Type::UNKNOWN;
    }
    let first = &operands[0];
    let last = &operands[operands.len() - 1];
    match lowercase(&direct_operator(predicate)).as_str() {
        "instanceof" => {
            if first.kind() != PHP_VARIABLE
                || query::variable_key(first) != parameter
                || last.kind() != PHP_NAME
            {
                return Type::UNKNOWN;
            }
            Type::named(
                &name_context.resolve_class(&query::name_value(last)),
                Vec::new(),
            )
        }
        operator @ ("||" | "or" | "&&" | "and") => {
            let left = arrow_predicate_constraint(Some(first), parameter, name_context);
            let right = arrow_predicate_constraint(Some(last), parameter, name_context);
            if left.is_unknown() || right.is_unknown() {
                return Type::UNKNOWN;
            }
            if operator == "||" || operator == "or" {
                Type::union([left, right])
            } else {
                Type::intersection([left, right])
            }
        }
        _ => Type::UNKNOWN,
    }
}

fn pathinfo_returns_string(context: &CallContext<'_>) -> bool {
    let Some(option) = context.argument_expression(1) else {
        return false;
    };
    let text = compact(context.text(&option));
    let name = text.strip_prefix('\\').unwrap_or(&text).to_uppercase();
    matches!(
        name.as_str(),
        "PATHINFO_DIRNAME"
            | "PATHINFO_BASENAME"
            | "PATHINFO_EXTENSION"
            | "PATHINFO_FILENAME"
            | "1"
            | "2"
            | "4"
            | "8"
    )
}

fn known_core_ini_option(value: &Type) -> bool {
    value.kind() == Kind::LiteralString
        && matches!(
            lowercase(value.name()).as_str(),
            "default_charset"
                | "max_execution_time"
                | "max_input_nesting_level"
                | "max_input_time"
                | "max_input_vars"
                | "memory_limit"
                | "post_max_size"
                | "upload_max_filesize"
        )
}

fn parse_url_component_return_type(context: &CallContext<'_>) -> Type {
    let Some(component) = context.argument_expression(1) else {
        return Type::UNKNOWN;
    };
    let text = compact(context.text(&component));
    let name = text.strip_prefix('\\').unwrap_or(&text).to_uppercase();
    match name.as_str() {
        "PHP_URL_PORT" | "2" => Type::union([Type::INT, Type::NULL, Type::FALSE]),
        "PHP_URL_SCHEME" | "PHP_URL_HOST" | "PHP_URL_USER" | "PHP_URL_PASS" | "PHP_URL_PATH"
        | "PHP_URL_QUERY" | "PHP_URL_FRAGMENT" | "0" | "1" | "3" | "4" | "5" | "6" | "7" => {
            Type::union([Type::STRING, Type::NULL, Type::FALSE])
        }
        _ => Type::UNKNOWN,
    }
}

fn replacement_subject_type(value: &Type, relations: Relations<'_>) -> Type {
    match value.kind() {
        Kind::Mixed => Type::MIXED,
        Kind::String | Kind::LiteralString | Kind::ClassString | Kind::Null | Kind::ArrayKey => {
            Type::STRING
        }
        Kind::List => Type::list(Type::STRING),
        Kind::NonEmptyList => Type::non_empty_list(Type::STRING),
        Kind::Array | Kind::ArrayShape => {
            let (key, _) = iterable_types(value, relations);
            Type::array(key, Type::STRING)
        }
        Kind::NonEmptyArray => {
            let (key, _) = iterable_types(value, relations);
            Type::non_empty_array(key, Type::STRING)
        }
        Kind::Union => {
            let mut result = Type::NEVER;
            for member in value.arguments() {
                let mapped = replacement_subject_type(member, relations);
                if mapped.is_unknown() {
                    return Type::UNKNOWN;
                }
                result = relations.join(&result, &mapped);
            }
            result
        }
        _ => Type::UNKNOWN,
    }
}
