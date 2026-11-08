//! Symfony's generated URL-generator catalogs: Symfony 4+ return arrays,
//! pre-2.8 `declaredRoutes` properties, and Symfony 2.8+ constructor
//! assignments. Generated path tokens are stored in reverse order, so this
//! restores their source path. Modern canonical aliases and legacy
//! I18nRoutingBundle names are normalized to the public route names.

use std::collections::HashMap;
use std::sync::LazyLock;

use regex::Regex;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, Tree};

use crate::model::Route;
use crate::php_routes::normalize_php_route_controller;
use crate::util::line_at;

static LEGACY_ASSETIC_ROUTE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^_assetic_[0-9a-z]+[_0-9+]*$").expect("valid pattern"));

/// Parses a compiled route file tree.
pub(crate) fn parse_compiled_routes_tree(file_path: &str, tree: Option<&Tree>) -> Vec<Route> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let root = tree.root();
    let line_index = LineIndex::new(tree.source_arc().clone());
    let mut catalog = Catalog::default();
    for statement in phpquery::nodes(&root, &[PHP_RETURN_STATEMENT]) {
        let Some(array) = direct_compiled_array(Some(&statement)) else {
            continue;
        };
        for item in phpquery::array_items(&array) {
            let name_node =
                phpquery::array_item_key(&item).and_then(|key| phpquery::string_at(&key));
            let config = direct_compiled_array(phpquery::array_item_value(&item).as_ref());
            let name = compiled_php_string(name_node.as_ref());
            let Some(config) = config.filter(|_| !name.is_empty()) else {
                continue;
            };
            let route = compiled_route_from_array(
                file_path,
                &name,
                name_node.as_ref(),
                &config,
                &line_index,
            );
            if route.path.is_empty() {
                continue;
            }
            let canonical = compiled_array_string_value(
                compiled_array_element(Some(&config), 1).as_ref(),
                "_canonical_route",
            );
            catalog.add(route.clone(), true);
            if canonical.is_empty() {
                continue;
            }
            catalog.add(
                Route {
                    name: canonical,
                    ..route
                },
                false,
            );
        }
    }
    for class in phpquery::classes(&root) {
        if !compiled_route_generator_class(&class) {
            continue;
        }
        for property in phpquery::properties(&class) {
            if !compiled_declared_routes_property(&property) {
                continue;
            }
            catalog.collect_legacy(
                file_path,
                direct_compiled_array(Some(&property)),
                &line_index,
            );
        }
        for method in phpquery::methods(&class) {
            if !swlsp_php_types::equal_fold(&phpquery::method_name(&method), "__construct") {
                continue;
            }
            for assignment in phpquery::nodes(&method, &[PHP_ASSIGNMENT_EXPRESSION]) {
                let Some((left, right)) = compiled_assignment_operands(&assignment) else {
                    continue;
                };
                if !compiled_declared_routes_access(&left) {
                    continue;
                }
                catalog.collect_legacy(file_path, direct_compiled_array(Some(&right)), &line_index);
            }
        }
    }
    catalog.into_routes()
}

#[derive(Default)]
struct Catalog {
    routes: HashMap<String, Route>,
    order: Vec<String>,
}

impl Catalog {
    fn add(&mut self, route: Route, overwrite: bool) {
        if route.name.is_empty() {
            return;
        }
        if let Some(existing) = self.routes.get_mut(&route.name) {
            if overwrite {
                *existing = route;
            }
            return;
        }
        self.order.push(route.name.clone());
        self.routes.insert(route.name.clone(), route);
    }

    fn collect_legacy(
        &mut self,
        file_path: &str,
        array: Option<SyntaxNode>,
        line_index: &LineIndex,
    ) {
        let Some(array) = direct_compiled_array(array.as_ref()) else {
            return;
        };
        for item in phpquery::array_items(&array) {
            let name_node =
                phpquery::array_item_key(&item).and_then(|key| phpquery::string_at(&key));
            let config = direct_compiled_array(phpquery::array_item_value(&item).as_ref());
            let name = compiled_php_string(name_node.as_ref());
            if name.is_empty() || LEGACY_ASSETIC_ROUTE.is_match(&name) {
                continue;
            }
            let Some(config) = config else { continue };
            let name = normalize_legacy_compiled_route_name(&name);
            let route =
                compiled_route_from_array(file_path, name, name_node.as_ref(), &config, line_index);
            if route.path.is_empty() {
                continue;
            }
            // The last localized variant is stored under the public name.
            self.add(route, true);
        }
    }

    fn into_routes(mut self) -> Vec<Route> {
        self.order
            .iter()
            .filter_map(|name| self.routes.remove(name))
            .collect()
    }
}

fn compiled_route_generator_class(class: &SyntaxNode) -> bool {
    if class.kind() != PHP_CLASS_DECLARATION {
        return false;
    }
    phpquery::class_extends(class)
        .into_iter()
        .chain(phpquery::class_implements(class))
        .any(|name| {
            let trimmed = name.trim();
            let normalized =
                swlsp_php_types::go_to_lower(trimmed.strip_prefix('\\').unwrap_or(trimmed));
            normalized == "urlgenerator"
                || normalized == "urlgeneratorinterface"
                || normalized.ends_with("\\routing\\generator\\urlgenerator")
                || normalized.ends_with("\\routing\\generator\\urlgeneratorinterface")
        })
}

fn compiled_declared_routes_property(property: &SyntaxNode) -> bool {
    phpquery::property_variables(property)
        .iter()
        .any(|variable| phpquery::variable_name(variable) == "declaredRoutes")
}

fn compiled_assignment_operands(assignment: &SyntaxNode) -> Option<(SyntaxNode, SyntaxNode)> {
    let operands: Vec<SyntaxNode> = assignment.children().collect();
    if operands.len() < 2 {
        return None;
    }
    Some((operands[0].clone(), operands[operands.len() - 1].clone()))
}

fn compiled_declared_routes_access(node: &SyntaxNode) -> bool {
    let text: String = node.text().to_string().split_whitespace().collect();
    text.ends_with("::$declaredRoutes")
}

fn normalize_legacy_compiled_route_name(name: &str) -> &str {
    let bytes = name.as_bytes();
    if bytes.len() >= 8
        && bytes[0].is_ascii_lowercase()
        && bytes[1].is_ascii_lowercase()
        && &bytes[2..8] == b"__RG__"
    {
        return &name[8..];
    }
    name
}

fn compiled_route_from_array(
    file_path: &str,
    name: &str,
    name_node: Option<&SyntaxNode>,
    config: &SyntaxNode,
    line_index: &LineIndex,
) -> Route {
    let line = name_node.map_or(1, |node| {
        line_at(line_index, node.range_trimmed_trivia().start())
    });
    let defaults = compiled_array_element(Some(config), 1);
    Route {
        name: name.to_owned(),
        path: compiled_route_path(compiled_array_element(Some(config), 3).as_ref()),
        controller: normalize_php_route_controller(&compiled_array_string_value(
            defaults.as_ref(),
            "_controller",
        )),
        file_path: file_path.to_owned(),
        line,
        ..Route::default()
    }
}

fn compiled_route_path(tokens: Option<&SyntaxNode>) -> String {
    let Some(tokens) = direct_compiled_array(tokens) else {
        return String::new();
    };
    let mut result = String::new();
    for item in phpquery::array_items(&tokens).iter().rev() {
        let token = direct_compiled_array(phpquery::array_item_value(item).as_ref());
        match compiled_array_string_at(token.as_ref(), 0).as_str() {
            "text" => result.push_str(&compiled_array_string_at(token.as_ref(), 1)),
            "variable" => {
                let name = compiled_array_string_at(token.as_ref(), 3);
                if name.is_empty() {
                    continue;
                }
                result.push_str(&compiled_array_string_at(token.as_ref(), 1));
                result.push('{');
                result.push_str(&name);
                result.push('}');
            }
            _ => {}
        }
    }
    result
}

fn compiled_array_string_value(array: Option<&SyntaxNode>, key: &str) -> String {
    let Some(array) = direct_compiled_array(array) else {
        return String::new();
    };
    for item in phpquery::array_items(&array) {
        if compiled_php_string(phpquery::array_item_key(&item).as_ref()) != key {
            continue;
        }
        return compiled_php_string(phpquery::array_item_value(&item).as_ref());
    }
    String::new()
}

fn compiled_array_string_at(array: Option<&SyntaxNode>, index: i64) -> String {
    compiled_php_string(compiled_array_element(array, index).as_ref())
}

fn compiled_array_element(array: Option<&SyntaxNode>, wanted: i64) -> Option<SyntaxNode> {
    let array = direct_compiled_array(array)?;
    if wanted < 0 {
        return None;
    }
    let mut position: i64 = 0;
    for item in phpquery::array_items(&array) {
        let mut item_index = position;
        if let Some(key) = phpquery::array_item_key(&item) {
            match go_atoi(key.text().to_string().trim()) {
                Some(parsed) => item_index = parsed,
                None => {
                    position += 1;
                    continue;
                }
            }
        }
        if item_index == wanted {
            return phpquery::array_item_value(&item);
        }
        position = item_index + 1;
    }
    None
}

/// Go `strconv.Atoi`: optional sign followed by ASCII digits.
fn go_atoi(value: &str) -> Option<i64> {
    let digits = value.strip_prefix(['+', '-']).unwrap_or(value);
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    value.parse().ok()
}

fn direct_compiled_array(node: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    let node = node?;
    if node.kind() == PHP_ARRAY {
        return Some(node.clone());
    }
    node.children().find(|child| child.kind() == PHP_ARRAY)
}

fn compiled_php_string(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node else {
        return String::new();
    };
    phpquery::string_value(node)
        .replace("\\\\", "\\")
        .replace("\\'", "'")
}

#[cfg(test)]
#[path = "tests_routes/compiled_routes.rs"]
mod tests;
