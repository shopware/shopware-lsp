//! Argument migration fixes.

use crate::diagnostics::ShopwareMigrationPayload;
use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_parser_php::syntax::{PHP_MEMBER_CALL, PHP_METHOD_DECLARATION, PHP_OBJECT_CREATION};
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::{SyntaxElement, SyntaxNode, TextRange, TextSize};

use super::{
    ancestor_node, document_root, element_ancestors, finish_php_rewrite, invalid, present_migration,
};
use crate::inspections::resolve_anchor;

pub(crate) const REMOVE_ARGUMENT_MIGRATION_FIX_ID: &str = "shopware-rector-remove-argument";
pub(crate) const ADD_DEFAULT_ARGUMENT_FIX_ID: &str = "shopware-rector-add-default-argument";
pub(crate) const THUMBNAIL_GENERATE_FIX_ID: &str = "shopware-rector-thumbnail-generate";

pub(crate) struct RemoveArgumentMigrationFix;

impl QuickFix for RemoveArgumentMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(REMOVE_ARGUMENT_MIGRATION_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "argument-migration",
            |_| "Shopware: Remove obsolete argument",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for RemoveArgumentMigrationFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        if !payload.safe || payload.rule != "argument-migration" || payload.argument_index < 0 {
            return Err(invalid("argument removal is no longer safe"));
        }
        let index = payload.argument_index as usize;
        let element = resolve_anchor(fix)?;
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        match payload.kind.as_str() {
            "call-argument" => {
                let Some(container) = migration_call_or_creation(&element) else {
                    return Err(invalid("argument removal target changed"));
                };
                editor.remove_argument(&container, index)?;
            }
            "constructor-parameter" => {
                let Some(constructor) = ancestor_node(&element, PHP_METHOD_DECLARATION) else {
                    return Err(invalid("constructor parameter target changed"));
                };
                editor.remove_parameter(&constructor, index)?;
            }
            other => {
                return Err(invalid(format!(
                    "unknown argument removal target {other:?}"
                )));
            }
        }
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct AddDefaultArgumentFix;

impl QuickFix for AddDefaultArgumentFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_DEFAULT_ARGUMENT_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "argument-migration",
            |_| "Shopware: Add new default argument",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AddDefaultArgumentFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        if !payload.safe
            || payload.rule != "argument-migration"
            || payload.argument_index < 0
            || payload.value.is_empty()
        {
            return Err(invalid("default argument insertion is no longer safe"));
        }
        let element = resolve_anchor(fix)?;
        let Some(call) = ancestor_node(&element, PHP_MEMBER_CALL) else {
            return Err(invalid("default argument target changed"));
        };
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        editor.insert_argument(&call, payload.argument_index as usize, &payload.value)?;
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct ThumbnailGenerateFix;

impl QuickFix for ThumbnailGenerateFix {
    fn id(&self) -> FixId {
        FixId::from(THUMBNAIL_GENERATE_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "thumbnail-generate",
            |_| "Shopware 6.5: Generate thumbnails as a batch",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ThumbnailGenerateFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        let source = fix.document.source();
        if !payload.safe
            || payload.rule != "thumbnail-generate"
            || payload.start >= payload.end
            || payload.end as usize > source.len()
            || payload.original.is_empty()
            || payload.replacement.is_empty()
            || source.get(payload.start as usize..payload.end as usize)
                != Some(payload.original.as_str())
        {
            return Err(invalid("thumbnail generation target changed"));
        }
        resolve_anchor(fix)?;
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(source, Some(&root));
        editor.replace_range(
            TextRange::new(TextSize::from(payload.start), TextSize::from(payload.end)),
            payload.replacement.clone(),
        )?;
        finish_php_rewrite(fix, &editor)
    }
}

fn migration_call_or_creation(element: &SyntaxElement) -> Option<SyntaxNode> {
    element_ancestors(element)
        .find(|node| matches!(node.kind(), PHP_MEMBER_CALL | PHP_OBJECT_CREATION))
}
