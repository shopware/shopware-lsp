//! Symfony `#[Route]` attributes and `RoutingConfigurator` definitions.

use std::collections::{HashMap, HashSet};

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::NameResolver;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, Tree};

use crate::model::Route;
use crate::util::line_at;

const ROUTING_CONFIGURATOR_CLASS: &str =
    "Symfony\\Component\\Routing\\Loader\\Configurator\\RoutingConfigurator";

/// Parses a PHP source string.
pub fn parse_php_routes(file_path: &str, source: &str) -> Vec<Route> {
    let parse = swlsp_parser_php::parse(source);
    parse_php_routes_tree(file_path, Some(&parse.tree), None)
}

/// Extracts routes from an already parsed PHP document, building a line
/// index when none is given.
pub fn parse_php_routes_tree(
    file_path: &str,
    tree: Option<&Tree>,
    line_index: Option<&LineIndex>,
) -> Vec<Route> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let owned;
    let line_index = match line_index {
        Some(index) => index,
        None => {
            owned = LineIndex::new(tree.source_arc().clone());
            &owned
        }
    };
    let root = tree.root();
    let namespace = phpquery::namespace(&root);
    let resolver = NameResolver::new(&root);
    let mut routes = Vec::new();

    for class in phpquery::classes(&root) {
        let class_name = phpquery::class_name(&class);
        let mut base_path = String::new();
        for attribute in route_attributes(&class) {
            let class_route = extract_route_from_attribute(&attribute, line_index, &resolver);
            if base_path.is_empty() {
                base_path = class_route.path;
            }
        }
        for method in phpquery::methods(&class) {
            let method_name = phpquery::method_name(&method);
            if method_name.is_empty() {
                continue;
            }
            for attribute in route_attributes(&method) {
                let mut route = extract_route_from_attribute(&attribute, line_index, &resolver);
                if !base_path.is_empty() && !route.path.is_empty() {
                    route.path = join_route_path(&base_path, &route.path);
                }
                let mut controller = format!("{class_name}::{method_name}");
                if !namespace.is_empty() {
                    controller = format!("{namespace}\\{controller}");
                }
                route.controller = controller;
                route.file_path = file_path.to_owned();
                if !route.name.is_empty()
                    || !route.name_constant.is_empty()
                    || !route.path.is_empty()
                {
                    routes.push(route);
                }
            }
        }
    }

    // Attributes outside a class declaration.
    if !file_path.contains("testdata/") {
        for attribute in phpquery::nodes(&root, &[PHP_ATTRIBUTE]) {
            if phpquery::class_at(&attribute).is_some()
                || phpquery::method_at(&attribute).is_some()
                || !is_route_attribute(&attribute)
            {
                continue;
            }
            let mut route = extract_route_from_attribute(&attribute, line_index, &resolver);
            route.file_path = file_path.to_owned();
            if !route.name.is_empty() || !route.path.is_empty() {
                routes.push(route);
            }
        }
    }

    routes.extend(parse_php_configurator_routes(
        file_path,
        &root,
        tree.source(),
        line_index,
    ));
    routes
}

fn route_attributes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    phpquery::attributes(node)
        .into_iter()
        .filter(is_route_attribute)
        .collect()
}

/// Whether `attribute` is a `Route` attribute (any namespace).
pub(crate) fn is_route_attribute(attribute: &SyntaxNode) -> bool {
    let name = phpquery::attribute_name(attribute);
    let name = name.strip_prefix('\\').unwrap_or(&name);
    let short = name.rfind('\\').map_or(name, |index| &name[index + 1..]);
    short == "Route"
}

fn extract_route_from_attribute(
    node: &SyntaxNode,
    line_index: &LineIndex,
    resolver: &NameResolver,
) -> Route {
    let mut route = Route {
        line: line_at(line_index, node.range_trimmed_trivia().start()),
        ..Route::default()
    };
    let mut positional = 0;
    for argument in phpquery::arguments(node) {
        let argument_name = phpquery::argument_name(&argument);
        if argument_name == "methods" {
            route.methods = php_route_method_values(Some(&argument));
            continue;
        }
        let value = first_string_value(&argument);
        if value.is_empty() {
            // Symfony accepts any constant expression for `name`; record the
            // reference for consumers holding the PHP index.
            if argument_name == "name" {
                route.name_constant = php_route_constant_reference(&argument, resolver);
            }
            continue;
        }
        match argument_name.as_str() {
            "name" => route.name = value,
            "path" => route.path = value,
            "controller" => route.controller = value,
            "" => {
                match positional {
                    0 => route.path = value,
                    1 => route.name = value,
                    _ => {}
                }
                positional += 1;
            }
            _ => {}
        }
    }
    route
}

/// A `Fully\Qualified\Class::CONSTANT` reference when the whole argument is
/// a class constant (not `Foo::class`).
fn php_route_constant_reference(argument: &SyntaxNode, resolver: &NameResolver) -> String {
    let value = collapse_route_expression(&phpquery::argument_value(argument));
    for access in phpquery::nodes(argument, &[PHP_SCOPED_ACCESS, PHP_MEMBER_ACCESS]) {
        let text = collapse_route_expression(&access.text().to_string());
        if text != value {
            continue;
        }
        let Some(separator) = text.rfind("::") else {
            continue;
        };
        if separator == 0 {
            continue;
        }
        let class = &text[..separator];
        let member = &text[separator + 2..];
        if class.is_empty() || member.is_empty() || swlsp_textutil::eq_ignore_case(member, "class")
        {
            continue;
        }
        let resolved = resolver.resolve(class);
        return format!(
            "{}::{member}",
            resolved.strip_prefix('\\').unwrap_or(&resolved)
        );
    }
    String::new()
}

/// `text` with all whitespace removed.
fn collapse_route_expression(text: &str) -> String {
    text.split_whitespace().collect()
}

fn first_string_value(node: &SyntaxNode) -> String {
    for string in phpquery::nodes(node, &[PHP_STRING]) {
        let value = phpquery::string_value(&string);
        if !value.is_empty() {
            return value;
        }
    }
    String::new()
}

fn join_route_path(base_path: &str, path: &str) -> String {
    if base_path.ends_with('/') || path.starts_with('/') {
        format!("{base_path}{path}")
    } else {
        format!("{base_path}/{path}")
    }
}

#[derive(Clone, Default)]
struct ConfiguratorState {
    name_prefix: String,
    path_prefix: String,
    route_index: Option<usize>,
    is_route: bool,
}

struct ConfiguratorEvaluator<'a> {
    file_path: &'a str,
    line_index: &'a LineIndex,
    resolver: &'a NameResolver,
    root_variables: HashSet<String>,
    variables: HashMap<String, ConfiguratorState>,
    routes_by_add: HashMap<SyntaxNode, usize>,
    routes: Vec<Route>,
}

fn parse_php_configurator_routes(
    file_path: &str,
    root: &SyntaxNode,
    source: &str,
    line_index: &LineIndex,
) -> Vec<Route> {
    if !source.contains("RoutingConfigurator") || !source.contains("->add") {
        return Vec::new();
    }
    let resolver = NameResolver::new(root);
    let mut routes = Vec::new();
    for function in phpquery::nodes(
        root,
        &[PHP_CLOSURE, PHP_FUNCTION_DECLARATION, PHP_ARROW_FUNCTION],
    ) {
        let root_variables = routing_configurator_parameters(&function, &resolver);
        if root_variables.is_empty() {
            continue;
        }
        let mut evaluator = ConfiguratorEvaluator {
            file_path,
            line_index,
            resolver: &resolver,
            root_variables,
            variables: HashMap::new(),
            routes_by_add: HashMap::new(),
            routes: Vec::new(),
        };
        evaluator.evaluate(&function);
        routes.extend(evaluator.routes);
    }
    routes
}

/// Parameter names (with `$`) of `function` typed as `RoutingConfigurator`.
pub(crate) fn routing_configurator_parameters(
    function: &SyntaxNode,
    resolver: &NameResolver,
) -> HashSet<String> {
    let mut result = HashSet::new();
    for parameter in phpquery::parameters(function) {
        if !is_routing_configurator_type(&phpquery::parameter_type(&parameter), resolver) {
            continue;
        }
        let name = phpquery::parameter_name(&parameter);
        if !name.is_empty() {
            result.insert(name);
        }
    }
    result
}

fn is_routing_configurator_type(value: &str, resolver: &NameResolver) -> bool {
    value.split(['|', '&', '(', ')']).any(|member| {
        let member = member.strip_prefix('?').unwrap_or(member).trim();
        !member.is_empty()
            && swlsp_textutil::eq_ignore_case(&resolver.resolve(member), ROUTING_CONFIGURATOR_CLASS)
    })
}

impl ConfiguratorEvaluator<'_> {
    fn evaluate(&mut self, function: &SyntaxNode) {
        for statement in phpquery::expression_statements(function) {
            if phpquery::function_like_at(&statement).as_ref() != Some(function) {
                continue;
            }
            self.evaluate_expression(&statement);
        }
        // Arrow functions have no expression statement around their body.
        if function.kind() == PHP_ARROW_FUNCTION {
            self.evaluate_expression(function);
        }
    }

    fn evaluate_expression(&mut self, root: &SyntaxNode) {
        for call in outermost_php_calls(root) {
            self.resolve(Some(&call));
        }
        let assigned = phpquery::assigned_variable(root);
        if assigned.is_empty() {
            return;
        }
        if let Some(value) = php_assignment_value(root) {
            match self.resolve(Some(&value)) {
                Some(state) => {
                    self.variables.insert(assigned, state);
                }
                None => {
                    self.variables.remove(&assigned);
                }
            }
        }
    }

    fn resolve(&mut self, expression: Option<&SyntaxNode>) -> Option<ConfiguratorState> {
        let expression = expression?;
        if expression.kind() == PHP_VARIABLE {
            let name = format!("${}", phpquery::variable_name(expression));
            if let Some(state) = self.variables.get(&name) {
                return Some(state.clone());
            }
            if self.root_variables.contains(&name) {
                return Some(ConfiguratorState::default());
            }
            return None;
        }
        if expression.kind() != PHP_MEMBER_CALL {
            for child in expression.children() {
                if let Some(state) = self.resolve(Some(&child)) {
                    return Some(state);
                }
            }
            return None;
        }
        let receiver = phpquery::call_receiver(expression);
        let mut state = self.resolve(receiver.as_ref())?;
        match phpquery::call_method_name(expression).as_str() {
            "namePrefix" => {
                if state.is_route {
                    return Some(state);
                }
                if let Some(value) = php_route_literal_argument(expression, 0) {
                    state.name_prefix.push_str(&value);
                }
            }
            "prefix" => {
                if state.is_route {
                    return Some(state);
                }
                if let Some(value) = php_route_literal_argument(expression, 0) {
                    state.path_prefix = merge_php_route_paths(&state.path_prefix, &value);
                }
            }
            "add" => {
                if state.is_route {
                    return None;
                }
                return self.add_route(expression, &state);
            }
            "controller" => {
                if let (true, Some(index)) = (state.is_route, state.route_index) {
                    let controller = self
                        .controller_argument(phpquery::argument_expression(expression, 0).as_ref());
                    if !controller.is_empty() {
                        self.routes[index].controller = controller;
                    }
                }
            }
            "defaults" => {
                if let (true, Some(index)) = (state.is_route, state.route_index) {
                    let controller = self.defaults_controller(expression);
                    if !controller.is_empty() {
                        self.routes[index].controller = controller;
                    }
                }
            }
            "methods" => {
                if let (true, Some(index)) = (state.is_route, state.route_index) {
                    let methods = php_route_method_values(
                        phpquery::argument_expression(expression, 0).as_ref(),
                    );
                    if !methods.is_empty() {
                        self.routes[index].methods = methods;
                    }
                }
            }
            _ => {}
        }
        Some(state)
    }

    fn add_route(
        &mut self,
        call: &SyntaxNode,
        configurator: &ConfiguratorState,
    ) -> Option<ConfiguratorState> {
        if let Some(&index) = self.routes_by_add.get(call) {
            return Some(ConfiguratorState {
                route_index: Some(index),
                is_route: true,
                ..ConfiguratorState::default()
            });
        }
        let name = php_route_literal_argument(call, 0)?;
        if name.trim().is_empty() {
            return None;
        }
        let path = php_route_literal_argument(call, 1).unwrap_or_default();
        let path = merge_php_route_paths(&configurator.path_prefix, &path);
        let line_node = phpquery::argument_expression(call, 0).unwrap_or_else(|| call.clone());
        self.routes.push(Route {
            name: format!("{}{name}", configurator.name_prefix),
            path,
            file_path: self.file_path.to_owned(),
            line: line_at(self.line_index, line_node.range_trimmed_trivia().start()),
            ..Route::default()
        });
        let index = self.routes.len() - 1;
        self.routes_by_add.insert(call.clone(), index);
        Some(ConfiguratorState {
            route_index: Some(index),
            is_route: true,
            ..ConfiguratorState::default()
        })
    }

    fn controller_argument(&self, expression: Option<&SyntaxNode>) -> String {
        let Some(expression) = expression else {
            return String::new();
        };
        if expression.kind() == PHP_STRING {
            return normalize_php_route_controller(&phpquery::string_value(expression));
        }
        if expression.kind() == PHP_ARRAY {
            let items = phpquery::array_items(expression);
            if items.len() != 2 {
                return String::new();
            }
            let class_name =
                self.controller_argument(phpquery::array_item_value(&items[0]).as_ref());
            let method = phpquery::array_item_value(&items[1]);
            let Some(method) = method.filter(|method| method.kind() == PHP_STRING) else {
                return String::new();
            };
            if class_name.is_empty() {
                return String::new();
            }
            let method = phpquery::string_value(&method);
            if method.is_empty() {
                return String::new();
            }
            return format!("{class_name}::{method}");
        }
        let class_name = phpquery::class_constant_name(expression);
        if !class_name.is_empty() {
            return normalize_php_route_controller(&self.resolver.resolve(&class_name));
        }
        String::new()
    }

    fn defaults_controller(&self, call: &SyntaxNode) -> String {
        let Some(array) =
            phpquery::argument_expression(call, 0).filter(|array| array.kind() == PHP_ARRAY)
        else {
            return String::new();
        };
        for item in phpquery::array_items(&array) {
            let Some(key) = phpquery::array_item_key(&item) else {
                continue;
            };
            if key.kind() != PHP_STRING || phpquery::string_value(&key) != "_controller" {
                continue;
            }
            return self.controller_argument(phpquery::array_item_value(&item).as_ref());
        }
        String::new()
    }
}

fn outermost_php_calls(root: &SyntaxNode) -> Vec<SyntaxNode> {
    phpquery::calls(root, &[])
        .into_iter()
        .filter(|call| {
            let mut parent = call.parent();
            while let Some(current) = parent {
                if current == *root {
                    break;
                }
                if matches!(
                    current.kind(),
                    PHP_MEMBER_CALL | PHP_SCOPED_CALL | PHP_FUNCTION_CALL
                ) {
                    return false;
                }
                parent = current.parent();
            }
            true
        })
        .collect()
}

fn php_assignment_value(root: &SyntaxNode) -> Option<SyntaxNode> {
    let assignment = phpquery::nodes(root, &[PHP_ASSIGNMENT_EXPRESSION])
        .into_iter()
        .next()?;
    assignment.children().nth(1)
}

/// The value of a quoted string literal argument.
pub(crate) fn php_route_literal_argument(call: &SyntaxNode, index: usize) -> Option<String> {
    let expression = phpquery::argument_expression(call, index)?;
    if expression.kind() != PHP_STRING {
        return None;
    }
    let text = expression.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() < 2 || (text[0] != b'\'' && text[0] != b'"') || text[text.len() - 1] != text[0] {
        return None;
    }
    Some(phpquery::string_value(&expression))
}

fn merge_php_route_paths(prefix: &str, path: &str) -> String {
    if prefix.is_empty() {
        return path.to_owned();
    }
    if path.is_empty() {
        return prefix.to_owned();
    }
    format!(
        "{}/{}",
        prefix.trim_end_matches('/'),
        path.trim_start_matches('/')
    )
}

fn php_route_method_values(node: Option<&SyntaxNode>) -> Vec<String> {
    let Some(node) = node else {
        return Vec::new();
    };
    let array = if node.kind() == PHP_ARRAY {
        node.clone()
    } else {
        match phpquery::nodes(node, &[PHP_ARRAY]).into_iter().next() {
            Some(array) => array,
            None => return Vec::new(),
        }
    };
    let mut methods = Vec::new();
    let mut seen = HashSet::new();
    for item in phpquery::array_items(&array) {
        let method = php_route_method_value(phpquery::array_item_value(&item).as_ref());
        if method.is_empty() || !seen.insert(swlsp_textutil::lowercase(&method)) {
            continue;
        }
        methods.push(method);
    }
    methods
}

fn php_route_method_value(expression: Option<&SyntaxNode>) -> String {
    let Some(expression) = expression else {
        return String::new();
    };
    if expression.kind() == PHP_STRING {
        return phpquery::string_value(expression);
    }
    if expression.kind() != PHP_MEMBER_ACCESS && expression.kind() != PHP_SCOPED_ACCESS {
        return String::new();
    }
    let text = expression.text().to_string();
    let text = text.trim();
    let Some(separator) = text.rfind("::") else {
        return String::new();
    };
    let constant = text[separator + 2..].trim();
    let Some(method) = constant.strip_prefix("METHOD_") else {
        return String::new();
    };
    match method {
        "CONNECT" | "DELETE" | "GET" | "HEAD" | "OPTIONS" | "PATCH" | "POST" | "PURGE" | "PUT"
        | "TRACE" => method.to_owned(),
        _ => String::new(),
    }
}

pub(crate) fn normalize_php_route_controller(value: &str) -> String {
    value.replace('/', "\\").trim_start_matches('\\').to_owned()
}

#[cfg(test)]
#[path = "tests_routes/php_routes.rs"]
mod tests;
