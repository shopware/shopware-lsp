//! Administration requests that drifted from the controller that answers
//! them.
//!
//! `this.httpClient.post('/_action/swag/run', payload, { params })` is
//! matched to its route, and the controller of the route is read as in
//! [`super::php_api_contract`]. The keys of the payload and of `params` are
//! checked against the keys the controller reads, and the declared response
//! type of the service method against what the controller answers.
//!
//! The keys of a request are known for an object literal and for a parameter
//! typed with a `type` or `interface` of the same file. A controller that
//! hands its request on, or reads a key that is not a literal, takes any key
//! and is not checked.
//!
//! The controller is read from disk, once per revision of the PHP index.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError};

use serde_json::json;
use swlsp_admin::{
    ApiRequest, api_request, api_route_is_requested, is_api_action_path, is_api_request_file,
};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_ARGUMENT, JS_ARGUMENT_LIST, JS_ARROW_FUNCTION, JS_BLOCK, JS_FUNCTION, JS_IDENTIFIER,
    JS_METHOD, JS_OBJECT, JS_PROPERTY, JS_STRING, JS_TYPE_DECLARATION, JS_VARIABLE_DECLARATION,
    TK_COLON, TK_COMMA, TK_IDENTIFIER, TK_KEYWORD, TK_OPERATOR, TK_SEMICOLON,
};
use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpIndex;
use swlsp_symfony::{Route, RouteIndexer};
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange};

use super::php_api_contract::{ApiAction, ApiController, api_controller};
use crate::api_service::{Field, Shape};
use crate::diagnostics::ProblemAnalyzer;

pub const ADMIN_API_REQUEST_UNKNOWN_KEY_CODE: &str = "admin.api.request-unknown-key";
pub const ADMIN_API_REQUEST_MISSING_KEY_CODE: &str = "admin.api.request-missing-key";
pub const ADMIN_API_RESPONSE_TYPE_CODE: &str = "admin.api.response-type";

/// Every code of [`AdminApiContractAnalyzer`].
pub const ADMIN_API_CONTRACT_CODES: &[&str] = &[
    ADMIN_API_REQUEST_UNKNOWN_KEY_CODE,
    ADMIN_API_REQUEST_MISSING_KEY_CODE,
    ADMIN_API_RESPONSE_TYPE_CODE,
];

type Controllers = Arc<Vec<ApiController>>;

pub struct AdminApiContractAnalyzer {
    routes: Option<Arc<RouteIndexer>>,
    php: Option<PhpIndex>,
    /// The controllers of a PHP file, by path, for one revision of the PHP
    /// index.
    controllers: Mutex<(u64, HashMap<String, Controllers>)>,
}

impl AdminApiContractAnalyzer {
    pub fn new(routes: Option<Arc<RouteIndexer>>, php: Option<PhpIndex>) -> Self {
        Self {
            routes,
            php,
            controllers: Mutex::new((0, HashMap::new())),
        }
    }

    /// The Admin API controllers `path` declares.
    fn controllers(&self, path: &str) -> Controllers {
        let Some(php) = &self.php else {
            return Controllers::default();
        };
        let revision = php.revision().wrapping_add(php.analysis_revision());
        // Held while reading, so documents that ask for one controller wait
        // for one analysis.
        let mut memo = self
            .controllers
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if memo.0 != revision {
            *memo = (revision, HashMap::new());
        }
        if let Some(known) = memo.1.get(path) {
            return known.clone();
        }
        let controllers = Arc::new(read_controllers(php, path).unwrap_or_default());
        memo.1.insert(path.to_owned(), controllers.clone());
        controllers
    }

    /// The action that answers `request`, when one controller method does.
    fn action(&self, routes: &[Route], request: &ApiRequest) -> Option<(String, ApiAction)> {
        let matching: Vec<&Route> = routes
            .iter()
            .filter(|route| {
                is_api_action_path(&route.path)
                    && api_route_is_requested(&route.path, &request.path)
                    && (route.methods.is_empty()
                        || route
                            .methods
                            .iter()
                            .any(|method| method.eq_ignore_ascii_case(&request.verb)))
            })
            .collect();
        // The compiled routes of the project name the same method again.
        let target = &matching.first()?.controller;
        if target.is_empty() || matching.iter().any(|route| route.controller != *target) {
            return None;
        }
        let method = target.rsplit("::").next().unwrap_or(target);
        matching.iter().find_map(|route| {
            let controllers = self.controllers(&route.file_path);
            controllers.iter().find_map(|controller| {
                let action = controller.actions.iter().find(|action| {
                    action.method == method
                        && action.path.trim_matches('/') == route.path.trim_matches('/')
                        && action.verb.eq_ignore_ascii_case(&request.verb)
                })?;
                Some((
                    format!("{}::{}", controller.class_name, action.method),
                    action.clone(),
                ))
            })
        })
    }
}

/// The Admin API controllers of the PHP file `path`, read from disk.
fn read_controllers(php: &PhpIndex, path: &str) -> Option<Vec<ApiController>> {
    let source = std::fs::read_to_string(path).ok()?;
    // Routes are also indexed from files that declare no controller.
    if !source.contains("Route") || !source.contains("class ") {
        return None;
    }
    let document = TextDocument::new(swlsp_uriutil::file_uri(path), &source, 0);
    let state = swlsp_lsp_phpanalysis::for_document(php, &document)
        .ok()
        .flatten()?;
    let root = document.root()?;
    Some(
        phpquery::classes(&root)
            .iter()
            .filter_map(|class| api_controller(&state.document, &state.snapshot, class))
            .collect(),
    )
}

impl ProblemAnalyzer for AdminApiContractAnalyzer {
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
        let aliases = type_aliases(&root);
        let mut problems: Vec<Problem> = Vec::new();
        for call in jsquery::iter_calls(&root, &[]) {
            ctx.check()?;
            let Some(request) = api_request(&call) else {
                continue;
            };
            let Some((controller, action)) = self.action(&routes, &request) else {
                continue;
            };
            let has_body = matches!(request.verb.as_str(), "post" | "put" | "patch");
            if has_body
                && let Some(body) = &action.body
                && let Some(argument) = jsquery::argument_expression(&call, 1)
            {
                check_request(
                    &mut problems,
                    &controller,
                    "payload",
                    body,
                    &argument,
                    &aliases,
                );
            }
            let params = jsquery::object_argument(&call, if has_body { 2 } else { 1 })
                .and_then(|config| jsquery::property(&config, "params"));
            if let Some(query) = &action.query
                && let Some(params) = params
                // A shorthand `params` is the variable of that name.
                && let Some(value) =
                    jsquery::property_value(&params).or_else(|| jsquery::property_name_node(&params))
            {
                check_request(&mut problems, &controller, "query", query, &value, &aliases);
            }
            check_response(
                &mut problems,
                &controller,
                &action.response,
                &call,
                &aliases,
            );
        }
        // A type several requests share is reported once.
        let mut seen: Vec<(String, TextRange, String)> = Vec::new();
        problems.retain(|problem| {
            let key = (
                problem.id.to_string(),
                problem.range,
                problem.message.clone(),
            );
            let new = !seen.contains(&key);
            if new {
                seen.push(key);
            }
            new
        });
        Ok(problems)
    }
}

/// A key a request sends or a type declares.
struct Key {
    name: String,
    /// The name, or the whole member of a type.
    range: TextRange,
    /// The member of a type, with what follows the colon.
    member: Option<Member>,
}

struct Member {
    /// The declared type, when it is a single word.
    simple_type: Option<SyntaxToken>,
}

/// The keys of an object literal or an object type.
struct Keys {
    keys: Vec<Key>,
    /// Whether no other key can be there.
    closed: bool,
}

fn trimmed(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

/// The tokens directly in `node`, without trivia.
fn tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| !token.kind().is_trivia())
}

/// The `type Name = { … }` and `interface Name { … }` declarations of the
/// file, by name. A type that extends another has more keys than it shows.
fn type_aliases(root: &SyntaxNode) -> HashMap<String, SyntaxNode> {
    let mut aliases = HashMap::new();
    for declaration in jsquery::nodes(root, &[JS_TYPE_DECLARATION]) {
        let mut words = tokens(&declaration)
            .filter(|token| !(token.kind() == TK_KEYWORD && token.text() == "export"));
        let (Some(keyword), Some(name)) = (words.next(), words.next()) else {
            continue;
        };
        let rest: Vec<String> = words.map(|token| token.text().to_owned()).collect();
        let plain = match keyword.text() {
            "type" => matches!(rest.as_slice(), [equals] | [equals, _] if equals == "="),
            "interface" => rest.is_empty(),
            _ => false,
        };
        let mut children = declaration.children();
        let object = children.next().filter(|_| children.next().is_none());
        if let Some(object) = object.filter(|object| plain && object.kind() == JS_OBJECT) {
            aliases.insert(name.text().to_owned(), object);
        }
    }
    aliases
}

/// The keys of an object literal.
fn literal_keys(object: &SyntaxNode) -> Keys {
    let mut keys = Keys {
        keys: Vec::new(),
        closed: true,
    };
    for property in object.children() {
        let name = property
            .children_with_tokens()
            .find(|element| !element.kind().is_trivia())
            .and_then(|element| element.into_node())
            .filter(|name| {
                property.kind() == JS_PROPERTY && matches!(name.kind(), JS_IDENTIFIER | JS_STRING)
            });
        // A spread or a computed key.
        let Some(name) = name else {
            keys.closed = false;
            continue;
        };
        if let Some(text) = jsquery::property_name(&property) {
            keys.keys.push(Key {
                name: text,
                range: name.range_trimmed_trivia(),
                member: None,
            });
        }
    }
    keys
}

/// The members of an object type.
fn type_keys(object: &SyntaxNode) -> Keys {
    let mut keys = literal_keys(object);
    let members: Vec<SyntaxNode> = object
        .children()
        .filter(|child| child.kind() == JS_PROPERTY)
        .collect();
    for key in &mut keys.keys {
        let Some(property) = members
            .iter()
            .find(|member| member.range_trimmed_trivia().contains_range(key.range))
        else {
            continue;
        };
        let after_colon: Vec<SyntaxElement> = property
            .children_with_tokens()
            .skip_while(|element| element.kind() != TK_COLON)
            .skip(1)
            .filter(|element| !element.kind().is_trivia())
            .collect();
        let simple_type = match after_colon.as_slice() {
            [SyntaxElement::Token(token)] if token.kind() == TK_IDENTIFIER => Some(token.clone()),
            _ => None,
        };
        // With the `;` or `,` that ends the member.
        let end = property
            .next_sibling_or_token()
            .and_then(|element| element.into_token())
            .filter(|token| matches!(token.kind(), TK_SEMICOLON | TK_COMMA))
            .map_or(property.range_trimmed_trivia().end(), |token| {
                token.text_range().end()
            });
        key.range = TextRange::new(property.range_trimmed_trivia().start(), end);
        key.member = Some(Member { simple_type });
    }
    keys
}

/// The function `node` is in.
fn enclosing_function(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors()
        .skip(1)
        .find(|ancestor| matches!(ancestor.kind(), JS_METHOD | JS_FUNCTION | JS_ARROW_FUNCTION))
}

/// The type of the parameter `name` of `function`, when it is one word.
fn parameter_type(function: &SyntaxNode, name: &str) -> Option<String> {
    let list = function
        .children()
        .find(|child| child.kind() == JS_ARGUMENT_LIST)?;
    list.children()
        .filter(|child| child.kind() == JS_ARGUMENT)
        .find_map(|argument| {
            let typed = argument.children().next()?;
            let parameter = typed
                .children()
                .find(|child| child.kind() == JS_IDENTIFIER)?;
            if typed.kind() != JS_IDENTIFIER || trimmed(&parameter) != name {
                return None;
            }
            let mut annotation = tokens(&typed).skip_while(|token| token.kind() != TK_COLON);
            annotation.next()?;
            let word = annotation.next().filter(|_| annotation.next().is_none())?;
            (word.kind() == TK_IDENTIFIER).then(|| word.text().to_owned())
        })
}

/// The object literal a `const name = { … }` of `function` holds, when it is
/// the only declaration of that name there.
fn local_object(function: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    let mut declarations = jsquery::nodes(function, &[JS_VARIABLE_DECLARATION])
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
    let constant =
        tokens(&declaration).any(|token| token.kind() == TK_KEYWORD && token.text() == "const");
    let mut children = declaration.children().skip(1);
    let value = children.next().filter(|_| children.next().is_none())?;
    (constant && value.kind() == JS_OBJECT).then_some(value)
}

/// The keys `expression` sends: an object literal, a constant that holds
/// one, or a parameter of a declared type.
fn request_keys(expression: &SyntaxNode, aliases: &HashMap<String, SyntaxNode>) -> Option<Keys> {
    match expression.kind() {
        JS_OBJECT => Some(literal_keys(expression)),
        JS_IDENTIFIER => {
            let name = trimmed(expression);
            let function = enclosing_function(expression)?;
            if let Some(alias) = parameter_type(&function, &name) {
                return aliases.get(&alias).map(type_keys);
            }
            local_object(&function, &name).map(|object| literal_keys(&object))
        }
        _ => None,
    }
}

/// A problem at `range`, with a rewrite of that range when `replacement` is
/// given.
fn problem(
    document_range: TextRange,
    source: &str,
    code: &str,
    message: String,
    fix: Option<(String, &str)>,
) -> Problem {
    let payload = fix.map(|(title, replacement)| {
        json!({
            "title": title,
            "expected": source,
            "replacement": replacement,
            "removeLine": replacement.is_empty(),
        })
    });
    Problem {
        id: code.into(),
        range: document_range,
        message,
        severity: Some(DiagnosticSeverity::WARNING),
        source: "shopware-lsp".into(),
        payload,
        ..Problem::default()
    }
}

/// The source of `range` in the file of `node`.
fn source_of(node: &SyntaxNode, range: TextRange) -> String {
    let root = node.ancestors().last().unwrap_or_else(|| node.clone());
    let text = root.text().to_string();
    text.get(usize::from(range.start())..usize::from(range.end()))
        .unwrap_or_default()
        .to_owned()
}

/// The fields of a contract that names every key.
fn closed_fields(shape: &Shape) -> Option<&[Field]> {
    match shape {
        Shape::Object {
            fields,
            open: false,
        } if !fields.is_empty() => Some(fields),
        _ => None,
    }
}

fn check_request(
    problems: &mut Vec<Problem>,
    controller: &str,
    part: &str,
    contract: &Shape,
    expression: &SyntaxNode,
    aliases: &HashMap<String, SyntaxNode>,
) {
    let (Some(fields), Some(sent)) = (closed_fields(contract), request_keys(expression, aliases))
    else {
        return;
    };
    for key in &sent.keys {
        if fields.iter().any(|field| field.name == key.name) {
            continue;
        }
        let fix = key
            .member
            .as_ref()
            .map(|_| (format!("Remove '{}' from the type", key.name), ""));
        problems.push(problem(
            key.range,
            &source_of(expression, key.range),
            ADMIN_API_REQUEST_UNKNOWN_KEY_CODE,
            format!("{controller} does not read the {part} key '{}'", key.name),
            fix,
        ));
    }
    if !sent.closed {
        return;
    }
    let range = expression.range_trimmed_trivia();
    for field in fields.iter().filter(|field| !field.optional) {
        if sent.keys.iter().any(|key| key.name == field.name) {
            continue;
        }
        problems.push(problem(
            range,
            "",
            ADMIN_API_REQUEST_MISSING_KEY_CODE,
            format!(
                "{controller} reads the {part} key '{}', which is not sent",
                field.name
            ),
            None,
        ));
    }
}

/// The word a contract type is in TypeScript, for the three it can be
/// compared by.
fn primitive(shape: &Shape) -> Option<&'static str> {
    match shape {
        Shape::String => Some("string"),
        Shape::Number => Some("number"),
        Shape::Boolean => Some("boolean"),
        _ => None,
    }
}

/// The type `Name` of a method that returns `Promise<Name>`.
fn returned_alias(method: &SyntaxNode) -> Option<String> {
    let mut after_parameters = false;
    let mut words: Vec<SyntaxToken> = Vec::new();
    for element in method.children_with_tokens() {
        match element {
            SyntaxElement::Node(node) if node.kind() == JS_ARGUMENT_LIST => after_parameters = true,
            SyntaxElement::Node(node) if node.kind() == JS_BLOCK => break,
            SyntaxElement::Node(_) if after_parameters => return None,
            SyntaxElement::Token(token) if after_parameters && !token.kind().is_trivia() => {
                words.push(token);
            }
            _ => {}
        }
    }
    match words.as_slice() {
        [colon, promise, open, name, close]
            if colon.kind() == TK_COLON
                && promise.text() == "Promise"
                && open.kind() == TK_OPERATOR
                && open.text() == "<"
                && name.kind() == TK_IDENTIFIER
                && close.text() == ">" =>
        {
            Some(name.text().to_owned())
        }
        _ => None,
    }
}

fn check_response(
    problems: &mut Vec<Problem>,
    controller: &str,
    contract: &Shape,
    call: &SyntaxNode,
    aliases: &HashMap<String, SyntaxNode>,
) {
    let Some(fields) = closed_fields(contract) else {
        return;
    };
    // The return type is that of the request when the method sends one.
    let Some(method) = enclosing_function(call).filter(|function| {
        function.kind() == JS_METHOD
            && jsquery::iter_calls(function, &[])
                .filter(|call| api_request(call).is_some())
                .count()
                == 1
    }) else {
        return;
    };
    let Some(object) = returned_alias(&method).and_then(|alias| aliases.get(&alias)) else {
        return;
    };
    for key in type_keys(object).keys {
        let Some(field) = fields.iter().find(|field| field.name == key.name) else {
            problems.push(problem(
                key.range,
                &source_of(object, key.range),
                ADMIN_API_RESPONSE_TYPE_CODE,
                format!("{controller} does not answer the key '{}'", key.name),
                Some((format!("Remove '{}' from the type", key.name), "")),
            ));
            continue;
        };
        let declared = key.member.and_then(|member| member.simple_type);
        if let (Some(answered), Some(declared)) = (primitive(&field.shape), declared)
            && primitive_word(declared.text())
            && declared.text() != answered
        {
            problems.push(problem(
                declared.text_range(),
                declared.text(),
                ADMIN_API_RESPONSE_TYPE_CODE,
                format!(
                    "{controller} answers '{}' as {answered}, not {}",
                    key.name,
                    declared.text()
                ),
                Some((format!("Change the type to {answered}"), answered)),
            ));
        }
    }
}

fn primitive_word(word: &str) -> bool {
    matches!(word, "string" | "number" | "boolean")
}
