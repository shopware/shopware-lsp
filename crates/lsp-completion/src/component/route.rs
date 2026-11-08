//! Route names, paths and parameters in PHP, Twig and JavaScript (Go
//! `route_completion.go`).

use std::cell::OnceCell;
use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_symfony::{Route, RouteIndexer, TwigHtmlRouteReference};
use swlsp_syntax::{LineIndex, SyntaxNodeExt, TextRange};

use super::util::{kind, request_offset};
use crate::common::{go_to_lower, php_context, protocol_range, text_edit};
use swlsp_gosort::sort_slice;

pub struct RouteCompletionProvider {
    routes: Arc<RouteIndexer>,
    /// Supplies the request's PHP analysis (Go reads it from the enriched
    /// context); without it PHP route contexts are recognized syntactically.
    php: Option<PhpIndex>,
}

impl RouteCompletionProvider {
    pub fn new(routes: Arc<RouteIndexer>, php: Option<PhpIndex>) -> Self {
        Self { routes, php }
    }

    fn php_context(&self, request: &CompletionRequest) -> Option<PhpContext> {
        php_context(self.php.as_ref()?, &request.syntax)
    }

    fn php_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let cell: OnceCell<Option<PhpContext>> = OnceCell::new();
        let context = |request: &CompletionRequest| -> Option<&PhpContext> {
            cell.get_or_init(|| self.php_context(request)).as_ref()
        };
        if let Some(node) = request.node.as_ref()
            && phpquery::string_at(node).is_some()
            && let Some(php_context) = context(request)
            && let Some(range) = swlsp_php::assistant_argument_reference(php_context, node, "Route")
        {
            return self.route_reference_completions(ctx, range, request.line_index());
        }
        if let Some(line_index) = request.line_index() {
            let offset = request_offset(request).unwrap_or_default();
            let source = request.source();
            if let Some(reference) =
                swlsp_symfony::php_route_annotation_path_reference_at(source, offset)
            {
                return self.route_path_completions(ctx, reference.range, line_index);
            }
            if let Some(reference) =
                swlsp_symfony::php_route_annotation_name_reference_at(source, offset)
            {
                return php_route_name_suggestion_completion(request, reference.range, offset);
            }
        }
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(reference) = swlsp_symfony::php_route_path_reference_at(node)
            && let Some(line_index) = request.line_index()
        {
            return self.route_path_completions(ctx, reference.range, line_index);
        }
        if let Some(reference) = swlsp_symfony::php_route_name_reference_at(node) {
            let offset = request_offset(request).unwrap_or(reference.range.start().into());
            return php_route_name_suggestion_completion(request, reference.range, offset);
        }
        if swlsp_symfony::is_php_route_name_in_context(None, node)
            && swlsp_symfony::is_php_route_name_in_context(context(request), node)
        {
            return self.route_completions(ctx);
        }
        if let Some(route_name) = swlsp_symfony::php_route_parameter_context(node) {
            return self.route_parameter_completions(ctx, &route_name);
        }
        Vec::new()
    }

    fn route_reference_completions(
        &self,
        ctx: &Context,
        range: TextRange,
        line_index: Option<&LineIndex>,
    ) -> Vec<protocol::CompletionItem> {
        let mut items = self.route_completions(ctx);
        let Some(line_index) = line_index else {
            return items;
        };
        let edit_range = protocol_range(range, line_index);
        for item in &mut items {
            item.text_edit = Some(text_edit(edit_range, item.label.clone()));
        }
        items
    }

    fn routes_view(&self) -> Option<Arc<[Route]>> {
        self.routes.get_routes_view().ok()
    }

    fn route_path_completions(
        &self,
        ctx: &Context,
        range: TextRange,
        line_index: &LineIndex,
    ) -> Vec<protocol::CompletionItem> {
        let Some(view) = self.routes_view() else {
            return Vec::new();
        };
        let routes = sorted_routes(&view, true);
        let edit_range = protocol_range(range, line_index);
        let mut seen = HashSet::with_capacity(routes.len());
        let mut result = Vec::with_capacity(routes.len());
        for route in routes {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if route.path.is_empty() || route.name.is_empty() || route.name.starts_with('_') {
                continue;
            }
            if !seen.insert(route.path.clone()) {
                continue;
            }
            result.push(protocol::CompletionItem {
                filter_text: format!("{} {}", route.path, route.name),
                kind: kind(protocol::CompletionItemKind::REFERENCE),
                text_edit: Some(text_edit(edit_range, route.path.clone())),
                label: route.path.clone(),
                detail: route.name.clone(),
                ..Default::default()
            });
        }
        result
    }

    fn twig_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if swlsp_symfony::is_twig_route_name(node) {
            return self.route_completions(ctx);
        }
        if let Some(route_name) = swlsp_symfony::twig_route_parameter_context(node) {
            return self.route_parameter_completions(ctx, &route_name);
        }
        if let Some(reference) = swlsp_symfony::twig_html_route_reference_at(node)
            && let Some(line_index) = request.line_index()
        {
            return self.html_route_completions(ctx, &reference, line_index);
        }
        Vec::new()
    }

    fn js_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let Some(reference) = swlsp_symfony::js_route_url_reference_at(node) else {
            return Vec::new();
        };
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        let Some(view) = self.routes_view() else {
            return Vec::new();
        };
        let routes = sorted_routes(&view, false);
        let edit_range = protocol_range(reference.range, line_index);
        let mut seen = HashSet::with_capacity(routes.len());
        let mut result = Vec::with_capacity(routes.len());
        for route in routes {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if route.name.is_empty() || route.path.is_empty() {
                continue;
            }
            if !seen.insert(route.name.clone()) {
                continue;
            }
            let detail = route_detail(route);
            let edit = text_edit(edit_range, route.path.clone());
            if !route.name.starts_with('_') {
                result.push(protocol::CompletionItem {
                    label: route.path.clone(),
                    filter_text: format!("{} {}", route.path, route.name),
                    kind: kind(protocol::CompletionItemKind::REFERENCE),
                    detail: route.name.clone(),
                    text_edit: Some(edit.clone()),
                    ..Default::default()
                });
            }
            result.push(protocol::CompletionItem {
                filter_text: format!("{} {}", route.name, route.path),
                label: route.name.clone(),
                kind: kind(protocol::CompletionItemKind::REFERENCE),
                detail,
                text_edit: Some(edit),
                ..Default::default()
            });
        }
        result
    }

    fn html_route_completions(
        &self,
        ctx: &Context,
        reference: &TwigHtmlRouteReference,
        line_index: &LineIndex,
    ) -> Vec<protocol::CompletionItem> {
        let Some(view) = self.routes_view() else {
            return Vec::new();
        };
        let routes = sorted_routes(&view, false);
        let edit_range = protocol_range(reference.node.range_trimmed_trivia(), line_index);
        let mut seen = HashSet::with_capacity(routes.len());
        let mut result = Vec::with_capacity(routes.len());
        for route in routes {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if route.name.is_empty() || !seen.insert(route.name.clone()) {
                continue;
            }
            result.push(protocol::CompletionItem {
                filter_text: format!("{} {}", route.name, route.path),
                kind: kind(protocol::CompletionItemKind::REFERENCE),
                detail: route_detail(route),
                text_edit: Some(text_edit(
                    edit_range,
                    twig_path_expression(reference, route),
                )),
                label: route.name.clone(),
                ..Default::default()
            });
        }
        result
    }

    fn route_completions(&self, ctx: &Context) -> Vec<protocol::CompletionItem> {
        let Some(view) = self.routes_view() else {
            return Vec::new();
        };
        let routes = sorted_routes(&view, false);
        let mut seen = HashSet::with_capacity(routes.len());
        let mut completions = Vec::with_capacity(routes.len());
        for route in routes {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if !seen.insert(route.name.clone()) {
                continue;
            }
            let mut detail = route_detail(route);
            let parameters = route.parameters();
            if !parameters.is_empty() {
                detail.push_str(&format!(" ({})", parameters.join(", ")));
            }
            completions.push(protocol::CompletionItem {
                label: route.name.clone(),
                kind: kind(protocol::CompletionItemKind::REFERENCE),
                detail,
                ..Default::default()
            });
        }
        completions
    }

    fn route_parameter_completions(
        &self,
        ctx: &Context,
        route_name: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Ok(routes) = self.routes.get_route(route_name) else {
            return Vec::new();
        };
        let mut names = Vec::new();
        let mut seen = HashSet::new();
        for route in &routes {
            for name in route.parameters() {
                if seen.insert(name.clone()) {
                    names.push(name);
                }
            }
        }
        if !seen.contains("_fragment") {
            names.push("_fragment".to_string());
        }
        names.sort();
        let mut completions = Vec::with_capacity(names.len());
        for name in names {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            completions.push(protocol::CompletionItem {
                label: name,
                kind: kind(protocol::CompletionItemKind::FIELD),
                detail: format!("Route parameter for {route_name}"),
                ..Default::default()
            });
        }
        completions
    }
}

impl CompletionProvider for RouteCompletionProvider {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        match go_to_lower(swlsp_uriutil::filepath::ext(
            &request.params.text_document.uri,
        ))
        .as_str()
        {
            ".php" => self.php_completions(ctx, request),
            ".twig" => self.twig_completions(ctx, request),
            ".js" | ".ts" => self.js_completions(ctx, request),
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}

fn route_detail(route: &Route) -> String {
    let mut detail = route.path.clone();
    if !route.methods.is_empty() {
        detail.push_str(&format!(" [{}]", route.methods.join("|")));
    }
    detail
}

fn php_route_name_suggestion_completion(
    request: &CompletionRequest,
    range: TextRange,
    offset: u32,
) -> Vec<protocol::CompletionItem> {
    let (Some(root), Some(node), Some(line_index)) = (
        request.root.as_ref(),
        request.node.as_ref(),
        request.line_index(),
    ) else {
        return Vec::new();
    };
    let name = swlsp_symfony::php_route_name_suggestion(root, node, offset);
    if name.is_empty() {
        return Vec::new();
    }
    let mut detail = "Generated Symfony route name".to_string();
    if let Some(method) = phpquery::method_at(node) {
        let class_name = phpquery::class_name(&method);
        let method_name = phpquery::method_name(&method);
        if !class_name.is_empty() && !method_name.is_empty() {
            detail.push_str(&format!(" · {class_name}::{method_name}"));
        }
    }
    vec![protocol::CompletionItem {
        label: name.clone(),
        kind: kind(protocol::CompletionItemKind::REFERENCE),
        detail,
        text_edit: Some(text_edit(protocol_range(range, line_index), name)),
        ..Default::default()
    }]
}

fn twig_path_expression(reference: &TwigHtmlRouteReference, route: &Route) -> String {
    let container_text = reference.container.text().to_string();
    let quote = if container_text.trim().starts_with('\'') {
        '"'
    } else {
        '\''
    };
    let quoted = |value: &str| {
        let mut escaped = value.replace('\\', "\\\\");
        if quote == '\'' {
            escaped = escaped.replace('\'', "\\'");
        } else {
            escaped = escaped.replace('"', "\\\"");
        }
        format!("{quote}{escaped}{quote}")
    };
    let mut result = String::from("{{ path(");
    result.push_str(&quoted(&route.name));
    let parameters = route.parameters();
    if !parameters.is_empty() {
        result.push_str(", {");
        for (index, parameter) in parameters.iter().enumerate() {
            if index != 0 {
                result.push_str(", ");
            }
            result.push_str(&quoted(parameter));
            result.push_str(": ");
            result.push_str(&quoted("x"));
        }
        result.push('}');
    }
    result.push_str(") }}");
    result
}

/// Routes of `view` in Go `sort.Slice` order, by name or by (path, name).
fn sorted_routes(view: &[Route], by_path: bool) -> Vec<&Route> {
    let mut routes: Vec<&Route> = view.iter().collect();
    if by_path {
        sort_slice(&mut routes, |left, right| {
            if left.path == right.path {
                left.name < right.name
            } else {
                left.path < right.path
            }
        });
    } else {
        sort_slice(&mut routes, |left, right| left.name < right.name);
    }
    routes
}
