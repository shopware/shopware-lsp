//! Missing and deprecated route names in PHP and Twig (Go
//! `route_diagnostics.go`).

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_symfony::{ClassConstantLookup, Route, RouteIndexer, ServiceIndex};
use swlsp_syntax::{SyntaxNode, TextRange};

use super::controller::DEPRECATED_CONTROLLER_CODE;
use super::{deprecated_tags, lower_ext, problem, similar_value};
use crate::ProblemAnalyzer;
use crate::common::value_node_text_range;

/// Go `RouteAnalyzer`.
pub struct RouteAnalyzer {
    route_index: Option<Arc<RouteIndexer>>,
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

impl RouteAnalyzer {
    pub fn new(
        route_index: Option<Arc<RouteIndexer>>,
        service_index: Option<Arc<ServiceIndex>>,
        php_index: Option<PhpIndex>,
    ) -> Self {
        Self {
            route_index,
            service_index,
            php_index,
        }
    }
}

impl ProblemAnalyzer for RouteAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::inputs(&self.route_index)
                + crate::inputs(&self.service_index)
                + crate::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(routes) = &self.route_index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        match lower_ext(&document.uri).as_str() {
            ".php" => {
                let mut literals = Vec::new();
                let mut seen: HashSet<TextRange> = HashSet::new();
                let context = match &self.php_index {
                    Some(php) => swlsp_lsp_phpanalysis::context_for_document(php, document)?,
                    None => None,
                };
                for call in phpquery::calls(&root, &[]) {
                    let Some(literal) = phpquery::string_argument(&call, 0) else {
                        continue;
                    };
                    if swlsp_symfony::is_php_route_name_in_context(context.as_ref(), &literal) {
                        seen.insert(literal.text_range());
                        literals.push(literal);
                    }
                }
                if self.php_index.is_some() {
                    for literal in phpquery::nodes(&root, &[PHP_STRING]) {
                        if seen.contains(&literal.text_range()) {
                            continue;
                        }
                        let tagged = context.as_ref().is_some_and(|context| {
                            swlsp_php::assistant_argument_tags(context, &literal, &["Route"])
                                .is_some_and(|(_, tags)| !tags.is_empty())
                        });
                        if tagged {
                            literals.push(literal);
                        }
                    }
                }
                self.missing_routes(ctx, routes, document, &literals)
            }
            ".twig" => {
                let mut literals =
                    twigquery::string_arguments_in_functions(&root, &["seoUrl", "url", "path"]);
                for reference in swlsp_symfony::twig_route_comparison_references(&root) {
                    literals.push(reference.node);
                }
                self.missing_routes(ctx, routes, document, &literals)
            }
            _ => Ok(Vec::new()),
        }
    }
}

impl RouteAnalyzer {
    fn missing_routes(
        &self,
        ctx: &Context,
        route_index: &RouteIndexer,
        document: &TextDocument,
        literals: &[SyntaxNode],
    ) -> Result<Vec<Problem>, BoxError> {
        let mut result = Vec::new();
        let mut candidate_names: Vec<String> = Vec::new();
        let mut resolved_names: HashSet<String> = HashSet::new();
        let mut candidates_loaded = false;
        for literal in literals {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let name = route_string_value(document, literal);
            if !swlsp_symfony::is_static_route_name(&name) {
                continue;
            }
            let routes = route_index
                .get_route(&name)
                .map_err(|error| format!("query Symfony route {name:?}: {error}"))?;
            if !routes.is_empty() {
                if self.route_controller_deprecated(&routes)? {
                    let mut problem = problem(
                        DEPRECATED_CONTROLLER_CODE,
                        value_node_text_range(Some(literal), &name),
                        format!("Controller action for route '{name}' is deprecated"),
                        DiagnosticSeverity::HINT,
                    );
                    problem.tags = deprecated_tags();
                    result.push(problem);
                }
                continue;
            }
            if !candidates_loaded {
                // Routes named by a class constant reach the index without a
                // literal name, because the constant lives in another file.
                // Ask for them here, where the PHP index is available to
                // resolve them, before deciding anything is missing.
                let lookup = self
                    .php_index
                    .as_ref()
                    .map(|php| php as &dyn ClassConstantLookup);
                let all_routes = route_index
                    .resolved_routes(lookup)
                    .map_err(|error| format!("query Symfony routes: {error}"))?;
                let mut seen = HashSet::new();
                for route in all_routes {
                    if route.name.is_empty() || !seen.insert(route.name.clone()) {
                        continue;
                    }
                    candidate_names.push(route.name.clone());
                    resolved_names.insert(route.name);
                }
                candidates_loaded = true;
            }
            if resolved_names.contains(&name) {
                continue;
            }
            let mut problem = problem(
                "symfony.route.missing",
                value_node_text_range(Some(literal), &name),
                format!("Route '{name}' not found"),
                DiagnosticSeverity::ERROR,
            );
            problem.payload = Some(json!({
                "routeName": name,
                "suggestions": similar_value(&name, &candidate_names),
            }));
            result.push(problem);
        }
        Ok(result)
    }

    fn route_controller_deprecated(&self, routes: &[Route]) -> Result<bool, BoxError> {
        let (Some(services), Some(php)) = (&self.service_index, &self.php_index) else {
            return Ok(false);
        };
        for route in routes {
            let Some(reference) = swlsp_symfony::parse_controller_reference(&route.controller)
            else {
                continue;
            };
            let resolution =
                swlsp_symfony::resolve_controller_reference(&reference, Some(services), Some(php))
                    .map_err(|error| {
                        format!("resolve controller for route {:?}: {error}", route.name)
                    })?;
            if resolution.deprecated() {
                return Ok(true);
            }
        }
        Ok(false)
    }
}

fn route_string_value(document: &TextDocument, node: &SyntaxNode) -> String {
    if lower_ext(&document.uri) == ".php" {
        return phpquery::string_value(node);
    }
    twigquery::string_value(node).unwrap_or_default()
}
