//! Context metadata, Faker property and ProductStream builder migrations.

use super::scan;
use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_ASSIGNMENT_EXPRESSION, PHP_BREAK_STATEMENT, PHP_CONTINUE_STATEMENT,
    PHP_DO_WHILE_STATEMENT, PHP_ECHO_STATEMENT, PHP_EXPRESSION_STATEMENT, PHP_FOR_STATEMENT,
    PHP_FOREACH_STATEMENT, PHP_GLOBAL_STATEMENT, PHP_IF_STATEMENT, PHP_MEMBER_ACCESS,
    PHP_MEMBER_CALL, PHP_RETURN_STATEMENT, PHP_STATIC_STATEMENT, PHP_SWITCH_STATEMENT,
    PHP_THROW_STATEMENT, PHP_TRY_STATEMENT, PHP_UNARY_EXPRESSION, PHP_VARIABLE,
    PHP_WHILE_STATEMENT, TK_ELLIPSIS,
};
use swlsp_php_semantic::{Document, Snapshot};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, call_name_range, migration_problem,
    node_text, php_argument_expression,
};
use crate::common::{ancestor_php_node, php_type_is_subtype};

const CONTEXT_METADATA_STATE_CODE: &str = "shopware.migration.context_metadata.state";
const FAKER_PROPERTY_CALL_CODE: &str = "shopware.migration.faker.property_call";
const PRODUCT_STREAM_BUILDER_CODE: &str = "shopware.migration.product_stream.enrich_criteria";

const SHOPWARE_CONTEXT_CLASS: &str = "Shopware\\Core\\Framework\\Context";
const FAKER_GENERATOR_CLASS: &str = "Faker\\Generator";
const PRODUCT_STREAM_BUILDER_INTERFACE_CLASS: &str =
    "Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface";
const PRODUCT_STREAM_MIGRATION_TODO: &str = "// TODO: Replace buildFilters() call with AbstractProductStreamBuilder::enrichCriteria() - please check manually";

impl ShopwareMigrationAnalyzer {
    pub(super) fn context_metadata_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for call in scan::member_calls(root) {
            if ctx.is_cancelled() {
                return result;
            }
            if !eq_ignore_case(&phpquery::call_method_name(&call), "addExtension") {
                continue;
            }
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !php_type_is_subtype(
                &document.type_of(&receiver).ty,
                snapshot,
                SHOPWARE_CONTEXT_CLASS,
            ) {
                continue;
            }
            let Some(constant) = phpquery::argument(&call, 0)
                .and_then(|argument| php_argument_expression(&argument))
            else {
                continue;
            };
            if constant.kind() != PHP_MEMBER_ACCESS {
                continue;
            }
            let constant_name = php_member_target_name(&constant);
            if !eq_ignore_case(&constant_name, "USE_INDEXING_QUEUE")
                && !eq_ignore_case(&constant_name, "DISABLE_INDEXING")
            {
                continue;
            }
            let safe = phpquery::arguments(&call).count() == 2;
            result.push(migration_problem(
                CONTEXT_METADATA_STATE_CODE,
                call_name_range(&call),
                &call,
                "Shopware 6.5: store indexing flags as Context state instead of extension metadata"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "context-metadata-state".into(),
                    kind: "method".into(),
                    safe,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    pub(super) fn faker_property_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for access in phpquery::nodes(root, &[PHP_MEMBER_ACCESS]) {
            if ctx.is_cancelled() {
                return result;
            }
            let (Some(receiver), Some(name)) =
                (php_member_receiver(&access), php_member_target(&access))
            else {
                continue;
            };
            if !php_type_is_subtype(
                &document.type_of(&receiver).ty,
                snapshot,
                FAKER_GENERATOR_CLASS,
            ) {
                continue;
            }
            result.push(migration_problem(
                FAKER_PROPERTY_CALL_CODE,
                name.range_trimmed_trivia(),
                &access,
                format!(
                    "Shopware 6.5: call Faker formatter {}() as a method",
                    node_text(&name).trim()
                ),
                &ShopwareMigrationPayload {
                    rule: "faker-property-call".into(),
                    kind: "member".into(),
                    safe: !php_member_is_write_target(&access),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    pub(super) fn product_stream_builder_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
        source: &str,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for call in scan::member_calls(root) {
            if ctx.is_cancelled() {
                return result;
            }
            if !eq_ignore_case(&phpquery::call_method_name(&call), "buildFilters") {
                continue;
            }
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !php_type_is_subtype(
                &document.type_of(&receiver).ty,
                snapshot,
                PRODUCT_STREAM_BUILDER_INTERFACE_CLASS,
            ) {
                continue;
            }
            let mut payload = ShopwareMigrationPayload {
                rule: "product-stream-enrich-criteria".into(),
                kind: "manual".into(),
                ..ShopwareMigrationPayload::default()
            };
            if phpquery::arguments(&call).count() == 2 {
                if let Some((start, end, replacement)) = product_stream_assignment_rewrite(&call) {
                    payload.kind = "assignment".into();
                    payload.safe = true;
                    payload.start = start;
                    payload.end = end;
                    payload.replacement = replacement;
                } else if let Some((start, end, replacement)) = product_stream_inline_rewrite(&call)
                {
                    payload.kind = "inline".into();
                    payload.safe = true;
                    payload.start = start;
                    payload.end = end;
                    payload.replacement = replacement;
                } else if let Some((start, replacement)) =
                    product_stream_manual_rewrite(&call, source)
                {
                    payload.safe = true;
                    payload.start = start;
                    payload.replacement = replacement;
                }
            }
            let mut message =
                "Shopware 6.8: replace ProductStreamBuilder::buildFilters() with enrichCriteria()"
                    .to_owned();
            if payload.kind == "manual" {
                message.push_str(" (manual migration required)");
            }
            result.push(migration_problem(
                PRODUCT_STREAM_BUILDER_CODE,
                call_name_range(&call),
                &call,
                message,
                &payload,
            ));
        }
        result
    }
}

fn product_stream_manual_rewrite(call: &SyntaxNode, source: &str) -> Option<(u32, String)> {
    let statement = nearest_php_statement(call)?;
    let start = u32::from(statement.range_trimmed_trivia().start());
    if start as usize > source.len() || product_stream_todo_immediately_precedes(source, start) {
        return None;
    }
    let start_index = start as usize;
    let line_start = source[..start_index]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let mut indent = &source[line_start..start_index];
    if !indent.trim().is_empty() {
        indent = "";
    }
    Some((start, format!("{PRODUCT_STREAM_MIGRATION_TODO}\n{indent}")))
}

fn product_stream_todo_immediately_precedes(source: &str, start: u32) -> bool {
    let start = start as usize;
    if start > source.len() {
        return false;
    }
    let Some(newline) = source[..start].rfind('\n') else {
        return false;
    };
    let previous_end = newline;
    let previous_start = source[..previous_end]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    source[previous_start..previous_end].trim() == PRODUCT_STREAM_MIGRATION_TODO
}

fn nearest_php_statement(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| {
        matches!(
            current.kind(),
            PHP_RETURN_STATEMENT
                | PHP_IF_STATEMENT
                | PHP_SWITCH_STATEMENT
                | PHP_WHILE_STATEMENT
                | PHP_DO_WHILE_STATEMENT
                | PHP_FOR_STATEMENT
                | PHP_FOREACH_STATEMENT
                | PHP_TRY_STATEMENT
                | PHP_THROW_STATEMENT
                | PHP_BREAK_STATEMENT
                | PHP_CONTINUE_STATEMENT
                | PHP_ECHO_STATEMENT
                | PHP_GLOBAL_STATEMENT
                | PHP_STATIC_STATEMENT
                | PHP_EXPRESSION_STATEMENT
        )
    })
}

fn product_stream_assignment_rewrite(call: &SyntaxNode) -> Option<(u32, u32, String)> {
    let assignment = ancestor_php_node(call, PHP_ASSIGNMENT_EXPRESSION)?;
    let statement = ancestor_php_node(call, PHP_EXPRESSION_STATEMENT)?;
    if call.parent().as_ref() != Some(&assignment) {
        return None;
    }
    let filters_variable = phpquery::assigned_variable(&statement);
    if filters_variable.is_empty() {
        return None;
    }
    let next = next_php_statement(&statement)?;
    let add_filter = direct_php_expression(&next)?;
    if add_filter.kind() != PHP_MEMBER_CALL
        || !eq_ignore_case(&phpquery::call_method_name(&add_filter), "addFilter")
        || phpquery::arguments(&add_filter).count() != 1
    {
        return None;
    }
    let argument = phpquery::argument(&add_filter, 0)?;
    let filter_expression = php_argument_expression(&argument)?;
    if argument.child_token_of_kind(TK_ELLIPSIS).is_none()
        || filter_expression.kind() != PHP_VARIABLE
        || !eq_ignore_case(node_text(&filter_expression).trim(), &filters_variable)
    {
        return None;
    }
    let replacement =
        product_stream_enrich_criteria_call(call, phpquery::call_receiver(&add_filter).as_ref())?;
    let first_range = statement.range_trimmed_trivia();
    let second_range = next.range_trimmed_trivia();
    Some((
        first_range.start().into(),
        second_range.end().into(),
        format!("{replacement};"),
    ))
}

fn product_stream_inline_rewrite(call: &SyntaxNode) -> Option<(u32, u32, String)> {
    let argument = call.parent()?;
    if argument.kind() != PHP_ARGUMENT
        || argument.child_token_of_kind(TK_ELLIPSIS).is_none()
        || php_argument_expression(&argument).as_ref() != Some(call)
    {
        return None;
    }
    let mut current = argument.parent();
    while let Some(node) = current {
        if node.kind() == PHP_EXPRESSION_STATEMENT {
            break;
        }
        if node.kind() != PHP_MEMBER_CALL
            || !eq_ignore_case(&phpquery::call_method_name(&node), "addFilter")
            || phpquery::arguments(&node).count() != 1
        {
            current = node.parent();
            continue;
        }
        let replacement =
            product_stream_enrich_criteria_call(call, phpquery::call_receiver(&node).as_ref())?;
        let range = node.range_trimmed_trivia();
        return Some((range.start().into(), range.end().into(), replacement));
    }
    None
}

fn product_stream_enrich_criteria_call(
    build_filters: &SyntaxNode,
    criteria: Option<&SyntaxNode>,
) -> Option<String> {
    let builder = phpquery::call_receiver(build_filters)?;
    let criteria = criteria?;
    let stream_id = phpquery::argument(build_filters, 0)
        .and_then(|argument| php_argument_expression(&argument))?;
    let shopware_context = phpquery::argument(build_filters, 1)
        .and_then(|argument| php_argument_expression(&argument))?;
    Some(format!(
        "{}->enrichCriteria({}, {}, {})",
        node_text(&builder).trim(),
        node_text(criteria).trim(),
        node_text(&stream_id).trim(),
        node_text(&shopware_context).trim(),
    ))
}

/// The first child node.
fn php_member_receiver(member: &SyntaxNode) -> Option<SyntaxNode> {
    member.children().next()
}

/// The last child node.
fn php_member_target(member: &SyntaxNode) -> Option<SyntaxNode> {
    member.children().last()
}

fn php_member_target_name(member: &SyntaxNode) -> String {
    php_member_target(member)
        .map_or_else(String::new, |target| node_text(&target).trim().to_owned())
}

fn php_member_is_write_target(member: &SyntaxNode) -> bool {
    let Some(parent) = member.parent() else {
        return false;
    };
    match parent.kind() {
        PHP_ASSIGNMENT_EXPRESSION => direct_php_expression(&parent).as_ref() == Some(member),
        PHP_UNARY_EXPRESSION => {
            let text = node_text(&parent);
            text.contains("++") || text.contains("--")
        }
        _ => false,
    }
}

/// The next sibling node of `statement`.
fn next_php_statement(statement: &SyntaxNode) -> Option<SyntaxNode> {
    statement.next_sibling()
}

/// The first child node.
fn direct_php_expression(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children().next()
}
