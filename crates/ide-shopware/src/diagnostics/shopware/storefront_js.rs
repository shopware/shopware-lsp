//! Storefront JavaScript rules of Shopware's `storefront-eslint-rules`:
//! imports of the plugin system, and the helpers Shopware 6.7 deprecates in
//! favour of the browser's own API (`DomAccess`, `HttpClient`, the
//! `query-string` package).

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_CALL_EXPRESSION, JS_IDENTIFIER, JS_IMPORT_STATEMENT, JS_MEMBER_EXPRESSION, JS_STRING,
    TK_IDENTIFIER, TK_OPEN_BRACE,
};
use swlsp_shopware::SupportedVersions;
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

pub const STOREFRONT_PLUGIN_SYSTEM_IMPORT_CODE: &str = "storefront.plugin-system.import";
pub const STOREFRONT_DOM_ACCESS_CODE: &str = "storefront.dom-access.deprecated";
pub const STOREFRONT_HTTP_CLIENT_CODE: &str = "storefront.http-client.deprecated";
pub const STOREFRONT_QUERY_STRING_CODE: &str = "storefront.query-string.deprecated";

/// Every code of [`StorefrontJavaScriptAnalyzer`].
pub const STOREFRONT_JAVASCRIPT_CODES: &[&str] = &[
    STOREFRONT_PLUGIN_SYSTEM_IMPORT_CODE,
    STOREFRONT_DOM_ACCESS_CODE,
    STOREFRONT_HTTP_CLIENT_CODE,
    STOREFRONT_QUERY_STRING_CODE,
];

const PLUGIN_SYSTEM_GLOBALS: &[(&str, &str)] = &[
    ("src/plugin-system/plugin.manager", "window.PluginManager"),
    ("src/plugin-system/plugin.class", "window.PluginBaseClass"),
];
const DOM_ACCESS_MODULE: &str = "src/helper/dom-access.helper";
const HTTP_CLIENT_MODULE: &str = "src/service/http-client.service";
const QUERY_STRING_MODULE: &str = "query-string";

#[derive(Debug, Clone)]
pub struct StorefrontJavaScriptAnalyzer {
    versions: SupportedVersions,
}

impl StorefrontJavaScriptAnalyzer {
    pub fn new(versions: impl Into<SupportedVersions>) -> Self {
        Self {
            versions: versions.into(),
        }
    }
}

impl ProblemAnalyzer for StorefrontJavaScriptAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        // The Storefront bundle itself imports its own sources.
        if !document.uri.contains("Resources/app/storefront")
            || lowercase(&document.uri).contains("/storefront/resources/app/storefront/")
            || !matches!(
                lowercase(filepath::ext(&document.uri)).as_str(),
                ".js" | ".ts"
            )
        {
            return Ok(Vec::new());
        }
        // `window.PluginManager` is the one instance from Shopware 6.6 on,
        // so the code has to run there. Shopware 6.7 deprecates the helpers,
        // and what replaces them is the browser's own.
        let plugin_globals = self
            .versions
            .minimum_for_uri(&document.uri)
            .at_least(6, 6, 0);
        let deprecations = self.versions.installed().at_least(6, 7, 0);
        if !(plugin_globals || deprecations) {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for import in jsquery::nodes(&root, &[JS_IMPORT_STATEMENT]) {
            ctx.check()?;
            let Some(source) = import.children().find(|child| child.kind() == JS_STRING) else {
                continue;
            };
            let module = jsquery::string_value(&source);
            let binding = default_binding(&import);
            if plugin_globals
                && let Some((_, global)) = PLUGIN_SYSTEM_GLOBALS
                    .iter()
                    .find(|(path, _)| *path == module)
            {
                let mut found = problem(
                    STOREFRONT_PLUGIN_SYSTEM_IMPORT_CODE,
                    &import,
                    format!(
                        "Importing {module} bundles a second copy of the plugin system; use {global}"
                    ),
                );
                if let Some(binding) = &binding {
                    found.payload = Some(rewrite(
                        &format!("Use {global}"),
                        &import,
                        &format!("const {binding} = {global};"),
                        true,
                        false,
                    ));
                }
                result.push(found);
            }
            if !deprecations {
                continue;
            }
            match module.as_str() {
                DOM_ACCESS_MODULE => {
                    result.push(deprecated_import(
                        STOREFRONT_DOM_ACCESS_CODE,
                        &root,
                        &import,
                        binding.as_deref(),
                        "DomAccess is deprecated and removed in Shopware 6.8; use the DOM API of the browser",
                    ));
                    if let Some(binding) = &binding {
                        dom_access_calls(&root, binding, &mut result);
                    }
                }
                QUERY_STRING_MODULE => {
                    result.push(deprecated_import(
                        STOREFRONT_QUERY_STRING_CODE,
                        &root,
                        &import,
                        binding.as_deref(),
                        "The query-string package is no longer part of the Storefront; use URLSearchParams",
                    ));
                    if let Some(binding) = &binding {
                        query_string_calls(&root, binding, &mut result);
                    }
                }
                HTTP_CLIENT_MODULE => result.push(problem(
                    STOREFRONT_HTTP_CLIENT_CODE,
                    &import,
                    "HttpClient is deprecated and removed in Shopware 6.8; use fetch()".to_owned(),
                )),
                _ => {}
            }
        }
        Ok(result)
    }
}

fn problem(code: &str, node: &SyntaxNode, message: String) -> Problem {
    Problem {
        id: code.into(),
        range: node.range_trimmed_trivia(),
        element: Some(SyntaxElement::Node(node.clone())),
        message,
        severity: Some(DiagnosticSeverity::WARNING),
        source: "shopware-lsp".into(),
        ..Problem::default()
    }
}

fn trimmed(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

/// The payload of a fix replacing `node` with `replacement`. `equivalent`
/// says the code means the same afterwards.
fn rewrite(
    title: &str,
    node: &SyntaxNode,
    replacement: &str,
    equivalent: bool,
    remove_line: bool,
) -> Value {
    let mut map = Map::new();
    map.insert("title".into(), Value::String(title.to_owned()));
    map.insert("expected".into(), Value::String(trimmed(node)));
    map.insert("replacement".into(), Value::String(replacement.to_owned()));
    map.insert("equivalent".into(), Value::Bool(equivalent));
    map.insert("removeLine".into(), Value::Bool(remove_line));
    Value::Object(map)
}

/// The name of `import Name from '…'`; none for named or namespace imports.
fn default_binding(import: &SyntaxNode) -> Option<String> {
    let mut tokens = import
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| !token.kind().is_trivia())
        .skip(1);
    let first = tokens.next()?;
    let second = tokens.next()?;
    (first.kind() == TK_IDENTIFIER && second.text() == "from" && first.kind() != TK_OPEN_BRACE)
        .then(|| first.text().to_owned())
}

/// The import of a deprecated helper. It can be removed once nothing in the
/// file uses its binding any more.
fn deprecated_import(
    code: &str,
    root: &SyntaxNode,
    import: &SyntaxNode,
    binding: Option<&str>,
    message: &str,
) -> Problem {
    let mut found = problem(code, import, message.to_owned());
    let unused = binding.is_some_and(|binding| {
        root.descendants_with_tokens()
            .filter_map(|element| element.into_token())
            .filter(|token| token.kind() == TK_IDENTIFIER && token.text() == binding)
            .count()
            == 1
    });
    if unused {
        found.payload = Some(rewrite("Remove the unused import", import, "", true, true));
    }
    found
}

/// Calls `binding.method(...)`, with the method name.
fn helper_calls(root: &SyntaxNode, binding: &str) -> Vec<(SyntaxNode, String)> {
    jsquery::nodes(root, &[JS_CALL_EXPRESSION])
        .into_iter()
        .filter_map(|call| {
            let callee = jsquery::call_callee(&call)?;
            if callee.kind() != JS_MEMBER_EXPRESSION {
                return None;
            }
            let mut parts = callee.children();
            let (object, method) = (parts.next()?, parts.next()?);
            (object.kind() == JS_IDENTIFIER
                && trimmed(&object) == binding
                && method.kind() == JS_IDENTIFIER
                && parts.next().is_none())
            .then(|| (call.clone(), trimmed(&method)))
        })
        .collect()
}

/// The one expression node an argument consists of; none for an argument
/// with operators (`a || b`).
fn single_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    let mut children = argument.children();
    let expression = children.next()?;
    (children.next().is_none() && trimmed(&expression) == trimmed(argument)).then_some(expression)
}

/// An argument as the receiver of a member access.
fn receiver(argument: &SyntaxNode) -> String {
    let text = trimmed(argument);
    match single_expression(argument).map(|expression| expression.kind()) {
        Some(JS_IDENTIFIER | JS_MEMBER_EXPRESSION | JS_CALL_EXPRESSION) => text,
        _ => format!("({text})"),
    }
}

fn dom_access_calls(root: &SyntaxNode, binding: &str, result: &mut Vec<Problem>) {
    for (call, method) in helper_calls(root, binding) {
        let mut found = problem(
            STOREFRONT_DOM_ACCESS_CODE,
            &call,
            format!(
                "DomAccess.{method}() is deprecated and removed in Shopware 6.8; use the DOM API of the browser"
            ),
        );
        let arguments: Vec<SyntaxNode> = jsquery::iter_arguments(&call).collect();
        // The helpers throw for a missing element or attribute unless their
        // third argument says otherwise; the DOM API returns null.
        let replacement = match (method.as_str(), arguments.as_slice()) {
            (
                "querySelector" | "querySelectorAll" | "hasAttribute" | "getAttribute",
                [element, name, ..],
            ) => Some(format!("{}.{method}({})", receiver(element), trimmed(name))),
            ("getDataAttribute", [element, name, ..]) => single_expression(name)
                .filter(|name| name.kind() == JS_STRING)
                .and_then(|name| dataset_key(&jsquery::string_value(&name)))
                .map(|key| format!("{}.dataset.{key}", receiver(element))),
            _ => None,
        };
        if let Some(replacement) = replacement {
            found.payload = Some(rewrite(
                "Use the DOM API (returns null where DomAccess throws)",
                &call,
                &replacement,
                false,
                false,
            ));
        }
        result.push(found);
    }
}

/// `data-foo-bar` as the `dataset` property `fooBar`.
fn dataset_key(attribute: &str) -> Option<String> {
    let name = attribute.strip_prefix("data-")?;
    if name.is_empty()
        || !name.chars().all(|character| {
            character.is_ascii_lowercase() || character.is_ascii_digit() || character == '-'
        })
        || name.starts_with(|character: char| character.is_ascii_digit() || character == '-')
    {
        return None;
    }
    let mut key = String::new();
    let mut upper = false;
    for character in name.chars() {
        if character == '-' {
            upper = true;
        } else if upper {
            key.push(character.to_ascii_uppercase());
            upper = false;
        } else {
            key.push(character);
        }
    }
    Some(key)
}

fn query_string_calls(root: &SyntaxNode, binding: &str, result: &mut Vec<Problem>) {
    for (call, method) in helper_calls(root, binding) {
        let argument = jsquery::iter_arguments(&call)
            .next()
            .map(|argument| trimmed(&argument));
        let single = jsquery::iter_arguments(&call).count() == 1;
        let replacement = match (method.as_str(), argument) {
            ("parse", Some(argument)) if single => Some(format!(
                "Object.fromEntries(new URLSearchParams({argument}).entries())"
            )),
            ("stringify", Some(argument)) if single => {
                Some(format!("new URLSearchParams({argument}).toString()"))
            }
            _ => None,
        };
        let mut found = problem(
            STOREFRONT_QUERY_STRING_CODE,
            &call,
            format!(
                "query-string {method}() is no longer available in the Storefront; use URLSearchParams"
            ),
        );
        if let Some(replacement) = replacement {
            found.payload = Some(rewrite(
                "Use URLSearchParams (repeated keys and value types differ)",
                &call,
                &replacement,
                false,
                false,
            ));
        }
        result.push(found);
    }
}
