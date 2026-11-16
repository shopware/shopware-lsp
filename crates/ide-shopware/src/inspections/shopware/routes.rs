//! The `shopware.php.route` inspection.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use crate::diagnostics::{ROUTE_CODES, ShopwareRouteAnalyzer};
use crate::inspections::inspection::BoundInspection;
use crate::inspections::resolve_anchor;
use swlsp_ide::codeaction::{php_import_block, php_import_insertion_offset};

pub const ROUTE_CONSTANT_FIX_ID: &str = "use-shopware-route-constant";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct RouteConstantPayload {
    #[serde(default)]
    class: String,
    #[serde(default)]
    constant: String,
}

pub fn new_shopware_routes(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.php.route".to_owned(),
            description: "Route attributes that break Shopware's conventions: no route scope, an API route outside of the path of its scope, a storefront route the storefront router does not prefix, no HTTP methods, and route defaults written as strings where Shopware declares constants.",
            domain: "shopware",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php],
            problems: ROUTE_CODES
                .iter()
                .map(|(code, severity)| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: "shopware-lsp".to_owned(),
                    default_severity: *severity,
                    disabled_by_default: false,
                })
                .collect(),
        },
        analyzers: vec![Arc::new(ShopwareRouteAnalyzer::new(php_index))],
        fixes: vec![Arc::new(RouteConstantFix)],
        bind: Some(Arc::new(|_, payload| {
            let field = |name: &str| match payload.get(name) {
                Some(Value::String(value)) => value.clone(),
                _ => String::new(),
            };
            let value = RouteConstantPayload {
                class: field("class"),
                constant: field("constant"),
            };
            if value.class.is_empty() || value.constant.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(ROUTE_CONSTANT_FIX_ID, value)]
        })),
    })
}

/// Replaces the string of a route default with the constant Shopware
/// declares for it, importing the class when the file does not yet.
struct RouteConstantFix;

fn short_name(class: &str) -> &str {
    class.rsplit('\\').next().unwrap_or(class)
}

impl QuickFix for RouteConstantFix {
    fn id(&self) -> FixId {
        FixId::from(ROUTE_CONSTANT_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        let payload: RouteConstantPayload = fix.fix_payload()?;
        if payload.class.is_empty() || payload.constant.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Use {}::{}", short_name(&payload.class), payload.constant),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for RouteConstantFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: RouteConstantPayload = fix.fix_payload()?;
        let stale = || -> BoxError { Box::new(HandleError::Stale) };
        let SyntaxElement::Node(literal) = resolve_anchor(fix)? else {
            return Err(stale());
        };
        if literal.kind() != PHP_STRING {
            return Err(stale());
        }
        let root = literal.ancestors().last().ok_or_else(stale)?;
        let class = payload.class.trim_start_matches('\\');
        let short = short_name(class);
        let namespace = class
            .strip_suffix(short)
            .unwrap_or("")
            .trim_end_matches('\\');
        // What the short name means in this file: the class itself (it is
        // imported, or lives in the namespace of the file), another class
        // imported under the same name, or nothing yet.
        // Each class import as (class, name it is known by).
        let imports: Vec<(String, String)> = phpquery::use_declarations(&root)
            .iter()
            .filter_map(phpquery::use_items)
            .flat_map(|list| list.items)
            .filter_map(|item| {
                let text = item.text.trim().trim_end_matches(';');
                let text = text.strip_prefix("use ")?.trim();
                if text.starts_with("function ") || text.starts_with("const ") {
                    return None;
                }
                let (name, alias) = match text.split_once(" as ") {
                    Some((name, alias)) => (name.trim(), alias.trim()),
                    None => (text, short_name(text)),
                };
                Some((name.trim_start_matches('\\').to_owned(), alias.to_owned()))
            })
            .collect();
        let known_as = imports
            .iter()
            .find(|(name, _)| name.eq_ignore_ascii_case(class))
            .map(|(_, alias)| alias.clone());
        let same_namespace = phpquery::namespace(&root)
            .trim_matches('\\')
            .eq_ignore_ascii_case(namespace);
        let taken = imports
            .iter()
            .any(|(_, alias)| alias.eq_ignore_ascii_case(short));
        let mut builder = Builder::new(fix.document.source());
        let reference = if let Some(alias) = known_as {
            alias
        } else if same_namespace {
            short.to_owned()
        } else if taken {
            format!("\\{class}")
        } else {
            builder.insert(
                php_import_insertion_offset(&root),
                php_import_block(&root, &[class.to_owned()]),
            )?;
            short.to_owned()
        };
        builder.replace_range(
            literal.range_trimmed_trivia(),
            format!("{reference}::{}", payload.constant),
        )?;
        let edits = builder.finish()?;
        let document = &fix.document;
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
