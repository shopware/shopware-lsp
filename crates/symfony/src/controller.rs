//! Symfony controller references (`Class::method`, `service:method`,
//! invokable classes) and their resolution against the PHP and service
//! indexes.

use std::collections::HashSet;

use swlsp_indexer::Result;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax as yamlsyntax;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_syntax::SyntaxNode;

use crate::model::Service;
use crate::service_index::ServiceIndex;
use crate::util::Lowercase;

/// A parsed controller reference.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct ControllerReference {
    pub value: String,
    pub target: String,
    pub method: String,
}

/// Parses `Class::method`, `service:method`, and invokable `Class`
/// references. Legacy `Bundle:Controller:action` shortcuts are rejected.
pub fn parse_controller_reference(value: &str) -> Option<ControllerReference> {
    let value = value.trim();
    if value.is_empty() || value.contains(['%', '$', '{', '}']) {
        return None;
    }
    let (target, method) = if value.matches("::").count() == 1 {
        value.split_once("::")?
    } else if value.matches(':').count() == 1 {
        value.split_once(':')?
    } else if !value.contains(':') {
        (value, "__invoke")
    } else {
        return None;
    };
    let target = target.trim();
    let method = method.trim();
    if target.is_empty() || method.is_empty() {
        return None;
    }
    Some(ControllerReference {
        value: value.to_owned(),
        target: target.to_owned(),
        method: method.to_owned(),
    })
}

/// The controller reference at a YAML route `controller:` or
/// `defaults: {_controller: ...}` scalar, plus the route name.
pub fn yaml_controller_reference(node: &SyntaxNode) -> Option<(ControllerReference, String)> {
    if node.kind() != yamlsyntax::YAML_SCALAR {
        return None;
    }
    let pair = yamlquery::ancestor_pair(node)?;
    if yamlquery::pair_key(&pair).as_ref() == Some(node) {
        return None;
    }
    let path = yamlquery::pair_path(node);
    if path.len() < 2 || path[0] == "services" {
        return None;
    }
    let valid = (path.len() == 2 && path[1] == "controller")
        || (path.len() == 3 && path[1] == "defaults" && path[2] == "_controller");
    if !valid {
        return None;
    }
    let reference = parse_controller_reference(&yamlquery::scalar_value(node))?;
    Some((reference, path[0].clone()))
}

fn attr_value(node: &SyntaxNode, name: &str) -> String {
    xmlquery::attribute(node, name)
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default()
}

/// The controller reference at an XML `<route controller="...">` attribute
/// or `<default key="_controller">` text, plus the route ID.
pub fn xml_controller_reference(node: &SyntaxNode) -> Option<(ControllerReference, String)> {
    if let Some(attribute) = xmlquery::attribute_at(node) {
        let route = xmlquery::element_at(&attribute)?;
        if xmlquery::element_name(&route).as_deref() != Some("route")
            || xmlquery::attribute_name(&attribute).as_deref() != Some("controller")
        {
            return None;
        }
        let reference =
            parse_controller_reference(&xmlquery::attribute_value(&attribute).unwrap_or_default())?;
        return Some((reference, attr_value(&route, "id")));
    }
    let text = xmlquery::text_at(node)?;
    let default = xmlquery::element_at(&text)?;
    if xmlquery::element_name(&default).as_deref() != Some("default")
        || attr_value(&default, "key") != "_controller"
    {
        return None;
    }
    let route = xmlquery::parent_element(&default)?;
    if xmlquery::element_name(&route).as_deref() != Some("route") {
        return None;
    }
    let reference = parse_controller_reference(xmlquery::text_content(&default).trim())?;
    Some((reference, attr_value(&route, "id")))
}

/// The result of resolving a controller reference.
#[derive(Clone, Debug, Default)]
pub struct ControllerResolution {
    pub reference: ControllerReference,
    pub class: Option<Symbol>,
    pub method: Option<Symbol>,
    pub target_exists: bool,
    pub class_found: bool,
    pub method_declared: bool,
    pub method_found: bool,
}

impl ControllerResolution {
    /// Whether the resolved public action or its class is `@deprecated`.
    pub fn deprecated(&self) -> bool {
        let deprecated = |symbol: &Option<Symbol>| {
            symbol
                .as_ref()
                .is_some_and(|s| s.flags.has(Flags::DEPRECATED))
        };
        (self.class_found && deprecated(&self.class))
            || (self.method_found && deprecated(&self.method))
    }
}

/// Resolves a controller reference to its class and public method through
/// service IDs/aliases, class names, and legacy `Bundle:Controller` targets.
pub fn resolve_controller_reference(
    reference: &ControllerReference,
    service_index: Option<&ServiceIndex>,
    php_index: Option<&PhpIndex>,
) -> Result<ControllerResolution> {
    let mut result = ControllerResolution {
        reference: reference.clone(),
        ..ControllerResolution::default()
    };
    let Some(php_index) = php_index else {
        return Ok(result);
    };
    let target = reference
        .target
        .strip_prefix('\\')
        .unwrap_or(&reference.target);
    let mut class_name = String::new();
    if target.contains(':') {
        let Some(class) = resolve_legacy_twig_controller_class(target, php_index) else {
            return Ok(result);
        };
        result.target_exists = true;
        result.class_found = true;
        class_name = class.fully_qualified.as_str().to_owned();
        result.class = Some(class);
    }
    if let Some(service_index) = service_index
        && let Some(service) = resolve_controller_service(
            service_index,
            target,
            &mut HashSet::new(),
        )
        .map_err(|error| {
            swlsp_indexer::Error::msg(format!("resolve controller service {target:?}: {error}"))
        })?
    {
        result.target_exists = true;
        class_name = service
            .class
            .strip_prefix('\\')
            .unwrap_or(&service.class)
            .to_owned();
        if class_name.is_empty() && service.id.contains('\\') {
            class_name = service
                .id
                .strip_prefix('\\')
                .unwrap_or(&service.id)
                .to_owned();
        }
    }
    if let Some(class) = php_index.find_class(target) {
        result.target_exists = true;
        result.class_found = true;
        class_name = class.fully_qualified.as_str().to_owned();
        result.class = Some(class);
    }
    if class_name.is_empty() {
        return Ok(result);
    }
    if !result.class_found {
        let Some(class) = php_index.find_class(&class_name) else {
            return Ok(result);
        };
        result.class = Some(class);
        result.class_found = true;
    }
    let mut method_names = vec![reference.method.clone()];
    if target.contains(':') {
        match reference.method.strip_suffix("Action") {
            Some(stripped) => method_names.push(stripped.to_owned()),
            None => method_names.push(format!("{}Action", reference.method)),
        }
    }
    let class_fqn = result
        .class
        .as_ref()
        .map(|class| class.fully_qualified.as_str().to_owned())
        .unwrap_or_default();
    for method_name in &method_names {
        for method in php_index.find_methods(&class_fqn, method_name) {
            if !result.method_declared {
                result.method = Some(method.clone());
                result.method_declared = true;
            }
            if method.visibility != Visibility::Public {
                continue;
            }
            result.method = Some(method);
            result.method_found = true;
            return Ok(result);
        }
    }
    Ok(result)
}

fn resolve_legacy_twig_controller_class(target: &str, php_index: &PhpIndex) -> Option<Symbol> {
    let (bundle, controller) = target.split_once(':')?;
    let bundle = bundle.trim().lowercase();
    let controller = controller.trim().replace('/', "\\");
    let controller = controller.trim_matches('\\');
    let controller = controller.strip_suffix("Controller").unwrap_or(controller);
    if bundle.is_empty() || controller.is_empty() {
        return None;
    }
    let expected_suffix = format!("\\Controller\\{controller}Controller").lowercase();
    let bundle_trimmed = bundle.strip_suffix("bundle").unwrap_or(&bundle);
    for class in php_index.class_symbols_view().iter() {
        if class.kind != SymbolKind::Class {
            continue;
        }
        let name = format!(
            "\\{}",
            class.fully_qualified.as_str().trim_start_matches('\\')
        );
        let lower = name.lowercase();
        let Some(prefix) = lower.strip_suffix(&expected_suffix) else {
            continue;
        };
        let segment = match prefix.rfind('\\') {
            Some(offset) => &prefix[offset + 1..],
            None => prefix,
        };
        if segment == bundle
            || segment == format!("{bundle}bundle")
            || segment.strip_suffix("bundle").unwrap_or(segment) == bundle_trimmed
        {
            return Some(class.clone());
        }
    }
    None
}

fn resolve_controller_service(
    index: &ServiceIndex,
    id: &str,
    visited: &mut HashSet<String>,
) -> Result<Option<Service>> {
    if !visited.insert(id.lowercase()) {
        return Ok(None);
    }
    let Some(service) = index.get_service_by_id(id)? else {
        return Ok(None);
    };
    if service.alias_target.is_empty() {
        return Ok(Some(service));
    }
    match resolve_controller_service(index, &service.alias_target.clone(), visited)? {
        Some(target) => Ok(Some(target)),
        None => Ok(Some(service)),
    }
}

#[cfg(test)]
#[path = "tests_routectx/controller.rs"]
mod tests;
