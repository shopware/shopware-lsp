//! The portable counterpart of the reference plugin's Symfony Endpoints
//! provider: HTTP method/path metadata at route declarations, navigating to
//! the resolved controller action.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Symbol;
use swlsp_symfony::{ControllerResolution, Route, ServiceIndex};

use crate::{
    by_line_title, cancelled, line_range, lower_ext, related_lens, related_source_line,
    related_target, same_path,
};

pub struct RouteEndpointCodeLensProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl RouteEndpointCodeLensProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }
}

impl CodeLensProvider for RouteEndpointCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(php) = &self.php else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        if document.tree().is_none() {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let routes = endpoint_routes_in_document(&path, document);
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for route in routes {
            cancelled(ctx)?;
            if route.path.is_empty() || route.name.starts_with('_') {
                continue;
            }
            let Some(reference) = swlsp_symfony::parse_controller_reference(&route.controller)
            else {
                continue;
            };
            let resolution = swlsp_symfony::resolve_controller_reference(
                &reference,
                self.services.as_deref(),
                Some(php),
            )?;
            let Some(target) = endpoint_controller_target(&path, document, &resolution) else {
                continue;
            };
            let line = route.line.saturating_sub(1);
            let title = endpoint_code_lens_title(&route);
            if !seen.insert(format!("{line}:{title}:{target}")) {
                continue;
            }
            result.push(related_lens(line_range(line), title, vec![target]));
        }
        result.sort_by(by_line_title);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

fn endpoint_routes_in_document(path: &str, document: &TextDocument) -> Vec<Route> {
    let tree = document.tree();
    let lines = document.line_index();
    match lower_ext(path).as_str() {
        ".php" => swlsp_symfony::parse_php_routes_tree(path, tree, Some(lines)),
        ".yaml" | ".yml" => swlsp_symfony::parse_yaml_routes_tree(path, tree, lines),
        ".xml" => swlsp_symfony::parse_xml_routes_tree(path, tree, lines),
        _ => Vec::new(),
    }
}

fn endpoint_code_lens_title(route: &Route) -> String {
    let mut methods: Vec<String> = Vec::with_capacity(route.methods.len());
    for method in &route.methods {
        let method = swlsp_textutil::uppercase(method.trim());
        if method.is_empty() || methods.contains(&method) {
            continue;
        }
        methods.push(method);
    }
    let method_label = if methods.is_empty() {
        "ANY".to_owned()
    } else {
        methods.join("|")
    };
    let mut title = format!("{method_label} {}", route.path);
    if !route.name.is_empty() {
        title.push_str(" · ");
        title.push_str(&route.name);
    }
    title
}

fn endpoint_controller_target(
    document_path: &str,
    document: &TextDocument,
    resolution: &ControllerResolution,
) -> Option<String> {
    let symbol: &Symbol = if resolution.method_found {
        resolution.method.as_ref()?
    } else if resolution.class_found {
        resolution.class.as_ref()?
    } else {
        return None;
    };
    let range = if symbol.selection_range.is_empty() {
        symbol.range
    } else {
        symbol.selection_range
    };
    let line = if same_path(&symbol.path, document_path) {
        i64::from(document.line_index().position_utf16(range.start().into()).0) + 1
    } else {
        related_source_line(&symbol.path, range.start().into())
    };
    Some(related_target(&symbol.path, line))
}
