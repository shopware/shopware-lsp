//! Shopware conventions: contexts are passed down, system config is read for
//! the current sales channel, and listeners for one route use its route
//! event.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::SymbolKind;
use swlsp_php_types::Kind as TypeKind;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::php_semantic::{
    Run, SHOPWARE_PHP_DEFAULT_CONTEXT_CODE, SHOPWARE_PHP_ROUTE_LISTENER_CODE,
    SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE,
};
use super::util::{equal_php_name, trim_php_name};
use crate::diagnostics::common::{
    call_target_name, local_call_argument, short_php_name, unwrap_php_parentheses,
};

const CONTEXT_CLASS: &str = "Shopware\\Core\\Framework\\Context";
const SYSTEM_CONFIG_SERVICE_CLASS: &str =
    "Shopware\\Core\\System\\SystemConfig\\SystemConfigService";
const SALES_CHANNEL_CONTEXT_CLASS: &str =
    "Shopware\\Core\\System\\SalesChannel\\SalesChannelContext";
const EVENT_LISTENER_ATTRIBUTE: &str =
    "Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener";

const SYSTEM_CONFIG_READERS: [&str; 5] = ["get", "getstring", "getint", "getfloat", "getbool"];

/// The kernel events Shopware also dispatches per route, with the event
/// class Symfony accepts in place of the name.
const ROUTE_EVENTS: [(&str, &str); 3] = [
    (
        "kernel.request",
        "Symfony\\Component\\HttpKernel\\Event\\RequestEvent",
    ),
    (
        "kernel.response",
        "Symfony\\Component\\HttpKernel\\Event\\ResponseEvent",
    ),
    (
        "kernel.controller",
        "Symfony\\Component\\HttpKernel\\Event\\ControllerEvent",
    ),
];

impl Run<'_> {
    /// `Context::createDefaultContext()`, once Shopware offers
    /// `createCLIContext()` for the command line.
    pub(super) fn check_default_context(&mut self, call: &SyntaxNode) {
        if !eq_ignore_case(&phpquery::call_method_name(call), "createDefaultContext") {
            return;
        }
        let class = phpquery::direct_child(call, PHP_NAME);
        let has_cli_context = self
            .resolved_symbols(class.as_ref(), SymbolKind::Class)
            .iter()
            .any(|class| {
                equal_php_name(&class.fully_qualified, CONTEXT_CLASS)
                    && !self
                        .snapshot
                        .members(class.id.as_str(), "createCLIContext")
                        .is_empty()
            });
        if !has_cli_context {
            return;
        }
        let range = call_target_name(call).map_or_else(
            || call.range_trimmed_trivia(),
            |name| name.range_trimmed_trivia(),
        );
        self.add(
            SHOPWARE_PHP_DEFAULT_CONTEXT_CODE,
            range,
            "Do not create a default context: pass the context down from the controller, or use \
             Context::createCLIContext() on the command line"
                .to_owned(),
        );
    }

    /// A system config read that ignores the sales channel context the
    /// surrounding function received.
    pub(super) fn check_system_config_call(&mut self, call: &SyntaxNode) {
        let method = phpquery::call_method_name(call);
        if !SYSTEM_CONFIG_READERS.contains(&lowercase(&method).as_str()) {
            return;
        }
        let Some(receiver) = phpquery::call_receiver(call) else {
            return;
        };
        if !self.is_instance(&receiver, SYSTEM_CONFIG_SERVICE_CLASS)
            || !self.receives_sales_channel_context(call)
        {
            return;
        }
        let Some(sales_channel) = local_call_argument(call, "salesChannelId", 1) else {
            let range = call_target_name(call).map_or_else(
                || call.range_trimmed_trivia(),
                |name| name.range_trimmed_trivia(),
            );
            self.add(
                SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE,
                range,
                format!(
                    "SystemConfigService::{method}() reads the global value; pass the sales \
                     channel ID of the SalesChannelContext"
                ),
            );
            return;
        };
        let sales_channel = unwrap_php_parentheses(&sales_channel);
        if self.is_sales_channel_id(&sales_channel) {
            return;
        }
        self.add(
            SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE,
            sales_channel.range_trimmed_trivia(),
            format!(
                "SystemConfigService::{method}() should receive the sales channel ID of the \
                 SalesChannelContext"
            ),
        );
    }

    /// Whether a function around `node` has a `SalesChannelContext`
    /// parameter.
    fn receives_sales_channel_context(&self, node: &SyntaxNode) -> bool {
        node.ancestors()
            .filter(|ancestor| {
                matches!(
                    ancestor.kind(),
                    PHP_METHOD_DECLARATION
                        | PHP_FUNCTION_DECLARATION
                        | PHP_CLOSURE
                        | PHP_ARROW_FUNCTION
                )
            })
            .flat_map(|function| phpquery::parameters(&function))
            .filter_map(|parameter| phpquery::direct_child(&parameter, PHP_TYPE))
            .flat_map(|ty| phpquery::nodes(&ty, &[PHP_NAME]))
            .any(|name| self.names_subtype(Some(&name), SALES_CHANNEL_CONTEXT_CLASS))
    }

    /// `$context->getSalesChannelId()`, `$context->getSalesChannel()->getId()`
    /// or a string variable holding one of them.
    fn is_sales_channel_id(&self, expression: &SyntaxNode) -> bool {
        match expression.kind() {
            PHP_VARIABLE => matches!(
                self.semantic.type_of(expression).ty.kind(),
                TypeKind::String | TypeKind::LiteralString
            ),
            PHP_MEMBER_CALL => {
                let method = lowercase(&phpquery::call_method_name(expression));
                let Some(receiver) = phpquery::call_receiver(expression) else {
                    return false;
                };
                match method.as_str() {
                    "getsaleschannelid" => self.is_instance(&receiver, SALES_CHANNEL_CONTEXT_CLASS),
                    "getid" => {
                        receiver.kind() == PHP_MEMBER_CALL
                            && eq_ignore_case(
                                &phpquery::call_method_name(&receiver),
                                "getSalesChannel",
                            )
                            && phpquery::call_receiver(&receiver).is_some_and(|context| {
                                self.is_instance(&context, SALES_CHANNEL_CONTEXT_CLASS)
                            })
                    }
                    _ => false,
                }
            }
            _ => false,
        }
    }

    /// Listeners of a kernel event that return unless the request is for
    /// one route: Shopware dispatches that event per route.
    pub(super) fn check_route_listeners(&mut self, class: &SyntaxNode) {
        let methods = phpquery::methods(class);
        // (method, event) pairs, the method in lower case.
        let mut listeners: Vec<(String, &'static str)> = Vec::new();
        for attribute in class_attributes(class) {
            if !self.is_event_listener_attribute(&attribute) {
                continue;
            }
            let Some(event) = self.route_event(local_call_argument(&attribute, "event", 0)) else {
                continue;
            };
            let method = local_call_argument(&attribute, "method", 1)
                .and_then(|method| self.expression_strings(Some(&method)).into_iter().next())
                // Symfony's default: `onKernelRequest` for `kernel.request`.
                .unwrap_or_else(|| format!("on{}", event.replace('.', "")));
            listeners.push((lowercase(&method), event));
        }
        for method in &methods {
            let name = phpquery::method_name(method);
            for attribute in phpquery::attributes(method) {
                if !self.is_event_listener_attribute(&attribute) {
                    continue;
                }
                let event = self
                    .route_event(local_call_argument(&attribute, "event", 0))
                    .or_else(|| self.parameter_event(method));
                if let Some(event) = event {
                    listeners.push((lowercase(&name), event));
                }
            }
            if eq_ignore_case(&name, "getSubscribedEvents") {
                self.subscribed_events(method, &mut listeners);
            }
        }
        if listeners.is_empty() {
            return;
        }
        for method in &methods {
            let name = lowercase(&phpquery::method_name(method));
            let Some((_, event)) = listeners.iter().find(|(listener, _)| *listener == name) else {
                continue;
            };
            let Some(route) = required_route(method) else {
                continue;
            };
            let range = phpquery::direct_child(method, PHP_NAME).map_or_else(
                || method.range_trimmed_trivia(),
                |name| name.range_trimmed_trivia(),
            );
            let suffix = event.trim_start_matches("kernel.");
            self.add(
                SHOPWARE_PHP_ROUTE_LISTENER_CODE,
                range,
                format!(
                    "Listen to {route}.{suffix} instead of {event}; the listener then only runs \
                     for that route"
                ),
            );
        }
    }

    fn is_event_listener_attribute(&self, attribute: &SyntaxNode) -> bool {
        let name = phpquery::direct_child(attribute, PHP_NAME);
        let resolved = self.resolved_symbols(name.as_ref(), SymbolKind::Class);
        if resolved.is_empty() {
            // Symfony may not be indexed.
            return short_php_name(&phpquery::attribute_name(attribute)) == "AsEventListener";
        }
        resolved
            .iter()
            .any(|class| equal_php_name(&class.fully_qualified, EVENT_LISTENER_ATTRIBUTE))
    }

    /// The kernel event `expression` names, by name or by event class.
    fn route_event(&self, expression: Option<SyntaxNode>) -> Option<&'static str> {
        let expression = unwrap_php_parentheses(&expression?);
        let mut names = self.expression_strings(Some(&expression));
        if expression.kind() == PHP_MEMBER_ACCESS
            && eq_ignore_case(
                expression
                    .children()
                    .last()
                    .map(|name| phpquery::name_value(&name))
                    .unwrap_or_default()
                    .as_str(),
                "class",
            )
        {
            let class = expression.children().next();
            names.extend(
                self.resolved_symbols(class.as_ref(), SymbolKind::Class)
                    .iter()
                    .map(|class| trim_php_name(&class.fully_qualified).to_owned()),
            );
        }
        ROUTE_EVENTS
            .iter()
            .find(|(event, class)| {
                names
                    .iter()
                    .any(|name| name == event || equal_php_name(name, class))
            })
            .map(|(event, _)| *event)
    }

    /// The kernel event a listener without an explicit one takes from its
    /// parameter type.
    fn parameter_event(&self, method: &SyntaxNode) -> Option<&'static str> {
        let parameter = phpquery::parameters(method).next()?;
        let ty = phpquery::direct_child(&parameter, PHP_TYPE)?;
        let name = phpquery::direct_child(&ty, PHP_NAME);
        let classes = self.resolved_symbols(name.as_ref(), SymbolKind::Class);
        ROUTE_EVENTS
            .iter()
            .find(|(_, class)| {
                classes
                    .iter()
                    .any(|candidate| equal_php_name(&candidate.fully_qualified, class))
            })
            .map(|(event, _)| *event)
    }

    /// The listeners of the array `getSubscribedEvents()` returns.
    fn subscribed_events(&self, method: &SyntaxNode, listeners: &mut Vec<(String, &'static str)>) {
        let Some(array) = phpquery::nodes(method, &[PHP_RETURN_STATEMENT])
            .first()
            .and_then(|statement| statement.children().next())
            .map(|value| unwrap_php_parentheses(&value))
            .filter(|value| value.kind() == PHP_ARRAY)
        else {
            return;
        };
        for item in phpquery::array_items(&array) {
            let (Some(event), Some(value)) = (
                self.route_event(phpquery::array_item_key(&item)),
                phpquery::array_item_value(&item),
            ) else {
                continue;
            };
            // 'method', ['method', priority] or [['method', priority], ...].
            let mut candidates = vec![unwrap_php_parentheses(&value)];
            if candidates[0].kind() == PHP_ARRAY {
                let items: Vec<SyntaxNode> = phpquery::array_items(&candidates[0])
                    .iter()
                    .filter_map(phpquery::array_item_value)
                    .map(|value| unwrap_php_parentheses(&value))
                    .collect();
                candidates = if items.first().is_some_and(|first| first.kind() == PHP_ARRAY) {
                    items
                        .iter()
                        .filter_map(|listener| phpquery::array_items(listener).into_iter().next())
                        .filter_map(|first| phpquery::array_item_value(&first))
                        .collect()
                } else {
                    items.into_iter().take(1).collect()
                };
            }
            for candidate in candidates {
                listeners.extend(
                    self.expression_strings(Some(&candidate))
                        .into_iter()
                        .map(|method| (lowercase(&method), event)),
                );
            }
        }
    }
}

/// The attributes of the class itself, not of its members.
fn class_attributes(class: &SyntaxNode) -> Vec<SyntaxNode> {
    class
        .children()
        .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
        .flat_map(|group| phpquery::nodes(&group, &[PHP_ATTRIBUTE]))
        .collect()
}

/// The route of `if ($request->attributes->get('_route') !== 'route')`.
fn required_route(method: &SyntaxNode) -> Option<String> {
    for statement in phpquery::nodes(method, &[PHP_IF_STATEMENT]) {
        let Some(condition) = statement
            .children()
            .find(|child| child.kind() == PHP_PARENTHESIZED)
            .map(|condition| unwrap_php_parentheses(&condition))
        else {
            continue;
        };
        if condition.kind() != PHP_BINARY_EXPRESSION
            || !["!==", "!="].contains(&phpquery::expression_operator(&condition).as_str())
        {
            continue;
        }
        let operands: Vec<SyntaxNode> = condition
            .children()
            .map(|operand| unwrap_php_parentheses(&operand))
            .collect();
        let [left, right] = operands.as_slice() else {
            continue;
        };
        for (getter, literal) in [(left, right), (right, left)] {
            if literal.kind() == PHP_STRING && is_route_getter(getter) {
                return Some(phpquery::string_value(literal));
            }
        }
    }
    None
}

/// `<request>->attributes->get('_route')`.
fn is_route_getter(call: &SyntaxNode) -> bool {
    if call.kind() != PHP_MEMBER_CALL
        || !eq_ignore_case(&phpquery::call_method_name(call), "get")
        || phpquery::arguments(call).count() != 1
        || phpquery::string_argument(call, 0)
            .is_none_or(|key| phpquery::string_value(&key) != "_route")
    {
        return false;
    }
    phpquery::call_receiver(call).is_some_and(|bag| {
        bag.kind() == PHP_MEMBER_ACCESS
            && bag
                .children()
                .last()
                .is_some_and(|name| phpquery::name_value(&name) == "attributes")
    })
}
