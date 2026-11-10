//! Symfony Messenger message and handler hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_messenger::{Index, ReferenceRole};
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNodeExt;

use crate::hover::{
    escape_markdown, hover_protocol_range, markdown_hover, php_context, request_uri,
};

pub struct MessengerHoverProvider {
    php: Option<PhpIndex>,
    index: Option<Arc<Index>>,
}

impl MessengerHoverProvider {
    pub fn new(php: Option<PhpIndex>, index: Option<Arc<Index>>) -> Self {
        Self { php, index }
    }
}

impl HoverProvider for MessengerHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(None);
        };
        let (Some(root), Some(node)) = (request.root.as_ref(), request.node.as_ref()) else {
            return Ok(None);
        };
        let context = php_context(Some(php), request);
        let Some(reference) =
            swlsp_messenger::reference_at(context.as_ref(), request_uri(request), root, node)
        else {
            return Ok(None);
        };
        if reference.name.is_empty() {
            return Ok(None);
        }
        let mut markdown = String::new();
        match reference.role {
            ReferenceRole::Message => {
                let _ = write!(
                    markdown,
                    "**Symfony Messenger message** `{}`",
                    escape_markdown(&reference.name)
                );
                if let Some(index) = self.index.as_deref()
                    && let Some(message) = index.message(&reference.name)?
                {
                    let _ = write!(
                        markdown,
                        "\n\n{} handler(s) · {} dispatch site(s)",
                        message.handlers().len(),
                        message.dispatches().len()
                    );
                }
            }
            ReferenceRole::HandlerMethod => {
                let _ = write!(
                    markdown,
                    "**Symfony Messenger handler method** `{}::{}()`",
                    escape_markdown(&reference.class),
                    escape_markdown(&reference.name)
                );
                let methods = php.find_methods(&reference.class, &reference.name);
                if let Some(method) = methods.first()
                    && !method.doc_summary().is_empty()
                {
                    let _ = write!(markdown, "\n\n{}", method.doc_summary());
                }
            }
            _ => return Ok(None),
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                reference.node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}
