//! Route names, route parameters, route paths and routing resources navigate to
//! route declarations and imported files.

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_symfony::{Route, RouteIndexer, RouteResourceResolver};
use swlsp_uriutil::file_uri;

use crate::common::{ext_lower, request_offset, request_uri};
use crate::php_common::{php_context, unclamped_line_location};

pub struct RouteDefinitionProvider {
    routes: Option<Arc<RouteIndexer>>,
    php: Option<PhpIndex>,
    resolver: RouteResourceResolver,
}

impl RouteDefinitionProvider {
    pub fn new(routes: Option<Arc<RouteIndexer>>, php: Option<PhpIndex>) -> Self {
        Self {
            routes,
            resolver: RouteResourceResolver::new(php.clone(), None),
            php,
        }
    }

    fn get_route(&self, name: &str) -> Vec<Route> {
        self.routes
            .as_deref()
            .and_then(|routes| routes.get_route(name).ok())
            .unwrap_or_default()
    }

    fn find_routes_by_path(&self, value: &str) -> Vec<Route> {
        self.routes
            .as_deref()
            .and_then(|routes| routes.find_routes_by_path(value).ok())
            .unwrap_or_default()
    }

    fn php_definition(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let context = php_context(self.php.as_ref(), request);
        if let Some(context) = context.as_ref()
            && swlsp_php::assistant_argument_reference(context, node, "Route").is_some()
            && self.routes.is_some()
        {
            return route_locations(&self.get_route(&phpquery::string_value(node)));
        }
        if let Some(reference) = swlsp_symfony::php_route_path_reference_at(node)
            && let Some(offset) = request_offset(request)
            && let Some(prefix) = reference.prefix_at(offset)
        {
            let routes = self
                .routes
                .as_deref()
                .and_then(|routes| {
                    routes
                        .find_routes_by_path_prefix(&prefix, &reference.value)
                        .ok()
                })
                .unwrap_or_default();
            return route_locations(&routes);
        }
        if swlsp_symfony::is_php_route_name_in_context(context.as_ref(), node) {
            return route_locations(&self.get_route(&phpquery::string_value(node)));
        }
        if let Some((route_name, parameter)) = swlsp_symfony::php_route_parameter_reference_at(node)
        {
            return route_parameter_locations(&self.get_route(&route_name), &parameter);
        }
        Vec::new()
    }

    fn twig_definition(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if swlsp_symfony::is_twig_route_name(node) {
            let name = twigquery::literal_string_at(node)
                .and_then(|literal| twigquery::string_value(&literal))
                .unwrap_or_default();
            return route_locations(&self.get_route(&name));
        }
        if let Some((route_name, parameter)) =
            swlsp_symfony::twig_route_parameter_reference_at(node)
        {
            return route_parameter_locations(&self.get_route(&route_name), &parameter);
        }
        if let Some(reference) = swlsp_symfony::twig_html_route_reference_at(node) {
            return route_locations(&self.find_routes_by_path(&reference.value));
        }
        Vec::new()
    }

    fn js_definition(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match swlsp_symfony::js_route_url_reference_at(node) {
            Some(reference) if !reference.value.is_empty() => {
                route_locations(&self.find_routes_by_path(&reference.value))
            }
            _ => Vec::new(),
        }
    }
}

impl GotoDefinitionProvider for RouteDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        match ext_lower(request_uri(request)).as_str() {
            ".php" => {
                if request.node.is_none() {
                    return Vec::new();
                }
                let locations = route_resource_definitions(request, &self.resolver);
                if !locations.is_empty() {
                    return locations;
                }
                self.php_definition(request)
            }
            ".yaml" | ".yml" | ".xml" => route_resource_definitions(request, &self.resolver),
            ".twig" => self.twig_definition(request),
            ".js" | ".ts" => self.js_definition(request),
            _ => Vec::new(),
        }
    }
}

fn route_resource_definitions(
    request: &DefinitionRequest,
    resolver: &RouteResourceResolver,
) -> Vec<protocol::Location> {
    let Some(node) = request.node.as_ref() else {
        return Vec::new();
    };
    let Some(reference) = swlsp_symfony::route_resource_reference_at(node) else {
        return Vec::new();
    };
    let Ok(current_path) = swlsp_uriutil::path(request_uri(request)) else {
        return Vec::new();
    };
    resolver
        .files(&current_path, &reference)
        .iter()
        .map(|path| protocol::Location {
            uri: file_uri(path),
            range: protocol::Range::default(),
        })
        .collect()
}

fn route_parameter_locations(routes: &[Route], parameter: &str) -> Vec<protocol::Location> {
    let matched: Vec<Route> = routes
        .iter()
        .filter(|route| route.parameters().iter().any(|name| name == parameter))
        .cloned()
        .collect();
    route_locations(&matched)
}

fn route_locations(routes: &[Route]) -> Vec<protocol::Location> {
    routes
        .iter()
        .map(|route| unclamped_line_location(&route.file_path, route.line))
        .collect()
}

#[cfg(test)]
#[path = "route_tests.rs"]
mod tests;
