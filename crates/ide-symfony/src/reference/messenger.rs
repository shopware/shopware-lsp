//! Symfony Messenger message and handler references.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_messenger::{Index, OccurrenceKind, ReferenceRole, reference_at};
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_php_semantic::SymbolKind;
use swlsp_syntax::SyntaxNodeExt;
use swlsp_textutil::eq_ignore_case;

use crate::reference::{FileLines, lower_ext, php_context, symbol_range, unique_locations};

pub struct MessengerReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl MessengerReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for MessengerReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(php), Some(node)) = (&self.index, &self.php, &request.node) else {
            return Ok(Vec::new());
        };
        let include_declaration = request.params.context.include_declaration;
        let uri = &request.params.text_document.uri;
        let context = php_context(Some(php), request);
        if let Some(root) = &request.root
            && let Some(reference) = reference_at(context.as_ref(), uri, root, node)
        {
            match reference.role {
                ReferenceRole::Message => {
                    return message_locations(index, php, &reference.name, include_declaration);
                }
                ReferenceRole::HandlerMethod => {
                    return handler_locations(
                        index,
                        php,
                        &reference.class,
                        &reference.name,
                        include_declaration,
                    );
                }
                ReferenceRole::None => {}
            }
        }
        if lower_ext(uri) != ".php" {
            return Ok(Vec::new());
        }
        let Some(context) = context else {
            return Ok(Vec::new());
        };
        let Some(symbol) = symbol_at(
            &context.document,
            &context.snapshot,
            node.range_trimmed_trivia().start().into(),
        ) else {
            return Ok(Vec::new());
        };
        match symbol.kind {
            SymbolKind::Class | SymbolKind::Interface | SymbolKind::Enum => {
                message_locations(index, php, &symbol.fully_qualified, include_declaration)
            }
            SymbolKind::Method => {
                let Some(class) = context.snapshot.symbol(&symbol.container) else {
                    return Ok(Vec::new());
                };
                handler_locations(
                    index,
                    php,
                    &class.fully_qualified,
                    &symbol.name,
                    include_declaration,
                )
            }
            _ => Ok(Vec::new()),
        }
    }
}

fn message_locations(
    index: &Index,
    php: &PhpIndex,
    name: &str,
    include_declaration: bool,
) -> ProviderResult<Vec<protocol::Location>> {
    let Some(message) = index.message(name)? else {
        return Ok(Vec::new());
    };
    let mut files = FileLines::default();
    let mut result = Vec::new();
    for occurrence in &message.occurrences {
        let range = if occurrence.message_range.is_empty() {
            occurrence.range
        } else {
            occurrence.message_range
        };
        result.extend(files.location(&occurrence.file, range));
    }
    if include_declaration && let Some(symbol) = php.find_class(name) {
        result.extend(files.location(&symbol.path, symbol_range(&symbol)));
    }
    Ok(unique_locations(result))
}

fn handler_locations(
    index: &Index,
    php: &PhpIndex,
    class_name: &str,
    method_name: &str,
    include_declaration: bool,
) -> ProviderResult<Vec<protocol::Location>> {
    let messages = index.messages_for_handler(class_name, method_name)?;
    let mut files = FileLines::default();
    let mut result = Vec::new();
    for message in &messages {
        for occurrence in &message.occurrences {
            let mut range = occurrence.message_range;
            if occurrence.kind == OccurrenceKind::Handler
                && eq_ignore_case(&occurrence.class, class_name)
                && eq_ignore_case(&occurrence.method, method_name)
                && !occurrence.handler_range.is_empty()
            {
                range = occurrence.handler_range;
            }
            if range.is_empty() {
                range = occurrence.range;
            }
            result.extend(files.location(&occurrence.file, range));
        }
    }
    if include_declaration {
        for method in php.find_methods(class_name, method_name) {
            result.extend(files.location(&method.path, symbol_range(&method)));
        }
    }
    Ok(unique_locations(result))
}
