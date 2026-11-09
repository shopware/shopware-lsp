//! PHP enum hover inside Twig `enum()` calls.

use std::fmt::Write as _;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind};

use crate::{escape_markdown, hover_protocol_range, lower_ext, markdown_hover, request_uri};

pub struct TwigEnumHoverProvider {
    php: Option<PhpIndex>,
}

impl TwigEnumHoverProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl HoverProvider for TwigEnumHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(None);
        };
        let (Some(node), Some(line_index)) = (request.node.as_ref(), request.line_index()) else {
            return Ok(None);
        };
        if lower_ext(request_uri(request)) != ".twig" {
            return Ok(None);
        }
        let Some(reference) = swlsp_twig::enum_reference_at(node) else {
            return Ok(None);
        };
        let Some(symbol) = php.find_class(&reference.name) else {
            return Ok(None);
        };
        let mut markdown = String::new();
        if symbol.kind == SymbolKind::Enum {
            let _ = write!(
                markdown,
                "**PHP enum** `{}`",
                escape_markdown(&symbol.fully_qualified)
            );
            let cases = twig_enum_cases(php, &symbol);
            if !cases.is_empty() {
                // The joined list is escaped as a whole, separators included.
                let _ = write!(
                    markdown,
                    "\n\nCases: `{}`",
                    escape_markdown(&cases.join("`, `"))
                );
            }
        } else {
            let _ = write!(
                markdown,
                "**PHP class** `{}`\n\nThis class is not an enum.",
                escape_markdown(&symbol.fully_qualified)
            );
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(reference.range, line_index)),
        )))
    }
}

fn twig_enum_cases(index: &PhpIndex, enumeration: &Symbol) -> Vec<String> {
    let mut result: Vec<String> = index
        .semantic_snapshot()
        .members_of(enumeration.id.as_str())
        .into_iter()
        .filter(|member| member.kind == SymbolKind::EnumCase)
        .map(|member| member.name.to_string())
        .collect();
    result.sort();
    result
}
