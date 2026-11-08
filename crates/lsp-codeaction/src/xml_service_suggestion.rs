//! Go `xml_service_suggestion_codeaction.go`: the Symfony plugin's "Suggest
//! Service" XML intention, one rewrite per compatible constructor service.

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, BoxError, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_xml::query as xmlquery;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Parameter;
use swlsp_php_types::{Kind, Type, equal_fold, go_to_lower};
use swlsp_symfony::{Service, ServiceIndex};
use swlsp_syntax::SyntaxNode;

use crate::service_tag::{html_escape, xml_attribute_value, xml_element_named};
use crate::shared::{end_of, offset_range, start_of};

pub struct XmlServiceSuggestionCodeActionProvider {
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

impl XmlServiceSuggestionCodeActionProvider {
    pub fn new(service_index: Option<Arc<ServiceIndex>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            service_index,
            php_index,
        }
    }
}

impl ActionProvider for XmlServiceSuggestionCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let (Some(service_index), Some(php_index)) =
            (self.service_index.as_ref(), self.php_index.as_ref())
        else {
            return Vec::new();
        };
        if ctx.is_cancelled() || request.root.is_none() {
            return Vec::new();
        }
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Xml) {
            return Vec::new();
        }
        let mut argument = xmlquery::element_at(node);
        while let Some(current) = argument.as_ref() {
            if xml_element_named(current) == "argument" {
                break;
            }
            argument = xmlquery::parent_element(current);
        }
        let Some(argument) = argument else {
            return Vec::new();
        };
        let mut service = xmlquery::parent_element(&argument);
        while let Some(current) = service.as_ref() {
            if xml_element_named(current) == "service" {
                break;
            }
            service = xmlquery::parent_element(current);
        }
        let Some(service) = service else {
            return Vec::new();
        };
        let mut class_name = xml_attribute_value(&service, "class");
        if class_name.is_empty() {
            class_name = xml_attribute_value(&service, "id");
        }
        let class_name = class_name.trim().trim_matches('\\');
        if class_name.is_empty() || class_name.contains('%') {
            return Vec::new();
        }
        let Some(parameter) =
            xml_constructor_argument_parameter(php_index, class_name, &service, &argument)
        else {
            return Vec::new();
        };
        if !xml_injectable_parameter_type(&parameter.ty) {
            return Vec::new();
        }
        let Ok(candidates) = xml_compatible_services(service_index, php_index, &parameter.ty)
        else {
            return Vec::new();
        };
        let current_id = xml_attribute_value(&argument, "id");
        let current_type = xml_attribute_value(&argument, "type");
        let owner_id = xml_attribute_value(&service, "id");
        let mut result = Vec::with_capacity(candidates.len());
        for candidate in candidates {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if candidate.id == owner_id
                || (candidate.id == current_id && equal_fold(&current_type, "service"))
            {
                continue;
            }
            let replacement = xml_suggested_service_argument(&argument, &candidate.id);
            if replacement.is_empty() {
                continue;
            }
            result.push(protocol::CodeAction {
                title: format!(
                    "Symfony: Use service '{}' for {}",
                    candidate.id,
                    xml_parameter_display_name(&parameter)
                ),
                kind: Some(CodeActionKind::REFACTOR_REWRITE),
                edit: Some(protocol::WorkspaceEdit {
                    changes: BTreeMap::from([(
                        request.params.text_document.uri.clone(),
                        vec![protocol::TextEdit::new(
                            offset_range(request, start_of(&argument), end_of(&argument)),
                            replacement,
                        )],
                    )]),
                    ..protocol::WorkspaceEdit::default()
                }),
                ..protocol::CodeAction::default()
            });
        }
        result
    }
}

fn xml_constructor_argument_parameter(
    php_index: &PhpIndex,
    class_name: &str,
    service: &SyntaxNode,
    target: &SyntaxNode,
) -> Option<Parameter> {
    let constructors = php_index.find_methods(class_name, "__construct");
    let constructor = constructors.first()?;
    let key_value = xml_attribute_value(target, "key");
    let key = key_value.strip_prefix('$').unwrap_or(&key_value);
    if !key.is_empty() {
        return constructor
            .parameters
            .iter()
            .find(|parameter| {
                let name: &str = &parameter.name;
                equal_fold(name.strip_prefix('$').unwrap_or(name), key)
            })
            .cloned();
    }
    let mut index = 0;
    for argument in xmlquery::child_elements(service, &["argument"]) {
        if &argument == target {
            break;
        }
        if xml_attribute_value(&argument, "key").is_empty() {
            index += 1;
        }
    }
    constructor.parameters.get(index).cloned()
}

fn xml_injectable_parameter_type(value: &Type) -> bool {
    match value.kind() {
        Kind::Object => !value.name().is_empty(),
        Kind::Union | Kind::Intersection => {
            value.arguments().iter().any(xml_injectable_parameter_type)
        }
        _ => false,
    }
}

fn xml_named_parameter_types(value: &Type) -> Vec<String> {
    match value.kind() {
        Kind::Object if !value.name().is_empty() => vec![value.name().to_owned()],
        Kind::Union | Kind::Intersection => value
            .arguments()
            .iter()
            .flat_map(xml_named_parameter_types)
            .collect(),
        _ => Vec::new(),
    }
}

fn xml_compatible_services(
    service_index: &ServiceIndex,
    php_index: &PhpIndex,
    expected: &Type,
) -> Result<Vec<Service>, BoxError> {
    let snapshot = php_index.semantic_snapshot();
    let relations = snapshot.relations();
    let mut by_id: HashMap<String, Service> = HashMap::new();
    for type_name in xml_named_parameter_types(expected) {
        for service in service_index.get_services_by_type(&type_name)? {
            let mut class_name = service.class.strip_prefix('\\').unwrap_or(&service.class);
            if class_name.is_empty() && service.id.contains('\\') {
                class_name = service.id.strip_prefix('\\').unwrap_or(&service.id);
            }
            if class_name.is_empty()
                || !relations.is_assignable_to(&Type::named(class_name, Vec::new()), expected)
            {
                continue;
            }
            by_id.insert(go_to_lower(&service.id), service);
        }
    }
    let mut result: Vec<Service> = by_id.into_values().collect();
    result.sort_by_key(|service| go_to_lower(&service.id));
    Ok(result)
}

fn xml_suggested_service_argument(argument: &SyntaxNode, service_id: &str) -> String {
    if service_id.is_empty() {
        return String::new();
    }
    let source_attributes = xmlquery::attributes(argument);
    let mut attributes = Vec::with_capacity(source_attributes.len() + 2);
    let (mut has_type, mut has_id) = (false, false);
    for attribute in source_attributes {
        match xmlquery::attribute_name(&attribute).as_deref() {
            Some("type") => {
                attributes.push("type=\"service\"".to_owned());
                has_type = true;
            }
            Some("id") => {
                attributes.push(format!("id=\"{}\"", html_escape(service_id)));
                has_id = true;
            }
            _ => attributes.push(attribute.text().to_string().trim().to_owned()),
        }
    }
    if !has_type {
        attributes.push("type=\"service\"".to_owned());
    }
    if !has_id {
        attributes.push(format!("id=\"{}\"", html_escape(service_id)));
    }
    format!("<argument {}/>", attributes.join(" "))
}

fn xml_parameter_display_name(parameter: &Parameter) -> String {
    let name: &str = &parameter.name;
    let name = name.strip_prefix('$').unwrap_or(name);
    if name.is_empty() {
        return "constructor argument".to_owned();
    }
    format!("${name}")
}

#[cfg(test)]
#[path = "tests/xml_service_suggestion.rs"]
mod tests;
