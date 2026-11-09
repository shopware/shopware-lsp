//! Controller action references.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_symfony::{
    ControllerUsage, RouteUsageIndexer, ServiceIndex, resolve_controller_reference,
    twig_controller_reference_at,
};

use crate::{
    FileLines, document_location, lower_ext, php_context, request_offset, request_path,
    sort_locations, symbol_range, unique_locations,
};

pub struct ControllerReferenceProvider {
    usages: Option<Arc<RouteUsageIndexer>>,
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ControllerReferenceProvider {
    pub fn new(
        usages: Option<Arc<RouteUsageIndexer>>,
        services: Option<Arc<ServiceIndex>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            usages,
            services,
            php,
        }
    }
}

impl ReferencesProvider for ControllerReferenceProvider {
    fn get_references(
        &self,
        ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(usages), Some(php), Some(_), Some(_)) =
            (&self.usages, &self.php, &request.node, request.line_index())
        else {
            return Ok(Vec::new());
        };
        match lower_ext(&request.params.text_document.uri).as_str() {
            ".twig" => self.twig_references(ctx, request, usages, php),
            ".php" => self.php_references(ctx, request, usages, php),
            _ => Ok(Vec::new()),
        }
    }
}

impl ControllerReferenceProvider {
    fn twig_references(
        &self,
        ctx: &Context,
        request: &ReferenceRequest,
        usages: &RouteUsageIndexer,
        php: &PhpIndex,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(node), Some(line_index)) = (&request.node, request.line_index()) else {
            return Ok(Vec::new());
        };
        let Some(reference) = twig_controller_reference_at(node) else {
            return Ok(Vec::new());
        };
        let resolution = resolve_controller_reference(
            &reference.reference,
            self.services.as_deref(),
            Some(php),
        )?;
        let found = if resolution.method_declared {
            let class = resolution.class.clone().unwrap_or_default();
            let method = resolution.method.clone().unwrap_or_default();
            usages.controller_usages_for_method(
                &class.fully_qualified,
                &method.name,
                self.services.as_deref(),
                Some(php),
            )?
        } else {
            usages.get_controller_usages(&reference.reference)?
        };
        let mut files = FileLines::default();
        let mut result = Vec::with_capacity(found.len() + 2);
        for usage in &found {
            ctx.check()?;
            result.push(usage_location(&mut files, usage));
        }
        // The open document may contain an unsaved usage not in the
        // persistent index yet.
        result.push(document_location(request, line_index, reference.range));
        if request.params.context.include_declaration {
            if resolution.method_declared {
                result.push(symbol_location(
                    &mut files,
                    &resolution.method.unwrap_or_default(),
                ));
            } else if resolution.class_found {
                result.push(symbol_location(
                    &mut files,
                    &resolution.class.unwrap_or_default(),
                ));
            }
        }
        Ok(unique_sorted(result))
    }

    fn php_references(
        &self,
        ctx: &Context,
        request: &ReferenceRequest,
        usages: &RouteUsageIndexer,
        php: &PhpIndex,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let path = request_path(request)?;
        let Some(offset) = request_offset(request) else {
            return Ok(Vec::new());
        };
        let (document, snapshot) = match php_context(Some(php), request) {
            Some(context) => (context.document, context.snapshot),
            None => {
                let Some(document) = php.semantic_document(&path) else {
                    return Ok(Vec::new());
                };
                (Arc::new(document), php.semantic_snapshot())
            }
        };
        let Some(method) = symbol_at(&document, &snapshot, offset) else {
            return Ok(Vec::new());
        };
        if method.kind != SymbolKind::Method {
            return Ok(Vec::new());
        }
        let Some(class) = snapshot.symbol(&method.container) else {
            return Ok(Vec::new());
        };
        if !class.is_class_like() {
            return Ok(Vec::new());
        }
        let found = usages.controller_usages_for_method(
            &class.fully_qualified,
            &method.name,
            self.services.as_deref(),
            Some(php),
        )?;
        let mut files = FileLines::default();
        let mut result = Vec::with_capacity(found.len() + 1);
        for usage in &found {
            ctx.check()?;
            result.push(usage_location(&mut files, usage));
        }
        if request.params.context.include_declaration {
            result.push(symbol_location(&mut files, &method));
        }
        Ok(unique_sorted(result))
    }
}

fn unique_sorted(locations: Vec<protocol::Location>) -> Vec<protocol::Location> {
    let mut result = unique_locations(locations);
    sort_locations(&mut result);
    result
}

/// The usage location; an unreadable file keeps a zero range.
fn usage_location(files: &mut FileLines, usage: &ControllerUsage) -> protocol::Location {
    files
        .location(&usage.file, usage.range)
        .unwrap_or_else(|| protocol::Location {
            uri: swlsp_uriutil::file_uri(&usage.file),
            range: protocol::Range::default(),
        })
}

fn symbol_location(files: &mut FileLines, symbol: &Symbol) -> protocol::Location {
    files
        .location(&symbol.path, symbol_range(symbol))
        .unwrap_or_else(|| protocol::Location {
            uri: swlsp_uriutil::file_uri(&symbol.path),
            range: protocol::Range::default(),
        })
}
