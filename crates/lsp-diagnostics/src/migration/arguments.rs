//! Removed and added call arguments (Go `shopware_migration_arguments.go`).

use super::scan;
use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_NAMED_ARGUMENT;
use swlsp_php_semantic::{Document, Snapshot};
use swlsp_php_types::equal_fold;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use super::api::valid_source_range;
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, call_name_range, migration_problem,
    node_text, php_argument_expression, php_class_is_subtype, php_own_method_for_migration,
};
use crate::common::php_type_is_subtype;

const REMOVE_ARGUMENT_MIGRATION_CODE: &str = "shopware.migration.arguments.remove";
const ADD_DEFAULT_ARGUMENT_CODE: &str = "shopware.migration.arguments.add_default";
const THUMBNAIL_GENERATE_CODE: &str = "shopware.migration.thumbnail.generate";

const CALCULATED_TAX_COLLECTION_CLASS: &str =
    "Shopware\\Core\\Checkout\\Cart\\Tax\\Struct\\CalculatedTaxCollection";
const THUMBNAIL_SERVICE_CLASS: &str = "Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService";
const MEDIA_COLLECTION_CLASS: &str = "Shopware\\Core\\Content\\Media\\MediaCollection";

const REMOVED_CONSTRUCTOR_ARGUMENT_CLASSES: &[&str] = &[
    "Shopware\\Core\\Checkout\\Customer\\Exception\\DuplicateWishlistProductException",
    "Shopware\\Core\\Content\\Newsletter\\Exception\\LanguageOfNewsletterDeleteException",
];

impl ShopwareMigrationAnalyzer {
    pub(super) fn argument_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
        source: &str,
    ) -> Vec<Problem> {
        let mut result = removed_call_argument_problems(ctx, root, document, snapshot);
        result.extend(removed_constructor_argument_problems(ctx, root, snapshot));
        result.extend(thumbnail_argument_problems(
            ctx, root, document, snapshot, source,
        ));
        result
    }
}

fn removed_call_argument_problems(
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
        if !equal_fold(&phpquery::call_method_name(&call), "merge") {
            continue;
        }
        let Some(receiver) = phpquery::call_receiver(&call) else {
            continue;
        };
        if phpquery::arguments(&call).count() <= 1
            || !php_type_is_subtype(
                &document.type_of(&receiver).ty,
                snapshot,
                CALCULATED_TAX_COLLECTION_CLASS,
            )
        {
            continue;
        }
        result.push(argument_migration_problem(
            REMOVE_ARGUMENT_MIGRATION_CODE,
            &call,
            "Shopware 6.5: remove the obsolete second CalculatedTaxCollection::merge() argument",
            "call-argument",
            1,
            "",
            true,
        ));
    }
    result
}

fn removed_constructor_argument_problems(
    ctx: &Context,
    root: &SyntaxNode,
    snapshot: Option<&Snapshot>,
) -> Vec<Problem> {
    let resolver = scan::resolver(root);
    let mut result = Vec::new();
    for creation in phpquery::object_creations(root, &[]) {
        if ctx.is_cancelled() {
            return result;
        }
        let resolved = resolver.resolve(&phpquery::object_class_name(&creation));
        let class = resolved.trim_matches('\\');
        if phpquery::arguments(&creation).next().is_none()
            || !php_class_matches_any(class, snapshot, REMOVED_CONSTRUCTOR_ARGUMENT_CLASSES)
        {
            continue;
        }
        result.push(argument_migration_problem(
            REMOVE_ARGUMENT_MIGRATION_CODE,
            &creation,
            "Shopware 6.5: remove the obsolete exception constructor argument",
            "call-argument",
            0,
            "",
            true,
        ));
    }
    for class in scan::classes(root) {
        if ctx.is_cancelled() {
            return result;
        }
        let matches = REMOVED_CONSTRUCTOR_ARGUMENT_CLASSES
            .iter()
            .any(|target| php_class_is_subtype(&class, root, snapshot, target));
        if !matches {
            continue;
        }
        let Some(constructor) = php_own_method_for_migration(&class, "__construct") else {
            continue;
        };
        if phpquery::parameters(&constructor).next().is_none() {
            continue;
        }
        result.push(argument_migration_problem(
            REMOVE_ARGUMENT_MIGRATION_CODE,
            &constructor,
            "Shopware 6.5: remove the obsolete first exception constructor parameter",
            "constructor-parameter",
            0,
            "",
            true,
        ));
    }
    result
}

fn thumbnail_argument_problems(
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
        let Some(receiver) = phpquery::call_receiver(&call) else {
            continue;
        };
        if !php_type_is_subtype(
            &document.type_of(&receiver).ty,
            snapshot,
            THUMBNAIL_SERVICE_CLASS,
        ) {
            continue;
        }
        let method = phpquery::call_method_name(&call);
        if equal_fold(&method, "updateThumbnails") {
            let arguments: Vec<SyntaxNode> = phpquery::arguments(&call).collect();
            if arguments.len() != 2 {
                continue;
            }
            result.push(argument_migration_problem(
                ADD_DEFAULT_ARGUMENT_CODE,
                &call,
                "Shopware 6.5: pass the new strict flag to ThumbnailService::updateThumbnails()",
                "call-argument",
                2,
                "false",
                php_arguments_are_positional(&arguments),
            ));
        } else if equal_fold(&method, "generateThumbnails") {
            let arguments: Vec<SyntaxNode> = phpquery::arguments(&call).collect();
            let first = phpquery::argument(&call, 0)
                .and_then(|argument| php_argument_expression(&argument));
            let second = phpquery::argument(&call, 1)
                .and_then(|argument| php_argument_expression(&argument));
            let range = call.range_trimmed_trivia();
            let mut replacement = String::new();
            let mut original = String::new();
            let mut safe = false;
            if let (Some(first), Some(second)) = (&first, &second)
                && arguments.len() == 2
                && valid_source_range(range.start().into(), range.end().into(), source)
                && php_arguments_are_positional(&arguments)
            {
                safe = true;
                replacement = format!(
                    "{}->generate(new \\{MEDIA_COLLECTION_CLASS}([{}]), {})",
                    node_text(&receiver).trim(),
                    node_text(first).trim(),
                    node_text(second).trim(),
                );
                original = source[range].to_owned();
            }
            result.push(migration_problem(
                THUMBNAIL_GENERATE_CODE,
                call_name_range(&call),
                &call,
                "Shopware 6.5: migrate single thumbnail generation to MediaCollection batching"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "thumbnail-generate".into(),
                    kind: "call".into(),
                    safe,
                    original,
                    replacement,
                    start: range.start().into(),
                    end: range.end().into(),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
    }
    result
}

fn argument_migration_problem(
    code: &str,
    element: &SyntaxNode,
    message: &str,
    kind: &str,
    index: i64,
    value: &str,
    safe: bool,
) -> Problem {
    migration_problem(
        code,
        call_name_range(element),
        element,
        message.to_owned(),
        &ShopwareMigrationPayload {
            rule: "argument-migration".into(),
            kind: kind.into(),
            safe,
            argument_index: index,
            value: value.into(),
            ..ShopwareMigrationPayload::default()
        },
    )
}

fn php_class_matches_any(class: &str, snapshot: Option<&Snapshot>, targets: &[&str]) -> bool {
    snapshot.is_some_and(|snapshot| {
        targets
            .iter()
            .any(|target| snapshot.is_subtype_of(class, target))
    })
}

fn php_arguments_are_positional(arguments: &[SyntaxNode]) -> bool {
    arguments
        .iter()
        .all(|argument| argument.kind() != PHP_NAMED_ARGUMENT)
}
