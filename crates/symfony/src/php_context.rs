//! Classification of Symfony configuration strings in PHP code.

use swlsp_parser_php::query as phpquery;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::reference::{normalize_service_reference, parameter_references};

/// What a configuration string refers to.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum PhpConfigReferenceKind {
    #[default]
    None,
    Service,
    Parameter,
    Tag,
    Class,
}

/// The method and receiver configured by Symfony's `AutowireCallable`
/// attribute. `class` keeps the unresolved PHP name from a `Foo::class`
/// expression.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PhpAutowireCallableMethodReference {
    pub service: String,
    pub class: String,
    pub method: String,
    pub range: TextRange,
}

pub const PHP_PARAMETER_BAG_INTERFACE: &str =
    "Symfony\\Component\\DependencyInjection\\ParameterBag\\ParameterBagInterface";
pub const PHP_CONTAINER_BAG_INTERFACE: &str =
    "Symfony\\Component\\DependencyInjection\\ParameterBag\\ContainerBagInterface";
pub const PHP_DI_CONTAINER_INTERFACE: &str =
    "Symfony\\Component\\DependencyInjection\\ContainerInterface";
pub const PHP_PARAMETERS_CONFIGURATOR: &str =
    "Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ParametersConfigurator";

pub fn php_parameter_bag_types() -> Vec<&'static str> {
    vec![PHP_PARAMETER_BAG_INTERFACE, PHP_CONTAINER_BAG_INTERFACE]
}

/// Receiver types of Symfony parameter APIs that provide completion and
/// navigation at their first string argument.
pub fn php_parameter_access_types(method: &str) -> Vec<&'static str> {
    match method {
        "get" | "has" => php_parameter_bag_types(),
        "set" => vec![PHP_PARAMETER_BAG_INTERFACE, PHP_PARAMETERS_CONFIGURATOR],
        "getParameter" | "hasParameter" => vec![PHP_DI_CONTAINER_INTERFACE],
        _ => Vec::new(),
    }
}

/// Like [`php_parameter_access_types`] without declaration APIs such as
/// `set()`, which must not report a missing parameter.
pub fn php_parameter_read_access_types(method: &str) -> Vec<&'static str> {
    match method {
        "get" | "has" | "getParameter" | "hasParameter" => php_parameter_access_types(method),
        _ => Vec::new(),
    }
}

/// Classifies string arguments used by Symfony's PHP configurator helpers
/// and definition configurators.
pub fn php_config_reference_at(node: &SyntaxNode) -> PhpConfigReferenceKind {
    if phpquery::string_at(node).is_none() {
        return PhpConfigReferenceKind::None;
    }
    let index = phpquery::string_argument_index(node);
    let name = phpquery::call_method_name(node);
    match short_php_call_name(&name) {
        "service" | "service_closure" | "get" if index == Some(0) => {
            PhpConfigReferenceKind::Service
        }
        "alias" if index == Some(1) => PhpConfigReferenceKind::Service,
        "param" if index == Some(0) => PhpConfigReferenceKind::Parameter,
        "tagged_iterator" | "tagged_locator" if index == Some(0) => PhpConfigReferenceKind::Tag,
        _ => PhpConfigReferenceKind::None,
    }
}

pub fn php_config_reference_value(node: &SyntaxNode) -> String {
    phpquery::string_value(node).replace("\\\\", "\\")
}

fn attribute_argument(attribute: &SyntaxNode, literal: &SyntaxNode) -> (Option<usize>, String) {
    let index = phpquery::argument_index(attribute, literal);
    let name = index
        .and_then(|index| phpquery::argument(attribute, index))
        .map(|argument| phpquery::argument_name(&argument))
        .unwrap_or_default();
    (index, name)
}

pub fn php_attribute_reference_at(node: &SyntaxNode) -> PhpConfigReferenceKind {
    use PhpConfigReferenceKind as Kind;
    let Some(literal) = phpquery::string_at(node) else {
        return Kind::None;
    };
    let Some(attribute) = phpquery::attribute_at(&literal) else {
        return Kind::None;
    };
    let attribute_name = phpquery::attribute_name(&attribute);
    let (index, argument_name) = attribute_argument(&attribute, &literal);
    let first = index == Some(0);
    match short_php_call_name(&attribute_name) {
        "Autowire" => {
            match argument_name.as_str() {
                "service" => return Kind::Service,
                "param" => return Kind::Parameter,
                _ => {}
            }
            if first {
                let value = phpquery::string_value(&literal);
                let value = value.trim();
                if normalize_service_reference(value).is_some() {
                    return Kind::Service;
                }
                if !parameter_references(value).is_empty() {
                    return Kind::Parameter;
                }
                if value.starts_with('@') && !value.starts_with("@@") {
                    return Kind::Service;
                }
                if value.starts_with('%') {
                    return Kind::Parameter;
                }
            }
            Kind::None
        }
        // `services` is a tag name when it is a string and a list of service
        // ids when it is an array.
        "AutowireLocator" if argument_name == "services" || (first && argument_name.is_empty()) => {
            let direct = index
                .and_then(|index| phpquery::argument_expression(&attribute, index))
                .is_some_and(|value| value.text_range() == literal.text_range());
            if direct { Kind::Tag } else { Kind::Service }
        }
        "AutowireIterator" if argument_name == "tag" || (first && argument_name.is_empty()) => {
            Kind::Tag
        }
        "AutowireLocator" | "AutowireIterator" | "TaggedIterator" | "TaggedLocator"
            if argument_name == "exclude" =>
        {
            Kind::Service
        }
        "AutowireServiceClosure" | "AutowireMethodOf" if first || argument_name == "service" => {
            Kind::Service
        }
        "AutowireCallable" if argument_name == "service" => Kind::Service,
        "TaggedIterator" | "TaggedLocator" if first || argument_name == "tag" => Kind::Tag,
        "Autoconfigure" if first || argument_name == "tags" => Kind::Tag,
        "AutoconfigureTag" if first || argument_name == "name" => Kind::Tag,
        "AsDecorator" if first || argument_name == "decorates" => Kind::Service,
        _ => Kind::None,
    }
}

/// Recognizes the deployment-environment argument of Symfony's `#[When]`.
pub fn php_when_environment_at(node: &SyntaxNode) -> bool {
    let Some(literal) = phpquery::string_at(node) else {
        return false;
    };
    let Some(attribute) = phpquery::attribute_at(&literal) else {
        return false;
    };
    if short_php_call_name(&phpquery::attribute_name(&attribute)) != "When" {
        return false;
    }
    let (index, argument_name) = attribute_argument(&attribute, &literal);
    match index {
        None => false,
        Some(index) => index == 0 || argument_name == "env",
    }
}

/// Recognizes the method argument of `#[AutowireCallable(...)]` and extracts
/// the service receiver (a service ID string or `Foo::class`).
pub fn php_autowire_callable_method_at(
    node: &SyntaxNode,
) -> Option<PhpAutowireCallableMethodReference> {
    let literal = phpquery::string_at(node)?;
    let attribute = phpquery::attribute_at(&literal)?;
    if short_php_call_name(&phpquery::attribute_name(&attribute)) != "AutowireCallable" {
        return None;
    }
    let (index, argument_name) = attribute_argument(&attribute, &literal);
    if index.is_none() || argument_name != "method" {
        return None;
    }
    let mut reference = PhpAutowireCallableMethodReference {
        method: phpquery::string_value(&literal),
        range: phpquery::string_content_range(&literal),
        ..PhpAutowireCallableMethodReference::default()
    };
    for (argument_index, argument) in phpquery::arguments(&attribute).enumerate() {
        if phpquery::argument_name(&argument) != "service" {
            continue;
        }
        match phpquery::string_argument(&attribute, argument_index) {
            Some(service) => reference.service = phpquery::string_value(&service),
            None => reference.class = phpquery::class_constant_name(&argument),
        }
        break;
    }
    if reference.service.is_empty() && reference.class.is_empty() {
        return None;
    }
    Some(reference)
}

pub(crate) fn short_php_call_name(name: &str) -> &str {
    match name.rfind('\\') {
        Some(index) => &name[index + 1..],
        None => name,
    }
}

#[cfg(test)]
#[path = "tests_phpsvc/php_context.rs"]
mod tests;
