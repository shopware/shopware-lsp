//! A fix that replaces a string literal with the class constant that
//! holds its value, importing the class when the file does not yet.
//!
//! The analyzer puts `class` (fully qualified) and `constant` in the problem
//! payload; `class` as the constant name writes `Name::class`.

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use swlsp_lsp::{
    BoundFix, BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use crate::codeaction::{php_import_block, php_import_insertion_offset};
use crate::inspections::inspection::resolve_anchor;

pub const CLASS_CONSTANT_FIX_ID: &str = "use-class-constant";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct ClassConstantPayload {
    #[serde(default)]
    class: String,
    #[serde(default)]
    constant: String,
}

/// The fix of a problem whose payload names a `class` and a `constant`.
pub fn class_constant_bound_fixes(payload: &Map<String, Value>) -> Vec<BoundFix> {
    let field = |name: &str| match payload.get(name) {
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    };
    let value = ClassConstantPayload {
        class: field("class"),
        constant: field("constant"),
    };
    if value.class.is_empty() || value.constant.is_empty() {
        return Vec::new();
    }
    vec![bind_fix(CLASS_CONSTANT_FIX_ID, value)]
}

/// Replaces the reported string literal with `Class::CONSTANT`.
#[derive(Debug, Clone, Copy, Default)]
pub struct ClassConstantFix;

fn short_name(class: &str) -> &str {
    class.rsplit('\\').next().unwrap_or(class)
}

impl QuickFix for ClassConstantFix {
    fn id(&self) -> FixId {
        FixId::from(CLASS_CONSTANT_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        let payload: ClassConstantPayload = fix.fix_payload()?;
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

impl RewriteQuickFix for ClassConstantFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ClassConstantPayload = fix.fix_payload()?;
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
