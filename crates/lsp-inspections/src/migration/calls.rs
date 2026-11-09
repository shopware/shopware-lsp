//! Context metadata, Faker and ProductStream fixes.

use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_lsp_diagnostics::common::call_target_name;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_MEMBER_ACCESS, PHP_MEMBER_CALL};
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::{Builder, WorkspacePlan};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::eq_ignore_case;

use super::{
    finish_edits, finish_php_rewrite, invalid, present_migration, resolve_migration_php_node,
    safe_payload,
};

pub(crate) const CONTEXT_METADATA_STATE_FIX_ID: &str = "shopware-rector-context-metadata-state";
pub(crate) const FAKER_PROPERTY_CALL_FIX_ID: &str = "shopware-rector-faker-property-call";
pub(crate) const PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID: &str =
    "shopware-rector-product-stream-enrich-criteria";

pub(crate) struct ContextMetadataStateFix;

impl QuickFix for ContextMetadataStateFix {
    fn id(&self) -> FixId {
        FixId::from(CONTEXT_METADATA_STATE_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "context-metadata-state",
            |_| "Shopware 6.5: Migrate Context extension flag to state",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ContextMetadataStateFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        safe_payload(
            fix,
            "context-metadata-state",
            "context state rewrite is no longer safe",
        )?;
        let (call, root) = resolve_migration_php_node(fix, PHP_MEMBER_CALL)?;
        if !eq_ignore_case(&phpquery::call_method_name(&call), "addExtension")
            || phpquery::arguments(&call).count() != 2
        {
            return Err(invalid("context addExtension() call changed"));
        }
        let Some(name) = call_target_name(&call) else {
            return Err(invalid("context addExtension() name is unavailable"));
        };
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        editor.replace_range(name.range_trimmed_trivia(), "addState")?;
        editor.remove_argument(&call, 1)?;
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct FakerPropertyCallFix;

impl QuickFix for FakerPropertyCallFix {
    fn id(&self) -> FixId {
        FixId::from(FAKER_PROPERTY_CALL_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "faker-property-call",
            |_| "Shopware 6.5: Call Faker formatter as a method",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for FakerPropertyCallFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        safe_payload(
            fix,
            "faker-property-call",
            "faker property rewrite is no longer safe",
        )?;
        let (access, root) = resolve_migration_php_node(fix, PHP_MEMBER_ACCESS)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        editor.insert(access.range_trimmed_trivia().end().into(), "()")?;
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct ProductStreamEnrichCriteriaFix;

impl QuickFix for ProductStreamEnrichCriteriaFix {
    fn id(&self) -> FixId {
        FixId::from(PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(fix, "product-stream-enrich-criteria", |payload| {
            if payload.kind == "manual" {
                "Shopware 6.8: Add buildFilters() migration TODO"
            } else {
                "Shopware 6.8: Replace buildFilters() with enrichCriteria()"
            }
        })
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ProductStreamEnrichCriteriaFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "product-stream-enrich-criteria",
            "ProductStream rewrite is no longer safe",
        )?;
        let (call, _) = resolve_migration_php_node(fix, PHP_MEMBER_CALL)?;
        if !eq_ignore_case(&phpquery::call_method_name(&call), "buildFilters")
            || payload.replacement.is_empty()
        {
            return Err(invalid("ProductStream migration target changed"));
        }
        let source = fix.document.source();
        let mut builder = Builder::new(source);
        match payload.kind.as_str() {
            "assignment" | "inline" => {
                if payload.start >= payload.end || payload.end as usize > source.len() {
                    return Err(invalid("ProductStream migration target changed"));
                }
                builder.replace_range(
                    TextRange::new(TextSize::from(payload.start), TextSize::from(payload.end)),
                    payload.replacement.clone(),
                )?;
            }
            "manual" => {
                if payload.start as usize > source.len() {
                    return Err(invalid("ProductStream migration target changed"));
                }
                builder.insert(payload.start, payload.replacement.clone())?;
            }
            other => {
                return Err(invalid(format!(
                    "unknown ProductStream migration target {other:?}"
                )));
            }
        }
        let edits = builder.finish()?;
        finish_edits(fix, edits, "ProductStream rewrite produced invalid PHP")
    }
}
