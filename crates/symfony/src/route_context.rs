//! Route name, path, and parameter contexts in PHP, Twig, and JavaScript.

use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax as jssyntax;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::ast::{AstNode, HtmlString, TwigBinaryExpression};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_php::PhpContext;
use swlsp_php_types::Type;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::eq_ignore_case;

use crate::model::is_route_parameter_character;
use crate::php_routes::is_route_attribute;
use crate::util::Lowercase;

const PHP_ROUTE_GENERATOR_METHODS: &[&str] = &["redirectToRoute", "generateUrl", "generate"];
const TWIG_ROUTE_GENERATOR_METHODS: &[&str] = &["seoUrl", "url", "path"];

const SYMFONY_ABSTRACT_CONTROLLER_CLASS: &str =
    "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController";
const SYMFONY_CONTROLLER_CLASS: &str = "Symfony\\Bundle\\FrameworkBundle\\Controller\\Controller";
const SYMFONY_CONTROLLER_TRAIT: &str =
    "Symfony\\Bundle\\FrameworkBundle\\Controller\\ControllerTrait";
const SYMFONY_URL_GENERATOR_INTERFACE: &str =
    "Symfony\\Component\\Routing\\Generator\\UrlGeneratorInterface";

/// Whether `node` is the first string argument of a route-generator call.
pub fn is_php_route_name(node: &SyntaxNode) -> bool {
    let Some(literal) = phpquery::string_at(node) else {
        return false;
    };
    let Some(call) = phpquery::call_at(&literal) else {
        return false;
    };
    phpquery::argument_expression(&call, 0).as_ref() == Some(&literal)
        && is_php_route_generator_method(&phpquery::call_method_name(&call))
}

/// Applies typed route-generator signatures when the PHP semantic document
/// is available; without it, the syntactic check alone decides.
pub fn is_php_route_name_in_context(context: Option<&PhpContext>, node: &SyntaxNode) -> bool {
    if !is_php_route_name(node) {
        return false;
    }
    let Some(context) = context else {
        return true;
    };
    let Some(call) = phpquery::string_at(node).and_then(|literal| phpquery::call_at(&literal))
    else {
        return false;
    };
    let Some(receiver) = phpquery::call_receiver(&call) else {
        return false;
    };
    let receiver_type = context.document.type_of(&receiver).ty;
    let method = phpquery::call_method_name(&call).lowercase();
    if receiver_type.is_unknown() {
        return receiver.text().to_string().trim() == "$this"
            && (method == "generateurl" || method == "redirecttoroute");
    }
    let relations = context.snapshot.relations();
    match method.as_str() {
        "generate" => relations.is_subtype(
            &receiver_type,
            &Type::named(SYMFONY_URL_GENERATOR_INTERFACE, Vec::new()),
        ),
        "generateurl" | "redirecttoroute" => [
            SYMFONY_ABSTRACT_CONTROLLER_CLASS,
            SYMFONY_CONTROLLER_CLASS,
            SYMFONY_CONTROLLER_TRAIT,
        ]
        .iter()
        .any(|class| relations.is_subtype(&receiver_type, &Type::named(class, Vec::new()))),
        _ => false,
    }
}

pub fn is_twig_route_name(node: &SyntaxNode) -> bool {
    twigquery::string_in_function(node, TWIG_ROUTE_GENERATOR_METHODS)
        || twig_route_comparison_reference_at(node).is_some()
}

/// A static route name compared with `app.request.attributes.get('_route')`.
/// The range covers only string contents.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TwigRouteComparisonReference {
    pub value: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// Recognizes equality/inequality, `same as` tests, and membership arrays
/// against the current route.
pub fn twig_route_comparison_reference_at(
    node: &SyntaxNode,
) -> Option<TwigRouteComparisonReference> {
    let literal = twigquery::literal_string_at(node)?;
    if !twigquery::string_is_static(&literal) {
        return None;
    }
    let binary = twigquery::closest_node_of_kind(&literal, &[twigsyntax::TWIG_BINARY_EXPRESSION])?;
    let typed = TwigBinaryExpression::cast(binary)?;
    let operator = typed.operator()?.kind();
    let left = typed.lhs_expression()?.syntax().clone();
    let right = typed.rhs_expression()?.syntax().clone();
    let matches = match operator {
        twigsyntax::TK_DOUBLE_EQUAL
        | twigsyntax::TK_TRIPLE_EQUAL
        | twigsyntax::TK_EXCLAMATION_MARK_EQUALS
        | twigsyntax::TK_EXCLAMATION_MARK_DOUBLE_EQUALS => {
            (twig_route_getter_expression(&left)
                && twig_expression_value(&right).as_ref() == Some(&literal))
                || (twig_route_getter_expression(&right)
                    && twig_expression_value(&left).as_ref() == Some(&literal))
        }
        twigsyntax::TK_IS | twigsyntax::TK_IS_NOT => {
            twig_route_getter_expression(&left)
                && twig_expression_value(&right).is_some_and(|call| {
                    call.kind() == twigsyntax::TWIG_FUNCTION_CALL
                        && twigquery::function_name(&call)
                            .is_some_and(|name| eq_ignore_case(&name, "same as"))
                        && twigquery::string_argument(&call, 0).as_ref() == Some(&literal)
                })
        }
        twigsyntax::TK_IN | twigsyntax::TK_NOT_IN => {
            twig_route_getter_expression(&left)
                && twig_expression_value(&right).is_some_and(|array| {
                    array.kind() == twigsyntax::TWIG_LITERAL_ARRAY
                        && twigquery::closest_node_of_kind(
                            &literal,
                            &[twigsyntax::TWIG_LITERAL_ARRAY],
                        )
                        .as_ref()
                            == Some(&array)
                })
        }
        _ => false,
    };
    if !matches {
        return None;
    }
    let value = twigquery::string_value(&literal).unwrap_or_default();
    if !is_static_route_name(&value) {
        return None;
    }
    Some(TwigRouteComparisonReference {
        value,
        range: twig_string_content_range(&literal),
        node: literal,
    })
}

pub fn twig_route_comparison_references(root: &SyntaxNode) -> Vec<TwigRouteComparisonReference> {
    twigquery::iterate_nodes(root, &[twigsyntax::TWIG_LITERAL_STRING])
        .filter_map(|literal| twig_route_comparison_reference_at(&literal))
        .collect()
}

fn twig_route_getter_expression(node: &SyntaxNode) -> bool {
    let Some(value) = twig_expression_value(node) else {
        return false;
    };
    if value.kind() != twigsyntax::TWIG_FUNCTION_CALL {
        return false;
    }
    let Some(argument) = twigquery::string_argument(&value, 0) else {
        return false;
    };
    if twigquery::string_value(&argument).as_deref() != Some("_route") {
        return false;
    }
    let compact: String = value
        .text()
        .to_string()
        .chars()
        .filter(|c| !c.is_whitespace())
        .collect();
    compact == "app.request.attributes.get('_route')"
        || compact == "app.request.attributes.get(\"_route\")"
}

fn twig_expression_value(node: &SyntaxNode) -> Option<SyntaxNode> {
    let mut node = node.clone();
    loop {
        match node.kind() {
            twigsyntax::TWIG_EXPRESSION
            | twigsyntax::TWIG_OPERAND
            | twigsyntax::TWIG_PARENTHESES_EXPRESSION => {
                node = node.first_child()?;
            }
            _ => return Some(node),
        }
    }
}

fn twig_string_content_range(literal: &SyntaxNode) -> TextRange {
    literal
        .children()
        .find(|child| child.kind() == twigsyntax::TWIG_LITERAL_STRING_INNER)
        .map_or_else(
            || literal.range_trimmed_trivia(),
            |inner| inner.range_trimmed_trivia(),
        )
}

/// A static route-aware HTML attribute value (`<a href>`, `<form action>`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TwigHtmlRouteReference {
    pub value: String,
    pub node: SyntaxNode,
    pub container: SyntaxNode,
}

/// A static route path or name string with its content range.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PhpRoutePathReference {
    pub value: String,
    pub range: TextRange,
}

/// A static request URL in a supported JavaScript context. The range covers
/// only string contents.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct JsRouteUrlReference {
    pub value: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// Recognizes `fetch()`/`axios()`/`axios.create()`/`Request()` first
/// arguments, `url` properties, and `axios.create({baseURL: ...})`.
pub fn js_route_url_reference_at(node: &SyntaxNode) -> Option<JsRouteUrlReference> {
    let literal = jsquery::string_at(node)?;
    let text = literal.text().to_string();
    if text.trim().starts_with('`') && text.contains("${") {
        return None;
    }
    let mut accepted = false;
    if let Some(call) = jsquery::call_at(&literal)
        && jsquery::string_argument_index(&literal) == Some(0)
        && jsquery::argument_expression(&call, 0).as_ref() == Some(&literal)
    {
        let name = jsquery::call_name(&call).unwrap_or_default().lowercase();
        accepted = matches!(
            name.as_str(),
            "axios" | "axios.create" | "fetch" | "request"
        );
    }
    if !accepted
        && let Some(property) = js_route_ancestor_of_kind(&literal, jssyntax::JS_PROPERTY)
        && jsquery::property_value(&property).as_ref() == Some(&literal)
    {
        match jsquery::property_name(&property)
            .unwrap_or_default()
            .lowercase()
            .as_str()
        {
            "url" => accepted = true,
            "baseurl" => {
                let object = js_route_ancestor_of_kind(&property, jssyntax::JS_OBJECT);
                accepted = object.as_ref().is_some_and(|object| {
                    jsquery::call_at(object).is_some_and(|call| {
                        jsquery::call_name(&call)
                            .is_some_and(|name| eq_ignore_case(&name, "axios.create"))
                            && jsquery::argument_expression(&call, 0).as_ref() == Some(object)
                    })
                });
            }
            _ => {}
        }
    }
    if !accepted {
        return None;
    }
    Some(JsRouteUrlReference {
        value: jsquery::string_value(&literal),
        range: js_string_content_range(&literal),
        node: literal,
    })
}

fn js_route_ancestor_of_kind(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn js_string_content_range(literal: &SyntaxNode) -> TextRange {
    for token in literal
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
    {
        if token.kind() != jssyntax::TK_STRING && token.kind() != jssyntax::TK_TEMPLATE {
            continue;
        }
        let mut range = token.text_range();
        let text = token.text().as_bytes();
        if !text.is_empty() && matches!(text[0], b'\'' | b'"' | b'`') {
            let mut start = range.start() + TextSize::from(1);
            let mut end = range.end();
            if text.len() > 1 && text[text.len() - 1] == text[0] {
                end -= TextSize::from(1);
            }
            if start > end {
                start = end;
            }
            range = TextRange::new(start, end);
        }
        return range;
    }
    literal.range_trimmed_trivia()
}

fn route_attribute_string(node: &SyntaxNode) -> Option<(SyntaxNode, SyntaxNode, usize)> {
    let literal = phpquery::string_at(node)?;
    let attribute = phpquery::attribute_at(&literal)?;
    if !is_route_attribute(&attribute) {
        return None;
    }
    let index = phpquery::argument_index(&attribute, &literal)?;
    if phpquery::argument_expression(&attribute, index).as_ref() != Some(&literal) {
        return None;
    }
    Some((literal, attribute, index))
}

/// The path (named or first positional) argument of a PHP `Route`
/// attribute.
pub fn php_route_path_reference_at(node: &SyntaxNode) -> Option<PhpRoutePathReference> {
    let (literal, _, index) = route_attribute_string(node)?;
    let name = phpquery::argument_name(&literal).lowercase();
    if name != "path" && (!name.is_empty() || index != 0) {
        return None;
    }
    Some(PhpRoutePathReference {
        value: phpquery::string_value(&literal),
        range: phpquery::string_content_range(&literal),
    })
}

/// Only the named `name` argument of a PHP `Route` attribute.
pub fn php_route_name_reference_at(node: &SyntaxNode) -> Option<PhpRoutePathReference> {
    let (literal, _, _) = route_attribute_string(node)?;
    if !eq_ignore_case(&phpquery::argument_name(&literal), "name") {
        return None;
    }
    Some(PhpRoutePathReference {
        value: phpquery::string_value(&literal),
        range: phpquery::string_content_range(&literal),
    })
}

fn rfind_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .rposition(|window| window == needle)
}

fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

fn php_route_annotation_reference_at(
    source: &str,
    offset: u32,
    property: &str,
) -> Option<PhpRoutePathReference> {
    let bytes = source.as_bytes();
    let cursor = offset as usize;
    if cursor > bytes.len() {
        return None;
    }
    let comment_start = rfind_bytes(&bytes[..cursor], b"/**")?;
    if rfind_bytes(&bytes[..cursor], b"*/").is_some_and(|close| close > comment_start) {
        return None;
    }
    let comment_end =
        find_bytes(&bytes[cursor..], b"*/").map_or(bytes.len(), |close| cursor + close);
    let open = php_route_annotation_open(bytes, comment_start, cursor)?;
    php_route_annotation_string_at(bytes, open, comment_end, cursor, property)
}

/// The default or named `path` string of a legacy `@Route` docblock
/// annotation around `offset`.
pub fn php_route_annotation_path_reference_at(
    source: &str,
    offset: u32,
) -> Option<PhpRoutePathReference> {
    php_route_annotation_reference_at(source, offset, "path")
}

/// Only a `name=` string of a legacy `@Route` docblock annotation.
pub fn php_route_annotation_name_reference_at(
    source: &str,
    offset: u32,
) -> Option<PhpRoutePathReference> {
    php_route_annotation_reference_at(source, offset, "name")
}

fn php_route_annotation_open(source: &[u8], start: usize, cursor: usize) -> Option<usize> {
    let mut result = None;
    for position in start..cursor {
        if source[position] != b'(' {
            continue;
        }
        let mut token_end = position;
        while token_end > start && matches!(source[token_end - 1], b' ' | b'\t') {
            token_end -= 1;
        }
        let mut token_start = token_end;
        while token_start > start && php_route_annotation_name_character(source[token_start - 1]) {
            token_start -= 1;
        }
        let token = &source[token_start..token_end];
        if token == b"@Route" || token.ends_with(b"\\Route") || token.ends_with(b".Route") {
            result = Some(position);
        }
    }
    result
}

fn php_route_annotation_name_character(value: u8) -> bool {
    matches!(value, b'@' | b'\\' | b'.' | b'_') || value.is_ascii_alphanumeric()
}

fn php_route_annotation_string_at(
    source: &[u8],
    open: usize,
    limit: usize,
    cursor: usize,
    property: &str,
) -> Option<PhpRoutePathReference> {
    let mut depth = 0usize;
    let mut argument_index = 0usize;
    let mut segment_start = open + 1;
    let mut position = open + 1;
    while position < limit {
        match source[position] {
            quote @ (b'\'' | b'"') => {
                let content_start = position + 1;
                let mut content_end = content_start;
                while content_end < limit {
                    if source[content_end] == quote
                        && (content_end == content_start || source[content_end - 1] != b'\\')
                    {
                        break;
                    }
                    content_end += 1;
                }
                if depth == 0
                    && cursor >= content_start
                    && cursor <= content_end
                    && php_route_annotation_path_argument(
                        &source[segment_start..position],
                        argument_index,
                        property,
                    )
                {
                    return Some(PhpRoutePathReference {
                        value: String::from_utf8_lossy(&source[content_start..content_end])
                            .into_owned(),
                        range: TextRange::new(
                            TextSize::from(content_start as u32),
                            TextSize::from(content_end as u32),
                        ),
                    });
                }
                position = content_end;
            }
            b'(' | b'{' | b'[' => depth += 1,
            b')' => {
                if depth == 0 {
                    return None;
                }
                depth -= 1;
            }
            b'}' | b']' => depth = depth.saturating_sub(1),
            b',' if depth == 0 => {
                argument_index += 1;
                segment_start = position + 1;
            }
            _ => {}
        }
        position += 1;
    }
    None
}

fn php_route_annotation_path_argument(
    prefix: &[u8],
    argument_index: usize,
    property: &str,
) -> bool {
    let prefix = String::from_utf8_lossy(prefix);
    let prefix = prefix.trim().replace([' ', '\t'], "").lowercase();
    if property == "name" {
        return prefix == "name=";
    }
    prefix == "path=" || (prefix.is_empty() && argument_index == 0)
}

/// Mirrors Symfony plugin route-name generation from a controller method.
/// When the cursor is in a leading docblock rather than the method, the
/// first following method is selected.
pub fn php_route_name_suggestion(root: &SyntaxNode, node: &SyntaxNode, offset: u32) -> String {
    let mut method = phpquery::method_at(node);
    if method.is_none() {
        for class in phpquery::classes(root) {
            for candidate in phpquery::methods(&class) {
                if u32::from(candidate.text_range().end()) < offset {
                    continue;
                }
                if method.as_ref().is_none_or(|current| {
                    candidate.text_range().start() < current.text_range().start()
                }) {
                    method = Some(candidate);
                }
            }
        }
    }
    let Some(method) = method else {
        return String::new();
    };
    let Some(class) = phpquery::class_at(&method) else {
        return String::new();
    };
    let class_name = phpquery::class_name(&class);
    let method_name = phpquery::method_name(&method);
    if class_name.is_empty() || method_name.is_empty() {
        return String::new();
    }
    let method_name = method_name.strip_suffix("Action").unwrap_or(&method_name);
    let mut qualified = phpquery::namespace(root);
    if !qualified.is_empty() {
        qualified.push('\\');
    }
    qualified.push_str(&class_name);
    let mut parts: Vec<String> = Vec::new();
    for part in qualified.trim_matches('\\').split('\\') {
        if part.is_empty() || eq_ignore_case(part, "controller") {
            continue;
        }
        let mut part = part.lowercase();
        if part.ends_with("bundle") && part != "bundle" {
            part.truncate(part.len() - "bundle".len());
        }
        if part.ends_with("controller") && part != "controller" {
            part.truncate(part.len() - "controller".len());
        }
        if !part.is_empty() {
            parts.push(part);
        }
    }
    if parts.is_empty() || method_name.is_empty() {
        return String::new();
    }
    let mut prefix = parts.join("_");
    if !method_name.starts_with('_') {
        prefix.push('_');
    }
    prefix + &method_name.lowercase()
}

impl PhpRoutePathReference {
    /// The complete path prefix ending at the next slash after the cursor,
    /// or `None` in the final segment.
    pub fn prefix_at(&self, offset: u32) -> Option<String> {
        let start = u32::from(self.range.start());
        let end = u32::from(self.range.end());
        if !(offset >= start && offset < end) && offset != end {
            return None;
        }
        let relative = (offset.saturating_sub(start) as usize).min(self.value.len());
        let slash = self.value.as_bytes()[relative..]
            .iter()
            .position(|&b| b == b'/')?;
        let prefix = &self.value[..relative + slash];
        if prefix.len() < 3
            || eq_ignore_case(prefix.trim_matches('/'), self.value.trim_matches('/'))
        {
            return None;
        }
        Some(format!("/{}", prefix.trim_matches('/')))
    }
}

/// The route-aware HTML contexts from the Symfony plugin: anchor `href` and
/// form `action` values.
pub fn twig_html_route_reference_at(node: &SyntaxNode) -> Option<TwigHtmlRouteReference> {
    let html_string = twigquery::closest_node_of_kind(node, &[twigsyntax::HTML_STRING])?;
    let attribute = twigquery::html_attribute_at(&html_string)?;
    let start_tag = twigquery::starting_html_tag_at(&attribute)?;
    let tag = twigquery::html_tag_name(&start_tag)
        .unwrap_or_default()
        .lowercase();
    let name = twigquery::html_attribute_name(&attribute)
        .unwrap_or_default()
        .lowercase();
    if !((tag == "a" && name == "href") || (tag == "form" && name == "action")) {
        return None;
    }
    let value_node = HtmlString::cast(html_string.clone())
        .and_then(|typed| typed.inner())
        .map_or_else(|| html_string.clone(), |inner| inner.syntax().clone());
    let value = value_node.text().to_string().trim().to_owned();
    if value.contains("{{") || value.contains("{%") || value.contains("{#") {
        return None;
    }
    Some(TwigHtmlRouteReference {
        value,
        node: value_node,
        container: html_string,
    })
}

pub fn twig_html_route_references(root: &SyntaxNode) -> Vec<TwigHtmlRouteReference> {
    twigquery::iterate_nodes(root, &[twigsyntax::HTML_STRING])
        .filter_map(|html_string| twig_html_route_reference_at(&html_string))
        .filter(|reference| !route_url_path(&reference.value).is_empty())
        .collect()
}

/// Normalizes route lookup input without forcing a leading slash. Absolute
/// and protocol-relative URLs are reduced to their raw path; query strings
/// and fragments are discarded; opaque values remain unchanged.
pub fn normalize_route_search_path(value: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        return String::new();
    }
    let has_authority_prefix = value.contains("://") || value.starts_with("//");
    // Braces would be escaped by EscapedPath; placeholders must stay intact.
    if !value.contains(['{', '}'])
        && let Some(parsed) = parse_route_url(value)
    {
        let path = parsed.path;
        if has_authority_prefix || parsed.has_host {
            if path.trim().is_empty() {
                return "/".to_owned();
            }
            return path;
        }
        if !parsed.absolute && !path.trim().is_empty() {
            return path;
        }
    }
    normalize_route_search_path_fallback(value)
}

/// The parts of a URL reference that route path normalization needs.
struct RouteUrl {
    /// The reference names a scheme (`https:`, `mailto:`).
    absolute: bool,
    /// The scheme is followed by an opaque value (`mailto:x`, `tel:1`).
    opaque: bool,
    has_host: bool,
    /// Escaped path without query and fragment ("" for opaque URLs).
    path: String,
}

/// Parses a URL or relative reference; `None` for malformed input.
fn parse_route_url(value: &str) -> Option<RouteUrl> {
    // The URL standard strips tabs and newlines instead of rejecting them.
    if value.bytes().any(|byte| byte < b' ' || byte == 0x7f) {
        return None;
    }
    let (url, absolute) = match url::Url::parse(value) {
        Ok(url) => (url, true),
        // Network-path reference: borrow a scheme to read host and path.
        Err(url::ParseError::RelativeUrlWithoutBase) if value.starts_with("//") => {
            (url::Url::parse(&format!("http:{value}")).ok()?, false)
        }
        Err(url::ParseError::RelativeUrlWithoutBase) => {
            let end = value.find(['?', '#']).unwrap_or(value.len());
            return Some(RouteUrl {
                absolute: false,
                opaque: false,
                has_host: false,
                path: swlsp_uriutil::escaped_path(&value[..end])?,
            });
        }
        Err(_) => return None,
    };
    let opaque = url.cannot_be_a_base();
    Some(RouteUrl {
        absolute,
        opaque,
        has_host: url.host_str().is_some_and(|host| !host.is_empty()),
        path: if opaque {
            String::new()
        } else {
            swlsp_uriutil::escaped_path(url.path())?
        },
    })
}

fn normalize_route_search_path_fallback(value: &str) -> String {
    let mut value = value;
    let mut path_start = None;
    let scheme_separator = value.find("://");
    if let Some(separator) = scheme_separator {
        if let Some(offset) = value[separator + 3..].find('/') {
            path_start = Some(separator + 3 + offset);
        }
    } else if value.starts_with("//")
        && let Some(offset) = value[2..].find('/')
    {
        path_start = Some(2 + offset);
    }
    if let Some(start) = path_start {
        value = &value[start..];
    } else if scheme_separator.is_some() || value.starts_with("//") {
        value = "/";
    }
    let mut end = value.len();
    if let Some(query) = value.find('?') {
        end = end.min(query);
    }
    if let Some(fragment) = value.find('#') {
        end = end.min(fragment);
    }
    value[..end].to_owned()
}

/// Normalizes a static HTML URL for route matching; fragment-only links and
/// opaque schemes (`mailto:`, `tel:`, ...) yield "".
pub fn route_url_path(value: &str) -> String {
    let value = value.trim();
    if value.is_empty() || value.starts_with('#') {
        return String::new();
    }
    if parse_route_url(value).is_some_and(|parsed| parsed.opaque) {
        return String::new();
    }
    let path = normalize_route_search_path(value);
    if path.is_empty() {
        return String::new();
    }
    format!("/{}", path.trim_start_matches('/'))
}

/// The route name of a route-generator call whose parameter array contains
/// the string key at `node`.
pub fn php_route_parameter_context(node: &SyntaxNode) -> Option<String> {
    let key = phpquery::string_at(node)?;
    let array = phpquery::array_at(node)?;
    let item = phpquery::array_item_at(node)?;
    if phpquery::array_item_key(&item).as_ref() != Some(&key) {
        return None;
    }
    let call = phpquery::call_at(&array)?;
    if !is_php_route_generator_method(&phpquery::call_method_name(&call))
        || phpquery::argument_expression(&call, 1).as_ref() != Some(&array)
    {
        return None;
    }
    let route_name = phpquery::string_argument(&call, 0)
        .map(|literal| phpquery::string_value(&literal))
        .unwrap_or_default();
    (!route_name.is_empty()).then_some(route_name)
}

/// The static route and parameter names for a quoted key in the second
/// argument of a PHP route-generator call.
pub fn php_route_parameter_reference_at(node: &SyntaxNode) -> Option<(String, String)> {
    let route_name = php_route_parameter_context(node)?;
    let parameter = phpquery::string_at(node)
        .map(|key| phpquery::string_value(&key))
        .unwrap_or_default();
    if parameter.is_empty() {
        return None;
    }
    Some((route_name, parameter))
}

pub fn twig_route_parameter_context(node: &SyntaxNode) -> Option<String> {
    let hash = twigquery::hash_at(node)?;
    let call = twigquery::function_call_at(&hash)?;
    if !twigquery::function_name(&call).is_some_and(|name| is_twig_route_generator_method(&name))
        || twigquery::function_argument_index(&hash) != Some(1)
    {
        return None;
    }
    if twigquery::closest_node_of_kind(node, &[twigsyntax::TWIG_LITERAL_HASH_PAIR]).is_some()
        && twigquery::hash_key_at(node).is_none()
    {
        return None;
    }
    let route_name = twigquery::string_argument(&call, 0)
        .and_then(|literal| twigquery::string_value(&literal))
        .unwrap_or_default();
    (!route_name.is_empty()).then_some(route_name)
}

/// Quoted, unquoted, and shorthand hash keys from the second argument of
/// `path()`/`url()`.
pub fn twig_route_parameter_reference_at(node: &SyntaxNode) -> Option<(String, String)> {
    let route_name = twig_route_parameter_context(node)?;
    let key = twigquery::hash_key_at(node)?;
    let parameter = match twigquery::literal_string_at(node) {
        Some(literal) if literal.ancestors().any(|current| current == key) => {
            twigquery::string_value(&literal).unwrap_or_default()
        }
        _ => {
            let text = key.text().to_string();
            let text = text.trim();
            let text = text.strip_suffix(':').unwrap_or(text).trim();
            text.trim_matches(['\'', '"', '`']).to_owned()
        }
    };
    if !route_parameter_name(&parameter) {
        return None;
    }
    Some((route_name, parameter))
}

fn route_parameter_name(value: &str) -> bool {
    !value.is_empty()
        && value
            .bytes()
            .enumerate()
            .all(|(index, byte)| is_route_parameter_character(byte, index == 0))
}

/// Excludes dynamic/interpolated values and controller FQCN shortcuts.
pub fn is_static_route_name(name: &str) -> bool {
    !name.is_empty() && !name.contains(['$', '\\']) && !name.contains("#{") && !name.contains("{{")
}

fn is_php_route_generator_method(name: &str) -> bool {
    PHP_ROUTE_GENERATOR_METHODS.contains(&name)
}

fn is_twig_route_generator_method(name: &str) -> bool {
    TWIG_ROUTE_GENERATOR_METHODS.contains(&name)
}

#[cfg(test)]
#[path = "tests_routectx/route_context.rs"]
mod tests;
