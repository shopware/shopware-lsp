//! Route annotation fix.

use crate::diagnostics::ShopwareMigrationPayload;
use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_parser_php::syntax::{PHP_CLASS_DECLARATION, PHP_METHOD_DECLARATION};
use swlsp_php_rewrite::{Editor, find_phpdoc_annotation};
use swlsp_rewrite::WorkspacePlan;

use super::{document_root, element_ancestors, finish_php_rewrite, invalid, present_migration};
use crate::inspections::resolve_anchor;

pub(crate) const ROUTE_ANNOTATION_MIGRATION_FIX_ID: &str =
    "shopware-rector-route-annotation-default";

pub(crate) struct RouteAnnotationMigrationFix;

impl QuickFix for RouteAnnotationMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(ROUTE_ANNOTATION_MIGRATION_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "route-annotation-default",
            |_| "Shopware 6.5: Migrate annotation to route defaults",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for RouteAnnotationMigrationFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        if !payload.safe
            || payload.rule != "route-annotation-default"
            || payload.value.is_empty()
            || payload.replacement.is_empty()
        {
            return Err(invalid("route annotation migration is no longer safe"));
        }
        let element = resolve_anchor(fix)?;
        let Some(owner) = element_ancestors(&element)
            .find(|node| matches!(node.kind(), PHP_METHOD_DECLARATION | PHP_CLASS_DECLARATION))
        else {
            return Err(invalid("route annotation owner changed"));
        };
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        if payload.kind == "route-scope" && find_phpdoc_annotation(&owner, "Route").is_none() {
            if !editor.replace_phpdoc_annotation(&owner, &payload.value, &payload.replacement)? {
                return Err(invalid("route scope annotation changed"));
            }
        } else {
            if !editor.replace_phpdoc_annotation(&owner, "Route", &payload.replacement)? {
                return Err(invalid("route annotation changed"));
            }
            if !editor.remove_phpdoc_annotation(&owner, &payload.value)? {
                return Err(invalid(if payload.kind == "route-scope" {
                    "route scope annotation changed"
                } else {
                    "legacy route annotation changed"
                }));
            }
        }
        finish_php_rewrite(fix, &editor)
    }
}
