//! Symfony service, parameter, tag and class completion in YAML, XML and PHP
//! configuration (port of Go `service_completion.go`).

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::{PhpContext, PhpIndex, assistant_argument_reference};
use swlsp_php_semantic::{Flags, Visibility};
use swlsp_symfony::{
    PhpArrayServiceMethodReference, PhpAutowireCallableMethodReference, PhpConfigReferenceKind,
    ServiceIndex,
};
use swlsp_syntax::{SyntaxNode, TextRange};

use super::{lower_ext, markdown, request_offset};
use crate::common::{go_to_lower, php_context, protocol_range, text_edit};

use protocol::CompletionItemKind as Kind;

/// Completes Symfony services, parameters, tags and classes (Go
/// `SymfonyCompletionProvider`).
pub struct ServiceCompletionProvider {
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

impl ServiceCompletionProvider {
    pub fn new(service_index: Option<Arc<ServiceIndex>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            service_index,
            php_index,
        }
    }
}

impl CompletionProvider for ServiceCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        if request.node.is_none() {
            return Vec::new();
        }
        match lower_ext(&request.params.text_document.uri).as_str() {
            ".yaml" | ".yml" => self.yaml_completions(request),
            ".xml" => self.xml_completion(request),
            ".php" => self.php_completions(request),
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["\"", "'", "%", "$", ":"].map(String::from).to_vec()
    }
}

fn item(label: impl Into<String>, kind: Kind) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label: label.into(),
        kind: Some(kind),
        ..Default::default()
    }
}

fn sort_by_lower_label(items: &mut [protocol::CompletionItem]) {
    crate::common::sort_completion_items(items);
}

impl ServiceCompletionProvider {
    fn php_completions(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let context = self
            .php_index
            .as_ref()
            .and_then(|index| php_context(index, &request.syntax));
        if let Some(context) = context.as_ref() {
            if let Some(reference) = assistant_argument_reference(context, node, "Service") {
                return self.assistant_service_completion_items(request, reference);
            }
            if let Some(reference) = assistant_argument_reference(context, node, "Parameter") {
                return self.assistant_parameter_completion_items(request, reference);
            }
        }
        if swlsp_symfony::php_when_environment_at(node) {
            return ["prod", "dev", "test", "never"]
                .into_iter()
                .map(|environment| item(environment, Kind::VALUE))
                .collect();
        }
        if let Some(reference) = swlsp_symfony::php_autowire_callable_method_at(node) {
            return self.autowire_callable_method_completions(request, &reference);
        }
        if let Some(root) = request.root.as_ref()
            && let Some(reference) = swlsp_symfony::php_array_service_method_at(root, node)
        {
            return self.php_array_service_method_completions(request, &reference);
        }
        let mut kind = swlsp_symfony::php_array_service_reference_at(node);
        if kind == PhpConfigReferenceKind::Parameter
            && swlsp_symfony::php_array_parameter_completion_at(node)
        {
            return self.php_array_parameter_completions(request, node);
        }
        if kind == PhpConfigReferenceKind::None {
            kind = swlsp_symfony::php_attribute_reference_at(node);
        }
        if kind == PhpConfigReferenceKind::None
            && phpquery::string_argument_index(node) == Some(0)
            && self.is_parameter_access_call(context.as_ref(), node)
        {
            kind = PhpConfigReferenceKind::Parameter;
        }
        if kind == PhpConfigReferenceKind::None
            && (request.source().contains("ContainerConfigurator")
                || swlsp_symfony::php_array_service_context_at(node))
        {
            kind = swlsp_symfony::php_config_reference_at(node);
        }
        if kind == PhpConfigReferenceKind::None
            && phpquery::string_in_call(node, 0, &["get", "has"])
            && self.is_container_call(context.as_ref(), node)
        {
            kind = PhpConfigReferenceKind::Service;
        }
        match kind {
            PhpConfigReferenceKind::Service => self.service_completion_items("", ""),
            PhpConfigReferenceKind::Parameter => {
                let Some(parameters) = self.parameters() else {
                    return Vec::new();
                };
                parameters
                    .into_iter()
                    .map(|parameter| item(parameter.name, Kind::CONSTANT))
                    .collect()
            }
            PhpConfigReferenceKind::Tag => {
                let Some(tags) = self.tags() else {
                    return Vec::new();
                };
                tags.into_iter()
                    .map(|tag| item(tag, Kind::CONSTANT))
                    .collect()
            }
            PhpConfigReferenceKind::Class => {
                let Some(php) = self.php_index.as_ref() else {
                    return Vec::new();
                };
                php.class_names_view()
                    .iter()
                    .map(|name| item(name.strip_prefix('\\').unwrap_or(name), Kind::CLASS))
                    .collect()
            }
            PhpConfigReferenceKind::None => Vec::new(),
        }
    }

    fn parameters(&self) -> Option<Vec<swlsp_symfony::Parameter>> {
        self.service_index.as_ref()?.get_all_parameters().ok()
    }

    fn tags(&self) -> Option<Vec<String>> {
        self.service_index.as_ref()?.get_all_tags().ok()
    }

    fn assistant_service_completion_items(
        &self,
        request: &CompletionRequest,
        reference: TextRange,
    ) -> Vec<protocol::CompletionItem> {
        if self.service_index.is_none() {
            return Vec::new();
        }
        let mut items = self.service_completion_items("", "");
        let Some(line_index) = request.line_index() else {
            return items;
        };
        let replacement = protocol_range(reference, line_index);
        for item in &mut items {
            item.text_edit = Some(text_edit(replacement, item.label.clone()));
        }
        items
    }

    fn assistant_parameter_completion_items(
        &self,
        request: &CompletionRequest,
        reference: TextRange,
    ) -> Vec<protocol::CompletionItem> {
        let Some(parameters) = self.parameters() else {
            return Vec::new();
        };
        let replacement = request
            .line_index()
            .map(|line_index| protocol_range(reference, line_index));
        parameters
            .into_iter()
            .map(|parameter| {
                let mut entry = item(parameter.name.clone(), Kind::REFERENCE);
                if let Some(replacement) = replacement {
                    entry.text_edit = Some(text_edit(replacement, parameter.name));
                }
                entry
            })
            .collect()
    }

    fn php_array_parameter_completions(
        &self,
        request: &CompletionRequest,
        node: &SyntaxNode,
    ) -> Vec<protocol::CompletionItem> {
        let Some(parameters) = self.parameters() else {
            return Vec::new();
        };
        let content_range = phpquery::string_at(node)
            .map(|literal| phpquery::string_content_range(&literal))
            .unwrap_or_default();
        parameters
            .into_iter()
            .map(|parameter| {
                let value = format!("%{}%", parameter.name);
                let mut entry = item(value.clone(), Kind::REFERENCE);
                if let Some(line_index) = request.line_index() {
                    entry.text_edit =
                        Some(text_edit(protocol_range(content_range, line_index), value));
                }
                entry
            })
            .collect()
    }

    fn method_items(
        &self,
        request: &CompletionRequest,
        class_name: &str,
        range: TextRange,
        allow_static: bool,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = self.php_index.as_ref() else {
            return Vec::new();
        };
        let methods = php.methods(class_name);
        let mut items = Vec::with_capacity(methods.len());
        for method in &methods {
            if method.visibility != Visibility::Public
                || !allow_static && method.flags.has(Flags::STATIC)
            {
                continue;
            }
            let mut entry = item(&*method.name, Kind::METHOD);
            entry.detail = format!("{class_name}::{}()", &*method.name);
            if let Some(line_index) = request.line_index() {
                entry.text_edit = Some(text_edit(
                    protocol_range(range, line_index),
                    method.name.to_string(),
                ));
            }
            items.push(entry);
        }
        sort_by_lower_label(&mut items);
        items
    }

    fn autowire_callable_method_completions(
        &self,
        request: &CompletionRequest,
        reference: &PhpAutowireCallableMethodReference,
    ) -> Vec<protocol::CompletionItem> {
        let class_name =
            self.service_or_class_name(request.root.as_ref(), &reference.class, &reference.service);
        if class_name.is_empty() {
            return Vec::new();
        }
        self.method_items(request, &class_name, reference.range, false)
    }

    fn php_array_service_method_completions(
        &self,
        request: &CompletionRequest,
        reference: &PhpArrayServiceMethodReference,
    ) -> Vec<protocol::CompletionItem> {
        let class_name =
            self.service_or_class_name(request.root.as_ref(), &reference.class, &reference.service);
        if class_name.is_empty() {
            return Vec::new();
        }
        self.method_items(
            request,
            &class_name,
            reference.range,
            reference.allow_static,
        )
    }

    fn service_or_class_name(
        &self,
        root: Option<&SyntaxNode>,
        class_name: &str,
        service_id: &str,
    ) -> String {
        if !class_name.is_empty()
            && let Some(root) = root
        {
            let resolved = swlsp_php::NameResolver::new(root).resolve(class_name);
            return resolved.strip_prefix('\\').unwrap_or(&resolved).to_string();
        }
        let mut service_id = service_id.trim().to_string();
        if let Some((normalized, _)) = swlsp_symfony::parse_service_reference(&service_id) {
            service_id = normalized;
        }
        if let Some(index) = self.service_index.as_ref()
            && let Ok(Some(class_name)) = index.resolve_service_class_name(&service_id)
        {
            return class_name;
        }
        if let Some(php) = self.php_index.as_ref()
            && let Some(class) = php.find_class(&service_id)
        {
            return class
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&class.fully_qualified)
                .to_string();
        }
        String::new()
    }

    fn is_parameter_access_call(&self, context: Option<&PhpContext>, node: &SyntaxNode) -> bool {
        let Some(php) = self.php_index.as_ref() else {
            return false;
        };
        swlsp_symfony::php_parameter_access_types(&phpquery::call_method_name(node))
            .into_iter()
            .any(|class_name| php.is_method_called_on_class(context, node, class_name))
    }

    fn is_container_call(&self, context: Option<&PhpContext>, node: &SyntaxNode) -> bool {
        if self.is_parameter_access_call(context, node) {
            return false;
        }
        let Some(php) = self.php_index.as_ref() else {
            return false;
        };
        [
            "Psr\\Container\\ContainerInterface",
            "Symfony\\Component\\DependencyInjection\\ContainerInterface",
            "Symfony\\Contracts\\Service\\ServiceProviderInterface",
        ]
        .into_iter()
        .any(|class_name| php.is_method_called_on_class(context, node, class_name))
    }

    fn xml_completion(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if !go_to_lower(&request.params.text_document.uri).ends_with(".xml") {
            return Vec::new();
        }
        if let Some(items) = self.configured_service_method_completions(request) {
            return items;
        }
        if swlsp_symfony::xml_service_is_service_reference(node) {
            let current = swlsp_symfony::xml_current_service_id(node);
            return self.service_completion_items("", &current);
        }
        if swlsp_symfony::xml_service_is_argument_tag(node) {
            let Some(tags) = self.tags() else {
                return Vec::new();
            };
            return tags
                .into_iter()
                .map(|tag| item(tag, Kind::VARIABLE))
                .collect();
        }
        if !swlsp_symfony::parameter_references(&swlsp_symfony::xml_context_value(node)).is_empty()
        {
            let Some(parameters) = self.parameters() else {
                return Vec::new();
            };
            let index = self.service_index.as_ref();
            return parameters
                .into_iter()
                .map(|parameter| {
                    let mut entry = item(parameter.name.clone(), Kind::CONSTANT);
                    entry.insert_text = format!("{}%", parameter.name);
                    if let Some(index) = index
                        && let Ok(Some(value)) = index.get_parameter_by_name(&parameter.name)
                    {
                        entry.documentation = markdown(format!(
                            "**Parameter:** `{}`\n\n**Value:** `{}`",
                            parameter.name, value.value
                        ));
                    }
                    entry
                })
                .collect();
        }
        if swlsp_symfony::xml_service_is_tag_element(node) {
            let Some(tags) = self.tags() else {
                return Vec::new();
            };
            return tags
                .into_iter()
                .map(|tag| item(tag, Kind::VARIABLE))
                .collect();
        }
        if swlsp_symfony::xml_service_is_service_id(node) {
            return self.class_completion_items();
        }
        Vec::new()
    }

    fn yaml_completions(&self, request: &CompletionRequest) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        if let Some(items) = self.configured_service_method_completions(request) {
            return items;
        }
        if let Some(items) = self.yaml_named_argument_completions(request) {
            return items;
        }
        if swlsp_symfony::is_yaml_service_id(node)
            || swlsp_symfony::is_yaml_class_property_in_service(node)
        {
            return self.class_completion_items();
        }
        if swlsp_symfony::is_yaml_argument_service_id(node) {
            return self.service_completion_items("@", "");
        }
        if !swlsp_symfony::parameter_references(&swlsp_symfony::yaml_context_value(node)).is_empty()
        {
            let Some(parameters) = self.parameters() else {
                return Vec::new();
            };
            return parameters
                .into_iter()
                .map(|parameter| item(parameter.name, Kind::CONSTANT))
                .collect();
        }
        Vec::new()
    }

    /// `Some` when the cursor is on a configured service method reference
    /// (the Go `matched` flag).
    fn configured_service_method_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let php = self.php_index.as_ref()?;
        let root = request.root.as_ref()?;
        let line_index = request.line_index()?;
        let offset = request_offset(request)?;
        let reference = match lower_ext(&request.params.text_document.uri).as_str() {
            ".yaml" | ".yml" => swlsp_symfony::yaml_service_method_reference_at(root, offset),
            ".xml" => swlsp_symfony::xml_service_method_reference_at(root, offset),
            _ => None,
        }?;
        let (service_id, explicit_class) = reference.receiver();
        let local = configured_service_map(request);
        let class_name = match swlsp_symfony::resolve_configured_service_class(
            service_id,
            explicit_class,
            &local,
            self.service_index.as_deref(),
        ) {
            Ok(Some(class_name)) => class_name,
            _ => return Some(Vec::new()),
        };
        let methods = php.methods(&class_name);
        let mut items = Vec::with_capacity(methods.len());
        let mut seen = HashSet::with_capacity(methods.len());
        for method in &methods {
            if method.visibility != Visibility::Public || method.name.starts_with("__") {
                continue;
            }
            if !seen.insert(go_to_lower(&method.name)) {
                continue;
            }
            let mut entry = item(&*method.name, Kind::METHOD);
            entry.detail = format!("{class_name}::{}()", &*method.name);
            entry.deprecated = method.flags.has(Flags::DEPRECATED);
            entry.text_edit = Some(text_edit(
                protocol_range(reference.range, line_index),
                method.name.to_string(),
            ));
            if !method.doc_summary().is_empty() {
                entry.documentation = markdown(method.doc_summary().to_string());
            }
            items.push(entry);
        }
        sort_by_lower_label(&mut items);
        Some(items)
    }

    fn yaml_named_argument_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let php = self.php_index.as_ref()?;
        let root = request.root.as_ref()?;
        let line_index = request.line_index()?;
        let offset = request_offset(request)?;
        let argument = swlsp_symfony::yaml_service_named_argument_at(root, offset)?;
        if argument.has_factory {
            return Some(Vec::new());
        }
        let class_name = match swlsp_symfony::resolve_yaml_service_named_argument_class(
            self.service_index.as_deref(),
            &argument,
        ) {
            Ok(Some(class_name)) => class_name,
            _ => return Some(Vec::new()),
        };
        let constructors = php.find_methods(&class_name, "__construct");
        let Some(constructor) = constructors.first() else {
            return Some(Vec::new());
        };
        let current = normalize_named_argument(&argument.name);
        let prefix = go_to_lower(current);
        let existing: HashSet<&str> = argument
            .existing
            .iter()
            .map(|name| normalize_named_argument(name))
            .filter(|name| !name.is_empty() && *name != current)
            .collect();
        let mut items = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        for parameter in &constructor.parameters {
            let name = normalize_named_argument(&parameter.name);
            if name.is_empty() || !go_to_lower(name).starts_with(&prefix) {
                continue;
            }
            if existing.contains(name) || !seen.insert(name.to_string()) {
                continue;
            }
            let label = format!("${name}");
            let mut new_text = label.clone();
            if !argument.complete {
                new_text.push_str(": ");
            }
            let mut entry = item(label, Kind::VARIABLE);
            entry.detail = parameter.ty.to_string();
            entry.text_edit = Some(text_edit(
                protocol_range(argument.range, line_index),
                new_text,
            ));
            entry.documentation = markdown(format!("Constructor argument of `{class_name}`"));
            items.push(entry);
        }
        let mut keyed: Vec<(String, protocol::CompletionItem)> = items
            .into_iter()
            .map(|entry| (go_to_lower(&entry.label), entry))
            .collect();
        swlsp_gosort::sort_slice(&mut keyed, |left, right| left.0 < right.0);
        Some(keyed.into_iter().map(|(_, entry)| entry).collect())
    }

    pub(super) fn service_completion_items(
        &self,
        insert_prefix: &str,
        excluded_id: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.service_index.as_ref() else {
            return Vec::new();
        };
        let Ok(services) = index.get_all_service_definitions() else {
            return Vec::new();
        };
        let mut items = Vec::with_capacity(services.len());
        for service in services {
            if service.id == excluded_id {
                continue;
            }
            let mut entry = item(service.id.clone(), Kind::VARIABLE);
            entry.deprecated = service.deprecated;
            if !insert_prefix.is_empty() {
                entry.insert_text = format!("{insert_prefix}{}", service.id);
            }
            if service.deprecated {
                entry.detail = "Deprecated Symfony service".to_string();
            } else if !service.class.is_empty() {
                entry.detail = service.class.clone();
            }
            let mut documentation = String::from("Symfony service ID");
            if !service.class.is_empty() {
                documentation.push_str("\n\n**Class:** `");
                documentation.push_str(&service.class);
                documentation.push('`');
            }
            if !service.alias_target.is_empty() {
                documentation.push_str("\n\n**Alias:** `");
                documentation.push_str(&service.alias_target);
                documentation.push('`');
            }
            if service.deprecated {
                documentation.push_str("\n\n**Deprecated:** ");
                let message = service.deprecation.trim();
                if message.is_empty() {
                    documentation.push_str("This service is deprecated.");
                } else {
                    documentation.push_str(
                        &message
                            .replace("%service_id%", &service.id)
                            .replace("%alias_id%", &service.id),
                    );
                }
            }
            if !service.tags.is_empty() {
                documentation.push_str("\n\n**Tags:**\n");
                // TagMap is a BTreeMap: keys are already in sort.Strings order.
                for tag in service.tags.keys() {
                    documentation.push_str("- ");
                    documentation.push_str(tag);
                    documentation.push('\n');
                }
            }
            entry.documentation = markdown(documentation);
            items.push(entry);
        }
        items
    }

    pub(super) fn class_completion_items(&self) -> Vec<protocol::CompletionItem> {
        let Some(php) = self.php_index.as_ref() else {
            return Vec::new();
        };
        let symbols = php.class_symbols_view();
        let mut items = Vec::with_capacity(symbols.len());
        let mut seen: HashSet<String> = HashSet::with_capacity(symbols.len());
        for symbol in symbols.iter() {
            let name: &str = &symbol.fully_qualified;
            if name.is_empty() || !seen.insert(go_to_lower(name)) {
                continue;
            }
            let deprecated = symbol.flags.has(Flags::DEPRECATED);
            let mut entry = item(name, Kind::VARIABLE);
            entry.deprecated = deprecated;
            if deprecated {
                entry.detail = "Deprecated PHP type".to_string();
                let mut value = String::from("**Deprecated PHP type**");
                if !symbol.doc_summary().is_empty() {
                    value.push_str("\n\n");
                    value.push_str(symbol.doc_summary());
                }
                entry.documentation = markdown(value);
            }
            items.push(entry);
        }
        items
    }
}

/// Go `configuredServiceMap`: services of the unsaved document by ID.
fn configured_service_map(request: &CompletionRequest) -> HashMap<String, swlsp_symfony::Service> {
    let mut result = HashMap::new();
    let Some(line_index) = request.line_index() else {
        return result;
    };
    if request.tree().is_none() {
        return result;
    }
    let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
        return result;
    };
    let configuration =
        swlsp_symfony::service_configuration_in_document(&path, request.tree(), line_index);
    for service in configuration.services {
        if !service.id.is_empty() {
            result.insert(service.id.clone(), service);
        }
    }
    result
}

fn normalize_named_argument(value: &str) -> &str {
    let value = value.trim();
    value.strip_prefix('$').unwrap_or(value)
}
