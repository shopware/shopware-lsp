//! Message handler subscriber fix (Go
//! `shopware_migration_message_handler.go`).

use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_EXTENDS_CLAUSE, PHP_IMPLEMENTS_CLAUSE, PHP_NAME};
use swlsp_php::NameResolver;
use swlsp_php_rewrite::Editor;
use swlsp_php_types::equal_fold;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use super::{
    finish_php_rewrite, invalid, php_own_method_for_migration_fix, present_migration,
    resolve_migration_class, safe_payload,
};

pub(crate) const MESSAGE_HANDLER_SUBSCRIBER_FIX_ID: &str =
    "shopware-rector-message-handler-subscriber";

const MESSAGE_SUBSCRIBER_INTERFACE: &str =
    "Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface";
const AS_MESSAGE_HANDLER_ATTRIBUTE: &str =
    "Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler";

pub(crate) struct MessageHandlerSubscriberFix;

impl QuickFix for MessageHandlerSubscriberFix {
    fn id(&self) -> FixId {
        FixId::from(MESSAGE_HANDLER_SUBSCRIBER_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(
            fix,
            "message-handler-subscriber",
            |_| "Shopware 6.5: Migrate message handler to subscriber",
        )
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for MessageHandlerSubscriberFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        safe_payload(
            fix,
            "message-handler-subscriber",
            "message handler migration is no longer safe",
        )?;
        let (class, root) = resolve_migration_class(fix)?;
        let handle = php_own_method_for_migration_fix(&class, "handle");
        let invoke = php_own_method_for_migration_fix(&class, "__invoke");
        if handle.is_some() && invoke.is_some() {
            return Err(invalid("message handler method changed"));
        }
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        let subscriber = editor.class_reference(MESSAGE_SUBSCRIBER_INTERFACE)?;
        let Some(extends) = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE) else {
            return Err(invalid("message handler parent changed"));
        };
        if phpquery::direct_child(&class, PHP_IMPLEMENTS_CLAUSE).is_none() {
            editor.replace_range(
                extends.range_trimmed_trivia(),
                format!("implements {subscriber}"),
            )?;
        } else {
            if !editor.remove_extends(&class)? {
                return Err(invalid("message handler parent changed"));
            }
            editor.add_implements(&class, &subscriber)?;
        }
        let resolver = NameResolver::new(&root);
        if !class_has_migration_attribute(&class, &resolver, AS_MESSAGE_HANDLER_ATTRIBUTE) {
            let attribute = editor.class_reference(AS_MESSAGE_HANDLER_ATTRIBUTE)?;
            editor.add_attribute(&class, &attribute)?;
        }
        if let Some(handle) = &handle {
            let Some(name) = phpquery::direct_child(handle, PHP_NAME) else {
                return Err(invalid("message handler method name changed"));
            };
            editor.replace_range(name.range_trimmed_trivia(), "__invoke")?;
        }
        if let Some(handled_messages) =
            php_own_method_for_migration_fix(&class, "getHandledMessages")
        {
            editor.remove_class_member(&handled_messages)?;
        }
        finish_php_rewrite(fix, &editor)
    }
}

fn class_has_migration_attribute(
    class: &SyntaxNode,
    resolver: &NameResolver,
    target: &str,
) -> bool {
    phpquery::attributes(class).iter().any(|attribute| {
        let name = phpquery::attribute_name(attribute);
        equal_fold(resolver.resolve(&name).trim_matches('\\'), target)
    })
}
