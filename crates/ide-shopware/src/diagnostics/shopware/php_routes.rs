//! Shopware conventions for `#[Route]` attributes.
//!
//! - Every route needs a scope (`_routeScope`): Shopware rejects a request
//!   to a controller route without one.
//! - An `api` or `store-api` route has to live below that path, or its
//!   scope refuses it.
//! - A storefront route has to be named `frontend.*`, `widgets.*` or
//!   `payment.*`: the storefront router only prefixes those with the sales
//!   channel URL when it generates a link.
//! - A route says which HTTP methods it answers.
//! - Route defaults are written with the constants Shopware declares for
//!   them, not with their string values.

use std::collections::BTreeSet;
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;
use swlsp_php_types::Kind as TypeKind;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use super::php_semantic::Run;
use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::{local_call_argument, unwrap_php_parentheses};

pub const ROUTE_SCOPE_MISSING_CODE: &str = "shopware.php.route.scope_missing";
pub const ROUTE_SCOPE_PATH_CODE: &str = "shopware.php.route.scope_path";
pub const ROUTE_NAME_PREFIX_CODE: &str = "shopware.php.route.name_prefix";
pub const ROUTE_METHODS_MISSING_CODE: &str = "shopware.php.route.methods_missing";
pub const ROUTE_ATTRIBUTE_LITERAL_CODE: &str = "shopware.php.route.attribute_literal";

/// The codes of [`ShopwareRouteAnalyzer`] with their default severity.
pub const ROUTE_CODES: &[(&str, DiagnosticSeverity)] = &[
    (ROUTE_SCOPE_MISSING_CODE, DiagnosticSeverity::WARNING),
    (ROUTE_SCOPE_PATH_CODE, DiagnosticSeverity::WARNING),
    (ROUTE_NAME_PREFIX_CODE, DiagnosticSeverity::WARNING),
    (ROUTE_METHODS_MISSING_CODE, DiagnosticSeverity::WARNING),
    (ROUTE_ATTRIBUTE_LITERAL_CODE, DiagnosticSeverity::HINT),
];

const PLATFORM_REQUEST_CLASS: &str = "Shopware\\Core\\PlatformRequest";
const ROUTE_SCOPE_KEY: &str = "_routeScope";
/// The scopes with the class that declares their `ID`.
const SCOPE_CLASSES: &[(&str, &str)] = &[
    ("api", "Shopware\\Core\\Framework\\Routing\\ApiRouteScope"),
    (
        "store-api",
        "Shopware\\Core\\Framework\\Routing\\StoreApiRouteScope",
    ),
    (
        "storefront",
        "Shopware\\Storefront\\Framework\\Routing\\StorefrontRouteScope",
    ),
    (
        "administration",
        "Shopware\\Administration\\Framework\\Routing\\AdministrationRouteScope",
    ),
];
/// The path a scope accepts requests below (`AbstractRouteScope::$allowedPaths`).
const SCOPE_PATHS: &[(&str, &str)] = &[("api", "api"), ("store-api", "store-api")];
/// A path of the api scope outside of `/api/`.
const API_DOMAIN_HASH_PATH: &str = "sw-domain-hash.html";
/// Shopware's own bundles. Their installer runs without the scope check and
/// some of their controllers are exempted by a `RouteScopeWhitelistInterface`
/// service, which no attribute shows.
const SHOPWARE_NAMESPACES: &[&str] = &[
    "Shopware\\Core\\",
    "Shopware\\Storefront\\",
    "Shopware\\Administration\\",
    "Shopware\\Elasticsearch\\",
];
/// The names `Storefront\Framework\Routing\Router` treats as storefront routes.
const STOREFRONT_NAME_PREFIXES: &[&str] = &["frontend.", "widgets.", "payment."];

pub struct ShopwareRouteAnalyzer {
    php_index: Option<PhpIndex>,
}

impl ShopwareRouteAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for ShopwareRouteAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if document.language != Some(LanguageId::Php) || !document.source().contains("Route") {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php_index, document)? else {
            return Ok(Vec::new());
        };
        // The conventions are Shopware's: a Symfony project has no scopes.
        let Some(platform_request) = state.snapshot.classes(PLATFORM_REQUEST_CLASS).pop() else {
            return Ok(Vec::new());
        };
        let small_entities = BTreeSet::new();
        let mut run = Run::new(&state.document, &state.snapshot, &small_entities);
        let constants = Constants::new(&run, platform_request.id.as_str());
        for class in phpquery::classes(&root) {
            ctx.check()?;
            run.check_route_conventions(&class, &constants);
        }
        Ok(run.into_problems())
    }
}

/// The constant Shopware declares for the string of a route default: the
/// class and the constant name.
struct Constants {
    /// `_routeScope` → `PlatformRequest::ATTRIBUTE_ROUTE_SCOPE`.
    keys: Vec<(String, (String, String))>,
    /// `storefront` → `StorefrontRouteScope::ID`.
    scopes: Vec<(String, (String, String))>,
}

impl Constants {
    fn new(run: &Run<'_>, platform_request: &str) -> Self {
        let keys = run
            .snapshot
            .members_of(platform_request)
            .into_iter()
            .filter(|constant| {
                constant.kind == SymbolKind::ClassConstant
                    && constant.name.starts_with("ATTRIBUTE_")
                    && constant.ty.kind() == TypeKind::LiteralString
                    && constant.ty.name().starts_with('_')
            })
            .map(|constant| {
                (
                    constant.ty.name().to_owned(),
                    (PLATFORM_REQUEST_CLASS.to_owned(), constant.name.to_string()),
                )
            })
            .collect();
        let scopes = SCOPE_CLASSES
            .iter()
            .filter(|(_, class)| !run.snapshot.classes(class).is_empty())
            .map(|(scope, class)| ((*scope).to_owned(), ((*class).to_owned(), "ID".to_owned())))
            .collect();
        Self { keys, scopes }
    }
}

fn problem(code: &str, range: TextRange, message: String) -> Problem {
    let severity = ROUTE_CODES
        .iter()
        .find(|(known, _)| *known == code)
        .map_or(DiagnosticSeverity::WARNING, |(_, severity)| *severity);
    Problem {
        id: DiagnosticId::from(code),
        range,
        message,
        severity: Some(severity),
        source: "shopware-lsp".to_owned(),
        ..Problem::default()
    }
}

/// The one string a route argument evaluates to, with the node to report.
fn single(values: Vec<String>) -> Option<String> {
    let [value] = <[String; 1]>::try_from(values).ok()?;
    Some(value)
}

impl Run<'_> {
    fn check_route_conventions(&mut self, class: &SyntaxNode, constants: &Constants) {
        let class_routes: Vec<SyntaxNode> = class
            .children()
            .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
            .flat_map(|group| phpquery::nodes(&group, &[PHP_ATTRIBUTE]))
            .filter(|attribute| self.is_route(attribute))
            .collect();
        for route in &class_routes {
            self.check_default_literals(route, constants);
        }
        let class_path = class_routes
            .iter()
            .find_map(|route| self.route_argument(route, "path", 0));
        let class_methods = class_routes
            .iter()
            .any(|route| local_call_argument(route, "methods", 6).is_some());
        let own_bundle = self.class_symbol(Some(class)).is_some_and(|symbol| {
            let name = symbol.fully_qualified.trim_start_matches('\\');
            SHOPWARE_NAMESPACES
                .iter()
                .any(|namespace| name.starts_with(namespace))
        });
        for method in phpquery::methods(class) {
            let routes: Vec<SyntaxNode> = phpquery::attributes(&method)
                .into_iter()
                .filter(|attribute| self.is_route(attribute))
                .collect();
            if routes.is_empty() {
                continue;
            }
            let all: Vec<&SyntaxNode> = class_routes.iter().chain(&routes).collect();
            let declared = all
                .iter()
                .any(|route| self.route_default_value(route, ROUTE_SCOPE_KEY).is_some());
            let scopes: BTreeSet<String> = all
                .iter()
                .flat_map(|route| self.route_scopes(route))
                .collect();
            if !declared && !own_bundle {
                let name = phpquery::direct_child(&method, PHP_NAME).unwrap_or(method.clone());
                self.push(problem(
                    ROUTE_SCOPE_MISSING_CODE,
                    name.range_trimmed_trivia(),
                    "This route has no scope, so Shopware rejects every request to it; set the \
                     _routeScope default on the route or on the controller class"
                        .to_owned(),
                ));
            }
            for route in &routes {
                self.check_default_literals(route, constants);
                if !class_methods && local_call_argument(route, "methods", 6).is_none() {
                    let name = phpquery::direct_child(route, PHP_NAME).unwrap_or(route.clone());
                    self.push(problem(
                        ROUTE_METHODS_MISSING_CODE,
                        name.range_trimmed_trivia(),
                        "This route answers every HTTP method; name the ones it handles in \
                         methods"
                            .to_owned(),
                    ));
                }
                self.check_route_name(route, &scopes);
                self.check_route_path(route, class_path.as_ref(), &scopes);
            }
        }
    }

    /// The argument `name` of a route when it is one known string, with
    /// its expression.
    fn route_argument(
        &self,
        route: &SyntaxNode,
        name: &str,
        position: usize,
    ) -> Option<(SyntaxNode, String)> {
        let value = unwrap_php_parentheses(&local_call_argument(route, name, position)?);
        let text = single(self.expression_strings(Some(&value)))?;
        Some((value, text))
    }

    /// The scopes a route lists in its `_routeScope` default.
    fn route_scopes(&self, route: &SyntaxNode) -> Vec<String> {
        self.route_scope_values(route)
            .iter()
            .flat_map(|value| self.expression_strings(Some(value)))
            .collect()
    }

    fn route_scope_values(&self, route: &SyntaxNode) -> Vec<SyntaxNode> {
        let Some(Some(value)) = self.route_default_value(route, ROUTE_SCOPE_KEY) else {
            return Vec::new();
        };
        let value = unwrap_php_parentheses(&value);
        if value.kind() == PHP_ARRAY {
            phpquery::array_items(&value)
                .iter()
                .filter_map(phpquery::array_item_value)
                .collect()
        } else {
            vec![value]
        }
    }

    /// A storefront route the storefront router does not know as one.
    fn check_route_name(&mut self, route: &SyntaxNode, scopes: &BTreeSet<String>) {
        if scopes.len() != 1 || !scopes.contains("storefront") {
            return;
        }
        let Some((node, name)) = self.route_argument(route, "name", 1) else {
            return;
        };
        if name.is_empty()
            || STOREFRONT_NAME_PREFIXES
                .iter()
                .any(|prefix| name.starts_with(prefix))
        {
            return;
        }
        self.push(problem(
            ROUTE_NAME_PREFIX_CODE,
            node.range_trimmed_trivia(),
            format!(
                "The storefront router only adds the sales channel URL to routes named \
                 frontend.*, widgets.* or payment.*; links to '{name}' miss it"
            ),
        ));
    }

    /// An API route outside of the path its scope accepts.
    fn check_route_path(
        &mut self,
        route: &SyntaxNode,
        class_path: Option<&(SyntaxNode, String)>,
        scopes: &BTreeSet<String>,
    ) {
        let Some(scope) = single(scopes.iter().cloned().collect()) else {
            return;
        };
        let Some((_, expected)) = SCOPE_PATHS.iter().find(|(name, _)| *name == scope) else {
            return;
        };
        let Some((node, path)) = self.route_argument(route, "path", 0) else {
            return;
        };
        let prefix = class_path.map_or("", |(_, path)| path.as_str());
        let full = format!(
            "{}/{}",
            prefix.trim_end_matches('/'),
            path.trim_start_matches('/')
        );
        let first = full.trim_start_matches('/').split('/').next().unwrap_or("");
        if first == *expected
            || first.starts_with('{')
            || (scope == "api" && first == API_DOMAIN_HASH_PATH)
        {
            return;
        }
        self.push(problem(
            ROUTE_SCOPE_PATH_CODE,
            node.range_trimmed_trivia(),
            format!(
                "The {scope} scope only accepts requests below /{expected}/, so this route \
                 cannot be called; start its path with /{expected}/"
            ),
        ));
    }

    /// `'_routeScope' => ['storefront']` written with strings where
    /// Shopware declares constants.
    fn check_default_literals(&mut self, route: &SyntaxNode, constants: &Constants) {
        let Some(defaults) = local_call_argument(route, "defaults", 4)
            .map(|value| unwrap_php_parentheses(&value))
            .filter(|value| value.kind() == PHP_ARRAY)
        else {
            return;
        };
        let literal = |run: &mut Self, node: &SyntaxNode, known: &[(String, (String, String))]| {
            if node.kind() != PHP_STRING {
                return;
            }
            let value = phpquery::string_value(node);
            let Some((_, (class, constant))) = known.iter().find(|(text, _)| *text == value) else {
                return;
            };
            let short = class.rsplit('\\').next().unwrap_or(class);
            let mut found = problem(
                ROUTE_ATTRIBUTE_LITERAL_CODE,
                node.range_trimmed_trivia(),
                format!("Use the constant {short}::{constant} for '{value}'"),
            );
            found.element = Some(swlsp_syntax::SyntaxElement::Node(node.clone()));
            found.payload = Some(json!({ "class": class, "constant": constant }));
            run.push(found);
        };
        for item in phpquery::array_items(&defaults) {
            if let Some(key) = phpquery::array_item_key(&item) {
                literal(self, &unwrap_php_parentheses(&key), &constants.keys);
            }
        }
        for value in self.route_scope_values(route) {
            literal(self, &unwrap_php_parentheses(&value), &constants.scopes);
        }
    }
}
