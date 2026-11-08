//! Versioned shopware-rector migrations and the Criteria ID-filter check
//! (port of Go `shopware_migration*.go` and `shopware_criteria.go`).

mod annotations;
mod api;
mod arguments;
mod calls;
mod criteria;
mod declarations;
mod message_handler;
mod scan;

#[cfg(test)]
mod tests;

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY_ACCESS, PHP_FOREACH_STATEMENT, PHP_FUNCTION_CALL, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL,
    PHP_NAME, PHP_OBJECT_CREATION, PHP_PARENTHESIZED, PHP_RETURN_STATEMENT, PHP_SCOPED_ACCESS,
    PHP_SCOPED_CALL, PHP_VARIABLE,
};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Snapshot;
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_shopware::ResolvedVersion;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::ProblemAnalyzer;
use crate::common::{call_target_name, direct_return_expression, php_type_is_subtype};

pub use criteria::ShopwareCriteriaAnalyzer;

const ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE: &str =
    "shopware.migration.entity_search_result.get_entities";
const ENTITY_EXTENSION_ENTITY_NAME_CODE: &str = "shopware.migration.entity_extension.entity_name";
const REVERSE_PROXY_BAN_ALL_CODE: &str = "shopware.migration.reverse_proxy.ban_all";
const SCHEDULED_TASK_LOGGER_CODE: &str = "shopware.migration.scheduled_task.exception_logger";
const ENTITY_SEARCH_RESULT_CLASS: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult";
const ENTITY_EXTENSION_CLASS: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension";
const REVERSE_PROXY_GATEWAY_CLASS: &str =
    "Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway";
const SCHEDULED_TASK_HANDLER_CLASS: &str =
    "Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler";
const LOGGER_INTERFACE_CLASS: &str = "Psr\\Log\\LoggerInterface";
const RECTOR_SOURCE: &str = "shopware-rector";

const ENTITY_SEARCH_RESULT_DELEGATED_METHODS: &[&str] = &[
    "count",
    "fill",
    "filter",
    "filterandreducebyproperty",
    "filterbyproperty",
    "filterinstance",
    "first",
    "firstwhere",
    "flatmap",
    "fmap",
    "get",
    "getat",
    "getcustomfieldsvalue",
    "getcustomfieldsvalues",
    "getelements",
    "getids",
    "getkeys",
    "getlist",
    "has",
    "insert",
    "isempty",
    "last",
    "map",
    "merge",
    "reduce",
    "remove",
    "set",
    "setcustomfields",
    "slice",
    "sort",
    "sortbyidarray",
];

const ENTITY_SEARCH_RESULT_DELEGATED_FUNCTIONS: &[&str] =
    &["count", "iterator_count", "iterator_to_array", "sizeof"];

fn is_zero_u32(value: &u32) -> bool {
    *value == 0
}

fn is_zero_i64(value: &i64) -> bool {
    *value == 0
}

fn is_false(value: &bool) -> bool {
    !*value
}

/// The problem and bound-fix payload of every migration (Go
/// `ShopwareMigrationPayload`; field names and `omitempty` are contracts).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ShopwareMigrationPayload {
    pub rule: String,
    pub kind: String,
    pub safe: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub replacement: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub original: String,
    #[serde(skip_serializing_if = "is_false")]
    pub remove_legacy: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub add_parameter: bool,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub start: u32,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub end: u32,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub edits: Vec<ShopwareMigrationEdit>,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub argument_index: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub value: String,
}

/// One source replacement of a multi-edit migration (Go
/// `ShopwareMigrationEdit`).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ShopwareMigrationEdit {
    pub start: u32,
    pub end: u32,
    pub original: String,
    pub replacement: String,
}

/// Ports versioned, deterministic shopware-rector rules to byte-oriented
/// problems. Unknown target versions disable the rules rather than guessing
/// which migration set applies (Go `ShopwareMigrationAnalyzer`).
pub struct ShopwareMigrationAnalyzer {
    php_index: Option<PhpIndex>,
    version: ResolvedVersion,
}

impl ShopwareMigrationAnalyzer {
    pub fn new(php_index: Option<PhpIndex>, version: ResolvedVersion) -> Self {
        Self { php_index, version }
    }

    pub(crate) fn at_least(&self, minor: i64, patch: i64) -> bool {
        self.version.at_least(6, minor, patch)
    }
}

/// Go `filepath.Ext`.
fn go_ext(path: &str) -> &str {
    for (position, character) in path.char_indices().rev() {
        match character {
            '/' => return "",
            '.' => return &path[position..],
            _ => {}
        }
    }
    ""
}

pub(crate) fn migration_problem(
    code: &str,
    range: TextRange,
    element: &SyntaxNode,
    message: String,
    payload: &ShopwareMigrationPayload,
) -> Problem {
    Problem {
        id: DiagnosticId::from(code),
        range,
        element: Some(element.clone().into()),
        message,
        severity: Some(DiagnosticSeverity::WARNING),
        source: RECTOR_SOURCE.to_owned(),
        payload: serde_json::to_value(payload).ok(),
        ..Problem::default()
    }
}

/// The trimmed range of a class name, or of the class itself.
pub(crate) fn class_name_range(class: &SyntaxNode) -> TextRange {
    phpquery::direct_child(class, PHP_NAME).map_or_else(
        || class.range_trimmed_trivia(),
        |name| name.range_trimmed_trivia(),
    )
}

/// The trimmed range of a call's target name, or of the call itself.
pub(crate) fn call_name_range(call: &SyntaxNode) -> TextRange {
    call_target_name(call).map_or_else(
        || call.range_trimmed_trivia(),
        |name| name.range_trimmed_trivia(),
    )
}

pub(crate) fn node_text(node: &SyntaxNode) -> String {
    node.text().to_string()
}

impl ProblemAnalyzer for ShopwareMigrationAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if !self.at_least(5, 0)
            || document.language != Some(LanguageId::Php)
            || go_to_lower(go_ext(&document.uri)) != ".php"
        {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let _scan = scan::ScanScope::enter(&root);
        let Some(analysis) = swlsp_lsp_phpanalysis::for_document(php_index, document)? else {
            return Ok(Vec::new());
        };
        let semantic = &analysis.document;
        let snapshot = Some(analysis.snapshot.as_ref());
        let source = document.source();
        let mut result = Vec::new();
        result.extend(self.entity_extension_problems(ctx, &root, snapshot));
        result.extend(self.reverse_proxy_problems(ctx, &root, snapshot));
        result.extend(self.context_metadata_problems(ctx, &root, semantic, snapshot));
        result.extend(self.faker_property_problems(ctx, &root, semantic, snapshot));
        result.extend(self.api_migration_problems(ctx, &root, semantic, snapshot, source));
        result.extend(self.argument_migration_problems(ctx, &root, semantic, snapshot, source));
        result.extend(self.declaration_migration_problems(ctx, &root, snapshot));
        result.extend(self.route_annotation_migration_problems(ctx, &root));
        result.extend(self.message_handler_migration_problems(ctx, &root));
        if self.at_least(7, 0) {
            result.extend(self.scheduled_task_logger_problems(ctx, &root, snapshot));
        }
        if !self.at_least(8, 0) {
            return Ok(result);
        }
        result.extend(self.product_stream_builder_problems(ctx, &root, semantic, snapshot, source));

        for call in scan::member_calls(&root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let method = go_to_lower(&phpquery::call_method_name(&call));
            if !ENTITY_SEARCH_RESULT_DELEGATED_METHODS.contains(&method.as_str()) {
                continue;
            }
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !php_type_is_subtype(
                &semantic.type_of(&receiver).ty,
                snapshot,
                ENTITY_SEARCH_RESULT_CLASS,
            ) {
                continue;
            }
            result.push(migration_problem(
                ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE,
                call_name_range(&call),
                &call,
                format!(
                    "Shopware 6.8: delegate EntitySearchResult::{method}() through getEntities()"
                ),
                &ShopwareMigrationPayload {
                    rule: "entity-search-result-get-entities".into(),
                    kind: "method".into(),
                    safe: true,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }

        for call in phpquery::nodes(&root, &[PHP_FUNCTION_CALL]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let call_name = phpquery::call_name(&call);
            let name = go_to_lower(call_name.strip_prefix('\\').unwrap_or(&call_name));
            if !ENTITY_SEARCH_RESULT_DELEGATED_FUNCTIONS.contains(&name.as_str()) {
                continue;
            }
            let Some(expression) = phpquery::argument(&call, 0)
                .and_then(|argument| php_argument_expression(&argument))
            else {
                continue;
            };
            if !php_type_is_subtype(
                &semantic.type_of(&expression).ty,
                snapshot,
                ENTITY_SEARCH_RESULT_CLASS,
            ) {
                continue;
            }
            result.push(migration_problem(
                ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE,
                expression.range_trimmed_trivia(),
                &call,
                format!("Shopware 6.8: pass EntitySearchResult::getEntities() to {name}()"),
                &ShopwareMigrationPayload {
                    rule: "entity-search-result-get-entities".into(),
                    kind: "function".into(),
                    safe: can_append_php_member_call(Some(&expression)),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }

        for foreach in phpquery::nodes(&root, &[PHP_FOREACH_STATEMENT]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(expression) = php_foreach_expression(&foreach) else {
                continue;
            };
            if !php_type_is_subtype(
                &semantic.type_of(&expression).ty,
                snapshot,
                ENTITY_SEARCH_RESULT_CLASS,
            ) {
                continue;
            }
            result.push(migration_problem(
                ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE,
                expression.range_trimmed_trivia(),
                &foreach,
                "Shopware 6.8: iterate EntitySearchResult::getEntities()".to_owned(),
                &ShopwareMigrationPayload {
                    rule: "entity-search-result-get-entities".into(),
                    kind: "foreach".into(),
                    safe: can_append_php_member_call(Some(&expression)),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        Ok(result)
    }
}

impl ShopwareMigrationAnalyzer {
    fn entity_extension_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        let remove_legacy = self.at_least(7, 0);
        for class in scan::classes(root) {
            if ctx.is_cancelled()
                || !php_class_is_subtype(&class, root, snapshot, ENTITY_EXTENSION_CLASS)
            {
                continue;
            }
            let get_entity_name = php_own_method_for_migration(&class, "getEntityName");
            let get_definition_class = php_own_method_for_migration(&class, "getDefinitionClass");
            let missing_entity_name = get_entity_name.is_none();
            let legacy_definition = remove_legacy && get_definition_class.is_some();
            if !missing_entity_name && !legacy_definition {
                continue;
            }
            let replacement = get_definition_class
                .as_ref()
                .map(entity_name_constant_reference)
                .unwrap_or_default();
            let safe = !missing_entity_name || !replacement.is_empty();
            let message = if legacy_definition && !missing_entity_name {
                "Shopware 6.7: remove legacy EntityExtension::getDefinitionClass()"
            } else if legacy_definition {
                "Shopware 6.7: replace EntityExtension::getDefinitionClass() with getEntityName()"
            } else {
                "Shopware: EntityExtension must implement getEntityName()"
            };
            result.push(migration_problem(
                ENTITY_EXTENSION_ENTITY_NAME_CODE,
                class_name_range(&class),
                &class,
                message.to_owned(),
                &ShopwareMigrationPayload {
                    rule: "entity-extension-entity-name".into(),
                    kind: "class".into(),
                    safe,
                    replacement,
                    remove_legacy: legacy_definition,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    fn reverse_proxy_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled()
                || !php_class_is_subtype(&class, root, snapshot, REVERSE_PROXY_GATEWAY_CLASS)
                || php_own_method_for_migration(&class, "banAll").is_some()
            {
                continue;
            }
            result.push(migration_problem(
                REVERSE_PROXY_BAN_ALL_CODE,
                class_name_range(&class),
                &class,
                "Shopware: reverse proxy gateways must implement banAll()".to_owned(),
                &ShopwareMigrationPayload {
                    rule: "reverse-proxy-ban-all".into(),
                    kind: "class".into(),
                    safe: true,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    fn scheduled_task_logger_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        let resolver = scan::resolver(root);
        for class in scan::classes(root) {
            if ctx.is_cancelled()
                || !php_class_is_subtype(&class, root, snapshot, SCHEDULED_TASK_HANDLER_CLASS)
            {
                continue;
            }
            let Some(constructor) = php_own_method_for_migration(&class, "__construct") else {
                continue;
            };
            if scheduled_task_parent_constructor_calls(&constructor).is_empty() {
                continue;
            }
            let mut parameter_exists = false;
            let mut parameter_compatible = false;
            for parameter in phpquery::parameters(&constructor) {
                if !equal_fold(&phpquery::parameter_name(&parameter), "$exceptionLogger") {
                    continue;
                }
                parameter_exists = true;
                let resolved = resolver.resolve(&phpquery::parameter_type(&parameter));
                let parameter_type = resolved.strip_prefix('\\').unwrap_or(&resolved);
                parameter_compatible = equal_fold(parameter_type, LOGGER_INTERFACE_CLASS);
                break;
            }
            let safe = !parameter_exists || parameter_compatible;
            result.push(migration_problem(
                SCHEDULED_TASK_LOGGER_CODE,
                class_name_range(&class),
                &class,
                "Shopware 6.7: pass an exception logger to ScheduledTaskHandler::__construct()"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "scheduled-task-exception-logger".into(),
                    kind: "class".into(),
                    safe,
                    add_parameter: !parameter_exists,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }
}

/// Go `scheduledTaskParentConstructorCalls`: `parent::__construct(x)` calls
/// with exactly one argument directly inside `constructor`.
pub(crate) fn scheduled_task_parent_constructor_calls(constructor: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    for call in phpquery::nodes(constructor, &[PHP_SCOPED_CALL]) {
        if phpquery::function_like_at(&call).as_ref() != Some(constructor)
            || !equal_fold(&phpquery::call_method_name(&call), "__construct")
            || phpquery::arguments(&call).count() != 1
        {
            continue;
        }
        if let Some(receiver) = phpquery::call_receiver(&call)
            && equal_fold(node_text(&receiver).trim(), "parent")
        {
            result.push(call);
        }
    }
    result
}

/// Go `phpClassIsSubtype`: whether the namespaced class declaration is a
/// subtype of `target`.
pub(crate) fn php_class_is_subtype(
    class: &SyntaxNode,
    root: &SyntaxNode,
    snapshot: Option<&Snapshot>,
    target: &str,
) -> bool {
    let Some(snapshot) = snapshot else {
        return false;
    };
    let mut name = phpquery::class_name(class);
    if name.is_empty() {
        return false;
    }
    let namespace = scan::namespace(root);
    if !namespace.is_empty() {
        name = format!("{namespace}\\{name}");
    }
    snapshot.is_subtype_of(&name, target)
}

/// Go `phpOwnMethodForMigration`: the class's own method named `name`
/// (case-insensitive).
pub(crate) fn php_own_method_for_migration(class: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| equal_fold(&phpquery::method_name(method), name))
}

/// Go `entityNameConstantReference`: `Foo::ENTITY_NAME` when every direct
/// return of the method is the same `Foo::class`.
fn entity_name_constant_reference(method: &SyntaxNode) -> String {
    let mut result = String::new();
    for php_return in phpquery::nodes(method, &[PHP_RETURN_STATEMENT]) {
        if phpquery::function_like_at(&php_return).as_ref() != Some(method) {
            continue;
        }
        let Some(expression) = direct_return_expression(&php_return) else {
            return String::new();
        };
        if expression.kind() != PHP_SCOPED_ACCESS && expression.kind() != PHP_MEMBER_ACCESS {
            return String::new();
        }
        let text = node_text(&expression);
        let value = text.trim();
        let separator = value.rfind("::");
        let Some(separator) = separator.filter(|separator| *separator > 0) else {
            return String::new();
        };
        if !equal_fold(value[separator + 2..].trim(), "class") {
            return String::new();
        }
        let candidate = format!("{}::ENTITY_NAME", value[..separator].trim());
        if !result.is_empty() && result != candidate {
            return String::new();
        }
        result = candidate;
    }
    result
}

/// Go `phpArgumentExpression`: the last child node of an argument.
pub(crate) fn php_argument_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    argument.children().last()
}

/// Go `phpForeachExpression`: the first child node of a foreach statement.
pub(crate) fn php_foreach_expression(foreach: &SyntaxNode) -> Option<SyntaxNode> {
    foreach.children().next()
}

/// Go `canAppendPHPMemberCall`: whether `->getEntities()` can be appended.
pub(crate) fn can_append_php_member_call(expression: Option<&SyntaxNode>) -> bool {
    expression.is_some_and(|expression| {
        matches!(
            expression.kind(),
            PHP_VARIABLE
                | PHP_MEMBER_CALL
                | PHP_SCOPED_CALL
                | PHP_FUNCTION_CALL
                | PHP_MEMBER_ACCESS
                | PHP_SCOPED_ACCESS
                | PHP_ARRAY_ACCESS
                | PHP_OBJECT_CREATION
                | PHP_PARENTHESIZED
        )
    })
}
