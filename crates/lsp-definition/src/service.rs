//! Service IDs, parameters, tags, classes, configured methods and constructor
//! arguments in XML, YAML and PHP service configuration and container calls.

use std::cell::OnceCell;
use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::{NameResolver, PhpContext, PhpIndex};
use swlsp_php_semantic::{Flags, Parameter, Symbol, SymbolId, Visibility};
use swlsp_php_types::{Kind, Type};
use swlsp_symfony::{
    PhpArrayServiceMethodReference, PhpAutowireCallableMethodReference, PhpConfigReferenceKind,
    Service, ServiceIndex, ServiceMethodReference,
};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use crate::common::{
    ext_lower, file_range_location_or_uri, line_location, php_symbol_location, request_offset,
    request_uri,
};
use crate::php_common::{php_context, unclamped_line_location};

const CONTAINER_INTERFACES: [&str; 3] = [
    "Psr\\Container\\ContainerInterface",
    "Symfony\\Component\\DependencyInjection\\ContainerInterface",
    "Symfony\\Contracts\\Service\\ServiceProviderInterface",
];

/// Definitions for service IDs, parameters, tags, classes and arguments in
/// service configuration and container calls.
pub struct ServiceDefinitionProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

/// Lazily computed request PHP context.
struct RequestContext<'a> {
    php: Option<&'a PhpIndex>,
    request: &'a DefinitionRequest,
    context: OnceCell<Option<PhpContext>>,
}

impl RequestContext<'_> {
    fn get(&self) -> Option<&PhpContext> {
        self.context
            .get_or_init(|| php_context(self.php, self.request))
            .as_ref()
    }
}

impl ServiceDefinitionProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }

    fn service_by_id(&self, id: &str) -> Option<Service> {
        self.services
            .as_deref()?
            .get_service_by_id(id)
            .ok()
            .flatten()
    }

    fn find_class(&self, name: &str) -> Option<Symbol> {
        self.php.as_ref()?.find_class(name)
    }

    fn find_methods(&self, class_name: &str, method: &str) -> Vec<Symbol> {
        self.php
            .as_ref()
            .map(|php| php.find_methods(class_name, method))
            .unwrap_or_default()
    }

    fn resolve_service_class_name(&self, id: &str) -> Option<String> {
        self.services
            .as_deref()?
            .resolve_service_class_name(id)
            .ok()
            .flatten()
    }

    fn xml_definition(
        &self,
        request: &DefinitionRequest,
        node: &SyntaxNode,
    ) -> Vec<protocol::Location> {
        if let Some(locations) = self.configured_service_method_definition(request) {
            return locations;
        }

        if swlsp_symfony::xml_service_is_service_reference(node) {
            let Some(service_id) = swlsp_symfony::xml_service_reference_name(node) else {
                return Vec::new();
            };
            return match self.service_by_id(&service_id) {
                Some(service) => vec![service_location(&service.path, service.line)],
                None => Vec::new(),
            };
        }

        if swlsp_symfony::xml_service_is_argument_tag(node)
            || swlsp_symfony::xml_service_is_tag_element(node)
        {
            let tag = swlsp_symfony::xml_context_value(node);
            if tag.is_empty() {
                return Vec::new();
            }
            let Some(services) = self.services.as_deref() else {
                return Vec::new();
            };
            let Ok(service_ids) = services.get_services_by_tag(&tag) else {
                return Vec::new();
            };
            return service_ids
                .iter()
                .filter_map(|id| self.service_by_id(id))
                .map(|service| unclamped_line_location(&service.path, service.line))
                .collect();
        }

        let references =
            swlsp_symfony::parameter_references(&swlsp_symfony::xml_context_value(node));
        if let Some(name) = references.first() {
            return self.parameter_id_definition(name);
        }

        if swlsp_symfony::xml_service_is_service_id(node)
            && let Some(class) = self.find_class(&swlsp_symfony::xml_context_value(node))
        {
            return vec![php_symbol_location(&class)];
        }
        Vec::new()
    }

    fn yaml_definition(
        &self,
        request: &DefinitionRequest,
        node: &SyntaxNode,
    ) -> Vec<protocol::Location> {
        if let Some(locations) = self.configured_service_method_definition(request) {
            return locations;
        }
        if let Some(locations) = self.yaml_default_binding_definition(request) {
            return locations;
        }
        if let Some(locations) = self.yaml_named_argument_definition(request) {
            return locations;
        }

        if (swlsp_symfony::is_yaml_service_id(node)
            || swlsp_symfony::is_yaml_class_property_in_service(node))
            && let Some(class) = self.find_class(&swlsp_symfony::yaml_context_value(node))
        {
            return vec![php_symbol_location(&class)];
        }

        if swlsp_symfony::is_yaml_argument_service_id(node) {
            let Some(service_id) = swlsp_symfony::yaml_service_reference_name(node) else {
                return Vec::new();
            };
            if let Some(service) = self.service_by_id(&service_id) {
                return vec![service_location(&service.path, service.line)];
            }
        }

        let references =
            swlsp_symfony::parameter_references(&swlsp_symfony::yaml_context_value(node));
        if let Some(name) = references.first() {
            let locations = self.parameter_id_definition(name);
            if !locations.is_empty() {
                return locations;
            }
        }
        Vec::new()
    }

    /// `Some` when the cursor is on a configured service method name, even
    /// if it resolves to nothing.
    fn configured_service_method_definition(
        &self,
        request: &DefinitionRequest,
    ) -> Option<Vec<protocol::Location>> {
        self.php.as_ref()?;
        let root = request.root.as_ref()?;
        let offset = request_offset(request)?;
        let reference: ServiceMethodReference = match ext_lower(request_uri(request)).as_str() {
            ".yaml" | ".yml" => swlsp_symfony::yaml_service_method_reference_at(root, offset),
            ".xml" => swlsp_symfony::xml_service_method_reference_at(root, offset),
            _ => None,
        }?;
        let (service_id, explicit_class) = reference.receiver();
        let local = configured_service_definition_map(request);
        let class_name = match swlsp_symfony::resolve_configured_service_class(
            service_id,
            explicit_class,
            &local,
            self.services.as_deref(),
        ) {
            Ok(Some(class_name)) if !reference.method_name.is_empty() => class_name,
            _ => return Some(Vec::new()),
        };
        Some(
            self.find_methods(&class_name, &reference.method_name)
                .iter()
                .filter(|method| method.visibility == Visibility::Public)
                .map(php_symbol_location)
                .collect(),
        )
    }

    fn yaml_default_binding_definition(
        &self,
        request: &DefinitionRequest,
    ) -> Option<Vec<protocol::Location>> {
        let php = self.php.as_ref()?;
        let root = request.root.as_ref()?;
        let tree = request.tree()?;
        let lines = request.line_index()?;
        let offset = request_offset(request)?;
        let binding = swlsp_symfony::yaml_service_default_binding_at(root, offset)?;

        let Ok(path) = swlsp_uriutil::path(request_uri(request)) else {
            return Some(Vec::new());
        };
        let configuration =
            swlsp_symfony::service_configuration_in_document(&path, Some(tree), lines);
        let mut class_names: Vec<String> = Vec::new();
        let mut seen_classes = HashSet::new();
        let mut add_class = |class_name: &str| {
            let class_name = class_name.trim();
            let class_name = class_name.strip_prefix('\\').unwrap_or(class_name);
            if class_name.is_empty() {
                return;
            }
            if seen_classes.insert(swlsp_textutil::lowercase(class_name)) {
                class_names.push(class_name.to_owned());
            }
        };
        for service in &configuration.services {
            if service.alias_target.is_empty() {
                add_class(&service.class);
            }
        }
        if let Some(services) = self.services.as_deref() {
            for prototype in &configuration.prototypes {
                for class in services.prototype_classes(prototype) {
                    add_class(&class.fully_qualified);
                }
            }
        }

        let name = binding.name.trim();
        let name = name.strip_prefix('$').unwrap_or(name);
        let mut seen: HashSet<SymbolId> = HashSet::new();
        let mut locations = Vec::new();
        for class_name in &class_names {
            for constructor in php.find_methods(class_name, "__construct") {
                for parameter in &constructor.parameters {
                    if parameter.name.strip_prefix('$').unwrap_or(&parameter.name) != name
                        || !yaml_default_binding_type_matches(parameter, &binding.type_name)
                    {
                        continue;
                    }
                    if !seen.insert(parameter.id.clone()) {
                        continue;
                    }
                    locations.push(file_range_location_or_uri(
                        &constructor.path,
                        parameter.selection_range,
                    ));
                }
            }
        }
        Some(locations)
    }

    fn yaml_named_argument_definition(
        &self,
        request: &DefinitionRequest,
    ) -> Option<Vec<protocol::Location>> {
        let php = self.php.as_ref()?;
        let root = request.root.as_ref()?;
        let offset = request_offset(request)?;
        let argument = swlsp_symfony::yaml_service_named_argument_at(root, offset)?;
        if argument.has_factory {
            return Some(Vec::new());
        }
        let Ok(Some(class_name)) = swlsp_symfony::resolve_yaml_service_named_argument_class(
            self.services.as_deref(),
            &argument,
        ) else {
            return Some(Vec::new());
        };
        let name = argument.name.trim();
        let name = name.strip_prefix('$').unwrap_or(name);
        let mut seen: HashSet<SymbolId> = HashSet::new();
        let mut locations = Vec::new();
        for constructor in php.find_methods(&class_name, "__construct") {
            for parameter in &constructor.parameters {
                if parameter.name.strip_prefix('$').unwrap_or(&parameter.name) != name {
                    continue;
                }
                if !seen.insert(parameter.id.clone()) {
                    continue;
                }
                locations.push(file_range_location_or_uri(
                    &constructor.path,
                    parameter.selection_range,
                ));
            }
        }
        Some(locations)
    }

    fn php_definition(
        &self,
        request: &DefinitionRequest,
        node: &SyntaxNode,
    ) -> Vec<protocol::Location> {
        let context = RequestContext {
            php: self.php.as_ref(),
            request,
            context: OnceCell::new(),
        };
        if let Some(php_context) = context.get() {
            if swlsp_php::assistant_argument_reference(php_context, node, "Service").is_some() {
                return self.service_id_definition(&phpquery::string_value(node));
            }
            if swlsp_php::assistant_argument_reference(php_context, node, "Parameter").is_some() {
                return self.parameter_id_definition(&phpquery::string_value(node));
            }
        }
        if let Some(reference) = swlsp_symfony::php_autowire_callable_method_at(node) {
            return self.autowire_callable_method_definitions(request.root.as_ref(), &reference);
        }
        if let Some(root) = request.root.as_ref()
            && let Some(reference) = swlsp_symfony::php_array_service_method_at(root, node)
        {
            return self.php_array_service_method_definitions(Some(root), &reference);
        }
        let mut kind = swlsp_symfony::php_array_service_reference_at(node);
        if kind == PhpConfigReferenceKind::None {
            kind = swlsp_symfony::php_attribute_reference_at(node);
        }
        if kind == PhpConfigReferenceKind::None
            && phpquery::string_argument_index(node) == Some(0)
            && self.is_parameter_access_call(context.get(), node)
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
            && self.is_container_call(context.get(), node)
        {
            kind = PhpConfigReferenceKind::Service;
        }
        let value = swlsp_symfony::php_config_reference_value(node);
        match kind {
            PhpConfigReferenceKind::Service => return self.service_id_definition(&value),
            PhpConfigReferenceKind::Parameter => {
                let references = swlsp_symfony::parameter_references(&value);
                let name = references.first().cloned().unwrap_or(value);
                return self.parameter_id_definition(&name);
            }
            PhpConfigReferenceKind::Tag => return self.tagged_service_locations(&value),
            PhpConfigReferenceKind::Class => {
                let value = value.trim();
                return self
                    .find_class(value.strip_prefix('\\').unwrap_or(value))
                    .map(|class| vec![php_symbol_location(&class)])
                    .unwrap_or_default();
            }
            PhpConfigReferenceKind::None => {}
        }

        let class_name = phpquery::class_constant_name(node);
        let Some(root) = request.root.as_ref() else {
            return Vec::new();
        };
        if class_name.is_empty() {
            return Vec::new();
        }
        let resolver = NameResolver::new(root);
        self.find_class(&resolver.resolve(&class_name))
            .map(|class| vec![php_symbol_location(&class)])
            .unwrap_or_default()
    }

    fn service_id_definition(&self, service_id: &str) -> Vec<protocol::Location> {
        if self.services.is_none() {
            return Vec::new();
        }
        let service_id = swlsp_symfony::parse_service_reference(service_id)
            .map_or_else(|| service_id.to_owned(), |(normalized, _)| normalized);
        match self.service_by_id(&service_id) {
            Some(service) => vec![service_location(&service.path, service.line)],
            None => Vec::new(),
        }
    }

    fn parameter_id_definition(&self, name: &str) -> Vec<protocol::Location> {
        let Some(services) = self.services.as_deref() else {
            return Vec::new();
        };
        match services.get_parameter_by_name(name) {
            Ok(Some(parameter)) => vec![service_location(&parameter.path, parameter.line)],
            _ => Vec::new(),
        }
    }

    fn tagged_service_locations(&self, tag: &str) -> Vec<protocol::Location> {
        let Some(services) = self.services.as_deref() else {
            return Vec::new();
        };
        let Ok(service_ids) = services.get_services_by_tag(tag) else {
            return Vec::new();
        };
        let mut seen = HashSet::new();
        let mut locations = Vec::new();
        for service_id in &service_ids {
            let Some(service) = self.service_by_id(service_id) else {
                continue;
            };
            let location = service_location(&service.path, service.line);
            if !seen.insert((location.uri.clone(), location.range.start.line)) {
                continue;
            }
            locations.push(location);
        }
        locations
    }

    fn autowire_callable_method_definitions(
        &self,
        root: Option<&SyntaxNode>,
        reference: &PhpAutowireCallableMethodReference,
    ) -> Vec<protocol::Location> {
        if reference.method.is_empty() {
            return Vec::new();
        }
        let class_name = match root {
            Some(root) if !reference.class.is_empty() => {
                let resolved = NameResolver::new(root).resolve(&reference.class);
                resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned()
            }
            _ => self.service_class_name(&reference.service),
        };
        if class_name.is_empty() {
            return Vec::new();
        }
        self.find_methods(&class_name, &reference.method)
            .iter()
            .filter(|method| {
                method.visibility == Visibility::Public && !method.flags.has(Flags::STATIC)
            })
            .map(php_symbol_location)
            .collect()
    }

    fn php_array_service_method_definitions(
        &self,
        root: Option<&SyntaxNode>,
        reference: &PhpArrayServiceMethodReference,
    ) -> Vec<protocol::Location> {
        if reference.method.is_empty() {
            return Vec::new();
        }
        let class_name = match root {
            Some(root) if !reference.class.is_empty() => {
                let resolved = NameResolver::new(root).resolve(&reference.class);
                resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned()
            }
            _ => self.service_class_name(&reference.service),
        };
        if class_name.is_empty() {
            return Vec::new();
        }
        self.find_methods(&class_name, &reference.method)
            .iter()
            .filter(|method| {
                method.visibility == Visibility::Public
                    && (reference.allow_static || !method.flags.has(Flags::STATIC))
            })
            .map(php_symbol_location)
            .collect()
    }

    /// The class behind a service ID receiver: the container
    /// class, else a class of that name.
    fn service_class_name(&self, service_id: &str) -> String {
        let service_id = service_id.trim();
        let service_id = swlsp_symfony::parse_service_reference(service_id)
            .map_or_else(|| service_id.to_owned(), |(normalized, _)| normalized);
        if let Some(resolved) = self.resolve_service_class_name(&service_id) {
            return resolved;
        }
        if let Some(class) = self.find_class(&service_id) {
            return class
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&class.fully_qualified)
                .to_owned();
        }
        String::new()
    }

    fn is_parameter_access_call(&self, context: Option<&PhpContext>, node: &SyntaxNode) -> bool {
        let Some(php) = self.php.as_ref() else {
            return false;
        };
        let method = phpquery::call_at(node)
            .map(|call| phpquery::call_method_name(&call))
            .unwrap_or_default();
        swlsp_symfony::php_parameter_access_types(&method)
            .into_iter()
            .any(|class_name| php.is_method_called_on_class(context, node, class_name))
    }

    fn is_container_call(&self, context: Option<&PhpContext>, node: &SyntaxNode) -> bool {
        if self.is_parameter_access_call(context, node) {
            return false;
        }
        let Some(php) = self.php.as_ref() else {
            return false;
        };
        CONTAINER_INTERFACES
            .into_iter()
            .any(|class_name| php.is_method_called_on_class(context, node, class_name))
    }
}

impl GotoDefinitionProvider for ServiceDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match ext_lower(request_uri(request)).as_str() {
            ".php" => self.php_definition(request, node),
            ".yaml" | ".yml" => self.yaml_definition(request, node),
            ".xml" => self.xml_definition(request, node),
            _ => Vec::new(),
        }
    }
}

fn configured_service_definition_map(request: &DefinitionRequest) -> HashMap<String, Service> {
    let mut result = HashMap::new();
    let (Some(tree), Some(lines)) = (request.tree(), request.line_index()) else {
        return result;
    };
    let Ok(path) = swlsp_uriutil::path(request_uri(request)) else {
        return result;
    };
    let configuration = swlsp_symfony::service_configuration_in_document(&path, Some(tree), lines);
    for service in configuration.services {
        if !service.id.is_empty() {
            result.insert(service.id.clone(), service);
        }
    }
    result
}

fn yaml_default_binding_type_matches(parameter: &Parameter, expected: &str) -> bool {
    let expected = normalize_yaml_default_binding_type(expected);
    if expected.is_empty() {
        return true;
    }
    yaml_default_binding_php_type_matches(&parameter.native_type, expected)
        || yaml_default_binding_php_type_matches(&parameter.ty, expected)
}

fn yaml_default_binding_php_type_matches(value: &Type, expected: &str) -> bool {
    if value.is_unknown() {
        return false;
    }
    if eq_ignore_case(
        normalize_yaml_default_binding_type(&value.to_string()),
        expected,
    ) {
        return true;
    }
    match value.kind() {
        Kind::Union | Kind::Intersection => value
            .arguments()
            .iter()
            .any(|member| yaml_default_binding_php_type_matches(member, expected)),
        _ => false,
    }
}

fn normalize_yaml_default_binding_type(value: &str) -> &str {
    let value = value.trim();
    value.strip_prefix('\\').unwrap_or(value)
}

/// The service line, clamped to the first line.
fn service_location(path: &str, line: u32) -> protocol::Location {
    line_location(path, i64::from(line))
}

#[cfg(test)]
#[path = "service_tests.rs"]
mod tests;
