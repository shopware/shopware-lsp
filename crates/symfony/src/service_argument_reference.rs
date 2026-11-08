//! Static service references and configured method names in YAML and XML
//! service definitions.

use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::model::{ServiceArgumentReference, ServiceMethodReference};
use crate::reference::parse_service_reference;
use crate::util::numeric_index;
use crate::util::{
    go_trim_space, xml_attr, xml_attribute_content_range, yaml_scalar_content_range,
};
use crate::yaml_context::static_php_class_name;
use crate::yaml_named_argument::{
    static_yaml_service_class_name, yaml_service_class_name, yaml_service_pairs,
};
use crate::yaml_services::yaml_scalar;

fn trim_backslash(value: &str) -> String {
    value.strip_prefix('\\').unwrap_or(value).to_owned()
}

fn trim_at(value: &str) -> String {
    value.strip_prefix('@').unwrap_or(value).to_owned()
}

/// Configured calls, tag callbacks, and factory methods of a YAML document.
pub fn yaml_service_method_references(root: &SyntaxNode) -> Vec<ServiceMethodReference> {
    yaml_method_references(root, false)
}

/// The configured method value at `offset`, including an empty scalar while
/// it is being edited.
pub fn yaml_service_method_reference_at(
    root: &SyntaxNode,
    offset: u32,
) -> Option<ServiceMethodReference> {
    yaml_method_references(root, true)
        .into_iter()
        .find(|reference| service_method_range_contains(reference.range, offset))
}

fn yaml_method_references(root: &SyntaxNode, include_empty: bool) -> Vec<ServiceMethodReference> {
    let mut result = Vec::new();
    for service_pair in yaml_service_pairs(root) {
        let service_id = yaml_scalar(yamlquery::pair_key(&service_pair).as_ref());
        if service_id.is_empty() || service_id.starts_with('_') {
            continue;
        }
        let Some(config) = yamlquery::pair_value(&service_pair).filter(yamlquery::is_mapping)
        else {
            continue;
        };
        let base = ServiceMethodReference {
            owner_class: yaml_service_class_name(&service_id, &config),
            owner_service_id: service_id,
            format: "yaml".to_owned(),
            ..ServiceMethodReference::default()
        };
        if let Some(calls) = yamlquery::property(&config, "calls").filter(yamlquery::is_sequence) {
            for call_item in yamlquery::items(&calls) {
                let Some(call) = yamlquery::item_value(&call_item) else {
                    continue;
                };
                if yamlquery::is_sequence(&call) {
                    if let Some(first) = yamlquery::items(&call).next() {
                        append_yaml_method_item(&mut result, base.clone(), &first, include_empty);
                    }
                } else if yamlquery::is_mapping(&call) {
                    for pair in yamlquery::pairs(&call) {
                        append_yaml_method(
                            &mut result,
                            base.clone(),
                            yamlquery::pair_key(&pair).as_ref(),
                            include_empty,
                        );
                    }
                }
            }
        }
        append_yaml_tag_methods(
            &mut result,
            &base,
            yamlquery::property(&config, "tags").as_ref(),
            include_empty,
        );
        append_yaml_factory_methods(&mut result, &base, &config, include_empty);
    }
    result
}

/// Configured calls, tag callbacks, and factory methods of an XML document.
pub fn xml_service_method_references(root: &SyntaxNode) -> Vec<ServiceMethodReference> {
    xml_method_references(root, false)
}

/// `<call method>`, `<tag method>`, `<factory method>`, and legacy
/// `factory-method` attributes at `offset`.
pub fn xml_service_method_reference_at(
    root: &SyntaxNode,
    offset: u32,
) -> Option<ServiceMethodReference> {
    xml_method_references(root, true)
        .into_iter()
        .find(|reference| service_method_range_contains(reference.range, offset))
}

fn xml_owner(service: &SyntaxNode) -> (String, String) {
    let owner_id = xml_attr(service, "id");
    let mut owner_class = xml_attr(service, "class");
    if !static_yaml_service_class_name(&owner_class) && static_php_class_name(&owner_id) {
        owner_class = owner_id.clone();
    }
    (owner_id, trim_backslash(&owner_class))
}

fn xml_method_references(root: &SyntaxNode, include_empty: bool) -> Vec<ServiceMethodReference> {
    let mut result = Vec::new();
    for service in xmlquery::elements(root, &["service"]) {
        let (owner_service_id, owner_class) = xml_owner(&service);
        let base = ServiceMethodReference {
            owner_service_id,
            owner_class,
            format: "xml".to_owned(),
            ..ServiceMethodReference::default()
        };
        for call in xmlquery::child_elements(&service, &["call"]) {
            append_xml_method(
                &mut result,
                base.clone(),
                xmlquery::attribute(&call, "method").as_ref(),
                include_empty,
            );
        }
        for tag in xmlquery::child_elements(&service, &["tag"]) {
            append_xml_method(
                &mut result,
                base.clone(),
                xmlquery::attribute(&tag, "method").as_ref(),
                include_empty,
            );
        }
        if let Some(factory) = xmlquery::child_element(&service, &["factory"]) {
            let mut reference = base.clone();
            reference.factory = true;
            xml_factory_receiver(&factory, &mut reference);
            append_xml_method(
                &mut result,
                reference,
                xmlquery::attribute(&factory, "method").as_ref(),
                include_empty,
            );
        }
        if let Some(method) = xmlquery::attribute(&service, "factory-method") {
            let mut reference = base.clone();
            reference.factory = true;
            let value = xml_attr(&service, "factory-service");
            if !value.is_empty() {
                reference.receiver_service = trim_at(&value);
            }
            reference.receiver_class = trim_backslash(&xml_attr(&service, "factory-class"));
            append_xml_method(&mut result, reference, Some(&method), include_empty);
        }
    }
    result
}

fn append_yaml_method(
    result: &mut Vec<ServiceMethodReference>,
    mut base: ServiceMethodReference,
    method: Option<&SyntaxNode>,
    include_empty: bool,
) {
    let Some(method) = method.filter(|node| node.kind() == yamlsyntax::YAML_SCALAR) else {
        return;
    };
    base.method_name = yamlquery::scalar_value(method);
    if base.method_name.is_empty() && !include_empty {
        return;
    }
    base.range = yaml_scalar_content_range(Some(method));
    result.push(base);
}

fn append_yaml_method_item(
    result: &mut Vec<ServiceMethodReference>,
    mut base: ServiceMethodReference,
    item: &SyntaxNode,
    include_empty: bool,
) {
    if let Some(value) = yamlquery::item_value(item) {
        append_yaml_method(result, base, Some(&value), include_empty);
        return;
    }
    if !include_empty {
        return;
    }
    base.range = item.range_trimmed_trivia();
    result.push(base);
}

fn append_yaml_tag_methods(
    result: &mut Vec<ServiceMethodReference>,
    base: &ServiceMethodReference,
    tags: Option<&SyntaxNode>,
    include_empty: bool,
) {
    let Some(tags) = tags else { return };
    let values: Vec<Option<SyntaxNode>> = if yamlquery::is_sequence(tags) {
        yamlquery::items(tags)
            .map(|item| yamlquery::item_value(&item))
            .collect()
    } else {
        vec![Some(tags.clone())]
    };
    for value in values.into_iter().flatten() {
        if !yamlquery::is_mapping(&value) {
            continue;
        }
        append_yaml_method(
            result,
            base.clone(),
            yamlquery::property(&value, "method").as_ref(),
            include_empty,
        );
    }
}

fn append_yaml_factory_methods(
    result: &mut Vec<ServiceMethodReference>,
    base: &ServiceMethodReference,
    config: &SyntaxNode,
    include_empty: bool,
) {
    match yamlquery::property(config, "factory") {
        Some(factory) if yamlquery::is_sequence(&factory) => {
            let parts: Vec<SyntaxNode> = yamlquery::items(&factory).collect();
            if parts.len() >= 2 {
                let mut reference = base.clone();
                reference.factory = true;
                yaml_factory_receiver(
                    &yaml_scalar(yamlquery::item_value(&parts[0]).as_ref()),
                    &mut reference,
                );
                append_yaml_method_item(result, reference, &parts[1], include_empty);
            }
        }
        Some(factory) if factory.kind() == yamlsyntax::YAML_SCALAR => {
            if let Some(reference) =
                yaml_scalar_factory_method_reference(base, &factory, include_empty)
            {
                result.push(reference);
            }
        }
        _ => {}
    }
    let Some(method) = yamlquery::property(config, "factory_method") else {
        return;
    };
    let mut reference = base.clone();
    reference.factory = true;
    yaml_factory_receiver(
        &yaml_scalar(yamlquery::property(config, "factory_service").as_ref()),
        &mut reference,
    );
    let class_name = yaml_scalar(yamlquery::property(config, "factory_class").as_ref());
    if !class_name.is_empty() {
        reference.receiver_class = trim_backslash(&class_name);
        reference.receiver_service.clear();
    }
    append_yaml_method(result, reference, Some(&method), include_empty);
}

fn yaml_scalar_factory_method_reference(
    base: &ServiceMethodReference,
    factory: &SyntaxNode,
    include_empty: bool,
) -> Option<ServiceMethodReference> {
    let value = yamlquery::scalar_value(factory);
    let (separator, width) = match value.rfind("::") {
        Some(index) => (index, 2),
        None => (value.rfind(':')?, 1),
    };
    if separator < 1 {
        return None;
    }
    let method = &value[separator + width..];
    if method.is_empty() && !include_empty {
        return None;
    }
    let mut reference = base.clone();
    reference.factory = true;
    yaml_factory_receiver(&value[..separator], &mut reference);
    reference.method_name = method.to_owned();
    let range = yaml_scalar_content_range(Some(factory));
    let start = range.start() + TextSize::from((separator + width) as u32);
    if start > range.end() {
        return None;
    }
    reference.range = TextRange::new(start, range.end());
    Some(reference)
}

fn yaml_factory_receiver(value: &str, reference: &mut ServiceMethodReference) {
    let value = go_trim_space(value);
    if let Some((service_id, _)) = parse_service_reference(value) {
        reference.receiver_service = service_id;
        return;
    }
    let value = value.strip_prefix('@').unwrap_or(value);
    if static_php_class_name(value) {
        reference.receiver_class = trim_backslash(value);
        return;
    }
    reference.receiver_service = value.to_owned();
}

fn xml_factory_receiver(factory: &SyntaxNode, reference: &mut ServiceMethodReference) {
    let service_id = trim_at(&xml_attr(factory, "service"));
    if !service_id.is_empty() {
        reference.receiver_service = service_id;
    }
    let class_name = trim_backslash(&xml_attr(factory, "class"));
    if !class_name.is_empty() {
        reference.receiver_class = class_name;
        reference.receiver_service.clear();
    }
}

fn append_xml_method(
    result: &mut Vec<ServiceMethodReference>,
    mut base: ServiceMethodReference,
    method: Option<&SyntaxNode>,
    include_empty: bool,
) {
    let Some(method) = method else { return };
    base.method_name = xmlquery::attribute_value(method).unwrap_or_default();
    if base.method_name.is_empty() && !include_empty {
        return;
    }
    base.range = xml_attribute_content_range(Some(method));
    result.push(base);
}

fn service_method_range_contains(range: TextRange, offset: u32) -> bool {
    let offset = TextSize::from(offset);
    if range.is_empty() {
        return offset == range.start();
    }
    offset >= range.start() && offset <= range.end()
}

/// Static service references passed to constructors and calls in YAML.
pub fn yaml_service_argument_references(root: &SyntaxNode) -> Vec<ServiceArgumentReference> {
    let mut result = Vec::new();
    for service_pair in yaml_service_pairs(root) {
        let service_id = yaml_scalar(yamlquery::pair_key(&service_pair).as_ref());
        if service_id.is_empty() || service_id.starts_with('_') {
            continue;
        }
        let Some(config) = yamlquery::pair_value(&service_pair).filter(yamlquery::is_mapping)
        else {
            continue;
        };
        let base = ServiceArgumentReference {
            owner_class: yaml_service_class_name(&service_id, &config),
            owner_service_id: service_id,
            parameter_index: -1,
            format: "yaml".to_owned(),
            ..ServiceArgumentReference::default()
        };
        if yamlquery::property(&config, "factory").is_none() {
            let arguments = yamlquery::property(&config, "arguments");
            match arguments.as_ref() {
                Some(arguments) if yamlquery::is_sequence(arguments) => {
                    for (index, item) in yamlquery::items(arguments).enumerate() {
                        if let Some(mut reference) = yaml_service_argument_reference(
                            yamlquery::item_value(&item).as_ref(),
                            &base,
                        ) {
                            reference.parameter_index = index as i32;
                            result.push(reference);
                        }
                    }
                }
                Some(arguments) if yamlquery::is_mapping(arguments) => {
                    for (index, pair) in yamlquery::pairs(arguments).enumerate() {
                        let Some(mut reference) = yaml_service_argument_reference(
                            yamlquery::pair_value(&pair).as_ref(),
                            &base,
                        ) else {
                            continue;
                        };
                        let key = yaml_scalar(yamlquery::pair_key(&pair).as_ref());
                        if key.starts_with('$') {
                            reference.parameter_name = key;
                        } else if numeric_index(&key) >= 0 {
                            reference.parameter_index = numeric_index(&key);
                        } else {
                            reference.parameter_index = index as i32;
                        }
                        result.push(reference);
                    }
                }
                _ => {
                    if let Some(mut reference) =
                        yaml_service_argument_reference(arguments.as_ref(), &base)
                    {
                        reference.parameter_index = 0;
                        result.push(reference);
                    }
                }
            }
        }
        result.extend(yaml_service_call_references(&config, &base));
    }
    result
}

fn yaml_service_argument_reference(
    value: Option<&SyntaxNode>,
    base: &ServiceArgumentReference,
) -> Option<ServiceArgumentReference> {
    let value = value?;
    let (service_id, _) = parse_service_reference(&yamlquery::scalar_value(value))?;
    Some(ServiceArgumentReference {
        service_id,
        range: yaml_scalar_content_range(Some(value)),
        ..base.clone()
    })
}

fn yaml_service_call_references(
    config: &SyntaxNode,
    base: &ServiceArgumentReference,
) -> Vec<ServiceArgumentReference> {
    let Some(calls) = yamlquery::property(config, "calls").filter(yamlquery::is_sequence) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for call_item in yamlquery::items(&calls) {
        let Some(call) = yamlquery::item_value(&call_item) else {
            continue;
        };
        if yamlquery::is_sequence(&call) {
            let parts: Vec<SyntaxNode> = yamlquery::items(&call).collect();
            if parts.len() < 2 {
                continue;
            }
            append_yaml_call_arguments(
                &mut result,
                base,
                &yaml_scalar(yamlquery::item_value(&parts[0]).as_ref()),
                yamlquery::item_value(&parts[1]).as_ref(),
            );
        } else if yamlquery::is_mapping(&call) {
            for pair in yamlquery::pairs(&call) {
                append_yaml_call_arguments(
                    &mut result,
                    base,
                    &yaml_scalar(yamlquery::pair_key(&pair).as_ref()),
                    yamlquery::pair_value(&pair).as_ref(),
                );
            }
        }
    }
    result
}

fn append_yaml_call_arguments(
    result: &mut Vec<ServiceArgumentReference>,
    base: &ServiceArgumentReference,
    method_name: &str,
    arguments: Option<&SyntaxNode>,
) {
    let Some(arguments) =
        arguments.filter(|node| !method_name.is_empty() && yamlquery::is_sequence(node))
    else {
        return;
    };
    for (index, argument) in yamlquery::items(arguments).enumerate() {
        let Some(mut reference) =
            yaml_service_argument_reference(yamlquery::item_value(&argument).as_ref(), base)
        else {
            continue;
        };
        reference.method_name = method_name.to_owned();
        reference.parameter_index = index as i32;
        result.push(reference);
    }
}

/// Static service references passed to constructors and calls in XML.
pub fn xml_service_argument_references(root: &SyntaxNode) -> Vec<ServiceArgumentReference> {
    let mut result = Vec::new();
    for service in xmlquery::elements(root, &["service"]) {
        let (owner_service_id, owner_class) = xml_owner(&service);
        let base = ServiceArgumentReference {
            owner_service_id,
            owner_class,
            parameter_index: -1,
            format: "xml".to_owned(),
            ..ServiceArgumentReference::default()
        };
        if xmlquery::child_element(&service, &["factory"]).is_none() {
            result.extend(xml_arguments(
                &xmlquery::child_elements(&service, &["argument"]),
                &base,
            ));
        }
        for call in xmlquery::child_elements(&service, &["call"]) {
            let method_name = xml_attr(&call, "method");
            if method_name.is_empty() {
                continue;
            }
            let call_base = ServiceArgumentReference {
                method_name,
                ..base.clone()
            };
            result.extend(xml_arguments(
                &xmlquery::child_elements(&call, &["argument"]),
                &call_base,
            ));
        }
    }
    result
}

fn xml_arguments(
    arguments: &[SyntaxNode],
    base: &ServiceArgumentReference,
) -> Vec<ServiceArgumentReference> {
    let mut result = Vec::new();
    for (index, argument) in arguments.iter().enumerate() {
        if !xml_attr(argument, "type").eq_ignore_ascii_case("service") {
            continue;
        }
        let id_attribute = xmlquery::attribute(argument, "id");
        let id = id_attribute
            .as_ref()
            .and_then(xmlquery::attribute_value)
            .unwrap_or_default();
        let Some(service_id) = xml_static_service_reference(&id) else {
            continue;
        };
        let mut reference = ServiceArgumentReference {
            service_id,
            parameter_index: index as i32,
            range: xml_attribute_content_range(id_attribute.as_ref()),
            ..base.clone()
        };
        let key = xml_attr(argument, "key");
        if key.starts_with('$') {
            reference.parameter_name = key;
            reference.parameter_index = -1;
        } else if numeric_index(&key) >= 0 {
            reference.parameter_index = numeric_index(&key);
        }
        result.push(reference);
    }
    result
}

fn xml_static_service_reference(value: &str) -> Option<String> {
    let value = go_trim_space(value);
    if let Some((service_id, _)) = parse_service_reference(value) {
        return Some(service_id);
    }
    let value = value.strip_prefix('@').unwrap_or(value);
    (!value.is_empty() && !value.contains(['%', '$', '{', '}'])).then(|| value.to_owned())
}

#[cfg(test)]
#[path = "tests_refs/service_argument_reference.rs"]
mod tests;
