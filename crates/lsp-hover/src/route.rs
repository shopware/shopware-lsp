//! Symfony route name and route URL hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_symfony::RouteIndexer;
use swlsp_syntax::SyntaxNodeExt;

use crate::{
    escape_markdown, hover_protocol_range, lower_ext, markdown_hover, php_context, request_uri,
};

pub struct RouteHoverProvider {
    route_index: Option<Arc<RouteIndexer>>,
    php: Option<PhpIndex>,
}

impl RouteHoverProvider {
    /// `php` stands in for Go's request-context PHP enrichment.
    pub fn new(route_index: Option<Arc<RouteIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { route_index, php }
    }
}

impl HoverProvider for RouteHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(route_index) = self.route_index.as_deref() else {
            return Ok(None);
        };
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let literal;
        let route_name;
        let mut routes;
        let mut html_url = false;
        match lower_ext(request_uri(request)).as_str() {
            ".php" => {
                let context = php_context(self.php.as_ref(), request);
                if !swlsp_symfony::is_php_route_name_in_context(context.as_ref(), node) {
                    return Ok(None);
                }
                literal = phpquery::string_at(node);
                route_name = literal
                    .as_ref()
                    .map(phpquery::string_value)
                    .unwrap_or_default();
                routes = route_index.get_route(&route_name)?;
            }
            ".twig" => {
                if swlsp_symfony::is_twig_route_name(node) {
                    literal = twigquery::literal_string_at(node);
                    route_name = literal
                        .as_ref()
                        .and_then(twigquery::string_value)
                        .unwrap_or_default();
                    routes = route_index.get_route(&route_name)?;
                } else if let Some(reference) = swlsp_symfony::twig_html_route_reference_at(node) {
                    literal = Some(reference.node);
                    route_name = reference.value;
                    routes = route_index.find_routes_by_path(&route_name)?;
                    html_url = true;
                } else {
                    return Ok(None);
                }
            }
            _ => return Ok(None),
        }
        if routes.is_empty() {
            return Ok(None);
        }
        swlsp_gosort::sort_slice(&mut routes, |left, right| left.file_path < right.file_path);

        let title = if html_url {
            "Symfony route URL"
        } else {
            "Symfony route"
        };
        let mut markdown = format!("**{title}** `{route_name}`\n\n");
        for (index, route) in routes.iter().enumerate() {
            if index != 0 {
                markdown.push_str("\n---\n\n");
            }
            if !route.path.is_empty() {
                let _ = writeln!(markdown, "- Path: `{}`", escape_markdown(&route.path));
            }
            if html_url {
                let _ = writeln!(markdown, "- Name: `{}`", escape_markdown(&route.name));
            }
            if !route.controller.is_empty() {
                let _ = writeln!(
                    markdown,
                    "- Controller: `{}`",
                    escape_markdown(&route.controller)
                );
            }
            if !route.methods.is_empty() {
                let _ = writeln!(markdown, "- Methods: `{}`", route.methods.join("`, `"));
            }
            let parameters = route.parameters();
            if !parameters.is_empty() {
                let _ = writeln!(markdown, "- Parameters: `{}`", parameters.join("`, `"));
            }
            let _ = writeln!(
                markdown,
                "- Defined at: `{}:{}`",
                escape_markdown(&route.file_path),
                route.line
            );
        }
        let (Some(literal), Some(line_index)) = (literal, request.line_index()) else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                literal.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}
