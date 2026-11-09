//! Twig template symbol recognition.

use crate::*;

use twigast::AstNode;

fn target(kind: AdminSymbolKind, owner: &str, name: &str) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind,
        owner: owner.to_owned(),
        name: name.to_owned(),
    }
}

/// The Administration symbol at `offset` of a template.
pub fn twig_symbol_at_offset(root: &SyntaxNode, offset: u32) -> Option<AdminSymbolTarget> {
    if let Some(reference) = twig_registry_reference_at_offset(root, offset)
        && !reference.name.is_empty()
    {
        return Some(target(reference.kind, "", &reference.name));
    }
    for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        if let Some(target) = twig_starting_tag_symbol_at_offset(&node, offset) {
            return Some(target);
        }
    }
    twig_ending_tag_symbol_at_offset(root, offset)
}

/// The dynamic component selector of `node` as a `(selector, dynamic)` pair;
/// the default selector and `false` when `node` has none.
pub(crate) fn twig_dynamic_component_selector_or_default(
    node: &SyntaxNode,
) -> (VueDynamicComponentSelector, bool) {
    match twig_dynamic_component_selector(node) {
        Some(selector) => (selector, true),
        None => (VueDynamicComponentSelector::default(), false),
    }
}

fn twig_starting_tag_symbol_at_offset(node: &SyntaxNode, offset: u32) -> Option<AdminSymbolTarget> {
    let tag = twigast::HtmlStartingTag::cast(node.clone())?;
    let name_token = tag.name()?;
    let tag_name = name_token.text().to_owned();
    let (selector, dynamic) = twig_dynamic_component_selector_or_default(node);
    if dynamic && let Some(candidate) = selector.candidate_at(offset) {
        return Some(target(AdminSymbolKind::Component, "", &candidate.name));
    }
    let contract_name = static_component_name_for_tag(node).unwrap_or_else(|| tag_name.clone());
    if is_component_tag(&tag_name) && range_contains(name_token.text_range(), offset) {
        return Some(target(AdminSymbolKind::Component, "", &tag_name));
    }
    for attribute in tag.attributes() {
        if let Some(target) = twig_attribute_symbol_at_offset(
            node,
            &attribute,
            &contract_name,
            &selector,
            dynamic,
            offset,
        ) {
            return Some(target);
        }
    }
    if tag_name == "slot" {
        return twig_slot_declaration_at_offset(&tag, offset);
    }
    None
}

fn twig_attribute_symbol_at_offset(
    node: &SyntaxNode,
    attribute: &twigast::HtmlAttribute,
    contract_name: &str,
    selector: &VueDynamicComponentSelector,
    dynamic: bool,
    offset: u32,
) -> Option<AdminSymbolTarget> {
    let name_token = attribute.name()?;
    let attribute_name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
    if let Some(directive) =
        vue_directive_reference_for_attribute(&attribute_name, name_token.text_range())
        && range_contains(directive.range, offset)
    {
        return Some(target(AdminSymbolKind::Directive, "", &directive.name));
    }
    if attribute_name == "v-bind"
        && is_component_tag(contract_name)
        && let Some(target) = twig_object_binding_symbol_at_offset(attribute, contract_name, offset)
    {
        return Some(target);
    }
    if !range_contains(name_token.text_range(), offset) {
        return None;
    }
    let component_attribute =
        is_component_tag(contract_name) && (!dynamic || attribute_name != selector.attribute_name);
    if component_attribute
        && let Some(target) = twig_model_or_event_target(&attribute_name, contract_name)
    {
        return Some(target);
    }
    let slot_name = normalize_slot_name(&attribute_name);
    if !slot_name.is_empty() && attribute_name != "v-slot" {
        let owner = if is_component_tag(contract_name) {
            contract_name.to_owned()
        } else {
            parent_component_name(node)
        };
        if !owner.is_empty() {
            return Some(target(AdminSymbolKind::ComponentSlot, &owner, &slot_name));
        }
    }
    if component_attribute {
        let prop_name = normalize_prop_name(&attribute_name);
        if !prop_name.is_empty() {
            return Some(target(
                AdminSymbolKind::ComponentProp,
                contract_name,
                &prop_name,
            ));
        }
    }
    None
}

fn twig_object_binding_symbol_at_offset(
    attribute: &twigast::HtmlAttribute,
    contract_name: &str,
    offset: u32,
) -> Option<AdminSymbolTarget> {
    let inner = attribute.value()?.inner()?;
    let (fields, _) = vue_object_binding_fields(
        &inner.syntax().text().to_string(),
        u32::from(inner.syntax().text_range().start()),
    );
    fields
        .iter()
        .find(|field| range_contains(field.name_range, offset))
        .map(|field| {
            target(
                AdminSymbolKind::ComponentProp,
                contract_name,
                &normalize_prop_name(&field.name),
            )
        })
}

fn twig_model_or_event_target(
    attribute_name: &str,
    contract_name: &str,
) -> Option<AdminSymbolTarget> {
    if let Some(argument) = normalize_model_argument(attribute_name) {
        let mut name = "v-model".to_owned();
        if !argument.is_empty() {
            name.push(':');
            name.push_str(&camel_to_kebab(&argument));
        }
        return Some(target(
            AdminSymbolKind::ComponentModel,
            contract_name,
            &name,
        ));
    }
    let event_name = normalize_event_name(attribute_name);
    if !event_name.is_empty() {
        return Some(target(
            AdminSymbolKind::ComponentEvent,
            contract_name,
            &event_name,
        ));
    }
    None
}

fn twig_slot_declaration_at_offset(
    tag: &twigast::HtmlStartingTag,
    offset: u32,
) -> Option<AdminSymbolTarget> {
    for attribute in tag.attributes() {
        if twigquery::html_attribute_name(attribute.syntax()).as_deref() != Some("name") {
            continue;
        }
        let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
            continue;
        };
        if !range_contains(inner.syntax().text_range(), offset) {
            continue;
        }
        let text = inner.syntax().text().to_string();
        let name = text.trim();
        if !name.is_empty() {
            return Some(target(AdminSymbolKind::ComponentSlot, "", name));
        }
    }
    None
}

fn twig_ending_tag_symbol_at_offset(root: &SyntaxNode, offset: u32) -> Option<AdminSymbolTarget> {
    for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_ENDING_TAG]) {
        let Some(name) = twigast::HtmlEndingTag::cast(node).and_then(|tag| tag.name()) else {
            continue;
        };
        if !is_component_tag(name.text()) || !range_contains(name.text_range(), offset) {
            continue;
        }
        return Some(target(AdminSymbolKind::Component, "", name.text()));
    }
    None
}
