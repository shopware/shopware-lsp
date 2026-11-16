//! Administration calls to Admin API action routes that no controller
//! declares.
//!
//! `this.httpClient.post('/_action/swag/run')` requests `/api/_action/swag/run`.
//! The path is followed through a local constant, a template string and the
//! `getApiBasePath()` of an `ApiService`, and matched against the indexed
//! routes. Only paths below a segment starting with `_` are checked: the
//! generic entity endpoints (`/api/product`, `/api/search/order`) answer
//! every entity and are generated, not declared.

use std::sync::Arc;

use swlsp_admin::{
    API_PATH_WILDCARD, ApiRequest, api_request, api_route_answers, is_api_action_path,
    is_api_request_file,
};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_symfony::{Route, RouteIndexer};
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};

use crate::diagnostics::ProblemAnalyzer;

pub const ADMIN_API_ROUTE_NOT_FOUND_CODE: &str = "admin.api.route-not-found";
pub const ADMIN_API_ROUTE_METHOD_CODE: &str = "admin.api.route-method";

/// Every code of [`AdminApiRouteAnalyzer`].
pub const ADMIN_API_ROUTE_CODES: &[&str] =
    &[ADMIN_API_ROUTE_NOT_FOUND_CODE, ADMIN_API_ROUTE_METHOD_CODE];

pub struct AdminApiRouteAnalyzer {
    routes: Option<Arc<RouteIndexer>>,
}

impl AdminApiRouteAnalyzer {
    pub fn new(routes: Option<Arc<RouteIndexer>>) -> Self {
        Self { routes }
    }
}

impl ProblemAnalyzer for AdminApiRouteAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        match &self.routes {
            Some(routes) if document.uri.contains("Resources/app/administration") => {
                Some(routes.revision())
            }
            _ => Some(0),
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.routes, document.root()) else {
            return Ok(Vec::new());
        };
        if !is_api_request_file(&document.uri) || !document.source().contains("httpClient") {
            return Ok(Vec::new());
        }
        let routes = index.get_routes_view()?;
        let actions = action_routes(&routes);
        // Without any action route the PHP side is not indexed (yet).
        if actions.is_empty() {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for call in jsquery::iter_calls(&root, &[]) {
            ctx.check()?;
            let Some(ApiRequest {
                verb,
                path,
                argument,
            }) = api_request(&call)
            else {
                continue;
            };
            let matching = answering_routes(&actions, &path);
            let shown = path.replace(API_PATH_WILDCARD, "{…}").replace("//", "/");
            let method = verb.to_uppercase();
            let problem = if matching.is_empty() {
                Some((
                    ADMIN_API_ROUTE_NOT_FOUND_CODE,
                    format!("No Admin API route answers {method} {shown}"),
                ))
            } else if matching.iter().all(|route| {
                !route.methods.is_empty()
                    && !route
                        .methods
                        .iter()
                        .any(|allowed| allowed.eq_ignore_ascii_case(&method))
            }) {
                let mut allowed: Vec<String> = matching
                    .iter()
                    .flat_map(|route| route.methods.iter().map(|method| method.to_uppercase()))
                    .collect();
                allowed.sort();
                allowed.dedup();
                Some((
                    ADMIN_API_ROUTE_METHOD_CODE,
                    format!(
                        "The route {shown} answers {}, not {method}",
                        allowed.join(", ")
                    ),
                ))
            } else {
                None
            };
            if let Some((code, message)) = problem {
                result.push(Problem {
                    id: code.into(),
                    range: argument.range_trimmed_trivia(),
                    element: Some(SyntaxElement::Node(argument)),
                    message,
                    severity: Some(DiagnosticSeverity::WARNING),
                    source: "shopware-lsp".into(),
                    ..Problem::default()
                });
            }
        }
        Ok(result)
    }
}

/// The routes among `routes` that can answer `path`.
pub(crate) fn answering_routes<'r>(routes: &[&'r Route], path: &str) -> Vec<&'r Route> {
    routes
        .iter()
        .copied()
        .filter(|route| api_route_answers(&route.path, path))
        .collect()
}

/// The declared action routes of the project.
pub(crate) fn action_routes(routes: &[Route]) -> Vec<&Route> {
    routes
        .iter()
        .filter(|route| is_api_action_path(&route.path))
        .collect()
}
