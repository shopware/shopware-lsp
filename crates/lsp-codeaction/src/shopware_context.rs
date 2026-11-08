//! Go `shopware_context_codeaction.go`.

use crate::shared::document_snapshot;
use std::sync::LazyLock;

use regex::Regex;
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{JS_ARGUMENT, JS_ARGUMENT_LIST, JS_METHOD, JS_OBJECT, JS_PROPERTY};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_CLASS_DECLARATION;
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNode;

use crate::shared::php_class_fully_qualified_name;
use crate::snippet::command_action;

pub const EXTEND_ADMIN_COMPONENT_ACTION: &str = "shopware.admin.extendComponent";
pub const OVERRIDE_ADMIN_METHOD_ACTION: &str = "shopware.admin.overrideMethod";
pub const CREATE_EVENT_LISTENER_ACTION: &str = "shopware.createEventListener";

static JS_PARAMETER_NAME_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^[\t\n\f\r ]*(?:\.\.\.)?([A-Za-z_$][A-Za-z0-9_$]*)(?:[\t\n\f\r ]*=.*)?$").unwrap()
});

/// Administration component extension and method override intents.
#[derive(Default)]
pub struct AdminContextProvider;

impl AdminContextProvider {
    pub fn new() -> Self {
        Self
    }
}

impl ActionProvider for AdminContextProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::JavaScript) {
            return Vec::new();
        }
        let component = admin_component_reference(node);
        if !component.is_empty() {
            return vec![command_action(
                &format!("Shopware: Extend or override component '{component}'"),
                CodeActionKind::REFACTOR_REWRITE,
                "Extend or override Administration component",
                EXTEND_ADMIN_COMPONENT_ACTION,
                vec![Value::from(component), Value::from(document.uri.clone())],
            )];
        }
        let Some(method) = node.ancestors().find(|current| current.kind() == JS_METHOD) else {
            return Vec::new();
        };
        let Some(call) = jsquery::call_at(&method) else {
            return Vec::new();
        };
        match jsquery::call_name(&call).as_deref() {
            Some(
                "Component.register"
                | "Shopware.Component.register"
                | "Component.extend"
                | "Shopware.Component.extend",
            ) => {}
            _ => return Vec::new(),
        }
        let component = jsquery::string_argument(&call, 0)
            .map(|node| jsquery::string_value(&node))
            .unwrap_or_default();
        let method_name = jsquery::method_name(&method).unwrap_or_default();
        if component.is_empty() || method_name.is_empty() {
            return Vec::new();
        }
        let Some(parameters) = admin_method_parameters(&method) else {
            return Vec::new();
        };
        vec![command_action(
            &format!("Shopware: Override method '{method_name}'"),
            CodeActionKind::REFACTOR_REWRITE,
            "Override Administration component method",
            OVERRIDE_ADMIN_METHOD_ACTION,
            vec![
                Value::from(component),
                Value::from(method_name),
                Value::from(admin_method_group(&method, &call)),
                Value::from(parameters.join(",")),
                Value::from(document.uri.clone()),
            ],
        )]
    }
}

fn admin_component_reference(node: &SyntaxNode) -> String {
    let Some(string) = jsquery::string_at(node) else {
        return String::new();
    };
    let index = jsquery::string_argument_index(&string);
    let name = jsquery::call_at(&string).and_then(|call| jsquery::call_name(&call));
    let wanted = match name.as_deref() {
        Some("Component.register" | "Shopware.Component.register") => 0,
        Some("Component.extend" | "Shopware.Component.extend") => 1,
        Some("Component.override" | "Shopware.Component.override") => 0,
        _ => return String::new(),
    };
    if index == Some(wanted) {
        jsquery::string_value(&string)
    } else {
        String::new()
    }
}

fn admin_method_parameters(method: &SyntaxNode) -> Option<Vec<String>> {
    let Some(list) = method
        .children()
        .find(|child| child.kind() == JS_ARGUMENT_LIST)
    else {
        return Some(Vec::new());
    };
    let mut result = Vec::new();
    for argument in list.children().filter(|child| child.kind() == JS_ARGUMENT) {
        let text = argument.text().to_string();
        let captures = JS_PARAMETER_NAME_PATTERN.captures(&text)?;
        result.push(captures.get(1)?.as_str().to_owned());
    }
    Some(result)
}

fn admin_method_group(method: &SyntaxNode, call: &SyntaxNode) -> String {
    let Some(object) = method.parent().filter(|parent| parent.kind() == JS_OBJECT) else {
        return String::new();
    };
    let count = jsquery::iter_arguments(call).count();
    let registration = count
        .checked_sub(1)
        .and_then(|index| jsquery::object_argument(call, index));
    if registration.as_ref() == Some(&object) {
        return String::new();
    }
    if let Some(property) = object.parent()
        && property.kind() == JS_PROPERTY
        && jsquery::property_value(&property).as_ref() == Some(&object)
    {
        return jsquery::property_name(&property).unwrap_or_default();
    }
    String::new()
}

/// Offers an `AsEventListener` scaffold on event class declarations.
pub struct EventListenerContextProvider {
    php_index: Option<PhpIndex>,
}

impl EventListenerContextProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ActionProvider for EventListenerContextProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(index), Some(document), Some(root), Some(node)) = (
            self.php_index.as_ref(),
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        if class.kind() != PHP_CLASS_DECLARATION {
            return Vec::new();
        }
        let class_name = php_class_fully_qualified_name(root, &class);
        if class_name.is_empty() {
            return Vec::new();
        }
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone());
        let snapshot = document_snapshot(index, document, &path, root);
        let is_event = [
            r"Symfony\Contracts\EventDispatcher\Event",
            r"Symfony\Component\EventDispatcher\Event",
        ]
        .iter()
        .any(|base| class_name != *base && snapshot.is_subtype_of(&class_name, base));
        if !is_event {
            return Vec::new();
        }
        let short = phpquery::class_name(&class);
        vec![command_action(
            &format!("Shopware: Create listener for {short}"),
            CodeActionKind::REFACTOR_REWRITE,
            "Create Shopware event listener",
            CREATE_EVENT_LISTENER_ACTION,
            vec![
                Value::from(class_name),
                Value::from(format!("{short}Listener")),
                Value::from(document.uri.clone()),
            ],
        )]
    }
}

#[cfg(test)]
#[path = "tests/shopware_context.rs"]
mod tests;
