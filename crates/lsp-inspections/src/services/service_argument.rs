//! Go `service_argument.go`: the `symfony.service.arguments` inspection.

use std::sync::Arc;

use serde_json::{Map, Value, json};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::ServiceArgumentAnalyzer;
use swlsp_lsp_protocol::{self as protocol, DiagnosticSeverity};
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::SyntaxElement;

use super::service_argument_rewrite::{missing_service_arguments, service_argument_rewrite};
use super::{PhpMethodFix, PhpMethodPayload, definition, map_string};
use crate::inspection::resolve_anchor;
use crate::{BoundInspection, SuggestionFix, suggestion_bound_fixes};

pub const SERVICE_ARGUMENTS_FIX_ID: &str = "add-missing-service-arguments";
pub const SERVICE_METHOD_FIX_ID: &str = "create-service-method";

/// Go `NewServiceArgument`.
pub fn new_service_argument(
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    let method_fix = PhpMethodFix {
        id: SERVICE_METHOD_FIX_ID,
        title_prefix: "service",
        php_index: php_index.clone(),
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "symfony.service.arguments".to_owned(),
            description: "Service definitions missing constructor arguments, passing arguments of the wrong type, naming unknown arguments, or calling methods that do not exist.",
            domain: "symfony.services",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
            problems: vec![
                definition(
                    "symfony.service.arguments.missing",
                    DiagnosticSeverity::WARNING,
                ),
                definition(
                    "symfony.service.method.missing",
                    DiagnosticSeverity::WARNING,
                ),
                definition("symfony.service.argument.type", DiagnosticSeverity::WARNING),
                definition(
                    "symfony.service.named_argument.unknown",
                    DiagnosticSeverity::WARNING,
                ),
            ],
        },
        analyzers: vec![Arc::new(ServiceArgumentAnalyzer::new(
            service_index,
            php_index,
        ))],
        fixes: vec![
            Arc::new(ServiceArgumentFix),
            Arc::new(method_fix),
            Arc::new(SuggestionFix),
        ],
        bind: Some(Arc::new(|code, payload| {
            let mut bound = suggestion_bound_fixes(payload);
            match code.as_str() {
                "symfony.service.arguments.missing" => {
                    if payload.contains_key("missingArguments") {
                        bound.push(bind_fix(SERVICE_ARGUMENTS_FIX_ID, json!({})));
                    }
                }
                "symfony.service.method.missing" => {
                    let method = PhpMethodPayload {
                        class_name: map_string(payload, "className"),
                        method_name: map_string(payload, "methodName"),
                        ..PhpMethodPayload::default()
                    };
                    if !method.class_name.is_empty() && !method.method_name.is_empty() {
                        bound.push(bind_fix(SERVICE_METHOD_FIX_ID, method));
                    }
                }
                _ => {}
            }
            bound
        })),
    })
}

/// Go `serviceArgumentFix`.
pub(crate) struct ServiceArgumentFix;

fn present_value(payload: &Map<String, Value>, key: &str) -> bool {
    !matches!(payload.get(key), None | Some(Value::Null))
}

impl QuickFix for ServiceArgumentFix {
    fn id(&self) -> FixId {
        FixId::from(SERVICE_ARGUMENTS_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: Map<String, Value> = fix.problem_payload()?;
        if !present_value(&payload, "format") || !present_value(&payload, "missingArguments") {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: "Symfony: Add missing service arguments".to_owned(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ServiceArgumentFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let element = resolve_anchor(fix)?;
        let node = match element {
            SyntaxElement::Node(node) => Some(node),
            SyntaxElement::Token(token) => token.parent(),
        };
        let Some(node) = node else {
            return Err("service definition anchor has no node".into());
        };
        let payload: Map<String, Value> = fix.problem_payload()?;
        let arguments = missing_service_arguments(payload.get("missingArguments"));
        let format = payload
            .get("format")
            .and_then(Value::as_str)
            .unwrap_or_default();
        if arguments.is_empty() {
            return Err("missing service arguments are no longer available".into());
        }
        let document = &fix.document;
        let Some((range, new_text)) =
            service_argument_rewrite(document.source(), &node, format, &arguments)
        else {
            return Err("service argument edit is no longer applicable".into());
        };
        let mut builder = Builder::new(document.source());
        builder.replace_range(range, new_text)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
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
        })
    }
}
