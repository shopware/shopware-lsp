//! PHP constant hover inside Twig `constant()` / `enum()` strings.

use std::collections::HashSet;
use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_twig::{PhpAccessResolver, TwigIndexer};

use crate::{escape_markdown, hover_protocol_range, lower_ext, markdown_hover, request_uri};

pub struct TwigConstantHoverProvider {
    php: Option<PhpIndex>,
    twig: Option<Arc<TwigIndexer>>,
}

impl TwigConstantHoverProvider {
    pub fn new(php: Option<PhpIndex>, twig: Option<Arc<TwigIndexer>>) -> Self {
        Self { php, twig }
    }
}

impl HoverProvider for TwigConstantHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(None);
        };
        let (Some(node), Some(root), Some(line_index)) = (
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Ok(None);
        };
        if lower_ext(request_uri(request)) != ".twig" {
            return Ok(None);
        }
        let Ok(path) = swlsp_uriutil::path(request_uri(request)) else {
            return Ok(None);
        };
        let resolver = PhpAccessResolver::new(Some(php), self.twig.as_deref());
        let references = swlsp_twig::constant_references_at(&path, root, node, &resolver);
        let mut symbols: Vec<Symbol> = Vec::new();
        let mut seen = HashSet::new();
        for reference in &references {
            let current = if reference.class.is_empty() {
                php.find_global_constants(&reference.name)
            } else {
                php.find_constants(&reference.class, &reference.name)
            };
            for symbol in current {
                if symbol.kind != SymbolKind::GlobalConstant
                    && symbol.visibility != Visibility::Public
                {
                    continue;
                }
                if !seen.insert(symbol.id.clone()) {
                    continue;
                }
                symbols.push(symbol);
            }
        }
        if symbols.is_empty() {
            return Ok(None);
        }
        let mut named: Vec<(String, Symbol)> = symbols
            .into_iter()
            .map(|symbol| (php.constant_symbol_name(&symbol), symbol))
            .collect();
        swlsp_gosort::sort_slice(&mut named, |left, right| left.0 < right.0);
        let mut markdown = String::new();
        for (index, (name, symbol)) in named.iter().enumerate() {
            if index != 0 {
                markdown.push_str("\n\n---\n\n");
            }
            let kind = match symbol.kind {
                SymbolKind::ClassConstant => "PHP class constant",
                SymbolKind::EnumCase => "PHP enum case",
                _ => "PHP constant",
            };
            let _ = write!(markdown, "**{kind}** `{}`", escape_markdown(name));
            if !symbol.ty.is_unknown() {
                let _ = write!(
                    markdown,
                    "\n\nType: `{}`",
                    escape_markdown(&symbol.ty.to_string())
                );
            }
            if !symbol.doc_summary().is_empty() {
                let _ = write!(markdown, "\n\n{}", escape_markdown(symbol.doc_summary()));
            }
            if symbol.flags.has(Flags::DEPRECATED) {
                markdown.push_str("\n\nDeprecated");
            }
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(references[0].range, line_index)),
        )))
    }
}
