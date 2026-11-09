//! Symfony framework intelligence: the
//! service container from XML/YAML/PHP configuration and the compiled
//! container, parameters, tags, routes, route and controller usages, and the
//! static configuration contexts used by LSP providers.
//!
//! Tag sets are ordered `BTreeMap`s so iteration is deterministic.

mod compiled_catalog_index;
mod compiled_route_catalog;
mod container_catalog;
mod model;
mod path_match;
mod reference;
mod service_index;
mod twig_components;
mod twig_globals;
mod util;
mod workspace_symbols;
mod xml_services;
mod yaml_services;

mod compiled_routes;
mod controller;
mod php_routes;
mod route_constant_names;
mod route_context;
mod route_indexer;
mod route_resource;
mod route_resource_resolver;
mod route_usage_indexer;
mod twig_controller;
mod xml_routes;
mod yaml_routes;

mod container_constant;
mod service_argument_reference;
mod service_navigation;
mod service_resolution;
mod service_tag_contract;
mod type_extension;
mod xml_context;
mod yaml_context;
mod yaml_named_argument;

mod php_array_context;
mod php_array_services;
mod php_context;
mod php_service_method_reference;
mod php_services;
mod php_services_evaluator;

mod services_yaml_convert;
mod services_yaml_model;
mod services_yaml_phpize;
mod services_yaml_plan;

#[cfg(test)]
mod tests;

pub use crate::container_constant::{
    ContainerConstantReference, ContainerValueKind, resolve_container_constant,
    resolve_container_value, supports_yaml_container_enum, supports_yaml_container_enum_class,
    xml_container_constant_references, yaml_container_constant_completion_at,
    yaml_container_constant_references,
};
pub use crate::controller::{
    ControllerReference, ControllerResolution, parse_controller_reference,
    resolve_controller_reference, xml_controller_reference, yaml_controller_reference,
};
pub use crate::php_array_context::{
    PhpArrayServiceMethodReference, php_array_parameter_completion_at,
    php_array_service_context_at, php_array_service_method_at, php_array_service_reference_at,
};
pub use crate::php_context::{
    PHP_CONTAINER_BAG_INTERFACE, PHP_DI_CONTAINER_INTERFACE, PHP_PARAMETER_BAG_INTERFACE,
    PHP_PARAMETERS_CONFIGURATOR, PhpAutowireCallableMethodReference, PhpConfigReferenceKind,
    php_attribute_reference_at, php_autowire_callable_method_at, php_config_reference_at,
    php_config_reference_value, php_parameter_access_types, php_parameter_bag_types,
    php_parameter_read_access_types, php_when_environment_at,
};
pub use crate::php_routes::{parse_php_routes, parse_php_routes_tree};
pub use crate::php_services::{
    parse_php_services, parse_php_services_tree, php_service_argument_references,
    php_service_method_references,
};
pub use crate::route_constant_names::{
    ClassConstantLookup, resolve_constant_route_name, resolve_constant_route_names,
};
pub use crate::route_context::{
    JsRouteUrlReference, PhpRoutePathReference, TwigHtmlRouteReference,
    TwigRouteComparisonReference, is_php_route_name, is_php_route_name_in_context,
    is_static_route_name, is_twig_route_name, js_route_url_reference_at,
    normalize_route_search_path, php_route_annotation_name_reference_at,
    php_route_annotation_path_reference_at, php_route_name_reference_at, php_route_name_suggestion,
    php_route_parameter_context, php_route_parameter_reference_at, php_route_path_reference_at,
    route_url_path, twig_html_route_reference_at, twig_html_route_references,
    twig_route_comparison_reference_at, twig_route_comparison_references,
    twig_route_parameter_context, twig_route_parameter_reference_at,
};
pub use crate::route_indexer::{
    RouteIndexer, RoutePathSearchMatcher, route_by_controller, route_path_matches,
    route_path_search_matches,
};
pub use crate::route_resource::{
    RouteResourceImport, RouteResourceReference, php_route_resource_completion_range_at,
    route_resource_files, route_resource_matches, route_resource_reference_at,
    route_resource_references,
};
pub use crate::route_resource_resolver::{
    BundleResourceCandidate, IndexedResourcePaths, RouteResourceResolver,
};
pub use crate::route_usage_indexer::{
    ControllerUsage, ControllerUsageGroup, RouteUsage, RouteUsageIndexer,
};
pub use crate::service_argument_reference::{
    xml_service_argument_references, xml_service_method_reference_at,
    xml_service_method_references, yaml_service_argument_references,
    yaml_service_method_reference_at, yaml_service_method_references,
};
pub use crate::service_navigation::{ServiceConfiguration, service_configuration_in_document};
pub use crate::service_resolution::resolve_configured_service_class;
pub use crate::service_tag_contract::{
    ServiceTagTypeHierarchy, required_service_tag_types, suggested_service_tags,
};
pub use crate::services_yaml_convert::{ConvertError, convert_container_to_yaml};
pub use crate::services_yaml_model::{Container, XmlAttr, parse_services_xml};
pub use crate::services_yaml_plan::{
    PlanError, ServicesXmlConversion, plan_services_xml_conversion,
};
pub use crate::twig_controller::{
    TwigControllerReference, TwigControllerValue, controller_reference_key,
    normalize_twig_controller_value, parse_twig_controller_reference, twig_controller_reference_at,
    twig_controller_references, twig_controller_value_at, twig_controller_values,
};
pub use crate::type_extension::PhpTypeExtension;
pub use crate::xml_context::{
    xml_class_reference_name, xml_context_value, xml_current_service_id,
    xml_parameter_reference_name, xml_service_is_argument_tag, xml_service_is_parameter_reference,
    xml_service_is_service_id, xml_service_is_service_reference, xml_service_is_tag_element,
    xml_service_reference_name, xml_service_reference_optional,
};
pub use crate::xml_routes::{parse_xml_routes, parse_xml_routes_tree};
pub use crate::yaml_context::{
    is_yaml_argument_service_id, is_yaml_class_property_in_service, is_yaml_service_id,
    yaml_class_reference_name, yaml_context_value, yaml_service_reference_name,
};
pub use crate::yaml_named_argument::{
    YamlServiceDefaultBinding, YamlServiceNamedArgument, resolve_yaml_service_named_argument_class,
    yaml_service_default_binding_at, yaml_service_named_argument_at, yaml_service_named_arguments,
};
pub use crate::yaml_routes::{parse_yaml_routes, parse_yaml_routes_tree};
pub use compiled_catalog_index::{CompiledCatalogIndex, CompiledCatalogRecord};
pub use compiled_route_catalog::{
    CompiledRouteCatalog, parse_compiled_routes, parse_compiled_routes_tree,
};
pub use container_catalog::ContainerCatalog;
pub use model::{
    ContainerTwigComponent, ContainerTwigGlobal, Location, Parameter, PhpServiceConfig, Route,
    Service, ServiceArgumentReference, ServiceMethodReference, ServicePrototype, TagMap,
};
pub use reference::{normalize_service_reference, parameter_references, parse_service_reference};
pub use service_index::ServiceIndex;
pub use twig_components::{parse_xml_twig_components, parse_xml_twig_components_tree};
pub use twig_globals::{parse_xml_twig_globals, parse_xml_twig_globals_tree};
pub use xml_services::{parse_xml_services, parse_xml_services_tree};
pub use yaml_services::{parse_yaml_services, parse_yaml_services_tree};

/// Parses a PHP service configuration file.
pub(crate) fn php_service_config(
    path: &str,
    root: &swlsp_syntax::SyntaxNode,
    line_index: &swlsp_syntax::LineIndex,
) -> PhpServiceConfig {
    php_services::parse_php_service_config_tree(path, root, line_index)
}
