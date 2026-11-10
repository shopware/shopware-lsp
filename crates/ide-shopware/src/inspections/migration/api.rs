//! API rename fix.

use crate::diagnostics::{ShopwareMigrationEdit, ShopwareMigrationPayload};
use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::{TextRange, TextSize};

use super::{document_root, finish_php_rewrite, invalid, present_migration};
use crate::inspections::resolve_anchor;

pub(crate) const API_RENAME_FIX_ID: &str = "shopware-rector-api-rename";

pub(crate) struct ApiRenameFix;

impl QuickFix for ApiRenameFix {
    fn id(&self) -> FixId {
        FixId::from(API_RENAME_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(fix, "api-rename", |payload| match payload.kind.as_str() {
            "class" => "Shopware: Rename class",
            "method" | "static-method" => "Shopware: Rename method",
            "constant" => "Shopware: Move class constant",
            "property" => "Shopware: Replace property access with method call",
            "exception-factory" => "Shopware: Replace exception construction with factory",
            _ => "Shopware: Rename deprecated API",
        })
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ApiRenameFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        let source = fix.document.source();
        if !payload.safe
            || payload.rule != "api-rename"
            || (payload.edits.is_empty()
                && (payload.start >= payload.end
                    || payload.end as usize > source.len()
                    || payload.replacement.is_empty()))
        {
            return Err(invalid("shopware API rename is no longer safe"));
        }
        resolve_anchor(fix)?;
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(source, Some(&root));
        let edits = if payload.edits.is_empty() {
            vec![ShopwareMigrationEdit {
                start: payload.start,
                end: payload.end,
                original: payload.original.clone(),
                replacement: payload.replacement.clone(),
            }]
        } else {
            payload.edits.clone()
        };
        for edit in &edits {
            if edit.start >= edit.end
                || edit.end as usize > source.len()
                || edit.original.is_empty()
                || edit.replacement.is_empty()
                || source.get(edit.start as usize..edit.end as usize)
                    != Some(edit.original.as_str())
            {
                return Err(invalid("shopware API rename target changed"));
            }
            editor.replace_range(
                TextRange::new(TextSize::from(edit.start), TextSize::from(edit.end)),
                edit.replacement.clone(),
            )?;
        }
        finish_php_rewrite(fix, &editor)
    }
}
