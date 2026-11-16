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

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_ARGUMENT, JS_ARROW_FUNCTION, JS_FUNCTION, JS_IDENTIFIER, JS_MEMBER_EXPRESSION, JS_METHOD,
    JS_STRING, JS_VARIABLE_DECLARATION, TK_KEYWORD,
};
use swlsp_symfony::{Route, RouteIndexer};
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

pub const ADMIN_API_ROUTE_NOT_FOUND_CODE: &str = "admin.api.route-not-found";
pub const ADMIN_API_ROUTE_METHOD_CODE: &str = "admin.api.route-method";

/// Every code of [`AdminApiRouteAnalyzer`].
pub const ADMIN_API_ROUTE_CODES: &[&str] =
    &[ADMIN_API_ROUTE_NOT_FOUND_CODE, ADMIN_API_ROUTE_METHOD_CODE];

const VERBS: &[&str] = &["get", "post", "patch", "put", "delete"];
/// A part of a path that is only known at runtime.
const WILDCARD: &str = "\u{0}";

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
        let name = lowercase(&filepath::base(&document.uri));
        if !document.uri.contains("Resources/app/administration")
            || !(name.ends_with(".js") || name.ends_with(".ts"))
            // Tests request paths that exist only in their mocks.
            || name.contains(".spec.")
            || name.contains(".test.")
            || !document.source().contains("httpClient")
        {
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
            let shown = path.replace(WILDCARD, "{…}");
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

/// A request `httpClient` sends to a declared Admin API action route.
pub(crate) struct ApiRequest {
    /// `get`, `post`, …
    pub verb: String,
    /// `/api/…`, with [`WILDCARD`] for what is only known at runtime.
    pub path: String,
    /// The argument that gives the path.
    pub argument: SyntaxNode,
}

/// The request of an `httpClient.<verb>(path, …)` call; `None` for any
/// other call, a path that cannot be told and a generic entity endpoint.
pub(crate) fn api_request(call: &SyntaxNode) -> Option<ApiRequest> {
    let verb = http_client_verb(call)?;
    let argument = jsquery::iter_arguments(call).next()?;
    let path = request_path(call, &argument)?;
    is_action_path(&segments(&path)).then_some(ApiRequest {
        verb,
        path,
        argument,
    })
}

/// The routes among `routes` that can answer `path`.
pub(crate) fn answering_routes<'r>(routes: &[&'r Route], path: &str) -> Vec<&'r Route> {
    let requested = segments(path);
    routes
        .iter()
        .copied()
        .filter(|route| path_matches(&segments(&route.path), &requested))
        .collect()
}

/// The declared action routes of the project.
pub(crate) fn action_routes(routes: &[Route]) -> Vec<&Route> {
    routes
        .iter()
        .filter(|route| is_action_path(&segments(&route.path)))
        .collect()
}

fn trimmed(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

/// `get`, `post`, … when `call` is `<something>.httpClient.<verb>(…)`.
fn http_client_verb(call: &SyntaxNode) -> Option<String> {
    let callee =
        jsquery::call_callee(call).filter(|callee| callee.kind() == JS_MEMBER_EXPRESSION)?;
    let verb = jsquery::call_method_name(call)?;
    if !VERBS.contains(&verb.as_str()) {
        return None;
    }
    let object = callee.children().next()?;
    let object: String = trimmed(&object)
        .chars()
        .filter(|character| !character.is_whitespace())
        .collect();
    (object == "httpClient" || object.ends_with(".httpClient")).then_some(verb)
}

/// The path segments of a request or route path, without the query.
fn segments(path: &str) -> Vec<String> {
    let path = path.split(['?', '#']).next().unwrap_or(path);
    path.split('/')
        .filter(|segment| !segment.is_empty())
        .map(str::to_owned)
        .collect()
}

/// `/api/_action/…`: a declared action, not a generic entity endpoint.
fn is_action_path(segments: &[String]) -> bool {
    matches!(segments, [api, first, ..] if api == "api" && first.starts_with('_'))
}

/// Whether a route path can answer the requested path. A placeholder
/// matches any segment, and as the last route segment also several: its
/// requirement may allow slashes.
fn path_matches(route: &[String], requested: &[String]) -> bool {
    let placeholder = |segment: &String| segment.contains('{');
    let unknown = |segment: &String| segment.contains(WILDCARD);
    if requested.len() < route.len() {
        // Trailing placeholders may have defaults.
        return route[requested.len()..].iter().all(placeholder)
            && route.iter().zip(requested).all(|(route, requested)| {
                placeholder(route) || unknown(requested) || route == requested
            });
    }
    if requested.len() > route.len() && !route.last().is_some_and(placeholder) {
        return false;
    }
    route
        .iter()
        .zip(requested)
        .all(|(route, requested)| placeholder(route) || unknown(requested) || route == requested)
}

/// The path a call requests, as `/api/…` with [`WILDCARD`] for what is only
/// known at runtime. `None` when the path cannot be told.
fn request_path(call: &SyntaxNode, argument: &SyntaxNode) -> Option<String> {
    let mut children = argument.children();
    let expression = children.next().filter(|_| children.next().is_none())?;
    let value = match expression.kind() {
        JS_STRING => expression,
        JS_IDENTIFIER => local_constant(call, &trimmed(&expression))?,
        _ => return None,
    };
    let text = trimmed(&value);
    let path = if text.starts_with('`') {
        template_path(&text, call)?
    } else {
        jsquery::string_value(&value)
    };
    if path.is_empty() || path.starts_with(WILDCARD) || path.contains("://") {
        return None;
    }
    let path = format!("/{}", path.trim_start_matches('/'));
    Some(if path == "/api" || path.starts_with("/api/") {
        path
    } else {
        format!("/api{path}")
    })
}

/// The function `node` is in.
fn enclosing_function(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors()
        .skip(1)
        .find(|ancestor| matches!(ancestor.kind(), JS_METHOD | JS_FUNCTION | JS_ARROW_FUNCTION))
}

/// The string a `const name = '…'` of the enclosing function holds, when it
/// is the only declaration of that name there.
fn local_constant(call: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    let function = enclosing_function(call)?;
    let mut declarations = jsquery::nodes(&function, &[JS_VARIABLE_DECLARATION])
        .into_iter()
        .filter(|declaration| {
            declaration
                .children()
                .next()
                .is_some_and(|binding| binding.kind() == JS_IDENTIFIER && trimmed(&binding) == name)
        });
    let declaration = declarations
        .next()
        .filter(|_| declarations.next().is_none())?;
    let constant = declaration
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| token.kind() == TK_KEYWORD && token.text() == "const");
    let mut children = declaration.children().skip(1);
    let value = children.next().filter(|_| children.next().is_none())?;
    (constant && value.kind() == JS_STRING).then_some(value)
}

/// A template string as a path: `${this.getApiBasePath()}` is the endpoint
/// of the service, any other interpolation is a [`WILDCARD`].
fn template_path(template: &str, call: &SyntaxNode) -> Option<String> {
    let body = template.strip_prefix('`')?.strip_suffix('`')?;
    let mut path = String::new();
    let mut rest = body;
    while let Some(start) = rest.find("${") {
        path.push_str(&rest[..start]);
        let expression = &rest[start + 2..];
        let mut depth = 1usize;
        let end = expression.char_indices().find_map(|(index, character)| {
            match character {
                '{' => depth += 1,
                '}' => depth -= 1,
                _ => {}
            }
            (depth == 0).then_some(index)
        })?;
        let inner: String = expression[..end]
            .chars()
            .filter(|character| !character.is_whitespace())
            .collect();
        if inner == "this.getApiBasePath()" {
            path.push('/');
            path.push_str(service_endpoint(call)?.trim_matches('/'));
        } else if inner.contains("getApiBasePath") {
            // With an ID or a prefix the base path has more to it.
            return None;
        } else {
            path.push_str(WILDCARD);
        }
        rest = &expression[end + 1..];
    }
    path.push_str(rest);
    Some(path)
}

/// The `apiEndpoint` of the `ApiService` class `node` is in: the default of
/// the constructor parameter, or the literal passed to `super()`.
fn service_endpoint(node: &SyntaxNode) -> Option<String> {
    let class = node.ancestors().find(|ancestor| {
        ancestor.kind() == JS_FUNCTION
            && ancestor
                .children_with_tokens()
                .filter_map(|element| element.into_token())
                .any(|token| token.kind() == TK_KEYWORD && token.text() == "class")
    })?;
    let constructor = jsquery::nodes(&class, &[JS_METHOD])
        .into_iter()
        .find(|method| jsquery::property_name(method).as_deref() == Some("constructor"))?;
    let parameter = jsquery::nodes(&constructor, &[JS_ARGUMENT])
        .into_iter()
        .find_map(|argument| {
            let mut children = argument.children();
            let name = children.next()?;
            let value = children.next()?;
            (name.kind() == JS_IDENTIFIER
                && trimmed(&name) == "apiEndpoint"
                && value.kind() == JS_STRING)
                .then(|| jsquery::string_value(&value))
        });
    parameter.or_else(|| {
        jsquery::iter_calls(&constructor, &[])
            .find(|call| jsquery::call_name(call).as_deref() == Some("super"))
            .and_then(|call| jsquery::argument_expression(&call, 2))
            .filter(|endpoint| endpoint.kind() == JS_STRING && !trimmed(endpoint).contains("${"))
            .map(|endpoint| jsquery::string_value(&endpoint))
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn route_paths_answer_requested_paths() {
        let segments = |path: &str| segments(&path.replace('*', WILDCARD));
        for (route, requested, expected) in [
            ("/api/_action/swag/run", "/api/_action/swag/run", true),
            ("/api/_action/swag/run", "/api/_action/swag/run?x=1", true),
            ("/api/_action/swag/run", "/api/_action/swag/other", false),
            (
                "/api/_action/order/{id}/state",
                "/api/_action/order/*/state",
                true,
            ),
            (
                "/api/_action/order/{id}/state",
                "/api/_action/order/abc/state",
                true,
            ),
            (
                "/api/_action/order/{id}/state",
                "/api/_action/order/*/other",
                false,
            ),
            ("/api/_action/swag/run", "/api/_action/*/run", true),
            // A last placeholder may take the rest, and may be left out.
            ("/api/_action/proxy/{path}", "/api/_action/proxy/a/b", true),
            ("/api/_action/list/{page}", "/api/_action/list", true),
            ("/api/_action/list/{page}/x", "/api/_action/list", false),
            ("/api/_action/swag", "/api/_action/swag/more", false),
        ] {
            assert_eq!(
                path_matches(&segments(route), &segments(requested)),
                expected,
                "{route} <- {requested}"
            );
        }
        assert!(is_action_path(&segments("/api/_action/x")));
        assert!(is_action_path(&segments("/api/_info/version")));
        assert!(!is_action_path(&segments("/api/product")));
        assert!(!is_action_path(&segments("/store-api/_action/x")));
    }
}
