//! Parameter hints for service arguments in XML and YAML container files.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Parameter, Symbol};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::{inlay_hint_byte_range, position};

pub struct ServiceArgumentProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ServiceArgumentProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }
}

#[derive(Clone, Default)]
struct Descriptor {
    class_name: String,
    alias: String,
}

#[derive(Clone, Default)]
struct Binding {
    service_id: String,
    class_name: String,
    method_name: String,
    parameter: String,
    index: i64,
    hint_position: u32,
}

type Services = HashMap<String, Descriptor>;

impl InlayHintProvider for ServiceArgumentProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let (services, bindings) = match document.language {
            Some(LanguageId::Xml) => xml_service_argument_bindings(&root),
            Some(LanguageId::Yaml) => yaml_service_argument_bindings(&root),
            _ => return Ok(Vec::new()),
        };
        let (range_start, range_end) = inlay_hint_byte_range(document, request.params.range);
        let mut result = Vec::new();
        for binding in bindings {
            if ctx.is_cancelled() {
                return Ok(result);
            }
            if binding.hint_position < range_start || binding.hint_position > range_end {
                continue;
            }
            let Some(class_name) =
                self.resolve_service_class(&binding.service_id, &binding.class_name, &services)?
            else {
                continue;
            };
            let method_name = if binding.method_name.is_empty() {
                "__construct"
            } else {
                &binding.method_name
            };
            let methods = php.find_methods(&class_name, method_name);
            let Some(method) = methods.first() else {
                continue;
            };
            let Some(parameter) = service_binding_parameter(method, &binding) else {
                continue;
            };
            let label = service_parameter_hint_label(parameter);
            if label.is_empty() {
                continue;
            }
            result.push(protocol::InlayHint {
                position: position(document, binding.hint_position),
                label: label.into(),
                kind: Some(InlayHintKind::PARAMETER),
                tooltip: service_parameter_hint_tooltip(parameter),
                padding_left: true,
                ..Default::default()
            });
        }
        Ok(result)
    }
}

impl ServiceArgumentProvider {
    fn resolve_service_class(
        &self,
        service_id: &str,
        explicit_class: &str,
        local: &Services,
    ) -> ProviderResult<Option<String>> {
        let class_name = static_service_class_name(explicit_class);
        if !class_name.is_empty() {
            return Ok(Some(class_name));
        }
        let mut visited = HashSet::new();
        let mut current = service_id.to_string();
        while !current.is_empty() {
            let key = lowercase(&current);
            if !visited.insert(key.clone()) {
                return Ok(None);
            }
            let Some(descriptor) = local.get(&key) else {
                break;
            };
            let class_name = static_service_class_name(&descriptor.class_name);
            if !class_name.is_empty() {
                return Ok(Some(class_name));
            }
            if descriptor.alias.is_empty() {
                break;
            }
            current = descriptor
                .alias
                .strip_prefix('@')
                .unwrap_or(&descriptor.alias)
                .to_string();
        }
        let class_name = static_service_class_name(&current);
        if !class_name.is_empty() && class_name.contains('\\') {
            return Ok(Some(class_name));
        }
        let Some(services) = &self.services else {
            return Ok(None);
        };
        Ok(services.resolve_service_class_name(&current)?)
    }
}

fn service_binding_parameter<'m>(method: &'m Symbol, binding: &Binding) -> Option<&'m Parameter> {
    if !binding.parameter.is_empty() {
        let wanted = binding
            .parameter
            .strip_prefix('$')
            .unwrap_or(&binding.parameter);
        return method.parameters.iter().find(|parameter| {
            eq_ignore_case(
                parameter.name.strip_prefix('$').unwrap_or(&parameter.name),
                wanted,
            )
        });
    }
    if binding.index < 0 {
        return None;
    }
    method.parameters.get(binding.index as usize)
}

fn service_parameter_hint_label(parameter: &Parameter) -> String {
    let class_name = first_object_type_name(&parameter.ty);
    if !class_name.is_empty() {
        return match class_name.rfind('\\') {
            Some(separator) => class_name[separator + 1..].to_string(),
            None => class_name,
        };
    }
    parameter
        .name
        .strip_prefix('$')
        .unwrap_or(&parameter.name)
        .to_string()
}

fn service_parameter_hint_tooltip(parameter: &Parameter) -> String {
    let name = parameter.name.strip_prefix('$').unwrap_or(&parameter.name);
    if name.is_empty() {
        return parameter.ty.to_string();
    }
    format!("${name}: {}", parameter.ty)
}

fn first_object_type_name(value: &Type) -> String {
    match value.kind() {
        TypeKind::Object => value.name().trim_matches(['\\', ' ']).to_string(),
        TypeKind::Union | TypeKind::Intersection => value
            .arguments()
            .iter()
            .map(first_object_type_name)
            .find(|name| !name.is_empty())
            .unwrap_or_default(),
        _ => String::new(),
    }
}

fn xml_attribute(node: &SyntaxNode, name: &str) -> String {
    xmlquery::attribute(node, name)
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default()
}

fn xml_service_argument_bindings(root: &SyntaxNode) -> (Services, Vec<Binding>) {
    let mut services = Services::new();
    let mut result = Vec::new();
    for service in xmlquery::elements(root, &["service"]) {
        let service_id = xml_attribute(&service, "id");
        let class_name = xml_attribute(&service, "class");
        services.insert(
            lowercase(&service_id),
            Descriptor {
                class_name: class_name.clone(),
                alias: xml_attribute(&service, "alias"),
            },
        );
        if xmlquery::child_element(&service, &["factory"]).is_none()
            && xmlquery::attribute(&service, "factory").is_none()
        {
            result.extend(xml_argument_bindings(
                &service_id,
                &class_name,
                "",
                &xmlquery::child_elements(&service, &["argument"]),
            ));
        }
        for call in xmlquery::child_elements(&service, &["call"]) {
            let method_name = xml_attribute(&call, "method");
            if method_name.is_empty() {
                continue;
            }
            result.extend(xml_argument_bindings(
                &service_id,
                &class_name,
                &method_name,
                &xmlquery::child_elements(&call, &["argument"]),
            ));
        }
    }
    for alias in xmlquery::elements(root, &["alias"]) {
        services.insert(
            lowercase(&xml_attribute(&alias, "id")),
            Descriptor {
                class_name: String::new(),
                alias: xml_attribute(&alias, "alias"),
            },
        );
    }
    (services, result)
}

fn keyed_binding(binding: &mut Binding, key: &str, positional_index: &mut i64) {
    if key.starts_with('$') {
        binding.parameter = key.to_string();
        binding.index = -1;
    } else if let Some(index) = numeric_service_argument_index(key) {
        binding.index = index;
    } else {
        *positional_index += 1;
    }
}

fn xml_argument_bindings(
    service_id: &str,
    class_name: &str,
    method_name: &str,
    arguments: &[SyntaxNode],
) -> Vec<Binding> {
    let mut result = Vec::with_capacity(arguments.len());
    let mut positional_index = 0;
    for argument in arguments {
        let key = xml_attribute(argument, "key");
        let mut binding = Binding {
            service_id: service_id.to_string(),
            class_name: class_name.to_string(),
            method_name: method_name.to_string(),
            index: positional_index,
            hint_position: argument.range_trimmed_trivia().end().into(),
            ..Default::default()
        };
        keyed_binding(&mut binding, &key, &mut positional_index);
        result.push(binding);
    }
    result
}

fn yaml_scalar(node: Option<&SyntaxNode>) -> String {
    node.map(yamlquery::scalar_value).unwrap_or_default()
}

fn yaml_is_mapping(node: Option<&SyntaxNode>) -> bool {
    node.is_some_and(yamlquery::is_mapping)
}

fn yaml_is_sequence(node: Option<&SyntaxNode>) -> bool {
    node.is_some_and(yamlquery::is_sequence)
}

fn yaml_property(node: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    yamlquery::property(node?, name)
}

fn yaml_service_argument_bindings(root: &SyntaxNode) -> (Services, Vec<Binding>) {
    let mut services = Services::new();
    let root_value = yamlquery::root_value(root);
    let service_mapping = yaml_property(root_value.as_ref(), "services");
    let Some(service_mapping) = service_mapping.filter(yamlquery::is_mapping) else {
        return (services, Vec::new());
    };
    let mut result = Vec::new();
    for service_pair in yamlquery::pairs(&service_mapping) {
        let service_id = yaml_scalar(yamlquery::pair_key(&service_pair).as_ref());
        if service_id.is_empty() || service_id.starts_with('_') {
            continue;
        }
        let config = yamlquery::pair_value(&service_pair);
        let config = config.as_ref();
        let mut descriptor = Descriptor::default();
        if yaml_is_mapping(config) {
            descriptor.class_name = yaml_scalar(yaml_property(config, "class").as_ref());
            descriptor.alias = yaml_scalar(yaml_property(config, "alias").as_ref());
        } else {
            let alias = yaml_scalar(config);
            descriptor.alias = alias.strip_prefix('@').unwrap_or(&alias).to_string();
        }
        services.insert(lowercase(&service_id), descriptor.clone());
        if !yaml_is_mapping(config) {
            continue;
        }
        if yaml_property(config, "factory").is_none() {
            result.extend(yaml_argument_bindings(
                &service_id,
                &descriptor.class_name,
                "",
                yaml_property(config, "arguments").as_ref(),
            ));
        }
        let calls = yaml_property(config, "calls");
        let Some(calls) = calls.filter(yamlquery::is_sequence) else {
            continue;
        };
        for call_item in yamlquery::items(&calls) {
            let call = yamlquery::item_value(&call_item);
            let Some(call) = call.filter(yamlquery::is_sequence) else {
                continue;
            };
            let parts: Vec<SyntaxNode> = yamlquery::items(&call).collect();
            if parts.len() < 2 {
                continue;
            }
            let method_name = yaml_scalar(yamlquery::item_value(&parts[0]).as_ref());
            if method_name.is_empty() {
                continue;
            }
            result.extend(yaml_argument_bindings(
                &service_id,
                &descriptor.class_name,
                &method_name,
                yamlquery::item_value(&parts[1]).as_ref(),
            ));
        }
    }
    (services, result)
}

fn yaml_argument_bindings(
    service_id: &str,
    class_name: &str,
    method_name: &str,
    arguments: Option<&SyntaxNode>,
) -> Vec<Binding> {
    let Some(arguments) = arguments else {
        return Vec::new();
    };
    let binding = |index: i64, value: &SyntaxNode| Binding {
        service_id: service_id.to_string(),
        class_name: class_name.to_string(),
        method_name: method_name.to_string(),
        index,
        hint_position: value.range_trimmed_trivia().end().into(),
        ..Default::default()
    };
    let mut result = Vec::new();
    if yaml_is_sequence(Some(arguments)) {
        for (index, item) in yamlquery::items(arguments).enumerate() {
            let value = yamlquery::item_value(&item);
            let Some(value) = value.filter(|value| yaml_hintable_argument(Some(value))) else {
                continue;
            };
            result.push(binding(index as i64, &value));
        }
    } else if yaml_is_mapping(Some(arguments)) {
        let mut positional_index = 0;
        for pair in yamlquery::pairs(arguments) {
            let value = yamlquery::pair_value(&pair);
            let Some(value) = value.filter(|value| yaml_hintable_argument(Some(value))) else {
                continue;
            };
            let key = yaml_scalar(yamlquery::pair_key(&pair).as_ref());
            let mut current = binding(positional_index, &value);
            keyed_binding(&mut current, &key, &mut positional_index);
            result.push(current);
        }
    } else if yaml_hintable_argument(Some(arguments)) {
        result.push(binding(0, arguments));
    }
    result
}

fn yaml_hintable_argument(node: Option<&SyntaxNode>) -> bool {
    node.is_some_and(|node| !yamlquery::scalar_value(node).is_empty() || yamlquery::is_null(node))
}

fn numeric_service_argument_index(value: &str) -> Option<i64> {
    if value.is_empty() {
        return None;
    }
    value.parse::<i64>().ok().filter(|index| *index >= 0)
}

fn static_service_class_name(value: &str) -> String {
    let value = value.trim_matches(['\\', ' ']);
    if value.is_empty() || value.contains(['%', '$', '{', '}', ' ', '\t', '\r', '\n']) {
        return String::new();
    }
    value.to_string()
}
