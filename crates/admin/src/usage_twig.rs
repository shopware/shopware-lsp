//! Template usage extraction.

use crate::*;

use twigast::AstNode;

struct AdminTwigUsageScanner<'a> {
    root: SyntaxNode,
    file_path: String,
    content: String,
    collector: AdminUsageCollector<'a>,
    shorthand_member_ranges: HashSet<TextRange>,
    vue_bindings: Vec<TwigVueBinding>,
    scoped_slots: Vec<TwigScopedSlot>,
}

pub(crate) fn parse_admin_twig_usages(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminUsageSet> {
    let content = root.text().to_string();
    let vue_bindings = twig_vue_for_bindings(root, &content);
    let scoped_slots = twig_scoped_slots(root);
    let mut scanner = AdminTwigUsageScanner {
        root: root.clone(),
        file_path: file_path.to_owned(),
        content,
        collector: AdminUsageCollector::new(file_path, line_index),
        shorthand_member_ranges: HashSet::new(),
        vue_bindings,
        scoped_slots,
    };
    scanner.collect_registry_references();
    scanner.collect_starting_tags();
    scanner.collect_ending_tags();
    scanner.collect_instance_members();
    scanner.collector.values()
}

impl AdminTwigUsageScanner<'_> {
    fn collect_registry_references(&mut self) {
        for reference in twig_registry_references(&self.root) {
            self.collector.add_range(
                reference.kind,
                "",
                &reference.name,
                reference.range,
                false,
                false,
            );
        }
    }

    fn collect_starting_tags(&mut self) {
        let nodes: Vec<SyntaxNode> =
            twigquery::iterate_nodes(&self.root, &[twigsyntax::HTML_STARTING_TAG]).collect();
        for node in nodes {
            let Some(tag) = twigast::HtmlStartingTag::cast(node.clone()) else {
                continue;
            };
            if tag.name().is_none() {
                continue;
            }
            self.collect_starting_tag(&node, &tag);
        }
    }

    fn collect_starting_tag(&mut self, node: &SyntaxNode, tag: &twigast::HtmlStartingTag) {
        let Some(name_token) = tag.name() else {
            return;
        };
        let name = name_token.text().to_owned();
        let (selector, dynamic) = twig_dynamic_component_selector_or_default(node);
        let contract_names = static_component_contract_names(Some(node));
        let dynamic_usage = dynamic && contract_names.is_empty();
        let dynamic_router_view =
            dynamic_usage && twig_dynamic_component_uses_router_view(node, &selector);
        if dynamic {
            for candidate in &selector.candidates {
                self.collector.add_range(
                    AdminSymbolKind::Component,
                    "",
                    &candidate.name,
                    candidate.range,
                    false,
                    false,
                );
            }
        }
        if is_component_tag(&name) {
            self.collector.add_range(
                AdminSymbolKind::Component,
                "",
                &name,
                name_token.text_range(),
                false,
                false,
            );
        }
        for attribute in tag.attributes() {
            self.collect_attribute(
                node,
                &attribute,
                &contract_names,
                &selector,
                dynamic,
                dynamic_usage,
                dynamic_router_view,
            );
        }
        if name == "slot" {
            collect_slot_declaration(tag, &self.file_path, &mut self.collector);
        }
    }

    fn collect_attribute(
        &mut self,
        node: &SyntaxNode,
        attribute: &twigast::HtmlAttribute,
        contract_names: &[String],
        selector: &VueDynamicComponentSelector,
        dynamic: bool,
        dynamic_usage: bool,
        dynamic_router_view: bool,
    ) {
        let Some(name_token) = attribute.name() else {
            return;
        };
        let name_range = name_token.text_range();
        let attribute_name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        if let Some(directive) = vue_directive_reference_for_attribute(&attribute_name, name_range)
        {
            self.collector.add_range(
                AdminSymbolKind::Directive,
                "",
                &directive.name,
                directive.range,
                false,
                false,
            );
        }
        if attribute_name == "v-bind" {
            self.collect_object_binding(
                attribute,
                contract_names,
                selector,
                dynamic_usage,
                dynamic_router_view,
            );
        }
        if !contract_names.is_empty() && (!dynamic || attribute_name != selector.attribute_name) {
            self.collect_contract_attribute(&attribute_name, name_range, contract_names);
        }
        if dynamic_usage && attribute_name != selector.attribute_name {
            self.collect_dynamic_attribute(
                &attribute_name,
                name_range,
                selector,
                dynamic_router_view,
            );
        }
        self.collect_slot_attribute(node, &attribute_name, name_range);
    }

    fn collect_object_binding(
        &mut self,
        attribute: &twigast::HtmlAttribute,
        contract_names: &[String],
        selector: &VueDynamicComponentSelector,
        dynamic_usage: bool,
        dynamic_router_view: bool,
    ) {
        let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
            return;
        };
        let (fields, _) = vue_object_binding_fields(
            &inner.syntax().text().to_string(),
            u32::from(inner.syntax().text_range().start()),
        );
        for field in &fields {
            if field.shorthand {
                self.shorthand_member_ranges.insert(field.expression_range);
            }
            let prop_name = normalize_prop_name(&field.name);
            if prop_name.is_empty() {
                continue;
            }
            let (style, identifier) = object_binding_name_style(field, &prop_name);
            for contract_name in contract_names {
                self.collector.add_styled_range(
                    AdminSymbolKind::ComponentProp,
                    contract_name,
                    &prop_name,
                    field.name_range,
                    false,
                    identifier,
                    style,
                );
            }
            if dynamic_usage {
                self.collector.add_dynamic_styled_range(
                    AdminSymbolKind::ComponentProp,
                    &prop_name,
                    field.name_range,
                    false,
                    identifier,
                    style,
                    &selector.expression,
                    dynamic_router_view,
                );
            }
        }
    }

    fn collect_contract_attribute(
        &mut self,
        attribute_name: &str,
        range: TextRange,
        contract_names: &[String],
    ) {
        for contract_name in contract_names {
            if let Some(argument) = normalize_model_argument(attribute_name) {
                let mut model_name = "v-model".to_owned();
                if !argument.is_empty() {
                    model_name.push(':');
                    model_name.push_str(&camel_to_kebab(&argument));
                }
                self.collector.add_range(
                    AdminSymbolKind::ComponentModel,
                    contract_name,
                    &model_name,
                    must_vue_attribute_argument_range(range, attribute_name),
                    false,
                    false,
                );
            }
            if let Some(event) = vue_event_reference_for_attribute(attribute_name, range) {
                self.collector.add_range(
                    AdminSymbolKind::ComponentEvent,
                    contract_name,
                    &event.name,
                    event.range,
                    false,
                    false,
                );
            }
            if let Some(prop) = vue_prop_reference_for_attribute(attribute_name, range) {
                self.collector.add_range(
                    AdminSymbolKind::ComponentProp,
                    contract_name,
                    &prop.name,
                    prop.range,
                    false,
                    false,
                );
            }
        }
    }

    fn collect_dynamic_attribute(
        &mut self,
        attribute_name: &str,
        range: TextRange,
        selector: &VueDynamicComponentSelector,
        dynamic_router_view: bool,
    ) {
        if let Some(argument) = normalize_model_argument(attribute_name) {
            let mut model_name = "v-model".to_owned();
            if !argument.is_empty() {
                model_name.push(':');
                model_name.push_str(&camel_to_kebab(&argument));
            }
            self.collector.add_dynamic_range(
                AdminSymbolKind::ComponentModel,
                &model_name,
                must_vue_attribute_argument_range(range, attribute_name),
                &selector.expression,
                dynamic_router_view,
            );
        }
        if let Some(event) = vue_event_reference_for_attribute(attribute_name, range) {
            self.collector.add_dynamic_range(
                AdminSymbolKind::ComponentEvent,
                &event.name,
                event.range,
                &selector.expression,
                dynamic_router_view,
            );
        }
        if let Some(prop) = vue_prop_reference_for_attribute(attribute_name, range) {
            self.collector.add_dynamic_range(
                AdminSymbolKind::ComponentProp,
                &prop.name,
                prop.range,
                &selector.expression,
                dynamic_router_view,
            );
        }
    }

    fn collect_slot_attribute(
        &mut self,
        node: &SyntaxNode,
        attribute_name: &str,
        range: TextRange,
    ) {
        let slot_name = normalize_slot_name(attribute_name);
        if slot_name.is_empty() || attribute_name == "v-slot" {
            return;
        }
        let owner_tag = twig_slot_owner_starting_tag(node);
        let mut owners = static_component_contract_names(owner_tag.as_ref());
        if owners.is_empty() && owner_tag.is_none() {
            let owner = parent_component_name(node);
            if !owner.is_empty() {
                owners = vec![owner];
            }
        }
        let argument_range = must_vue_attribute_argument_range(range, attribute_name);
        for owner in &owners {
            self.collector.add_range(
                AdminSymbolKind::ComponentSlot,
                owner,
                &slot_name,
                argument_range,
                false,
                false,
            );
        }
        if !owners.is_empty() {
            return;
        }
        let Some(owner_tag) = owner_tag else {
            return;
        };
        let Some(slot_selector) = twig_dynamic_component_selector(&owner_tag) else {
            return;
        };
        let router_view = twig_dynamic_component_uses_router_view(&owner_tag, &slot_selector);
        self.collector.add_dynamic_range(
            AdminSymbolKind::ComponentSlot,
            &slot_name,
            argument_range,
            &slot_selector.expression,
            router_view,
        );
    }

    fn collect_ending_tags(&mut self) {
        let nodes: Vec<SyntaxNode> =
            twigquery::iterate_nodes(&self.root, &[twigsyntax::HTML_ENDING_TAG]).collect();
        for node in nodes {
            let Some(name) = twigast::HtmlEndingTag::cast(node).and_then(|tag| tag.name()) else {
                continue;
            };
            if !is_component_tag(name.text()) {
                continue;
            }
            self.collector.add_range(
                AdminSymbolKind::Component,
                "",
                name.text(),
                name.text_range(),
                false,
                false,
            );
        }
    }

    fn collect_instance_members(&mut self) {
        for identifier in twig_vue_expression_root_identifiers(&self.root, &self.content) {
            if twig_vue_root_identifier_is_local_in(
                &self.root,
                &self.vue_bindings,
                &self.scoped_slots,
                &identifier,
            ) {
                continue;
            }
            let style = if self.shorthand_member_ranges.contains(&identifier.range) {
                AdminNameStyle::Shorthand
            } else {
                AdminNameStyle::Exact
            };
            self.collector.add_styled_range(
                AdminSymbolKind::ComponentMember,
                "",
                &identifier.name,
                identifier.range,
                false,
                true,
                style,
            );
        }
    }
}

pub(crate) fn object_binding_name_style(
    field: &VueObjectBindingField,
    prop_name: &str,
) -> (AdminNameStyle, bool) {
    if field.shorthand {
        return (AdminNameStyle::Shorthand, true);
    }
    (AdminNameStyle::Exact, field.name == prop_name)
}
