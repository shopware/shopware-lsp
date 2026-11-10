//! The Shopware migration and `shopware.criteria` inspections and their quick
//! fixes.

mod annotations;
mod api;
mod arguments;
mod calls;
mod criteria;
mod declarations;
mod message_handler;
mod replacements;

#[cfg(test)]
mod tests;

use std::sync::Arc;

use crate::diagnostics::{
    CodeGroup, SharedAnalysis, ShopwareMigrationAnalyzer, ShopwareMigrationPayload,
};
use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution,
    Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix,
    RewriteQuickFix, TextDocument, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY_ACCESS, PHP_CLASS_DECLARATION, PHP_FOREACH_STATEMENT, PHP_FUNCTION_CALL,
    PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_OBJECT_CREATION, PHP_PARENTHESIZED, PHP_SCOPED_ACCESS,
    PHP_SCOPED_CALL, PHP_VARIABLE, TK_NULLSAFE_OBJECT_OPERATOR,
};
use swlsp_php::PhpIndex;
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::{Builder, DocumentPlan, Edit, WorkspacePlan};
use swlsp_shopware::ResolvedVersion;
use swlsp_syntax::{Kind, SyntaxElement, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::inspections::inspection::{BindProblems, BoundInspection};
use crate::inspections::resolve_anchor;

pub use criteria::new_shopware_criteria;

pub(crate) const ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID: &str =
    "shopware-rector-entity-search-result-get-entities";
pub(crate) const ENTITY_EXTENSION_ENTITY_NAME_FIX_ID: &str =
    "shopware-rector-entity-extension-entity-name";
pub(crate) const REVERSE_PROXY_BAN_ALL_FIX_ID: &str = "shopware-rector-reverse-proxy-ban-all";
pub(crate) const SCHEDULED_TASK_LOGGER_FIX_ID: &str =
    "shopware-rector-scheduled-task-exception-logger";

/// The migration codes, one list per inspection.
const API_CODES: &[&str] = &[
    "shopware.migration.api.class.rename",
    "shopware.migration.api.method.rename",
    "shopware.migration.api.static_method.rename",
    "shopware.migration.api.constant.rename",
    "shopware.migration.api.property",
    "shopware.migration.api.exception_factory",
];
const SIGNATURE_CODES: &[&str] = &[
    "shopware.migration.declaration.interface_to_abstract",
    "shopware.migration.declaration.parameter.add",
    "shopware.migration.declaration.type",
    "shopware.migration.arguments.remove",
    "shopware.migration.arguments.add_default",
    "shopware.migration.thumbnail.generate",
];
const BEHAVIOR_CODES: &[&str] = &[
    "shopware.migration.entity_search_result.get_entities",
    "shopware.migration.entity_extension.entity_name",
    "shopware.migration.reverse_proxy.ban_all",
    "shopware.migration.scheduled_task.exception_logger",
    "shopware.migration.context_metadata.state",
    "shopware.migration.faker.property_call",
    "shopware.migration.product_stream.enrich_criteria",
    "shopware.migration.annotation.route_defaults",
    "shopware.migration.message_handler.subscriber",
    "shopware.migration.cart_behavior.permission",
    "shopware.migration.price_definition.filterable",
    "shopware.migration.reflection_helper.native",
];

/// The Shopware migration inspections, split by kind of change. They share one
/// run of [`ShopwareMigrationAnalyzer`] per document.
pub fn new_shopware_migration_inspections(
    php_index: Option<PhpIndex>,
    version: ResolvedVersion,
) -> Vec<Arc<dyn Inspection>> {
    let shared = SharedAnalysis::new(ShopwareMigrationAnalyzer::new(php_index, version));
    let inspection = |id: &str,
                      description: &'static str,
                      codes: &'static [&'static str]|
     -> Arc<dyn Inspection> {
        let bind: BindProblems = Arc::new(bind_migration_fix);
        Arc::new(BoundInspection {
            definition: InspectionDefinition {
                id: id.to_owned(),
                description,
                domain: "shopware.migrations",
                scope: InspectionScope::Framework,
                languages: vec![LanguageId::Php],
                problems: codes
                    .iter()
                    .map(|code| ProblemDefinition {
                        id: DiagnosticId::from(*code),
                        source: "shopware-rector".to_owned(),
                        default_severity: protocol::DiagnosticSeverity::WARNING,
                        disabled_by_default: false,
                    })
                    .collect(),
            },
            analyzers: vec![Arc::new(CodeGroup::new(shared.clone(), codes))],
            // `bind_migration_fix` binds only the fixes its codes use.
            fixes: migration_fixes(),
            bind: Some(bind),
        })
    };
    vec![
        inspection(
            "shopware.migration.api",
            "Shopware API that the upgrade to the target version renames or removes: classes (including the moves the installed Shopware announces), methods, static methods, constants, properties and exception factories, with automated fixes where the change is safe.",
            API_CODES,
        ),
        inspection(
            "shopware.migration.signature",
            "Declarations and calls whose signature changes with the upgrade to the target version: interfaces replaced by abstract classes, added or removed parameters and arguments, and changed native types, including the changes the installed Shopware announces through its BCChange attributes.",
            SIGNATURE_CODES,
        ),
        inspection(
            "shopware.migration.behavior",
            "Code relying on behavior the upgrade to the target version replaces: entity search results, entity extension names, reverse proxy invalidation, scheduled task logging, context metadata, Faker properties, product stream criteria, route defaults, message handlers, cart recalculation checks, filterable price definitions and the reflection test helper.",
            BEHAVIOR_CODES,
        ),
    ]
}

fn migration_fixes() -> Vec<Arc<dyn QuickFix>> {
    vec![
        Arc::new(EntitySearchResultGetEntitiesFix),
        Arc::new(EntityExtensionEntityNameFix),
        Arc::new(ReverseProxyBanAllFix),
        Arc::new(ScheduledTaskLoggerFix),
        Arc::new(calls::ContextMetadataStateFix),
        Arc::new(calls::FakerPropertyCallFix),
        Arc::new(calls::ProductStreamEnrichCriteriaFix),
        Arc::new(api::ApiRenameFix),
        Arc::new(arguments::RemoveArgumentMigrationFix),
        Arc::new(arguments::AddDefaultArgumentFix),
        Arc::new(arguments::ThumbnailGenerateFix),
        Arc::new(declarations::DeclarationMigrationFix),
        Arc::new(annotations::RouteAnnotationMigrationFix),
        Arc::new(message_handler::MessageHandlerSubscriberFix),
        Arc::new(replacements::CartBehaviorPermissionFix),
        Arc::new(replacements::FilterablePriceDefinitionFix),
        Arc::new(replacements::ReflectionHelperFix),
    ]
}

fn bind_migration_fix(code: &DiagnosticId, payload: &Map<String, Value>) -> Vec<BoundFix> {
    if payload.get("safe") != Some(&Value::Bool(true)) {
        return Vec::new();
    }
    let fix_id = match code.as_str() {
        "shopware.migration.entity_search_result.get_entities" => {
            ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID
        }
        "shopware.migration.entity_extension.entity_name" => ENTITY_EXTENSION_ENTITY_NAME_FIX_ID,
        "shopware.migration.reverse_proxy.ban_all" => REVERSE_PROXY_BAN_ALL_FIX_ID,
        "shopware.migration.scheduled_task.exception_logger" => SCHEDULED_TASK_LOGGER_FIX_ID,
        "shopware.migration.context_metadata.state" => calls::CONTEXT_METADATA_STATE_FIX_ID,
        "shopware.migration.faker.property_call" => calls::FAKER_PROPERTY_CALL_FIX_ID,
        "shopware.migration.product_stream.enrich_criteria" => {
            calls::PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID
        }
        "shopware.migration.api.class.rename"
        | "shopware.migration.api.method.rename"
        | "shopware.migration.api.static_method.rename"
        | "shopware.migration.api.constant.rename"
        | "shopware.migration.api.property"
        | "shopware.migration.api.exception_factory" => api::API_RENAME_FIX_ID,
        "shopware.migration.arguments.remove" => arguments::REMOVE_ARGUMENT_MIGRATION_FIX_ID,
        "shopware.migration.arguments.add_default" => arguments::ADD_DEFAULT_ARGUMENT_FIX_ID,
        "shopware.migration.thumbnail.generate" => arguments::THUMBNAIL_GENERATE_FIX_ID,
        "shopware.migration.declaration.interface_to_abstract"
        | "shopware.migration.declaration.parameter.add"
        | "shopware.migration.declaration.type" => declarations::DECLARATION_MIGRATION_FIX_ID,
        "shopware.migration.annotation.route_defaults" => {
            annotations::ROUTE_ANNOTATION_MIGRATION_FIX_ID
        }
        "shopware.migration.message_handler.subscriber" => {
            message_handler::MESSAGE_HANDLER_SUBSCRIBER_FIX_ID
        }
        "shopware.migration.cart_behavior.permission" => {
            replacements::CART_BEHAVIOR_PERMISSION_FIX_ID
        }
        "shopware.migration.price_definition.filterable" => {
            replacements::FILTERABLE_PRICE_DEFINITION_FIX_ID
        }
        "shopware.migration.reflection_helper.native" => replacements::REFLECTION_HELPER_FIX_ID,
        _ => return Vec::new(),
    };
    vec![bind_fix(fix_id, Value::Object(payload.clone()))]
}

// ---------------------------------------------------------------------------
// Shared fix helpers

/// The presentation every migration fix uses.
pub(crate) fn quick_fix_presentation(title: &str) -> FixPresentation {
    FixPresentation {
        title: title.to_owned(),
        kind: Some(protocol::CodeActionKind::QUICKFIX),
        preferred: true,
        resolution: FixResolution::Eager,
    }
}

/// Decodes the bound payload and presents `title` when it is a safe payload of
/// `rule`.
pub(crate) fn present_migration(
    fix: &FixContext,
    rule: &str,
    title: impl FnOnce(&ShopwareMigrationPayload) -> &'static str,
) -> Result<Option<FixPresentation>, BoxError> {
    let payload: ShopwareMigrationPayload = fix.fix_payload()?;
    if !payload.safe || payload.rule != rule {
        return Ok(None);
    }
    Ok(Some(quick_fix_presentation(title(&payload))))
}

/// The bound payload of a migration fix, which must be a safe payload of
/// `rule`.
pub(crate) fn safe_payload(
    fix: &FixContext,
    rule: &str,
    unsafe_message: &str,
) -> Result<ShopwareMigrationPayload, BoxError> {
    let payload: ShopwareMigrationPayload = fix.fix_payload()?;
    if !payload.safe || payload.rule != rule {
        return Err(unsafe_message.into());
    }
    Ok(payload)
}

pub(crate) fn invalid(message: impl Into<String>) -> BoxError {
    message.into().into()
}

/// The element or its nearest ancestor node of `kind`.
pub(crate) fn ancestor_node(element: &SyntaxElement, kind: Kind) -> Option<SyntaxNode> {
    element_ancestors(element).find(|node| node.kind() == kind)
}

/// The element (when a node) and its ancestors.
pub(crate) fn element_ancestors(element: &SyntaxElement) -> impl Iterator<Item = SyntaxNode> {
    let start = match element {
        SyntaxElement::Node(node) => Some(node.clone()),
        SyntaxElement::Token(token) => token.parent(),
    };
    start.into_iter().flat_map(|node| node.ancestors())
}

pub(crate) fn document_root(document: &TextDocument) -> Result<SyntaxNode, BoxError> {
    document
        .root()
        .ok_or_else(|| invalid("shopware migration document has no syntax tree"))
}

/// The class enclosing the fix anchor.
pub(crate) fn resolve_migration_class(
    fix: &FixContext,
) -> Result<(SyntaxNode, SyntaxNode), BoxError> {
    let element = resolve_anchor(fix)?;
    let class = ancestor_node(&element, PHP_CLASS_DECLARATION)
        .ok_or_else(|| invalid("shopware migration class is unavailable"))?;
    Ok((class, document_root(&fix.document)?))
}

pub(crate) fn resolve_migration_php_node(
    fix: &FixContext,
    kind: Kind,
) -> Result<(SyntaxNode, SyntaxNode), BoxError> {
    let element = resolve_anchor(fix)?;
    let node = ancestor_node(&element, kind)
        .ok_or_else(|| invalid("shopware migration target is unavailable"))?;
    Ok((node, document_root(&fix.document)?))
}

pub(crate) fn php_own_method_for_migration_fix(
    class: &SyntaxNode,
    name: &str,
) -> Option<SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| eq_ignore_case(&phpquery::method_name(method), name))
}

/// Applies `edits`, rejects results that do not parse, and returns the
/// single-document plan (shared by the text-edit and builder-based fixes).
pub(crate) fn finish_edits(
    fix: &FixContext,
    edits: Vec<Edit>,
    invalid_message: &str,
) -> Result<WorkspacePlan, BoxError> {
    let document = &fix.document;
    let updated = swlsp_rewrite::apply(document.source(), &edits)?;
    if !TextDocument::new(document.uri.clone(), &updated, document.version + 1)
        .parse_errors()
        .is_empty()
    {
        return Err(invalid(invalid_message));
    }
    Ok(single_document_plan(document, edits))
}

pub(crate) fn single_document_plan(
    document: &Arc<TextDocument>,
    edits: Vec<Edit>,
) -> WorkspacePlan {
    WorkspacePlan {
        documents: vec![
            DocumentPlan::new(
                document.uri.clone(),
                Some(document.version),
                document.source_arc().clone(),
                edits,
            )
            .with_line_index(document.line_index_arc().clone()),
        ],
        ..WorkspacePlan::default()
    }
}

pub(crate) fn finish_php_rewrite(
    fix: &FixContext,
    editor: &Editor,
) -> Result<WorkspacePlan, BoxError> {
    // The migration may replace the last use of an imported name.
    let edits =
        swlsp_php_imports::remove_orphaned_imports(fix.document.source(), editor.finish()?)?;
    finish_edits(fix, edits, "shopware migration produced invalid PHP")
}

// ---------------------------------------------------------------------------
// EntitySearchResult::getEntities()

struct EntitySearchResultGetEntitiesFix;

impl QuickFix for EntitySearchResultGetEntitiesFix {
    fn id(&self) -> FixId {
        FixId::from(ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "entity-search-result-get-entities",
            |_| "Shopware 6.8: Delegate through getEntities()",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for EntitySearchResultGetEntitiesFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "entity-search-result-get-entities",
            "EntitySearchResult rewrite is no longer safe",
        )?;
        let element = resolve_anchor(fix)?;
        let SyntaxElement::Node(node) = element else {
            return Err(invalid(
                "EntitySearchResult migration target is unavailable",
            ));
        };
        let mut builder = Builder::new(fix.document.source());
        match payload.kind.as_str() {
            "method" => rewrite_entity_search_result_method(&mut builder, &node)?,
            "function" => {
                if node.kind() != PHP_FUNCTION_CALL {
                    return Err(invalid("EntitySearchResult function call changed"));
                }
                let expression =
                    phpquery::argument(&node, 0).and_then(|argument| argument.children().last());
                let Some(expression) = expression.filter(can_append_php_member_call_for_fix) else {
                    return Err(invalid("EntitySearchResult function argument changed"));
                };
                builder.insert(
                    expression.range_trimmed_trivia().end().into(),
                    "->getEntities()",
                )?;
            }
            "foreach" => {
                if node.kind() != PHP_FOREACH_STATEMENT {
                    return Err(invalid("EntitySearchResult foreach changed"));
                }
                let Some(expression) = node
                    .children()
                    .next()
                    .filter(can_append_php_member_call_for_fix)
                else {
                    return Err(invalid("EntitySearchResult foreach expression changed"));
                };
                builder.insert(
                    expression.range_trimmed_trivia().end().into(),
                    "->getEntities()",
                )?;
            }
            other => {
                return Err(invalid(format!(
                    "unknown EntitySearchResult migration target {other:?}"
                )));
            }
        }
        let edits = builder.finish()?;
        finish_edits(
            fix,
            edits,
            "EntitySearchResult rewrite produced invalid PHP",
        )
    }
}

fn rewrite_entity_search_result_method(
    builder: &mut Builder,
    call: &SyntaxNode,
) -> Result<(), BoxError> {
    if call.kind() != PHP_MEMBER_CALL {
        return Err(invalid("EntitySearchResult method call changed"));
    }
    let method = lowercase(&phpquery::call_method_name(call));
    if !entity_search_result_delegated_method_for_fix(&method) {
        return Err(invalid(format!(
            "EntitySearchResult method {method:?} is not delegated"
        )));
    }
    let Some(receiver) = phpquery::call_receiver(call) else {
        return Err(invalid("EntitySearchResult receiver is unavailable"));
    };
    let end: u32 = receiver.range_trimmed_trivia().end().into();
    if let Some(operator) = call.child_token_of_kind(TK_NULLSAFE_OBJECT_OPERATOR) {
        builder.insert(end, "?->getEntities()")?;
        builder.replace_range(operator.text_range(), "->")?;
        return Ok(());
    }
    builder.insert(end, "->getEntities()")?;
    Ok(())
}

fn entity_search_result_delegated_method_for_fix(method: &str) -> bool {
    matches!(
        method,
        "count"
            | "fill"
            | "filter"
            | "filterandreducebyproperty"
            | "filterbyproperty"
            | "filterinstance"
            | "first"
            | "firstwhere"
            | "flatmap"
            | "fmap"
            | "get"
            | "getat"
            | "getcustomfieldsvalue"
            | "getcustomfieldsvalues"
            | "getelements"
            | "getids"
            | "getkeys"
            | "getlist"
            | "has"
            | "insert"
            | "isempty"
            | "last"
            | "map"
            | "merge"
            | "reduce"
            | "remove"
            | "set"
            | "setcustomfields"
            | "slice"
            | "sort"
            | "sortbyidarray"
    )
}

fn can_append_php_member_call_for_fix(expression: &SyntaxNode) -> bool {
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
}

// ---------------------------------------------------------------------------
// EntityExtension, reverse proxy and scheduled task fixes

struct EntityExtensionEntityNameFix;

impl QuickFix for EntityExtensionEntityNameFix {
    fn id(&self) -> FixId {
        FixId::from(ENTITY_EXTENSION_ENTITY_NAME_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "entity-extension-entity-name",
            |_| "Shopware: Migrate EntityExtension entity name",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for EntityExtensionEntityNameFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "entity-extension-entity-name",
            "EntityExtension rewrite is no longer safe",
        )?;
        let (class, root) = resolve_migration_class(fix)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        let entity_name = php_own_method_for_migration_fix(&class, "getEntityName");
        let definition = php_own_method_for_migration_fix(&class, "getDefinitionClass");
        if entity_name.is_none() {
            if payload.replacement.is_empty() {
                return Err(invalid("EntityExtension entity name is unavailable"));
            }
            editor.insert_class_member(
                &class,
                &format!(
                    "\npublic function getEntityName(): string\n{{\n    return {};\n}}",
                    payload.replacement
                ),
            )?;
        }
        if payload.remove_legacy
            && let Some(definition) = &definition
        {
            editor.remove_class_member(definition)?;
        }
        finish_php_rewrite(fix, &editor)
    }
}

struct ReverseProxyBanAllFix;

impl QuickFix for ReverseProxyBanAllFix {
    fn id(&self) -> FixId {
        FixId::from(REVERSE_PROXY_BAN_ALL_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "reverse-proxy-ban-all",
            |_| "Shopware: Add reverse proxy banAll()",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ReverseProxyBanAllFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        safe_payload(
            fix,
            "reverse-proxy-ban-all",
            "reverse proxy rewrite is no longer safe",
        )?;
        let (class, root) = resolve_migration_class(fix)?;
        if php_own_method_for_migration_fix(&class, "banAll").is_some() {
            return Err(invalid("reverse proxy banAll() already exists"));
        }
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        editor.insert_class_member(
            &class,
            "\npublic function banAll(): void\n{\n    $this->ban([]);\n}",
        )?;
        finish_php_rewrite(fix, &editor)
    }
}

struct ScheduledTaskLoggerFix;

impl QuickFix for ScheduledTaskLoggerFix {
    fn id(&self) -> FixId {
        FixId::from(SCHEDULED_TASK_LOGGER_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "scheduled-task-exception-logger",
            |_| "Shopware 6.7: Add ScheduledTaskHandler exception logger",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ScheduledTaskLoggerFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "scheduled-task-exception-logger",
            "scheduled task logger rewrite is no longer safe",
        )?;
        let (class, root) = resolve_migration_class(fix)?;
        let Some(constructor) = php_own_method_for_migration_fix(&class, "__construct") else {
            return Err(invalid("scheduled task constructor is unavailable"));
        };
        let calls = scheduled_task_parent_constructor_calls_for_fix(&constructor);
        if calls.is_empty() {
            return Err(invalid("scheduled task parent constructor call changed"));
        }
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        if payload.add_parameter {
            let qualifier = editor.class_reference("Psr\\Log\\LoggerInterface")?;
            let index = required_parameter_insertion_index(&constructor);
            editor.insert_parameter(
                &constructor,
                index,
                &format!("{qualifier} $exceptionLogger"),
            )?;
        }
        for call in &calls {
            editor.append_argument(call, "$exceptionLogger")?;
        }
        finish_php_rewrite(fix, &editor)
    }
}

fn scheduled_task_parent_constructor_calls_for_fix(constructor: &SyntaxNode) -> Vec<SyntaxNode> {
    phpquery::nodes(constructor, &[PHP_SCOPED_CALL])
        .into_iter()
        .filter(|call| {
            phpquery::function_like_at(call).as_ref() == Some(constructor)
                && eq_ignore_case(&phpquery::call_method_name(call), "__construct")
                && phpquery::arguments(call).count() == 1
                && phpquery::call_receiver(call).is_some_and(|receiver| {
                    eq_ignore_case(receiver.text().to_string().trim(), "parent")
                })
        })
        .collect()
}

fn required_parameter_insertion_index(constructor: &SyntaxNode) -> usize {
    let mut index = 0;
    for parameter in phpquery::parameters(constructor) {
        if phpquery::parameter_optional(&parameter) || phpquery::parameter_variadic(&parameter) {
            return index;
        }
        index += 1;
    }
    index
}
