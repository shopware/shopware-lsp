//! AbstractMessageHandler migration.

use super::scan;
use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_EXTENDS_CLAUSE, PHP_NAME};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, class_name_range, migration_problem,
    node_text, php_own_method_for_migration,
};

const MESSAGE_HANDLER_SUBSCRIBER_CODE: &str = "shopware.migration.message_handler.subscriber";
const ABSTRACT_MESSAGE_HANDLER_CLASS: &str =
    "Shopware\\Core\\Framework\\MessageQueue\\Handler\\AbstractMessageHandler";

impl ShopwareMigrationAnalyzer {
    pub(super) fn message_handler_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
    ) -> Vec<Problem> {
        let resolver = scan::resolver(root);
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                return result;
            }
            let Some(parent) = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE)
                .and_then(|extends| phpquery::direct_child(&extends, PHP_NAME))
            else {
                continue;
            };
            if !eq_ignore_case(
                resolver
                    .resolve(node_text(&parent).trim())
                    .trim_matches('\\'),
                ABSTRACT_MESSAGE_HANDLER_CLASS,
            ) {
                continue;
            }
            let handle = php_own_method_for_migration(&class, "handle");
            let invoke = php_own_method_for_migration(&class, "__invoke");
            // Rector can still perform the structural migration when a handler
            // has neither method (for example, an abstract base handler). Only
            // an existing handle() and __invoke() pair would make renaming
            // handle() ambiguous.
            let safe = handle.is_none() || invoke.is_none();
            result.push(migration_problem(
                MESSAGE_HANDLER_SUBSCRIBER_CODE,
                class_name_range(&class),
                &class,
                "Shopware 6.5: migrate AbstractMessageHandler to an attributed Messenger subscriber"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "message-handler-subscriber".into(),
                    kind: "class".into(),
                    safe,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }
}
