//! Symfony translation key and domain hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNodeExt;
use swlsp_translation::{Index, ReferenceRole};
use swlsp_uriutil::filepath;

use crate::hover::{
    hover_protocol_range, lower_ext, markdown_hover, php_context, rel_or, request_uri,
};

pub struct TranslationHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl TranslationHoverProvider {
    pub fn new(root: impl Into<String>, index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self {
            root: root.into(),
            index,
            php,
        }
    }
}

impl HoverProvider for TranslationHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let uri = request_uri(request);
        let extension = lower_ext(uri);
        if extension != ".php" && extension != ".twig" {
            return Ok(None);
        }
        let Some(reference) = swlsp_translation::reference_at(uri, node, request.source()) else {
            return Ok(None);
        };
        if extension == ".php" {
            let context = php_context(self.php.as_ref(), request);
            if !swlsp_translation::validate_php_reference(
                context.as_ref(),
                &reference,
                self.php.as_ref(),
            ) {
                return Ok(None);
            }
        }
        let mut markdown = String::new();
        match reference.role {
            ReferenceRole::Key => {
                let messages = index.messages_for(&reference.domain, &reference.key)?;
                if messages.is_empty() {
                    return Ok(None);
                }
                let _ = write!(
                    markdown,
                    "**Symfony translation** `{}` · `{}`\n\n",
                    escape_translation_markdown(&reference.key),
                    escape_translation_markdown(&reference.domain)
                );
                for message in &messages {
                    let display = rel_or(&self.root, &message.file);
                    let _ = writeln!(
                        markdown,
                        "- **{}**: {}  \n  `{}:{}`",
                        escape_translation_markdown(&message.locale),
                        escape_translation_markdown(&message.text),
                        escape_translation_markdown(&filepath::to_slash(&display)),
                        message.line + 1
                    );
                }
            }
            ReferenceRole::Domain => {
                let keys = index.keys(&reference.domain)?;
                if keys.is_empty() {
                    return Ok(None);
                }
                let _ = write!(
                    markdown,
                    "**Symfony translation domain** `{}`\n\n{} translation keys",
                    escape_translation_markdown(&reference.domain),
                    keys.len()
                );
            }
            _ => return Ok(None),
        }
        let (Some(node), Some(line_index)) = (reference.node.as_ref(), request.line_index()) else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}

fn escape_translation_markdown(value: &str) -> String {
    value
        .replace('`', "\\`")
        .replace("\r\n", " ")
        .replace('\n', " ")
}
