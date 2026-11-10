//! Route name, path and controller references.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_symfony::{
    Route, RouteIndexer, RouteUsage, RouteUsageIndexer, is_php_route_name,
    is_php_route_name_in_context, is_twig_route_name, route_by_controller, route_path_matches,
    twig_html_route_reference_at,
};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::reference::{line_location, php_context};

/// `php` supplies the request's semantic PHP context.
pub struct RouteReferenceProvider {
    routes: Arc<RouteIndexer>,
    usages: Arc<RouteUsageIndexer>,
    php: Option<PhpIndex>,
}

impl RouteReferenceProvider {
    pub fn new(
        routes: Arc<RouteIndexer>,
        usages: Arc<RouteUsageIndexer>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            routes,
            usages,
            php,
        }
    }
}

impl ReferencesProvider for RouteReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let Some(node) = &request.node else {
            return Ok(Vec::new());
        };
        let include_declaration = request.params.context.include_declaration;
        match filepath::ext(&request.params.text_document.uri) {
            ".php" => {
                // The semantic context only refines syntactic route-name
                // candidates; skip building it for every other PHP node.
                if is_php_route_name(node)
                    && is_php_route_name_in_context(
                        php_context(self.php.as_ref(), request).as_ref(),
                        node,
                    )
                {
                    let routes = self.routes.get_route(&phpquery::string_value(node))?;
                    return self.references_for_routes(&routes, include_declaration);
                }
                self.php_references(request, node)
            }
            ".twig" => {
                if is_twig_route_name(node) {
                    let name = twigquery::literal_string_at(node)
                        .and_then(|literal| twigquery::string_value(&literal))
                        .unwrap_or_default();
                    let routes = self.routes.get_route(&name)?;
                    return self.references_for_routes(&routes, include_declaration);
                }
                if let Some(reference) = twig_html_route_reference_at(node) {
                    let routes = self.routes.find_routes_by_path(&reference.value)?;
                    return self.references_for_routes(&routes, include_declaration);
                }
                Ok(Vec::new())
            }
            _ => Ok(Vec::new()),
        }
    }
}

impl RouteReferenceProvider {
    fn php_references(
        &self,
        request: &ReferenceRequest,
        node: &swlsp_syntax::SyntaxNode,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let method_name = phpquery::method_name(node);
        let mut class_name = phpquery::class_name(node);
        if method_name.is_empty() || class_name.is_empty() {
            return Ok(Vec::new());
        }
        if let Some(root) = &request.root {
            let namespace = phpquery::namespace(root);
            if !namespace.is_empty() {
                class_name = format!("{namespace}\\{class_name}");
            }
        }
        // Shared cached view: `get_routes` deep-copied every route per probe.
        let routes = self.routes.get_routes_view()?;
        match route_by_controller(&routes, &format!("{class_name}::{method_name}")) {
            Some(route) => self.references_for_routes(
                std::slice::from_ref(route),
                request.params.context.include_declaration,
            ),
            None => Ok(Vec::new()),
        }
    }

    pub(crate) fn references_for_routes(
        &self,
        routes: &[Route],
        include_declaration: bool,
    ) -> ProviderResult<Vec<protocol::Location>> {
        if routes.is_empty() {
            return Ok(Vec::new());
        }
        let html_usages = self.usages.get_html_route_usages()?;
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut add = |location: protocol::Location| {
            if seen.insert((location.uri.clone(), location.range.start)) {
                result.push(location);
            }
        };
        let mut route_names = HashSet::with_capacity(routes.len());
        for route in routes {
            if route.name.is_empty() || !route_names.insert(lowercase(&route.name)) {
                continue;
            }
            for usage in self.usages.get_route(&route.name)? {
                add(route_usage_location(&usage));
            }
            if include_declaration {
                add(line_location(&route.file_path, i64::from(route.line)));
            }
        }
        for usage in &html_usages {
            if routes
                .iter()
                .any(|route| route_path_matches(&route.path, &usage.path))
            {
                add(route_usage_location(usage));
            }
        }
        Ok(result)
    }
}

fn route_usage_location(usage: &RouteUsage) -> protocol::Location {
    line_location(&usage.file, i64::from(usage.line))
}
