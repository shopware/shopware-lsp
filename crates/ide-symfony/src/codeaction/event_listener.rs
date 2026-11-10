//! Scaffold an event listener for an event class.

use swlsp_ide::codeaction::document_snapshot;

use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_CLASS_DECLARATION;
use swlsp_php::PhpIndex;

use swlsp_ide::codeaction::command_action;
use swlsp_ide::codeaction::php_class_fully_qualified_name;

pub const CREATE_EVENT_LISTENER_ACTION: &str = "shopware.createEventListener";

/// Offers an `AsEventListener` scaffold on event class declarations.
pub struct EventListenerContextProvider {
    php_index: Option<PhpIndex>,
}

impl EventListenerContextProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ActionProvider for EventListenerContextProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(index), Some(document), Some(root), Some(node)) = (
            self.php_index.as_ref(),
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        if class.kind() != PHP_CLASS_DECLARATION {
            return Vec::new();
        }
        let class_name = php_class_fully_qualified_name(root, &class);
        if class_name.is_empty() {
            return Vec::new();
        }
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone());
        let snapshot = document_snapshot(index, document, &path, root);
        let is_event = [
            r"Symfony\Contracts\EventDispatcher\Event",
            r"Symfony\Component\EventDispatcher\Event",
        ]
        .iter()
        .any(|base| class_name != *base && snapshot.is_subtype_of(&class_name, base));
        if !is_event {
            return Vec::new();
        }
        let short = phpquery::class_name(&class);
        vec![command_action(
            &format!("Shopware: Create listener for {short}"),
            CodeActionKind::REFACTOR_REWRITE,
            "Create Shopware event listener",
            CREATE_EVENT_LISTENER_ACTION,
            vec![
                Value::from(class_name),
                Value::from(format!("{short}Listener")),
                Value::from(document.uri.clone()),
            ],
        )]
    }
}

#[cfg(test)]
#[path = "tests/event_listener.rs"]
mod tests;
