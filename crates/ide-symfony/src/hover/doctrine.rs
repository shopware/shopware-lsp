//! Doctrine DBAL type registration, table, column, and alias hover.

use std::sync::Arc;

use swlsp_doctrine::{DbalReferenceRole, Index};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use crate::hover::{
    escape_markdown, hover_protocol_range, lower_ext, markdown_hover, php_context, request_offset,
    request_uri,
};

pub struct DoctrineHoverProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl DoctrineHoverProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

fn doctrine_hover(markdown: String, range: TextRange, line_index: &LineIndex) -> protocol::Hover {
    markdown_hover(markdown, Some(hover_protocol_range(range, line_index)))
}

impl HoverProvider for DoctrineHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(root), Some(_document), Some(line_index)) = (
            request.root.as_ref(),
            request.document.as_ref(),
            request.line_index(),
        ) else {
            return Ok(None);
        };
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let extension = lower_ext(&path);
        let offset = request_offset(request).unwrap_or_default();
        if let Some(registration) =
            swlsp_doctrine::type_registration_reference_at(&path, root, TextSize::from(offset))
            && !registration.name.is_empty()
            && !registration.class.is_empty()
        {
            let mut markdown = format!(
                "**Doctrine DBAL type registration** `{}`\n\nClass: `{}`",
                escape_markdown(&registration.name),
                escape_markdown(&registration.class)
            );
            if let Some(php) = self.php.as_ref()
                && let Some(symbol) = php.find_class(&registration.class)
                && !symbol.doc_summary().is_empty()
            {
                markdown.push_str("\n\n");
                markdown.push_str(&escape_markdown(symbol.doc_summary()));
            }
            return Ok(Some(doctrine_hover(
                markdown,
                registration.range,
                line_index,
            )));
        }
        if extension != ".php" {
            return Ok(None);
        }
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) = index.dbal_reference_at(context.as_ref(), root, node) else {
            return Ok(None);
        };
        match reference.role {
            DbalReferenceRole::Table => {
                let Some(model) = index.definition_for_table(&reference.name)? else {
                    return Ok(None);
                };
                Ok(Some(doctrine_hover(
                    format!(
                        "**Shopware DBAL table** `{}`\n\nDefinition: `{}`",
                        escape_markdown(&model.name),
                        escape_markdown(&model.class)
                    ),
                    reference.range,
                    line_index,
                )))
            }
            DbalReferenceRole::Column => {
                let Some((model, field)) =
                    index.field_for_column(&reference.table, &reference.name)?
                else {
                    return Ok(None);
                };
                Ok(Some(doctrine_hover(
                    format!(
                        "**Shopware DBAL column** `{}`\n\nField: `{}`\n\nType: `{}`",
                        escape_markdown(&reference.name),
                        escape_markdown(&format!("{}::{}", model.class, field.name)),
                        escape_markdown(&field.type_)
                    ),
                    reference.range,
                    line_index,
                )))
            }
            DbalReferenceRole::Alias => Ok(Some(doctrine_hover(
                format!(
                    "**Doctrine DBAL join alias** `{}`",
                    escape_markdown(&reference.name)
                ),
                reference.range,
                line_index,
            ))),
            #[allow(unreachable_patterns)]
            _ => Ok(None),
        }
    }
}
