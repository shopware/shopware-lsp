//! Symfony `Response::HTTP_*` status constants.

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::PHP_BINARY_EXPRESSION;
use swlsp_php::PhpIndex;
use swlsp_php_resolver::{ImportKind, MemberResolver, parse_use_declaration};
use swlsp_php_semantic::{Document, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use super::markdown;
use crate::completion::common::php_context;

const SYMFONY_RESPONSE_CLASS: &str = "Symfony\\Component\\HttpFoundation\\Response";

pub struct ResponseConstantCompletionProvider {
    php: Option<PhpIndex>,
}

impl ResponseConstantCompletionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }

    fn is_status_code_context(
        php: &PhpIndex,
        context: &swlsp_php::PhpContext,
        node: &SyntaxNode,
        offset: u32,
    ) -> bool {
        if let Some(call) = query::call_at(node)
            && eq_ignore_case(&query::call_method_name(&call), "setStatusCode")
            && response_active_argument(&call, offset) == 0
            && php.is_method_called_on_class(Some(context), &call, SYMFONY_RESPONSE_CLASS)
        {
            return true;
        }
        for current in node.ancestors() {
            if current.kind() != PHP_BINARY_EXPRESSION {
                continue;
            }
            let Some(left) = current.children().next() else {
                return false;
            };
            if u32::from(left.text_range().end()) > offset
                || !eq_ignore_case(&query::call_method_name(&left), "getStatusCode")
            {
                return false;
            }
            return php.is_method_called_on_class(Some(context), &left, SYMFONY_RESPONSE_CLASS);
        }
        false
    }
}

fn response_active_argument(call: &SyntaxNode, offset: u32) -> usize {
    let arguments: Vec<SyntaxNode> = query::arguments(call).collect();
    if arguments.is_empty() {
        return 0;
    }
    arguments
        .iter()
        .position(|argument| offset <= u32::from(argument.text_range().end()))
        .unwrap_or(arguments.len())
}

fn response_class_qualifier(document: &Document, root: &SyntaxNode, offset: u32) -> String {
    for declaration in query::use_declarations(root) {
        for imported in parse_use_declaration(&declaration.text().to_string()) {
            if imported.kind == ImportKind::Class
                && eq_ignore_case(
                    imported
                        .target
                        .strip_prefix('\\')
                        .unwrap_or(&imported.target),
                    SYMFONY_RESPONSE_CLASS,
                )
            {
                return imported.alias;
            }
        }
    }
    if let Some(scope) = document.scope_at(offset)
        && eq_ignore_case(&scope.namespace, "Symfony\\Component\\HttpFoundation")
    {
        return "Response".to_string();
    }
    format!("\\{SYMFONY_RESPONSE_CLASS}")
}

impl CompletionProvider for ResponseConstantCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(php), Some(node), Some(root), Some(line_index)) = (
            self.php.as_ref(),
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let Some(context) = php_context(php, request) else {
            return Vec::new();
        };
        let position = request.params.position;
        let offset = line_index.offset_utf16(position.line, position.character);
        if !Self::is_status_code_context(php, &context, node, offset) {
            return Vec::new();
        }
        let qualifier = response_class_qualifier(&context.document, root, offset);
        let members = MemberResolver {
            snapshot: Some(&context.snapshot),
        }
        .all(&Type::named(SYMFONY_RESPONSE_CLASS, Vec::new()));
        let mut items = Vec::new();
        for member in members {
            let symbol = member.symbol;
            if symbol.kind != SymbolKind::ClassConstant
                || symbol.visibility != Visibility::Public
                || !symbol.name.starts_with("HTTP_")
            {
                continue;
            }
            let insert_text = format!("{qualifier}::{}", symbol.name);
            let mut item = protocol::CompletionItem {
                label: insert_text.clone(),
                filter_text: format!("{} {insert_text}", symbol.name),
                insert_text,
                kind: Some(CompletionItemKind::CONSTANT),
                detail: format!("Symfony HTTP status constant · {SYMFONY_RESPONSE_CLASS}"),
                ..Default::default()
            };
            if !symbol.doc_summary().is_empty() {
                item.documentation = Some(markdown(symbol.doc_summary()));
            }
            items.push(item);
        }
        items.sort_unstable_by(crate::completion::common::compare_completion_items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
