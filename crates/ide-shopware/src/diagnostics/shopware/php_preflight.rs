//! Recurring Store review findings that one file proves: each rule
//! matches a pattern with no legitimate reading, and leaves what needs
//! context (ownership, payments, cleanup) to tests and reviewers.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::SymbolKind;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::php_local::{normalized_local_function_name, php_boolean_literal};
use super::php_semantic::{
    Run, SHOPWARE_PHP_DEBUG_OUTPUT_CODE, SHOPWARE_PHP_EMPTY_CATCH_CODE,
    SHOPWARE_PHP_GET_MUTATION_CODE, SHOPWARE_PHP_GETCWD_CODE, SHOPWARE_PHP_MESSAGE_PAYLOAD_CODE,
    SHOPWARE_PHP_ROUTE_ACL_CODE, SHOPWARE_PHP_STATE_NAME_CODE,
    SHOPWARE_PHP_SYSTEM_CONFIG_CONSTRUCTOR_CODE, SHOPWARE_PHP_UNBOUNDED_SEARCH_CODE,
};
use super::util::equal_php_name;
use crate::diagnostics::common::{
    call_target_name, local_call_argument, short_php_name, unwrap_php_parentheses,
};

const ENTITY_REPOSITORY_CLASS: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository";
const SALES_CHANNEL_REPOSITORY_CLASS: &str =
    "Shopware\\Core\\System\\SalesChannel\\Entity\\SalesChannelRepository";
const CRITERIA_CLASS: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\Criteria";
const SYSTEM_CONFIG_SERVICE_CLASS: &str =
    "Shopware\\Core\\System\\SystemConfig\\SystemConfigService";
const STATE_ENTITY_CLASS: &str =
    "Shopware\\Core\\System\\StateMachine\\Aggregation\\StateMachineState\\StateMachineStateEntity";
const ASYNC_MESSAGE_INTERFACE: &str =
    "Shopware\\Core\\Framework\\MessageQueue\\AsyncMessageInterface";
const LOW_PRIORITY_MESSAGE_INTERFACE: &str =
    "Shopware\\Core\\Framework\\MessageQueue\\LowPriorityMessageInterface";
/// What a queue message should name by ID instead of carrying.
const MESSAGE_PAYLOAD_CLASSES: [&str; 3] = [
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCollection",
    "Shopware\\Core\\System\\SalesChannel\\SalesChannelContext",
];
const ROUTE_CLASSES: [&str; 2] = [
    "Symfony\\Component\\Routing\\Attribute\\Route",
    "Symfony\\Component\\Routing\\Annotation\\Route",
];

const SYSTEM_CONFIG_READERS: [&str; 7] = [
    "get",
    "getstring",
    "getint",
    "getfloat",
    "getbool",
    "getdomain",
    "all",
];
const REPOSITORY_WRITERS: [&str; 3] = ["update", "upsert", "delete"];
/// Functions whose only purpose is output for the developer.
const DEBUG_FUNCTIONS: [&str; 5] = [
    "debug_print_backtrace",
    "debug_zval_dump",
    "phpinfo",
    "xdebug_break",
    "xdebug_var_dump",
];
/// Functions that print unless told to return.
const PRINTING_FUNCTIONS: [&str; 2] = ["print_r", "var_export"];

impl Run<'_> {
    /// `$repository->search(new Criteria(), $context)`: every row of the
    /// table, as entities.
    pub(super) fn check_unbounded_search(&mut self, call: &SyntaxNode) {
        if !eq_ignore_case(&phpquery::call_method_name(call), "search") {
            return;
        }
        let Some(criteria) =
            local_call_argument(call, "criteria", 0).map(|value| unwrap_php_parentheses(&value))
        else {
            return;
        };
        let unrestricted = match criteria.kind() {
            PHP_OBJECT_CREATION => self.is_fresh_criteria(&criteria),
            PHP_VARIABLE => self.is_untouched_criteria(&criteria),
            _ => false,
        };
        if !unrestricted {
            return;
        }
        let Some(receiver) = phpquery::call_receiver(call) else {
            return;
        };
        if !self.is_instance(&receiver, ENTITY_REPOSITORY_CLASS)
            && !self.is_instance(&receiver, SALES_CHANNEL_REPOSITORY_CLASS)
        {
            return;
        }
        self.add(
            SHOPWARE_PHP_UNBOUNDED_SEARCH_CODE,
            name_range(call),
            "search() with an unrestricted Criteria loads every row as an entity; add IDs, a \
             filter or a limit, or page through the result"
                .to_owned(),
        );
    }

    /// `new Criteria()` without IDs.
    fn is_fresh_criteria(&self, creation: &SyntaxNode) -> bool {
        phpquery::arguments(creation).next().is_none()
            && self.names_subtype(
                phpquery::direct_child(creation, PHP_NAME).as_ref(),
                CRITERIA_CLASS,
            )
    }

    /// A variable assigned `new Criteria()` whose only other use in the
    /// function is this one: nothing restricted it in between.
    fn is_untouched_criteria(&self, variable: &SyntaxNode) -> bool {
        let Some(function) = phpquery::function_like_at(variable) else {
            return false;
        };
        let name = phpquery::variable_name(variable);
        let uses: Vec<SyntaxNode> = phpquery::nodes(&function, &[PHP_VARIABLE])
            .into_iter()
            .filter(|other| phpquery::variable_name(other) == name)
            .collect();
        let [assigned, used] = uses.as_slice() else {
            return false;
        };
        used == variable
            && assigned.parent().is_some_and(|assignment| {
                assignment.kind() == PHP_ASSIGNMENT_EXPRESSION
                    && assignment.children().next().as_ref() == Some(assigned)
                    && assignment
                        .children()
                        .last()
                        .map(|value| unwrap_php_parentheses(&value))
                        .is_some_and(|value| {
                            value.kind() == PHP_OBJECT_CREATION && self.is_fresh_criteria(&value)
                        })
            })
    }

    /// A system config read while the service is constructed.
    pub(super) fn check_system_config_in_constructor(&mut self, call: &SyntaxNode) {
        let method = phpquery::call_method_name(call);
        if !SYSTEM_CONFIG_READERS.contains(&lowercase(&method).as_str())
            || !phpquery::method_at(call).is_some_and(|method| {
                eq_ignore_case(&phpquery::method_name(&method), "__construct")
            })
        {
            return;
        }
        if !phpquery::call_receiver(call)
            .is_some_and(|receiver| self.is_instance(&receiver, SYSTEM_CONFIG_SERVICE_CLASS))
        {
            return;
        }
        self.add(
            SHOPWARE_PHP_SYSTEM_CONFIG_CONSTRUCTOR_CODE,
            name_range(call),
            format!(
                "SystemConfigService::{method}() in a constructor runs whenever the service is \
                 built and fixes one value for every sales channel; read it where it is used"
            ),
        );
    }

    /// `$state->getName() === 'Open'`: the name is translated.
    pub(super) fn check_state_name_comparison(&mut self, call: &SyntaxNode) {
        if !eq_ignore_case(&phpquery::call_method_name(call), "getName") {
            return;
        }
        let mut value = call.clone();
        while let Some(parent) = value
            .parent()
            .filter(|parent| parent.kind() == PHP_PARENTHESIZED)
        {
            value = parent;
        }
        let Some(comparison) = value.parent().filter(|parent| {
            parent.kind() == PHP_BINARY_EXPRESSION
                && ["==", "===", "!=", "!=="]
                    .contains(&phpquery::expression_operator(parent).as_str())
        }) else {
            return;
        };
        let literal = comparison
            .children()
            .filter(|operand| *operand != value)
            .any(|operand| unwrap_php_parentheses(&operand).kind() == PHP_STRING);
        if !literal
            || !phpquery::call_receiver(call)
                .is_some_and(|receiver| self.is_instance(&receiver, STATE_ENTITY_CLASS))
        {
            return;
        }
        self.add(
            SHOPWARE_PHP_STATE_NAME_CODE,
            name_range(call),
            "The name of a state is translated and can be renamed; compare getTechnicalName()"
                .to_owned(),
        );
    }

    /// The class-level rules: queue messages and routes.
    pub(super) fn check_preflight_class(&mut self, class: &SyntaxNode) {
        self.check_message_payload(class);
        self.check_routes(class);
    }

    /// An asynchronous message carrying entities or a sales channel
    /// context instead of their IDs.
    fn check_message_payload(&mut self, class: &SyntaxNode) {
        let Some(symbol) = self.class_symbol(Some(class)) else {
            return;
        };
        let name = symbol.fully_qualified.trim_matches('\\');
        if !self.snapshot.is_subtype_of(name, ASYNC_MESSAGE_INTERFACE)
            && !self
                .snapshot
                .is_subtype_of(name, LOW_PRIORITY_MESSAGE_INTERFACE)
        {
            return;
        }
        let constructor_parameters = phpquery::methods(class)
            .into_iter()
            .filter(|method| eq_ignore_case(&phpquery::method_name(method), "__construct"))
            .flat_map(|method| phpquery::parameters(&method));
        let types = phpquery::properties(class)
            .into_iter()
            .chain(constructor_parameters)
            .flat_map(|declaration| {
                declaration
                    .children()
                    .filter(|child| {
                        matches!(
                            child.kind(),
                            PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
                        )
                    })
                    .collect::<Vec<_>>()
            });
        for ty in types {
            for name in phpquery::nodes(&ty, &[PHP_NAME]) {
                if !MESSAGE_PAYLOAD_CLASSES
                    .iter()
                    .any(|payload| self.names_subtype(Some(&name), payload))
                {
                    continue;
                }
                self.add(
                    SHOPWARE_PHP_MESSAGE_PAYLOAD_CODE,
                    name.range_trimmed_trivia(),
                    format!(
                        "A queue message is serialized and handled later: carry the ID instead \
                         of {}, which may be stale or too large by then",
                        short_php_name(&phpquery::name_value(&name))
                    ),
                );
            }
        }
    }

    fn check_routes(&mut self, class: &SyntaxNode) {
        let class_routes: Vec<SyntaxNode> = class
            .children()
            .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
            .flat_map(|group| phpquery::nodes(&group, &[PHP_ATTRIBUTE]))
            .filter(|attribute| self.is_route(attribute))
            .collect();
        for method in phpquery::methods(class) {
            let routes: Vec<SyntaxNode> = phpquery::attributes(&method)
                .into_iter()
                .filter(|attribute| self.is_route(attribute))
                .collect();
            if routes.is_empty() {
                continue;
            }
            let verbs: Vec<String> = routes
                .iter()
                .flat_map(|route| self.route_methods(route))
                .collect();
            if !verbs.is_empty() && verbs.iter().all(|verb| verb == "GET" || verb == "HEAD") {
                self.check_get_mutations(&method);
            }
            // An Admin API route that changes something and names no
            // privilege is open to every authenticated user, unless what it
            // does is checked further down. `'_acl' => []` states that it
            // needs none.
            let writes = verbs
                .iter()
                .any(|verb| ["POST", "PUT", "PATCH", "DELETE"].contains(&verb.as_str()));
            let all: Vec<&SyntaxNode> = class_routes.iter().chain(&routes).collect();
            if writes
                && all
                    .iter()
                    .any(|route| self.route_default(route, "_routeScope", "api"))
                && !all
                    .iter()
                    .any(|route| self.route_default_value(route, "_acl").is_some())
            {
                self.add(
                    SHOPWARE_PHP_ROUTE_ACL_CODE,
                    phpquery::direct_child(&method, PHP_NAME).map_or_else(
                        || method.range_trimmed_trivia(),
                        |name| name.range_trimmed_trivia(),
                    ),
                    "This Admin API route accepts writes from any authenticated user; name the \
                     privileges it needs in the _acl route default, or set it to [] when it \
                     needs none"
                        .to_owned(),
                );
            }
        }
    }

    /// The value of the route default `key`; `Some(None)` for a key
    /// without one.
    #[allow(clippy::option_option)]
    fn route_default_value(&self, route: &SyntaxNode, key: &str) -> Option<Option<SyntaxNode>> {
        let defaults = unwrap_php_parentheses(&local_call_argument(route, "defaults", 4)?);
        if defaults.kind() != PHP_ARRAY {
            return None;
        }
        phpquery::array_items(&defaults)
            .into_iter()
            .find_map(|item| {
                let item_key = phpquery::array_item_key(&item)?;
                self.expression_strings(Some(&item_key))
                    .iter()
                    .any(|name| name == key)
                    .then(|| phpquery::array_item_value(&item))
            })
    }

    /// Whether the route default `key` is, or lists, `expected`.
    fn route_default(&self, route: &SyntaxNode, key: &str, expected: &str) -> bool {
        let Some(Some(value)) = self.route_default_value(route, key) else {
            return false;
        };
        let value = unwrap_php_parentheses(&value);
        let values: Vec<SyntaxNode> = if value.kind() == PHP_ARRAY {
            phpquery::array_items(&value)
                .iter()
                .filter_map(phpquery::array_item_value)
                .collect()
        } else {
            vec![value]
        };
        values.iter().any(|value| {
            self.expression_strings(Some(value))
                .iter()
                .any(|scope| scope == expected)
        })
    }

    /// Repository writes in a route that only answers safe requests.
    fn check_get_mutations(&mut self, method: &SyntaxNode) {
        for call in phpquery::nodes(method, &[PHP_MEMBER_CALL]) {
            let name = phpquery::call_method_name(&call);
            if !REPOSITORY_WRITERS.contains(&lowercase(&name).as_str()) {
                continue;
            }
            if !phpquery::call_receiver(&call)
                .is_some_and(|receiver| self.is_instance(&receiver, ENTITY_REPOSITORY_CLASS))
            {
                continue;
            }
            self.add(
                SHOPWARE_PHP_GET_MUTATION_CODE,
                name_range(&call),
                format!(
                    "This GET route calls {name}(); crawlers, prefetchers and mail scanners \
                     follow links, so change data in a POST, PATCH or DELETE route"
                ),
            );
        }
    }

    fn is_route(&self, attribute: &SyntaxNode) -> bool {
        let name = phpquery::direct_child(attribute, PHP_NAME);
        let resolved = self.resolved_symbols(name.as_ref(), SymbolKind::Class);
        if resolved.is_empty() {
            // Symfony may not be indexed.
            return short_php_name(&phpquery::attribute_name(attribute)) == "Route";
        }
        resolved.iter().any(|class| {
            ROUTE_CLASSES
                .iter()
                .any(|route| equal_php_name(&class.fully_qualified, route))
        })
    }

    /// The HTTP methods a route attribute names, in upper case.
    fn route_methods(&self, route: &SyntaxNode) -> Vec<String> {
        let Some(methods) =
            local_call_argument(route, "methods", 6).map(|value| unwrap_php_parentheses(&value))
        else {
            return Vec::new();
        };
        let values: Vec<SyntaxNode> = if methods.kind() == PHP_ARRAY {
            phpquery::array_items(&methods)
                .iter()
                .filter_map(phpquery::array_item_value)
                .collect()
        } else {
            vec![methods]
        };
        values
            .iter()
            .flat_map(|value| self.expression_strings(Some(value)))
            .map(|verb| swlsp_textutil::uppercase(&verb))
            .collect()
    }

    /// `getcwd()` and functions that only print for the developer.
    pub(super) fn check_preflight_function(&mut self, call: &SyntaxNode) {
        let name = normalized_local_function_name(call);
        if name == "getcwd" {
            self.add(
                SHOPWARE_PHP_GETCWD_CODE,
                name_range(call),
                "getcwd() differs between web requests, the console and workers; build paths \
                 from the kernel.project_dir parameter"
                    .to_owned(),
            );
            return;
        }
        let debug = DEBUG_FUNCTIONS.contains(&name.as_str())
            || PRINTING_FUNCTIONS.contains(&name.as_str())
                // With `return: true` they format instead of printing.
                && local_call_argument(call, "return", 1)
                    .is_none_or(|flag| php_boolean_literal(Some(&flag)) == Some(false));
        if debug {
            self.add(
                SHOPWARE_PHP_DEBUG_OUTPUT_CODE,
                name_range(call),
                format!("{name}() prints debug output into the response; remove it or log instead"),
            );
        }
    }

    /// `catch (\Throwable) {}` without even a comment.
    pub(super) fn check_empty_catch(&mut self, clause: &SyntaxNode) {
        let Some(block) = phpquery::direct_child(clause, PHP_BLOCK) else {
            return;
        };
        let text = block.text().to_string();
        let body = text
            .trim()
            .trim_start_matches('{')
            .trim_end_matches('}')
            .trim();
        if !body.is_empty() || block.children().next().is_some() {
            return;
        }
        // Ignoring one expected exception is a decision; ignoring every
        // failure is not.
        let broad = phpquery::nodes(clause, &[PHP_NAME])
            .iter()
            .filter(|name| !block.text_range().contains_range(name.text_range()))
            .any(|name| {
                let name = phpquery::name_value(name);
                ["throwable", "exception"]
                    .contains(&lowercase(name.trim_start_matches('\\')).as_str())
            });
        if !broad {
            return;
        }
        self.add(
            SHOPWARE_PHP_EMPTY_CATCH_CODE,
            block.range_trimmed_trivia(),
            "This empty catch block hides every failure; handle it, log it, or say in a comment \
             why it can be ignored"
                .to_owned(),
        );
    }
}

fn name_range(call: &SyntaxNode) -> swlsp_syntax::TextRange {
    call_target_name(call).map_or_else(
        || call.range_trimmed_trivia(),
        |name| name.range_trimmed_trivia(),
    )
}
