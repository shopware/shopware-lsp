//! CartBehavior, filterable price definition and ReflectionHelper fixes.

use crate::diagnostics::common::call_target_name;
use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_MEMBER_CALL;
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::eq_ignore_case;

use super::{
    document_root, finish_php_rewrite, invalid, present_migration, resolve_migration_class,
    resolve_migration_php_node, safe_payload,
};
use crate::inspections::resolve_anchor;

pub(crate) const CART_BEHAVIOR_PERMISSION_FIX_ID: &str = "shopware-rector-cart-behavior-permission";
pub(crate) const FILTERABLE_PRICE_DEFINITION_FIX_ID: &str =
    "shopware-rector-filterable-price-definition";
pub(crate) const REFLECTION_HELPER_FIX_ID: &str = "shopware-rector-reflection-helper-native";

const CHECKOUT_PERMISSIONS_CLASS: &str = "Shopware\\Core\\Checkout\\CheckoutPermissions";

pub(crate) struct CartBehaviorPermissionFix;

impl QuickFix for CartBehaviorPermissionFix {
    fn id(&self) -> FixId {
        FixId::from(CART_BEHAVIOR_PERMISSION_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "cart-behavior-permission",
            |_| "Shopware 6.8: Check the cart persistence permission",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for CartBehaviorPermissionFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        safe_payload(
            fix,
            "cart-behavior-permission",
            "cart behavior rewrite is no longer safe",
        )?;
        let (call, root) = resolve_migration_php_node(fix, PHP_MEMBER_CALL)?;
        if !eq_ignore_case(&phpquery::call_method_name(&call), "isRecalculation")
            || phpquery::arguments(&call).next().is_some()
        {
            return Err(invalid("isRecalculation() call changed"));
        }
        let Some(name) = call_target_name(&call) else {
            return Err(invalid("isRecalculation() name is unavailable"));
        };
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        let permissions = editor.class_reference(CHECKOUT_PERMISSIONS_CLASS)?;
        editor.replace_range(name.range_trimmed_trivia(), "hasPermission")?;
        editor.append_argument(&call, &format!("{permissions}::SKIP_CART_PERSISTENCE"))?;
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct FilterablePriceDefinitionFix;

impl QuickFix for FilterablePriceDefinitionFix {
    fn id(&self) -> FixId {
        FixId::from(FILTERABLE_PRICE_DEFINITION_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "filterable-price-definition",
            |_| "Shopware 6.7: Implement FilterableInterface",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for FilterablePriceDefinitionFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "filterable-price-definition",
            "price definition rewrite is no longer safe",
        )?;
        if payload.replacement.is_empty() {
            return Err(invalid("price definition interface is unavailable"));
        }
        let (class, root) = resolve_migration_class(fix)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        let interface = editor.class_reference(payload.replacement.trim_matches('\\'))?;
        editor.add_implements(&class, &interface)?;
        finish_php_rewrite(fix, &editor)
    }
}

pub(crate) struct ReflectionHelperFix;

impl QuickFix for ReflectionHelperFix {
    fn id(&self) -> FixId {
        FixId::from(REFLECTION_HELPER_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "reflection-helper-native",
            |_| "Shopware 6.7: Use native reflection",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for ReflectionHelperFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload = safe_payload(
            fix,
            "reflection-helper-native",
            "reflection helper rewrite is no longer safe",
        )?;
        let source = fix.document.source();
        if payload.start >= payload.end
            || payload.replacement.is_empty()
            || source.get(payload.start as usize..payload.end as usize)
                != Some(payload.original.as_str())
        {
            return Err(invalid("reflection helper call changed"));
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
