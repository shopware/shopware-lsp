//! Registry reference contexts in JavaScript.

use crate::*;

/// String positions which refer to an Administration service rather than
/// register one.
pub fn is_service_reference(node: &SyntaxNode) -> bool {
    if jsquery::string_in_call(
        node,
        0,
        &[
            "Shopware.Service",
            "Service",
            "Application.addServiceProviderMiddleware",
            "Shopware.Application.addServiceProviderMiddleware",
            "Application.addServiceProviderDecorator",
            "Shopware.Application.addServiceProviderDecorator",
        ],
    ) {
        return true;
    }
    if jsquery::string_at(node).is_none() {
        return false;
    }
    node.ancestors().any(|current| {
        current.kind() == jssyntax::JS_PROPERTY
            && jsquery::property_name(&current).as_deref() == Some("inject")
    })
}

pub fn is_store_reference(node: &SyntaxNode) -> bool {
    jsquery::string_in_call(
        node,
        0,
        &[
            "Shopware.Store.get",
            "Store.get",
            "Shopware.Store.unregister",
            "Store.unregister",
        ],
    )
}

pub fn is_privilege_reference(node: &SyntaxNode) -> bool {
    if jsquery::string_at(node).is_none() {
        return false;
    }
    if jsquery::string_argument_index(node) == Some(0) {
        let call_name = jsquery::call_name(node).unwrap_or_default();
        if call_name == "acl.can"
            || call_name.ends_with(".acl.can")
            || call_name.ends_with("Service('acl').can")
            || call_name.ends_with("Service(\"acl\").can")
            || call_name.ends_with("Service('privileges').getPrivileges")
            || call_name.ends_with("Service(\"privileges\").getPrivileges")
        {
            return true;
        }
    }
    for current in node.ancestors() {
        if current.kind() != jssyntax::JS_PROPERTY {
            continue;
        }
        match jsquery::property_name(&current).as_deref() {
            Some("privilege") => return true,
            Some("dependencies") => {
                let Some(call) = jsquery::call_at(&current) else {
                    return false;
                };
                return jsquery::call_method_name(&call).as_deref()
                    == Some("addPrivilegeMappingEntry")
                    && jsquery::call_name(&call)
                        .unwrap_or_default()
                        .ends_with(".addPrivilegeMappingEntry");
            }
            _ => {}
        }
    }
    false
}
