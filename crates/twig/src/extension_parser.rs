//! Twig extension functions, filters, tests and operators declared in PHP
//! (port of `extension_parser.go`).

use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::NameResolver;
use swlsp_php_types::equal_fold;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange};

use crate::global::is_twig_extension_class;

macro_rules! callable_type {
    ($(#[$meta:meta])* $name:ident) => {
        $(#[$meta])*
        #[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
        pub struct $name {
            pub name: String,
            pub usage: String,
            pub method: String,
            pub line: i64,
            pub parameters: Vec<TwigParameter>,
            pub file_path: String,
            pub deprecated: bool,
            pub deprecation: String,
            pub deprecated_range: TextRange,
        }
    };
}

callable_type!(
    /// A function defined in a Twig extension.
    TwigFunction
);
callable_type!(
    /// A filter defined in a Twig extension.
    TwigFilter
);
callable_type!(
    /// A test defined in a Twig extension.
    TwigTest
);

/// An operator registered by a Twig extension (`getOperators()` up to Twig
/// 3.20, `getExpressionParsers()` from 3.21).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigOperator {
    pub name: String,
    pub file_path: String,
    pub range: TextRange,
    pub unary: bool,
    pub alias: bool,
    pub legacy: bool,
}

/// A parameter of a function or filter.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigParameter {
    pub name: String,
    #[serde(rename = "type")]
    pub ty: String,
    pub optional: bool,
}

/// All callables of one PHP file.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigCallables {
    pub functions: Vec<TwigFunction>,
    pub filters: Vec<TwigFilter>,
    pub tests: Vec<TwigTest>,
}

pub(crate) fn sv(node: Option<&SyntaxNode>) -> String {
    node.map(phpquery::string_value).unwrap_or_default()
}

fn string_argument_value(node: &SyntaxNode, index: usize) -> String {
    sv(phpquery::string_argument(node, index).as_ref())
}

/// Twig extension declarations from the shared PHP CST.
pub fn parse_twig_extension(file_path: &str, root: &SyntaxNode, content: &str) -> TwigCallables {
    parse_twig_extension_tree(file_path, root, content, &LineIndex::new(content))
}

pub fn parse_twig_extension_tree(
    file_path: &str,
    root: &SyntaxNode,
    content: &str,
    line_index: &LineIndex,
) -> TwigCallables {
    if !is_twig_callable_definition_candidate(content) {
        return TwigCallables::default();
    }
    let mut result = TwigCallables::default();
    if let Some(class_node) = find_extension_class(root) {
        let params = build_method_parameter_map(&class_node);
        let deprecations = build_method_deprecation_map(&class_node);
        let resolver = NameResolver::new(root);
        result.functions =
            parse_extension_functions(file_path, &class_node, &params, &deprecations, line_index);
        result.filters =
            parse_extension_filters(file_path, &class_node, &params, &deprecations, line_index);
        result.tests = parse_extension_tests(
            file_path,
            &class_node,
            &params,
            &deprecations,
            line_index,
            &resolver,
        );
    }
    let attributes = parse_attribute_twig_callables(file_path, root, line_index);
    result.functions.extend(attributes.functions);
    result.filters.extend(attributes.filters);
    result.tests.extend(attributes.tests);
    result
}

/// Legacy `TwigTest`/`Twig_SimpleTest` registrations and modern
/// `#[AsTwigTest]` declarations.
pub fn parse_twig_tests(file_path: &str, root: &SyntaxNode, content: &str) -> Vec<TwigTest> {
    parse_twig_extension(file_path, root, content).tests
}

pub(crate) fn is_twig_callable_definition_candidate(content: &str) -> bool {
    [
        "TwigFunction",
        "TwigFilter",
        "TwigTest",
        "AsTwig",
        "Twig_SimpleFunction",
        "Twig_SimpleFilter",
        "Twig_SimpleTest",
        "Twig_Test",
    ]
    .iter()
    .any(|needle| content.contains(needle))
}

/// Legacy `getOperators()` declarations and Twig 3.21+/4 expression-parser
/// registrations from extension classes.
pub fn parse_twig_operators(
    file_path: &str,
    root: &SyntaxNode,
    content: &str,
) -> Vec<TwigOperator> {
    if !content.contains("getOperators") && !content.contains("getExpressionParsers") {
        return Vec::new();
    }
    let mut operators = Vec::new();
    let resolver = NameResolver::new(root);
    for class_node in phpquery::classes(root) {
        if !is_twig_extension_class(&class_node, &resolver) {
            continue;
        }
        if let Some(method) = method_named(&class_node, "getOperators") {
            operators.extend(parse_legacy_twig_operators(file_path, &method));
        }
        if let Some(method) = method_named(&class_node, "getExpressionParsers") {
            operators.extend(parse_twig_expression_parsers(file_path, &method));
        }
    }
    unique_twig_operators(operators)
}

fn parse_legacy_twig_operators(file_path: &str, method: &SyntaxNode) -> Vec<TwigOperator> {
    let mut operators = Vec::new();
    for return_node in phpquery::nodes(method, &[PHP_RETURN_STATEMENT]) {
        let Some(expression) = return_node.children().next() else {
            continue;
        };
        if expression.kind() != PHP_ARRAY {
            continue;
        }
        for (group_index, group_item) in phpquery::array_items(&expression).into_iter().enumerate()
        {
            let Some(group) = phpquery::array_item_value(&group_item) else {
                continue;
            };
            if group.kind() != PHP_ARRAY {
                continue;
            }
            for operator_item in phpquery::array_items(&group) {
                let Some(key) = phpquery::array_item_key(&operator_item) else {
                    continue;
                };
                let name = phpquery::string_value(&key);
                if name.is_empty() {
                    continue;
                }
                operators.push(TwigOperator {
                    name,
                    file_path: file_path.to_owned(),
                    range: phpquery::string_content_range(&key),
                    unary: group_index == 0,
                    alias: false,
                    legacy: true,
                });
            }
        }
    }
    operators
}

fn parse_twig_expression_parsers(file_path: &str, method: &SyntaxNode) -> Vec<TwigOperator> {
    let mut operators = Vec::new();
    for object in phpquery::object_creations(method, &[]) {
        let class_name = last_name_part(&phpquery::object_class_name(&object)).to_owned();
        let (unary, alias_index) = match class_name.as_str() {
            "BinaryOperatorExpressionParser" => (false, 6),
            "UnaryOperatorExpressionParser" => (true, 5),
            _ => continue,
        };
        if let Some(name_node) = phpquery::string_argument(&object, 1) {
            let name = phpquery::string_value(&name_node);
            if !name.is_empty() {
                operators.push(TwigOperator {
                    name,
                    file_path: file_path.to_owned(),
                    range: phpquery::string_content_range(&name_node),
                    unary,
                    alias: false,
                    legacy: false,
                });
            }
        }
        let Some(aliases) =
            argument_expression_by_name_or_position(&object, "aliases", alias_index)
        else {
            continue;
        };
        if aliases.kind() != PHP_ARRAY {
            continue;
        }
        for item in phpquery::array_items(&aliases) {
            let Some(value) = phpquery::array_item_value(&item) else {
                continue;
            };
            let name = phpquery::string_value(&value);
            if name.is_empty() {
                continue;
            }
            operators.push(TwigOperator {
                name,
                file_path: file_path.to_owned(),
                range: phpquery::string_content_range(&value),
                unary,
                alias: true,
                legacy: false,
            });
        }
    }
    operators
}

fn argument_expression_by_name_or_position(
    container: &SyntaxNode,
    name: &str,
    position: usize,
) -> Option<SyntaxNode> {
    let arguments: Vec<SyntaxNode> = phpquery::arguments(container).collect();
    if position < arguments.len() && phpquery::argument_name(&arguments[position]).is_empty() {
        return phpquery::argument_expression(container, position);
    }
    for (index, argument) in arguments.iter().enumerate() {
        if equal_fold(&phpquery::argument_name(argument), name) {
            return phpquery::argument_expression(container, index);
        }
    }
    if position < arguments.len() {
        return phpquery::argument_expression(container, position);
    }
    None
}

fn unique_twig_operators(operators: Vec<TwigOperator>) -> Vec<TwigOperator> {
    let mut seen = std::collections::HashSet::with_capacity(operators.len());
    operators
        .into_iter()
        .filter(|operator| {
            seen.insert((
                operator.file_path.clone(),
                operator.range,
                operator.name.clone(),
            ))
        })
        .collect()
}

const AS_TWIG_FUNCTION: &str = "Twig\\Attribute\\AsTwigFunction";
const AS_TWIG_FILTER: &str = "Twig\\Attribute\\AsTwigFilter";
const AS_TWIG_TEST: &str = "Twig\\Attribute\\AsTwigTest";

fn parse_attribute_twig_callables(
    file_path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> TwigCallables {
    let resolver = NameResolver::new(root);
    let namespace = phpquery::namespace(root);
    let mut result = TwigCallables::default();
    for class_node in phpquery::classes(root) {
        let mut class_name = phpquery::class_name(&class_node);
        if !namespace.is_empty() {
            class_name = format!("{namespace}\\{class_name}");
        }
        let params = build_method_parameter_map(&class_node);
        let deprecations = build_method_deprecation_map(&class_node);
        for method in phpquery::methods(&class_node) {
            let method_name = phpquery::method_name(&method);
            let parameters = params.get(&method_name).cloned().unwrap_or_default();
            let line = line_of(line_index, &method);
            for attribute in phpquery::attributes(&method) {
                let attribute_name = resolver
                    .resolve(&phpquery::attribute_name(&attribute))
                    .trim_matches('\\')
                    .to_owned();
                let is_function = equal_fold(&attribute_name, AS_TWIG_FUNCTION);
                let is_filter = equal_fold(&attribute_name, AS_TWIG_FILTER);
                if !is_function && !is_filter && !equal_fold(&attribute_name, AS_TWIG_TEST) {
                    continue;
                }
                let name = twig_callable_attribute_name(&attribute);
                if name.is_empty() || method_name.is_empty() {
                    continue;
                }
                let callback = format!("{class_name}::{method_name}");
                let deprecation = twig_attribute_callable_deprecation(
                    &attribute,
                    &name,
                    deprecations.get(&method_name).cloned().unwrap_or_default(),
                );
                let usage = build_usage(&name, &parameters);
                macro_rules! callable {
                    ($ty:ident) => {
                        $ty {
                            name,
                            usage,
                            method: callback,
                            line,
                            parameters: parameters.clone(),
                            file_path: file_path.to_owned(),
                            deprecated: deprecation.deprecated,
                            deprecation: deprecation.message,
                            deprecated_range: deprecation.source_range,
                        }
                    };
                }
                if is_function {
                    result.functions.push(callable!(TwigFunction));
                } else if is_filter {
                    result.filters.push(callable!(TwigFilter));
                } else {
                    result.tests.push(callable!(TwigTest));
                }
            }
        }
    }
    result
}

fn line_of(line_index: &LineIndex, node: &SyntaxNode) -> i64 {
    i64::from(
        line_index
            .position(u32::from(node.range_trimmed_trivia().start()))
            .0,
    ) + 1
}

fn twig_callable_attribute_name(attribute: &SyntaxNode) -> String {
    let mut positional = 0;
    for argument in phpquery::arguments(attribute) {
        let name = phpquery::argument_name(&argument);
        if !name.is_empty() && !equal_fold(&name, "name") {
            continue;
        }
        if name.is_empty() && positional > 0 {
            positional += 1;
            continue;
        }
        if let Some(expression) = twig_attribute_argument_expression(&argument)
            && expression.kind() == PHP_STRING
        {
            return phpquery::string_value(&expression);
        }
        positional += 1;
    }
    String::new()
}

fn twig_attribute_callable_deprecation(
    attribute: &SyntaxNode,
    callable_name: &str,
    mut method: CallableDeprecation,
) -> CallableDeprecation {
    if !method.deprecated {
        method.apply_body_trigger(callable_name);
    }
    let mut option = CallableDeprecation::default();
    let mut alternative = String::new();
    for argument in phpquery::arguments(attribute) {
        let name = swlsp_php_types::go_to_lower(&phpquery::argument_name(&argument));
        if name.is_empty() {
            continue;
        }
        let Some(expression) = twig_attribute_argument_expression(&argument) else {
            continue;
        };
        match name.as_str() {
            "alternative" => alternative = phpquery::string_value(&expression),
            "deprecated" => {
                let raw = raw_lower(&expression);
                if raw == "false" || raw == "null" || raw == "0" {
                    continue;
                }
                option.deprecated = true;
                option.source_range = argument.range_trimmed_trivia();
                let since = phpquery::string_value(&expression);
                if !since.is_empty() {
                    option.message = format!("Deprecated since {since}.");
                }
            }
            "deprecationinfo" => {
                let raw = raw_lower(&expression);
                if raw == "false" || raw == "null" {
                    continue;
                }
                option.deprecated = true;
                option.source_range = argument.range_trimmed_trivia();
                if equal_fold(
                    last_name_part(&phpquery::object_class_name(&expression)),
                    "DeprecatedCallableInfo",
                ) {
                    let package = string_argument_value(&expression, 0);
                    let version = string_argument_value(&expression, 1);
                    alternative = string_argument_value(&expression, 2);
                    option.message = deprecated_since_message(&package, &version);
                }
            }
            _ => {}
        }
    }
    if !alternative.is_empty() {
        if !option.message.is_empty() {
            option.message.push(' ');
        }
        option
            .message
            .push_str(&format!("Use {alternative} instead."));
    }
    if !option.deprecated {
        return method;
    }
    if option.message.is_empty() {
        option.message = method.message;
    }
    option
}

fn raw_lower(node: &SyntaxNode) -> String {
    swlsp_php_types::go_to_lower(node.text().to_string().trim())
}

fn twig_attribute_argument_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    let named = argument.kind() == PHP_NAMED_ARGUMENT;
    argument
        .children()
        .find(|child| !(named && child.kind() == PHP_NAME))
}

fn find_extension_class(root: &SyntaxNode) -> Option<SyntaxNode> {
    let resolver = NameResolver::new(root);
    phpquery::classes(root)
        .into_iter()
        .find(|class_node| is_twig_extension_class(class_node, &resolver))
}

type ParameterMap = HashMap<String, Vec<TwigParameter>>;
type DeprecationMap = HashMap<String, CallableDeprecation>;

struct RegisteredCallable {
    name: String,
    callback: String,
    line: i64,
    deprecation: CallableDeprecation,
}

fn registered_callables(
    class_node: &SyntaxNode,
    getter: &str,
    class_names: &[&str],
    deprecations: &DeprecationMap,
    line_index: &LineIndex,
    fallback: impl Fn(&SyntaxNode) -> String,
) -> Vec<RegisteredCallable> {
    let Some(method) = method_named(class_node, getter) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for object in phpquery::object_creations(&method, &[]) {
        let class_name = phpquery::object_class_name(&object);
        if !class_names.contains(&last_name_part(&class_name)) {
            continue;
        }
        let name = string_argument_value(&object, 0);
        let mut callback = callback_name(phpquery::argument_expression(&object, 1).as_ref());
        if callback.is_empty() {
            callback = fallback(&object);
        }
        if name.is_empty() || callback.is_empty() {
            continue;
        }
        let deprecation = callable_deprecation(&object, &callback, deprecations);
        result.push(RegisteredCallable {
            name,
            callback,
            line: line_of(line_index, &object),
            deprecation,
        });
    }
    result
}

fn parse_extension_functions(
    file_path: &str,
    class_node: &SyntaxNode,
    params: &ParameterMap,
    deprecations: &DeprecationMap,
    line_index: &LineIndex,
) -> Vec<TwigFunction> {
    registered_callables(
        class_node,
        "getFunctions",
        &["TwigFunction", "Twig_SimpleFunction"],
        deprecations,
        line_index,
        |_| String::new(),
    )
    .into_iter()
    .map(|callable| {
        let parameters = params
            .get(callback_method_name(&callable.callback))
            .cloned()
            .unwrap_or_default();
        TwigFunction {
            usage: build_usage(&callable.name, &parameters),
            name: callable.name,
            method: callable.callback,
            line: callable.line,
            parameters,
            file_path: file_path.to_owned(),
            deprecated: callable.deprecation.deprecated,
            deprecation: callable.deprecation.message,
            deprecated_range: callable.deprecation.source_range,
        }
    })
    .collect()
}

fn parse_extension_filters(
    file_path: &str,
    class_node: &SyntaxNode,
    params: &ParameterMap,
    deprecations: &DeprecationMap,
    line_index: &LineIndex,
) -> Vec<TwigFilter> {
    registered_callables(
        class_node,
        "getFilters",
        &["TwigFilter", "Twig_SimpleFilter"],
        deprecations,
        line_index,
        |_| String::new(),
    )
    .into_iter()
    .map(|callable| {
        let mut filter = TwigFilter {
            name: callable.name,
            method: callable.callback,
            line: callable.line,
            file_path: file_path.to_owned(),
            deprecated: callable.deprecation.deprecated,
            deprecation: callable.deprecation.message,
            deprecated_range: callable.deprecation.source_range,
            ..TwigFilter::default()
        };
        if filter.method.contains("::") || filter.method.contains("->") {
            filter.parameters = params
                .get(callback_method_name(&filter.method))
                .cloned()
                .unwrap_or_default();
            filter.usage = build_usage(&filter.name, &filter.parameters);
        }
        filter
    })
    .collect()
}

fn parse_extension_tests(
    file_path: &str,
    class_node: &SyntaxNode,
    params: &ParameterMap,
    deprecations: &DeprecationMap,
    line_index: &LineIndex,
    resolver: &NameResolver,
) -> Vec<TwigTest> {
    registered_callables(
        class_node,
        "getTests",
        &["TwigTest", "Twig_SimpleTest", "Twig_Test"],
        deprecations,
        line_index,
        |object| twig_test_node_class_callback(object, resolver),
    )
    .into_iter()
    .map(|callable| {
        let parameters = params
            .get(callback_method_name(&callable.callback))
            .cloned()
            .unwrap_or_default();
        TwigTest {
            usage: build_usage(&callable.name, &parameters),
            name: callable.name,
            method: callable.callback,
            line: callable.line,
            parameters,
            file_path: file_path.to_owned(),
            deprecated: callable.deprecation.deprecated,
            deprecation: callable.deprecation.message,
            deprecated_range: callable.deprecation.source_range,
        }
    })
    .collect()
}

fn twig_test_node_class_callback(object: &SyntaxNode, resolver: &NameResolver) -> String {
    let Some(options) = phpquery::argument_expression(object, 2) else {
        return String::new();
    };
    if options.kind() != PHP_ARRAY {
        return String::new();
    }
    for item in phpquery::array_items(&options) {
        if sv(phpquery::array_item_key(&item).as_ref()) != "node_class" {
            continue;
        }
        let value = phpquery::array_item_value(&item);
        let mut class_name = value
            .as_ref()
            .map(phpquery::class_constant_name)
            .unwrap_or_default();
        if !class_name.is_empty() {
            class_name = resolver.resolve(&class_name);
        } else {
            class_name = sv(value.as_ref());
        }
        let class_name = class_name.trim_matches('\\');
        if class_name.is_empty() {
            return String::new();
        }
        return format!("{class_name}::compile");
    }
    String::new()
}

fn callback_name(expression: Option<&SyntaxNode>) -> String {
    let Some(expression) = expression else {
        return String::new();
    };
    match expression.kind() {
        PHP_STRING => return phpquery::string_value(expression),
        PHP_ARRAY => {
            let items = phpquery::array_items(expression);
            if items.len() < 2 {
                return String::new();
            }
            let receiver = compact_callback_receiver(&items[0].text().to_string());
            let method = phpquery::nodes(&items[1], &[PHP_STRING])
                .first()
                .map(phpquery::string_value)
                .unwrap_or_default();
            if method.is_empty() {
                return String::new();
            }
            if receiver.is_empty() {
                return method;
            }
            if receiver.starts_with('$') {
                return format!("{receiver}->{method}");
            }
            return format!("{receiver}::{method}");
        }
        PHP_MEMBER_CALL | PHP_SCOPED_CALL | PHP_FUNCTION_CALL => {
            return phpquery::call_name(expression);
        }
        _ => {}
    }
    for call in phpquery::calls(expression, &[]) {
        let name = phpquery::call_name(&call);
        if !name.is_empty() {
            return name;
        }
    }
    String::new()
}

pub(crate) fn method_named(class_node: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    phpquery::methods(class_node)
        .into_iter()
        .find(|method| phpquery::method_name(method) == name)
}

fn build_method_parameter_map(class_node: &SyntaxNode) -> ParameterMap {
    let mut result = HashMap::new();
    for method in phpquery::methods(class_node) {
        let name = phpquery::method_name(&method);
        if name.is_empty() {
            continue;
        }
        let parameters = phpquery::parameters(&method)
            .filter_map(|parameter| {
                let name = phpquery::parameter_name(&parameter);
                (!name.is_empty()).then(|| TwigParameter {
                    name,
                    ty: phpquery::parameter_type(&parameter),
                    optional: phpquery::parameter_optional(&parameter),
                })
            })
            .collect();
        result.insert(name, parameters);
    }
    result
}

#[derive(Clone, Debug, Default)]
pub(crate) struct CallableDeprecation {
    pub(crate) deprecated: bool,
    pub(crate) message: String,
    pub(crate) source_range: TextRange,
    pub(crate) body_triggers: Vec<(String, TextRange)>,
}

impl CallableDeprecation {
    /// Promotes the first body `trigger_deprecation()` whose message names
    /// the callable.
    fn apply_body_trigger(&mut self, callable_name: &str) {
        if let Some((message, range)) = self
            .body_triggers
            .iter()
            .find(|(message, _)| message_deprecates_twig_callable(message, callable_name))
        {
            self.deprecated = true;
            self.message = message.clone();
            self.source_range = *range;
        }
    }
}

fn build_method_deprecation_map(class_node: &SyntaxNode) -> DeprecationMap {
    let mut result = HashMap::new();
    for method in phpquery::methods(class_node) {
        let name = phpquery::method_name(&method);
        if name.is_empty() {
            continue;
        }
        let mut deprecation = CallableDeprecation::default();
        for call in phpquery::calls(&method, &[]) {
            let call_name = swlsp_php_types::go_to_lower(&phpquery::call_method_name(&call));
            if call_name != "trigger_deprecation" && !call_name.contains("triggerdeprecation") {
                continue;
            }
            if call_name == "triggerdeprecation" && call.kind() != PHP_FUNCTION_CALL {
                // Twig's DeprecatedCallableInfo::triggerDeprecation() is itself
                // an explicit declaration that the callback is deprecated.
                deprecation.deprecated = true;
                deprecation.source_range = call.range_trimmed_trivia();
                break;
            }
            if let Some(alternative) = deprecated_method_alternative(&call) {
                deprecation.deprecated = true;
                deprecation.source_range = call.range_trimmed_trivia();
                deprecation.message = format!("Callback method {name} is deprecated.");
                if !alternative.is_empty() {
                    deprecation
                        .message
                        .push_str(&format!(" Use {alternative} instead."));
                }
                break;
            }
            deprecation
                .body_triggers
                .push((last_string_argument(&call), call.range_trimmed_trivia()));
        }
        if deprecation.deprecated || !deprecation.body_triggers.is_empty() {
            result.insert(name, deprecation);
        }
    }
    result
}

fn callable_deprecation(
    object: &SyntaxNode,
    callback: &str,
    methods: &DeprecationMap,
) -> CallableDeprecation {
    let mut option = callable_option_deprecation(object);
    let mut method = methods
        .get(callback_method_name(callback))
        .cloned()
        .unwrap_or_default();
    if !method.deprecated {
        method.apply_body_trigger(&string_argument_value(object, 0));
    }
    if !option.deprecated {
        if method.deprecated {
            return method;
        }
        return CallableDeprecation::default();
    }
    if option.message.is_empty() {
        option.message = method.message;
    }
    option
}

fn callable_option_deprecation(object: &SyntaxNode) -> CallableDeprecation {
    let Some(options) = phpquery::argument_expression(object, 2) else {
        return CallableDeprecation::default();
    };
    if options.kind() != PHP_ARRAY {
        return CallableDeprecation::default();
    }
    let mut result = CallableDeprecation::default();
    let mut alternative = String::new();
    let mut deprecating_package = String::new();
    for item in phpquery::array_items(&options) {
        let key = sv(phpquery::array_item_key(&item).as_ref());
        let Some(value) = phpquery::array_item_value(&item) else {
            continue;
        };
        match key.as_str() {
            "alternative" => alternative = phpquery::string_value(&value),
            "deprecating_package" => deprecating_package = phpquery::string_value(&value),
            "deprecated" => {
                let raw = raw_lower(&value);
                if raw == "false" || raw == "null" || raw == "0" {
                    continue;
                }
                result.deprecated = true;
                result.source_range = item.range_trimmed_trivia();
                let since = phpquery::string_value(&value);
                if !since.is_empty() {
                    result.message = format!("Deprecated since {since}.");
                }
            }
            "deprecation_info" => {
                let raw = raw_lower(&value);
                if raw == "false" || raw == "null" {
                    continue;
                }
                result.deprecated = true;
                result.source_range = item.range_trimmed_trivia();
                if equal_fold(
                    last_name_part(&phpquery::object_class_name(&value)),
                    "DeprecatedCallableInfo",
                ) {
                    let package = string_argument_value(&value, 0);
                    let version = string_argument_value(&value, 1);
                    alternative = string_argument_value(&value, 2);
                    result.message = deprecated_since_message(&package, &version);
                }
            }
            _ => {}
        }
    }
    if result.deprecated
        && !result.message.is_empty()
        && !deprecating_package.is_empty()
        && !result.message.contains(&deprecating_package)
    {
        result.message = format!(
            "{} by {deprecating_package}.",
            result.message.strip_suffix('.').unwrap_or(&result.message)
        );
    }
    if !alternative.is_empty() {
        if !result.message.is_empty() {
            result.message.push(' ');
        }
        result
            .message
            .push_str(&format!("Use {alternative} instead."));
    }
    result
}

pub(crate) fn last_string_argument(call: &SyntaxNode) -> String {
    let mut message = String::new();
    for index in 0..phpquery::arguments(call).count() {
        let value = string_argument_value(call, index);
        if !value.is_empty() {
            message = value;
        }
    }
    message
}

fn deprecated_method_alternative(call: &SyntaxNode) -> Option<String> {
    phpquery::calls(call, &[])
        .into_iter()
        .find(|nested| {
            equal_fold(
                &phpquery::call_method_name(nested),
                "deprecatedMethodMessage",
            )
        })
        .map(|nested| string_argument_value(&nested, 3))
}

fn message_deprecates_twig_callable(message: &str, callable: &str) -> bool {
    let message = swlsp_php_types::go_to_lower(message.trim());
    let callable = swlsp_php_types::go_to_lower(callable.trim());
    if message.is_empty()
        || callable.is_empty()
        || !message.contains("deprecated")
        || (!message.contains("function") && !message.contains("filter"))
    {
        return false;
    }
    message.contains(&callable)
}

fn deprecated_since_message(package: &str, version: &str) -> String {
    match (package.is_empty(), version.is_empty()) {
        (false, false) => format!("Deprecated since {package} {version}."),
        (true, false) => format!("Deprecated since {version}."),
        (false, true) => format!("Deprecated by {package}."),
        (true, true) => String::new(),
    }
}

fn build_usage(name: &str, parameters: &[TwigParameter]) -> String {
    let names: Vec<&str> = parameters
        .iter()
        .map(|parameter| parameter.name.as_str())
        .collect();
    format!("{name}({})", names.join(", "))
}

pub(crate) fn callback_method_name(callback: &str) -> &str {
    if let Some(index) = callback.rfind("::") {
        return &callback[index + 2..];
    }
    if let Some(index) = callback.rfind("->") {
        return &callback[index + 2..];
    }
    callback
}

fn compact_callback_receiver(text: &str) -> String {
    let text = text.trim().trim_matches(['[', ']', '(', ')', ',']);
    text.split_whitespace().collect()
}

pub(crate) fn last_name_part(name: &str) -> &str {
    let name = name.strip_prefix('\\').unwrap_or(name);
    match name.rfind('\\') {
        Some(index) => &name[index + 1..],
        None => name,
    }
}
