//! Literal registration symbols (port of Go `usage_symbol_registration.go`).

use crate::*;

/// Declarations recognized only after semantic references were checked by
/// [`javascript_symbol_at`].
pub(crate) fn javascript_literal_symbol_at(literal: &SyntaxNode) -> Option<AdminSymbolTarget> {
    let call = jsquery::call_at(literal);
    let argument = jsquery::string_argument_index(literal);
    let kind = javascript_registration_argument_kind(
        &jsquery::call_name(literal).unwrap_or_default(),
        argument,
    );
    if !kind.is_unset() {
        return string_target(kind, literal);
    }
    if let Some(call) = &call
        && argument == Some(0)
    {
        let method = jsquery::call_method_name(call).unwrap_or_default();
        if method == "addServiceProvider"
            || (method == "register" && call.text().to_string().contains("Service()"))
        {
            return string_target(AdminSymbolKind::Service, literal);
        }
    }
    javascript_registration_property_symbol(literal, call.as_ref())
}

/// `Component.extend` owns both the new name and its parent argument; all
/// other registrations own only argument zero.
pub(crate) fn javascript_registration_argument_kind(
    name: &str,
    argument: Option<usize>,
) -> AdminSymbolKind {
    if name == "Component.extend" || name == "Shopware.Component.extend" {
        return if matches!(argument, Some(0 | 1)) {
            AdminSymbolKind::Component
        } else {
            AdminSymbolKind::Unset
        };
    }
    if argument != Some(0) {
        return AdminSymbolKind::Unset;
    }
    match name {
        "Component.register"
        | "Shopware.Component.register"
        | "Component.override"
        | "Shopware.Component.override" => AdminSymbolKind::Component,
        "Mixin.register" | "Shopware.Mixin.register" => AdminSymbolKind::Mixin,
        "Directive.register" | "Shopware.Directive.register" => AdminSymbolKind::Directive,
        "Filter.register" | "Shopware.Filter.register" => AdminSymbolKind::Filter,
        "Module.register" | "Shopware.Module.register" => AdminSymbolKind::Module,
        "Store.register" | "Shopware.Store.register" => AdminSymbolKind::Store,
        _ => AdminSymbolKind::Unset,
    }
}

pub(crate) fn javascript_registration_property_symbol(
    literal: &SyntaxNode,
    call: Option<&SyntaxNode>,
) -> Option<AdminSymbolTarget> {
    let call = call?;
    let name = jsquery::call_name(call).unwrap_or_default();
    let property_name =
        jsquery::property_at(literal).and_then(|property| jsquery::property_name(&property));
    match property_name.as_deref() {
        Some("id") if name == "Shopware.Store.register" || name == "Store.register" => {
            string_target(AdminSymbolKind::Store, literal)
        }
        Some("component") if name == "Module.register" || name == "Shopware.Module.register" => {
            string_target(AdminSymbolKind::Component, literal)
        }
        _ => None,
    }
}
