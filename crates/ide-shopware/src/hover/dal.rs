//! Shopware DAL entity and field hover in Administration JavaScript.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult, effective_syntax_language};
use swlsp_lsp_protocol as protocol;
use swlsp_shopware_dal::{Index, JsFieldReferenceKind};

use crate::hover::{lower_ext, markdown_hover, request_offset, request_uri};

pub struct DalHoverProvider {
    index: Option<Arc<Index>>,
}

impl DalHoverProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }

    fn entity_hover(&self, index: &Index, name: &str) -> ProviderResult<Option<protocol::Hover>> {
        let mut definitions = index.definition(name)?;
        if definitions.is_empty() {
            return Ok(None);
        }
        definitions.sort_unstable_by(|left, right| {
            (
                &left.class,
                &left.file,
                left.line,
                &left.fully_qualified_class,
            )
                .cmp(&(
                    &right.class,
                    &right.file,
                    right.line,
                    &right.fully_qualified_class,
                ))
        });
        let sections: Vec<String> = definitions
            .iter()
            .map(|definition| {
                let mut section = format!(
                    "**Shopware DAL entity** `{}`\n\nPHP definition: `{}`.",
                    definition.name, definition.class
                );
                if !definition.fields.is_empty() {
                    let _ = write!(section, "\n\nIndexed fields: {}.", definition.fields.len());
                }
                section
            })
            .collect();
        Ok(Some(markdown_hover(sections.join("\n\n---\n\n"), None)))
    }
}

impl HoverProvider for DalHoverProvider {
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
        let Some(offset) = request_offset(request) else {
            return Ok(None);
        };
        let extension = lower_ext(request_uri(request));
        if extension != ".js" && extension != ".ts" && extension != ".vue" {
            return Ok(None);
        }
        if extension == ".vue"
            && effective_syntax_language(Some(LanguageId::Vue), Some(node))
                != Some(LanguageId::JavaScript)
        {
            return Ok(None);
        }
        if let Some(reference) = swlsp_shopware_dal::js_entity_reference_at(node) {
            return self.entity_hover(index, &reference.name);
        }
        let reference_kind = swlsp_shopware_dal::js_field_reference_at(node);
        if reference_kind == JsFieldReferenceKind::None {
            return Ok(None);
        }
        let (name, association_segment) =
            swlsp_shopware_dal::js_field_reference_segment(node, offset);
        let association =
            reference_kind == JsFieldReferenceKind::Association || association_segment;
        let mut fields = index.field_definitions(&name, association)?;
        if fields.is_empty() {
            return Ok(None);
        }
        fields.sort_unstable_by(|left, right| {
            (
                &left.entity,
                &left.field.type_,
                &left.file,
                left.field.line,
                &left.class,
            )
                .cmp(&(
                    &right.entity,
                    &right.field.type_,
                    &right.file,
                    right.field.line,
                    &right.class,
                ))
        });
        let kind = if association { "association" } else { "field" };
        let mut markdown = format!("**Shopware DAL {kind}** `{name}`");
        const LIMIT: usize = 12;
        for (position, field) in fields.iter().enumerate() {
            if position == LIMIT {
                let _ = write!(
                    markdown,
                    "\n\n- …and {} more definitions",
                    fields.len() - LIMIT
                );
                break;
            }
            let _ = write!(
                markdown,
                "\n\n- `{}` — `{}` (`{}`)",
                field.entity, field.field.type_, field.class
            );
        }
        Ok(Some(markdown_hover(markdown, None)))
    }
}
