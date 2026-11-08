//! JavaScript symbol recognition (port of Go `usage_symbol_javascript.go`).

use crate::*;

fn target(kind: AdminSymbolKind, owner: &str, name: &str) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind,
        owner: owner.to_owned(),
        name: name.to_owned(),
    }
}

/// Registry-backed Administration symbols at a cursor.
pub fn javascript_symbol_at(node: &SyntaxNode) -> Option<AdminSymbolTarget> {
    if let Some((container_name, member_name)) = javascript_application_container_member(node)
        && container_name == "service"
        && !member_name.is_empty()
    {
        return Some(target(AdminSymbolKind::Service, "", &member_name));
    }
    if let Some(store_member) = jsquery::store_member(node)
        && !store_member.member.is_empty()
    {
        return Some(target(
            AdminSymbolKind::StoreMember,
            &store_member.store,
            &store_member.member,
        ));
    }
    if is_service_reference(node) {
        return string_target(AdminSymbolKind::Service, node);
    }
    if is_store_reference(node) {
        return string_target(AdminSymbolKind::Store, node);
    }
    if is_privilege_reference(node) {
        return string_target(AdminSymbolKind::Privilege, node);
    }
    if let Some((_, event_name)) = javascript_shopware_event_bus_event_at(node)
        && !event_name.is_empty()
    {
        return Some(target(AdminSymbolKind::EventBusEvent, "", &event_name));
    }
    if is_javascript_module_route_reference(node) {
        return string_target(AdminSymbolKind::ModuleRoute, node);
    }
    if let Some(reference) = javascript_cms_reference_at(node) {
        return Some(reference.target);
    }
    if let Some(target) = javascript_cms_component_reference_at(node) {
        return Some(target);
    }
    if let Some(reference) = javascript_registry_reference_at(node) {
        return Some(reference.target);
    }
    if jsquery::string_in_call(node, 0, &["Mixin.getByName", "Shopware.Mixin.getByName"]) {
        return string_target(AdminSymbolKind::Mixin, node);
    }
    if jsquery::string_in_call(
        node,
        0,
        &["Directive.getByName", "Shopware.Directive.getByName"],
    ) {
        return string_target(AdminSymbolKind::Directive, node);
    }
    if jsquery::string_in_call(node, 0, &["Filter.getByName", "Shopware.Filter.getByName"]) {
        return string_target(AdminSymbolKind::Filter, node);
    }
    // Context-specific lookups above take precedence over literal registrations.
    javascript_literal_symbol_at(&jsquery::string_at(node)?)
}

/// The concrete Vue component links owned directly by a CMS registration.
pub fn javascript_cms_component_reference_at(node: &SyntaxNode) -> Option<AdminSymbolTarget> {
    let literal = jsquery::string_at(node)?;
    let (_, config) = cms_registration_config_at(&literal)?;
    let property = jsquery::property_at(&literal)?;
    if property.parent().as_ref() != Some(&config) {
        return None;
    }
    match jsquery::property_name(&property).as_deref() {
        Some("component" | "configComponent" | "previewComponent") => {}
        _ => return None,
    }
    let name = jsquery::string_value(&literal);
    if name.is_empty() {
        return None;
    }
    Some(target(AdminSymbolKind::Component, "", &name))
}

/// Shopping Experiences registry declarations, lookups and block-slot
/// element references.
pub fn javascript_cms_reference_at(node: &SyntaxNode) -> Option<JavaScriptRegistryReference> {
    let literal = jsquery::string_at(node)?;
    let call = jsquery::call_at(&literal)?;
    let method = jsquery::call_method_name(&call).unwrap_or_default();
    if jsquery::string_argument_index(&literal) == Some(0) {
        let kind = match method.as_str() {
            "getCmsElementConfigByName" => Some(AdminSymbolKind::CmsElement),
            "getCmsBlockConfigByName" => Some(AdminSymbolKind::CmsBlock),
            _ => None,
        };
        if let Some(kind) = kind {
            let name = jsquery::string_value(&literal);
            if !name.is_empty() {
                return Some(JavaScriptRegistryReference {
                    target: target(kind, "", &name),
                    operation: "lookup".to_owned(),
                });
            }
        }
    }
    let (kind, config) = cms_registration_config_at(&literal)?;
    let property = jsquery::property_at(&literal)?;
    let name = jsquery::string_value(&literal);
    if name.is_empty() {
        return None;
    }
    if property.parent().as_ref() == Some(&config)
        && jsquery::property_name(&property).as_deref() == Some("name")
    {
        return Some(JavaScriptRegistryReference {
            target: target(cms_admin_symbol_kind(kind), "", &name),
            operation: "register".to_owned(),
        });
    }
    if kind == AdminCMSRegistrationKind::Block
        && cms_block_slot_element_reference(Some(&property), &config)
    {
        return Some(JavaScriptRegistryReference {
            target: target(AdminSymbolKind::CmsElement, "", &name),
            operation: "slot".to_owned(),
        });
    }
    None
}

pub fn javascript_cms_completion_kind_at(node: &SyntaxNode) -> Option<AdminCMSRegistrationKind> {
    let literal = jsquery::string_at(node)?;
    let call = jsquery::call_at(&literal)?;
    if jsquery::string_argument_index(&literal) == Some(0) {
        match jsquery::call_method_name(&call).as_deref() {
            Some("getCmsElementConfigByName") => return Some(AdminCMSRegistrationKind::Element),
            Some("getCmsBlockConfigByName") => return Some(AdminCMSRegistrationKind::Block),
            _ => {}
        }
    }
    let (kind, config) = cms_registration_config_at(&literal)?;
    if kind != AdminCMSRegistrationKind::Block {
        return None;
    }
    let property = jsquery::property_at(&literal);
    cms_block_slot_element_reference(property.as_ref(), &config)
        .then_some(AdminCMSRegistrationKind::Element)
}

fn cms_block_slot_element_reference(property: Option<&SyntaxNode>, config: &SyntaxNode) -> bool {
    let Some(property) = property else {
        return false;
    };
    let Some(slots_object) = jsquery::property(config, "slots")
        .and_then(|slots| jsquery::property_value(&slots))
        .filter(|value| value.kind() == jssyntax::JS_OBJECT)
    else {
        return false;
    };
    if property.parent().as_ref() == Some(&slots_object) {
        return true;
    }
    if jsquery::property_name(property).as_deref() != Some("type") {
        return false;
    }
    let Some(slot_object) = property
        .parent()
        .filter(|parent| parent.kind() == jssyntax::JS_OBJECT)
    else {
        return false;
    };
    slot_object
        .parent()
        .and_then(|slot_property| slot_property.parent())
        .is_some_and(|parent| parent == slots_object)
}

fn cms_registration_config_at(node: &SyntaxNode) -> Option<(AdminCMSRegistrationKind, SyntaxNode)> {
    let call = jsquery::call_at(node)?;
    let kind = match jsquery::call_method_name(&call).as_deref() {
        Some("registerCmsElement") => AdminCMSRegistrationKind::Element,
        Some("registerCmsBlock") => AdminCMSRegistrationKind::Block,
        _ => return None,
    };
    let config = jsquery::object_argument(&call, 0)?;
    Some((kind, config))
}

fn cms_admin_symbol_kind(kind: AdminCMSRegistrationKind) -> AdminSymbolKind {
    if kind == AdminCMSRegistrationKind::Block {
        AdminSymbolKind::CmsBlock
    } else {
        AdminSymbolKind::CmsElement
    }
}

/// A component prop declaration at the cursor.
pub fn javascript_component_prop_at(node: &SyntaxNode) -> Option<String> {
    let offset = u32::from(node.text_range().start());
    for current in node.ancestors() {
        if current.kind() != jssyntax::JS_PROPERTY
            || jsquery::property_name(&current).as_deref() != Some("props")
        {
            continue;
        }
        let value = jsquery::property_value(&current)?;
        if value.kind() == jssyntax::JS_ARRAY {
            for item in jsquery::array_items(&value) {
                if item.kind() != jssyntax::JS_STRING || !range_contains(item.text_range(), offset)
                {
                    continue;
                }
                let name = jsquery::string_value(&item);
                return (!name.is_empty()).then_some(name);
            }
        } else if value.kind() == jssyntax::JS_OBJECT {
            for property in jsquery::properties(&value) {
                let Some(name_node) = jsquery::property_name_node(&property) else {
                    continue;
                };
                if !range_contains(name_node.text_range(), offset) {
                    continue;
                }
                let name = jsquery::property_name(&property).unwrap_or_default();
                return (!name.is_empty()).then_some(name);
            }
        }
        return None;
    }
    None
}

/// The static event tokens that form a component's public event contract.
pub fn javascript_component_event_at(node: &SyntaxNode) -> Option<String> {
    let literal = jsquery::string_at(node);
    if let Some(literal) = &literal
        && jsquery::string_argument_index(literal) == Some(0)
        && matches!(
            jsquery::call_name(literal).as_deref(),
            Some("this.$emit" | "$emit" | "emit" | "context.emit")
        )
    {
        let name = canonical_event_name(&jsquery::string_value(literal));
        return (!name.is_empty()).then_some(name);
    }
    let offset = u32::from(node.text_range().start());
    for current in node.ancestors() {
        if current.kind() != jssyntax::JS_PROPERTY
            || jsquery::property_name(&current).as_deref() != Some("emits")
        {
            continue;
        }
        let value = jsquery::property_value(&current)?;
        if value.kind() == jssyntax::JS_ARRAY {
            let literal = literal.as_ref()?;
            if !range_contains(value.text_range(), offset) {
                return None;
            }
            let name = canonical_event_name(&jsquery::string_value(literal));
            return (!name.is_empty()).then_some(name);
        } else if value.kind() == jssyntax::JS_OBJECT {
            for property in jsquery::properties(&value) {
                let Some(name_node) = jsquery::property_name_node(&property) else {
                    continue;
                };
                if !range_contains(name_node.text_range(), offset) {
                    continue;
                }
                let name =
                    canonical_event_name(&jsquery::property_name(&property).unwrap_or_default());
                return (!name.is_empty()).then_some(name);
            }
        }
        return None;
    }
    None
}

/// Static component, module, directive and filter registry lookups.
pub fn javascript_registry_reference_at(node: &SyntaxNode) -> Option<JavaScriptRegistryReference> {
    let literal = jsquery::string_at(node)?;
    if jsquery::string_argument_index(&literal) != Some(0) {
        return None;
    }
    let name = jsquery::string_value(&literal);
    if name.is_empty() {
        return None;
    }
    let call_name = jsquery::call_name(&literal).unwrap_or_default();
    let operation = jsquery::call_method_name(&literal).unwrap_or_default();
    let kind = match call_name.as_str() {
        "Shopware.Component.getComponentRegistry().get"
        | "Component.getComponentRegistry().get"
        | "Shopware.Component.getComponentRegistry().has"
        | "Component.getComponentRegistry().has" => AdminSymbolKind::Component,
        "Shopware.Module.getModuleRegistry().get"
        | "Module.getModuleRegistry().get"
        | "Shopware.Module.getModuleRegistry().has"
        | "Module.getModuleRegistry().has" => AdminSymbolKind::Module,
        "Shopware.Directive.getByName" | "Directive.getByName" => AdminSymbolKind::Directive,
        "Shopware.Filter.getByName" | "Filter.getByName" => AdminSymbolKind::Filter,
        _ => return None,
    };
    Some(JavaScriptRegistryReference {
        target: target(kind, "", &name),
        operation,
    })
}

/// Static route identities used by module metadata and Vue Router locations.
pub fn is_javascript_module_route_reference(node: &SyntaxNode) -> bool {
    if jsquery::string_at(node).is_none() {
        return false;
    }
    let property_name = jsquery::property_at(node)
        .and_then(|property| jsquery::property_name(&property))
        .unwrap_or_default();
    match property_name.as_str() {
        "parentPath" => true,
        "name" => {
            if matches!(
                jsquery::call_name(node).as_deref(),
                Some(
                    "router.push"
                        | "this.$router.push"
                        | "$router.push"
                        | "router.replace"
                        | "this.$router.replace"
                        | "$router.replace"
                        | "router.resolve"
                        | "this.$router.resolve"
                        | "$router.resolve"
                )
            ) {
                return true;
            }
            has_javascript_property_ancestor(node, "redirect")
        }
        "path" if has_javascript_property_ancestor(node, "navigation") => true,
        "to" => has_javascript_property_ancestor(node, "settingsItem"),
        _ => false,
    }
}

fn has_javascript_property_ancestor(node: &SyntaxNode, name: &str) -> bool {
    node.ancestors().any(|current| {
        current.kind() == jssyntax::JS_PROPERTY
            && jsquery::property_name(&current).as_deref() == Some(name)
    })
}
