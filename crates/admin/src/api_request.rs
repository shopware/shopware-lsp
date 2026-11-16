//! Requests Administration code sends to Admin API action routes.
//!
//! `this.httpClient.post('/_action/swag/run')` requests `/api/_action/swag/run`.
//! The path is followed through a local constant, a template string and the
//! `getApiBasePath()` of an `ApiService`. Only paths below a segment
//! starting with `_` count: the generic entity endpoints (`/api/product`,
//! `/api/search/order`) answer every entity and are generated, not declared.
//!
//! [`ApiRequestIndex`] keeps the requests of every Administration file, so
//! the callers of a route are found without reading the workspace.

use std::path::Path;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, ResultExt, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_ARGUMENT, JS_ARROW_FUNCTION, JS_FUNCTION, JS_IDENTIFIER, JS_MEMBER_EXPRESSION, JS_METHOD,
    JS_STRING, JS_VARIABLE_DECLARATION, TK_KEYWORD,
};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

const VERBS: &[&str] = &["get", "post", "patch", "put", "delete"];
/// A part of a path that is only known at runtime.
pub const API_PATH_WILDCARD: &str = "\u{0}";
const WILDCARD: &str = API_PATH_WILDCARD;

/// A request `httpClient` sends to a declared Admin API action route.
pub struct ApiRequest {
    /// `get`, `post`, …
    pub verb: String,
    /// `/api/…`, with [`API_PATH_WILDCARD`] for what is only known at runtime.
    pub path: String,
    /// The argument that gives the path.
    pub argument: SyntaxNode,
}

/// The request of an `httpClient.<verb>(path, …)` call; `None` for any
/// other call, a path that cannot be told and a generic entity endpoint.
pub fn api_request(call: &SyntaxNode) -> Option<ApiRequest> {
    let verb = http_client_verb(call)?;
    let argument = jsquery::iter_arguments(call).next()?;
    let path = request_path(call, &argument)?;
    is_api_action_path(&path).then_some(ApiRequest {
        verb,
        path,
        argument,
    })
}

/// Whether `path` is `/api/_…`: a declared action, not an entity endpoint.
pub fn is_api_action_path(path: &str) -> bool {
    is_action_path(&segments(path))
}

/// Whether the route path `route` can answer the requested `path`.
pub fn api_route_answers(route: &str, path: &str) -> bool {
    path_matches(&segments(route), &segments(path))
}

/// Whether the route path `route` is the one `path` requests: as
/// [`api_route_answers`], but a part of the request that is only known at
/// runtime has to stand where the route has a placeholder. A request to
/// `/api/_action/{…}` is no caller of every action.
pub fn api_route_is_requested(route: &str, path: &str) -> bool {
    let (route, requested) = (segments(route), segments(path));
    path_matches(&route, &requested)
        && requested
            .iter()
            .enumerate()
            .filter(|(_, segment)| segment.contains(WILDCARD))
            .all(|(index, _)| {
                route
                    .get(index)
                    .or(route.last())
                    .is_some_and(|segment| segment.contains('{'))
            })
}

/// Whether a file can hold Administration requests: script code of an
/// Administration, tests aside, which request paths of their mocks.
pub fn is_api_request_file(path: &str) -> bool {
    let path = path.replace('\\', "/");
    let name = path.rsplit('/').next().unwrap_or(&path).to_lowercase();
    path.contains("Resources/app/administration")
        && (name.ends_with(".js") || name.ends_with(".ts"))
        && !name.contains(".spec.")
        && !name.contains(".test.")
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
        } else if inner == "this.apiEndpoint" {
            // The endpoint as the service was given it, without a slash.
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

/// One request of an Administration file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ApiRequestUsage {
    /// Upper case: `GET`, `POST`, …
    pub method: String,
    /// `/api/…`, with [`API_PATH_WILDCARD`] for what is only known at runtime.
    pub path: String,
    pub file: String,
    /// The path argument, as zero-based UTF-16 positions.
    pub start_line: u32,
    pub start_character: u32,
    pub end_line: u32,
    pub end_character: u32,
}

/// The requests of every Administration file.
pub struct ApiRequestIndex {
    requests: DataIndexer<ApiRequestUsage>,
}

/// Every request is stored under one key: there are few, and a path with
/// runtime parts cannot be looked up by a route path.
const REQUEST_KEY: &str = "request";

impl ApiRequestIndex {
    /// Uses the shared workspace `store`, or an isolated database in
    /// `config_dir` without one.
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let requests = DataIndexer::repository(
            config_dir.as_ref().join("admin_api_requests.db"),
            "admin.api_requests",
            store,
        )?;
        Ok(Self { requests })
    }

    pub fn revision(&self) -> u64 {
        self.requests.revision()
    }

    /// The requests a route with `path` and `methods` (none: every method)
    /// answers, by file and position.
    pub fn requests_to(&self, path: &str, methods: &[String]) -> Result<Vec<ApiRequestUsage>> {
        let all: Arc<[ApiRequestUsage]> = self.requests.get_all_values_view()?;
        let mut result: Vec<ApiRequestUsage> = all
            .iter()
            .filter(|request| {
                api_route_is_requested(path, &request.path)
                    && (methods.is_empty()
                        || methods
                            .iter()
                            .any(|method| method.eq_ignore_ascii_case(&request.method)))
            })
            .cloned()
            .collect();
        result.sort_by(|left, right| {
            (&left.file, left.start_line, left.start_character).cmp(&(
                &right.file,
                right.start_line,
                right.start_character,
            ))
        });
        Ok(result)
    }
}

impl Indexer for ApiRequestIndex {
    fn id(&self) -> &str {
        "admin.api_requests"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !is_api_request_file(path) {
            return Ok(());
        }
        let mut requests: Vec<ApiRequestUsage> = Vec::new();
        if file.source().contains("httpClient")
            && let Some(tree) = file.syntax_tree()
        {
            let lines = file.line_index();
            for call in jsquery::iter_calls(&tree.root(), &[]) {
                let Some(request) = api_request(&call) else {
                    continue;
                };
                let range = request.argument.range_trimmed_trivia();
                let (start_line, start_character) = lines.position_utf16(range.start().into());
                let (end_line, end_character) = lines.position_utf16(range.end().into());
                requests.push(ApiRequestUsage {
                    method: request.verb.to_uppercase(),
                    path: request.path,
                    file: path.to_owned(),
                    start_line,
                    start_character,
                    end_line,
                    end_character,
                });
            }
        }
        // Saving an empty list drops what the file held before.
        self.requests
            .batch_save_items_in(
                file.mutation(),
                [(path, requests.iter().map(|request| (REQUEST_KEY, request)))],
            )
            .context("saving administration API requests")
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.requests
            .batch_delete_by_file_paths(paths)
            .context("removing administration API requests")
    }

    fn close(&self) -> Result<()> {
        self.requests.close()
    }

    fn clear(&self) -> Result<()> {
        self.requests.clear()
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for ApiRequestIndex {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.requests
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for ApiRequestIndex {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.requests.clear_in(Some(mutation))
    }
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
        for (route, requested, expected) in [
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
                "/api/_action/proxy/{path}",
                "/api/_action/proxy/*/more",
                true,
            ),
            // What is only known at runtime names no particular action.
            ("/api/_action/index", "/api/_action/*", false),
            ("/api/_action/swag/run", "/api/_action/*/run", false),
        ] {
            assert_eq!(
                api_route_is_requested(route, &requested.replace('*', WILDCARD)),
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
