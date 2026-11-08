//! JavaScript usage extraction (port of Go `usage_javascript.go`).

use crate::*;

fn string_in_call(node: &SyntaxNode, names: &[&str]) -> bool {
    jsquery::string_in_call(node, 0, names)
}

pub(crate) fn parse_admin_javascript_usages(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminUsageSet> {
    let mut collector = AdminUsageCollector::new(file_path, line_index);
    let analysis = JavaScriptDocumentAnalysis::new(root);
    let component_objects = component_usage_objects(&analysis);
    for object in &component_objects {
        if object.events {
            collect_component_event_object_usages(&object.node, file_path, &mut collector);
        }
    }
    for object in &component_objects {
        collect_component_member_object_usages(&object.node, file_path, &mut collector);
    }
    for literal in analysis.nodes(&[jssyntax::JS_STRING]) {
        if let Some((_, event_name)) = analysis.shopware_event_bus_event_at(&literal)
            && !event_name.is_empty()
        {
            collector.add_js_string(AdminSymbolKind::EventBusEvent, "", Some(&literal), false);
        }
        if let Some(reference) = javascript_cms_reference_at(&literal) {
            collector.add_js_string(
                reference.target.kind,
                "",
                Some(&literal),
                reference.operation == "register",
            );
        }
        if let Some(reference) = javascript_registry_reference_at(&literal) {
            collector.add_js_string(reference.target.kind, "", Some(&literal), false);
        }
        if is_service_reference(&literal) {
            collector.add_js_string(AdminSymbolKind::Service, "", Some(&literal), false);
        } else if is_store_reference(&literal) {
            collector.add_js_string(AdminSymbolKind::Store, "", Some(&literal), false);
        } else if is_privilege_reference(&literal) {
            collector.add_js_string(AdminSymbolKind::Privilege, "", Some(&literal), false);
        } else if string_in_call(&literal, &["Mixin.getByName", "Shopware.Mixin.getByName"]) {
            collector.add_js_string(AdminSymbolKind::Mixin, "", Some(&literal), false);
        } else if string_in_call(
            &literal,
            &["Directive.getByName", "Shopware.Directive.getByName"],
        ) {
            collector.add_js_string(AdminSymbolKind::Directive, "", Some(&literal), false);
        } else if string_in_call(&literal, &["Filter.getByName", "Shopware.Filter.getByName"]) {
            collector.add_js_string(AdminSymbolKind::Filter, "", Some(&literal), false);
        }
        let property = jsquery::property_at(&literal);
        if is_javascript_module_route_reference(&literal) {
            collector.add_js_string(AdminSymbolKind::ModuleRoute, "", Some(&literal), false);
        }
        if let Some(target) = javascript_cms_component_reference_at(&literal) {
            collector.add_js_string(target.kind, &target.owner, Some(&literal), false);
        }
        if property
            .as_ref()
            .and_then(jsquery::property_name)
            .as_deref()
            == Some("component")
        {
            let call_name = jsquery::call_at(&literal).and_then(|call| jsquery::call_name(&call));
            if matches!(
                call_name.as_deref(),
                Some("Module.register" | "Shopware.Module.register")
            ) {
                collector.add_js_string(AdminSymbolKind::Component, "", Some(&literal), false);
            }
        }
    }

    for call in analysis.iterate_calls(&[]) {
        let name = jsquery::call_name(call).unwrap_or_default();
        let method = jsquery::call_method_name(call).unwrap_or_default();
        let argument = |index| jsquery::string_argument(call, index);
        match name.as_str() {
            "Component.register" | "Shopware.Component.register" => {
                collector.add_js_string(AdminSymbolKind::Component, "", argument(0).as_ref(), true);
            }
            "Component.extend" | "Shopware.Component.extend" => {
                collector.add_js_string(AdminSymbolKind::Component, "", argument(0).as_ref(), true);
                collector.add_js_string(
                    AdminSymbolKind::Component,
                    "",
                    argument(1).as_ref(),
                    false,
                );
            }
            "Component.override" | "Shopware.Component.override" => {
                collector.add_js_string(
                    AdminSymbolKind::Component,
                    "",
                    argument(0).as_ref(),
                    false,
                );
            }
            "Mixin.register" | "Shopware.Mixin.register" => {
                collector.add_js_string(AdminSymbolKind::Mixin, "", argument(0).as_ref(), true);
            }
            "Directive.register" | "Shopware.Directive.register" => {
                collector.add_js_string(AdminSymbolKind::Directive, "", argument(0).as_ref(), true);
            }
            "Filter.register" | "Shopware.Filter.register" => {
                collector.add_js_string(AdminSymbolKind::Filter, "", argument(0).as_ref(), true);
            }
            "Module.register" | "Shopware.Module.register" => {
                collector.add_js_string(AdminSymbolKind::Module, "", argument(0).as_ref(), true);
            }
            "Shopware.Store.register" | "Store.register" => {
                collector.add_store_declaration(call);
            }
            _ => {}
        }
        if method == "addServiceProvider" {
            collector.add_js_string(AdminSymbolKind::Service, "", argument(0).as_ref(), true);
        }
        if method == "register" && call.text().to_string().contains("Service()") {
            collector.add_js_string(AdminSymbolKind::Service, "", argument(0).as_ref(), true);
        }
    }

    let application_container_aliases =
        application_container_const_alias_names(root, Some(&analysis));
    let members = analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]);
    for member in &members {
        let container =
            if potential_application_container_member(member, &application_container_aliases) {
                analysis.application_container_member(member)
            } else {
                None
            };
        if let Some((container_name, member_name)) = container
            && container_name == "service"
            && !member_name.is_empty()
        {
            collector.add_node(
                AdminSymbolKind::Service,
                "",
                &member_name,
                last_js_identifier(member).as_ref(),
                false,
            );
            continue;
        }
        let Some(store_member) = jsquery::store_member(member) else {
            continue;
        };
        if store_member.member.is_empty() {
            continue;
        }
        collector.add_node(
            AdminSymbolKind::StoreMember,
            &store_member.store,
            &store_member.member,
            last_js_identifier(member).as_ref(),
            false,
        );
    }

    let definition = parse_component_definition_with_line_index(root, line_index);
    if !definition.injected.is_empty() {
        let injected: HashSet<&str> = definition.injected.iter().map(String::as_str).collect();
        for member in &members {
            let Some(name) = jsquery::this_member(member) else {
                continue;
            };
            if !injected.contains(name.as_str()) {
                continue;
            }
            collector.add_node(
                AdminSymbolKind::Service,
                "",
                &name,
                jsquery::this_member_name_node(member).as_ref(),
                false,
            );
        }
    }
    collector.values()
}

/// Administration symbol occurrences of a live JavaScript/TypeScript CST,
/// without touching the persistent index.
pub fn collect_javascript_usages(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminUsageSet> {
    parse_admin_javascript_usages(root, file_path, line_index)
}

pub(crate) struct ComponentUsageObject {
    pub(crate) node: SyntaxNode,
    pub(crate) events: bool,
}

pub(crate) fn component_usage_objects(
    analysis: &JavaScriptDocumentAnalysis,
) -> Vec<ComponentUsageObject> {
    let mut seen: HashMap<TextRange, usize> = HashMap::new();
    let mut objects: Vec<ComponentUsageObject> = Vec::new();
    let mut add_object = |object: Option<SyntaxNode>, events: bool| {
        let Some(object) = object else {
            return;
        };
        let range = object.range_trimmed_trivia();
        if let Some(&position) = seen.get(&range) {
            objects[position].events = objects[position].events || events;
            return;
        }
        seen.insert(range, objects.len());
        objects.push(ComponentUsageObject {
            node: object,
            events,
        });
    };
    for export in analysis.nodes(&[jssyntax::JS_EXPORT_DEFAULT]) {
        add_object(
            jsquery::export_default_expression(&export)
                .and_then(|expression| component_definition_object(&expression)),
            true,
        );
    }
    for call in analysis.iterate_calls(&[
        "Component.register",
        "Shopware.Component.register",
        "Component.extend",
        "Shopware.Component.extend",
        "Component.override",
        "Shopware.Component.override",
        "Mixin.register",
        "Shopware.Mixin.register",
    ]) {
        let call_name = jsquery::call_name(call).unwrap_or_default();
        let argument = if call_name.ends_with(".extend") { 2 } else { 1 };
        add_object(
            jsquery::argument_expression(call, argument)
                .and_then(|expression| component_definition_object(&expression)),
            call_name != "Mixin.register" && call_name != "Shopware.Mixin.register",
        );
    }
    objects
}

pub(crate) fn collect_component_member_object_usages(
    object: &SyntaxNode,
    file_path: &str,
    collector: &mut AdminUsageCollector<'_>,
) {
    let mut definition = ComponentDefinition {
        file_path: file_path.to_owned(),
        ..Default::default()
    };
    parse_definition_object(object, &mut definition, collector.line_index);
    set_definition_file_path(&mut definition, file_path);
    let component = VueComponent {
        definition_path: file_path.to_owned(),
        props: definition.props.clone(),
        injected: definition.injected.clone(),
        members: definition.members.clone(),
        local_directives: definition.local_directives.clone(),
        ..Default::default()
    };
    for directive in &definition.local_directives {
        let style = if directive.shorthand {
            AdminNameStyle::Shorthand
        } else if !directive.quoted {
            AdminNameStyle::Camel
        } else {
            AdminNameStyle::Exact
        };
        collector.add_source_range(
            AdminSymbolKind::Directive,
            &directive.file_path,
            &directive.name,
            &directive.name_range,
            style,
        );
    }
    for member in &definition.members {
        if !member.renameable() {
            continue;
        }
        let style = if member.shorthand {
            AdminNameStyle::Shorthand
        } else {
            AdminNameStyle::Exact
        };
        collector.add_source_range(
            AdminSymbolKind::ComponentMember,
            &member.source_identity(),
            &member.name,
            &member.name_range,
            style,
        );
    }
    let props: HashSet<&str> = component
        .props
        .iter()
        .map(|prop| prop.name.as_str())
        .collect();
    let injected: HashSet<&str> = component.injected.iter().map(String::as_str).collect();
    let mut template_members: HashMap<String, VueComponentMember> = HashMap::new();
    for member in component.template_members() {
        template_members.insert(member.name.clone(), member);
    }
    for expression in jsquery::nodes(object, &[jssyntax::JS_MEMBER_EXPRESSION]) {
        let Some(name) = jsquery::this_member(&expression) else {
            continue;
        };
        if name.is_empty() || props.contains(name.as_str()) || injected.contains(name.as_str()) {
            continue;
        }
        let owner = match template_members.get(&name) {
            Some(member) if member.renameable() => member.source_identity(),
            _ => String::new(),
        };
        collector.add_node(
            AdminSymbolKind::ComponentMember,
            &owner,
            &name,
            jsquery::this_member_name_node(&expression).as_ref(),
            false,
        );
    }
}

pub(crate) fn collect_component_event_object_usages(
    object: &SyntaxNode,
    file_path: &str,
    collector: &mut AdminUsageCollector<'_>,
) {
    let property_value = |name: &str| {
        jsquery::property(object, name).and_then(|property| jsquery::property_value(&property))
    };
    let mut prop_names: HashSet<String> = HashSet::new();
    if let Some(props) = property_value("props") {
        if props.kind() == jssyntax::JS_ARRAY {
            for item in jsquery::array_items(&props) {
                if item.kind() != jssyntax::JS_STRING {
                    continue;
                }
                let name = jsquery::string_value(&item);
                if name.is_empty() {
                    continue;
                }
                prop_names.insert(name.clone());
                collector.add_named_js_node(
                    AdminSymbolKind::ComponentProp,
                    file_path,
                    &name,
                    Some(&item),
                    true,
                );
            }
        } else if props.kind() == jssyntax::JS_OBJECT {
            for property in jsquery::properties(&props) {
                let name = jsquery::property_name(&property).unwrap_or_default();
                if name.is_empty() {
                    continue;
                }
                prop_names.insert(name.clone());
                collector.add_named_js_node(
                    AdminSymbolKind::ComponentProp,
                    file_path,
                    &name,
                    jsquery::property_name_node(&property).as_ref(),
                    true,
                );
            }
        }
    }
    for member in jsquery::nodes(object, &[jssyntax::JS_MEMBER_EXPRESSION]) {
        let Some(name) = jsquery::this_member(&member) else {
            continue;
        };
        if !prop_names.contains(&name) {
            continue;
        }
        collector.add_named_js_node(
            AdminSymbolKind::ComponentProp,
            file_path,
            &name,
            jsquery::this_member_name_node(&member).as_ref(),
            false,
        );
    }
    if let Some(emits) = property_value("emits") {
        if emits.kind() == jssyntax::JS_ARRAY {
            for item in jsquery::array_items(&emits) {
                if item.kind() != jssyntax::JS_STRING {
                    continue;
                }
                collector.add_named_js_node(
                    AdminSymbolKind::ComponentEvent,
                    file_path,
                    &canonical_event_name(&jsquery::string_value(&item)),
                    Some(&item),
                    true,
                );
            }
        } else if emits.kind() == jssyntax::JS_OBJECT {
            for property in jsquery::properties(&emits) {
                collector.add_named_js_node(
                    AdminSymbolKind::ComponentEvent,
                    file_path,
                    &canonical_event_name(&jsquery::property_name(&property).unwrap_or_default()),
                    jsquery::property_name_node(&property).as_ref(),
                    true,
                );
            }
        }
    }
    for call in jsquery::iter_calls(object, &[]) {
        if !matches!(
            jsquery::call_name(&call).as_deref(),
            Some("this.$emit" | "$emit" | "emit" | "context.emit")
        ) {
            continue;
        }
        let Some(argument) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        if argument.kind() != jssyntax::JS_STRING {
            continue;
        }
        collector.add_named_js_node(
            AdminSymbolKind::ComponentEvent,
            file_path,
            &canonical_event_name(&jsquery::string_value(&argument)),
            Some(&argument),
            false,
        );
    }
}
