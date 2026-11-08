//! `constant()` and `enum()` style constant references in Twig to PHP
//! constants (port of Go `twig_constant_definition.go`).

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind, Visibility};
use swlsp_twig::{ConstantReference, PhpAccessResolver, TwigIndexer};

use crate::common::{ext_lower, php_symbol_location, request_uri};

pub struct TwigConstantDefinitionProvider {
    php: Option<PhpIndex>,
    twig: Option<Arc<TwigIndexer>>,
}

impl TwigConstantDefinitionProvider {
    pub fn new(php: Option<PhpIndex>, twig: Option<Arc<TwigIndexer>>) -> Self {
        Self { php, twig }
    }
}

impl GotoDefinitionProvider for TwigConstantDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(php), Some(node), Some(root)) = (
            self.php.as_ref(),
            request.node.as_ref(),
            request.root.as_ref(),
        ) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        if ext_lower(uri) != ".twig" {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let resolver = PhpAccessResolver::new(Some(php), self.twig.as_deref());
        let references = swlsp_twig::constant_references_at(&path, root, node, &resolver);
        let mut seen = HashSet::new();
        let mut locations = Vec::new();
        for reference in &references {
            for symbol in twig_constant_symbols(php, reference) {
                if symbol.visibility != Visibility::Public
                    && symbol.kind != SymbolKind::GlobalConstant
                {
                    continue;
                }
                if !seen.insert(symbol.id.clone()) {
                    continue;
                }
                locations.push(php_symbol_location(&symbol));
            }
        }
        locations
    }
}

fn twig_constant_symbols(index: &PhpIndex, reference: &ConstantReference) -> Vec<Symbol> {
    if !reference.class.is_empty() {
        return index.find_constants(&reference.class, &reference.name);
    }
    index.find_global_constants(&reference.name)
}

#[cfg(test)]
#[path = "twig_constant_tests.rs"]
mod tests;
