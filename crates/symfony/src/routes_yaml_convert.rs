//! Lossless conversion of a Symfony `routes.xml` file into the equivalent
//! `routes.yaml`, with the rules of `services_yaml_convert`: whatever YAML
//! cannot express is refused, so a conversion never drops configuration.
//!
//! Routes, imports and `when` blocks keep their document order: it decides
//! which route matches first.

use std::collections::HashSet;

use crate::services_yaml_convert::emit::{self, Node};
use crate::services_yaml_convert::{
    ConvertError, Result, context, err, insert_blank_lines, map_put, scalar_value,
};
use crate::services_yaml_model::{
    Element, XmlAttr, decode_root, unknown_attribute_error, unknown_element_error,
};
use crate::services_yaml_phpize::{PhpValue, phpize};

/// Points the YAML language server of editors to the JSON schema Symfony
/// 7.4+ ships for the routes.yaml format.
const ROUTING_SCHEMA_COMMENT: &str = "# yaml-language-server: $schema=https://raw.githubusercontent.com/symfony/routing/7.4/Loader/schema/routing.schema.json\n";

const ROUTE_ATTRIBUTES: &[&str] = &[
    "id",
    "path",
    "host",
    "controller",
    "schemes",
    "methods",
    "locale",
    "format",
    "utf8",
    "stateless",
];
const ROUTE_ELEMENTS: &[&str] = &[
    "path",
    "host",
    "default",
    "requirement",
    "option",
    "condition",
];
const IMPORT_ATTRIBUTES: &[&str] = &[
    "resource",
    "type",
    "prefix",
    "name-prefix",
    "host",
    "schemes",
    "methods",
    "locale",
    "format",
    "utf8",
    "stateless",
    "trailing-slash-on-root",
    "exclude",
];
const IMPORT_ELEMENTS: &[&str] = &[
    "prefix",
    "host",
    "default",
    "requirement",
    "option",
    "exclude",
];

/// The parsed representation of a Symfony routes.xml file.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Routes {
    root: Element,
}

/// Parses the content of a Symfony routes.xml file.
pub fn parse_routes_xml(content: &str) -> Result<Routes> {
    let root = decode_root(content, "routes.xml")?;
    if root.local != "routes" {
        return Err(ConvertError(format!(
            "failed to parse routes.xml: expected element type <routes> but have <{}>",
            root.local
        )));
    }
    Ok(Routes { root })
}

/// One `<import>`: the resource and, when given, the loader type.
pub(crate) struct ImportReference<'a> {
    pub(crate) resource: &'a mut String,
    pub(crate) kind: Option<&'a mut String>,
}

impl Routes {
    /// The imports of the file, including those inside `when` blocks, so
    /// a planner can point them at converted files.
    pub(crate) fn import_references(&mut self) -> Vec<ImportReference<'_>> {
        let mut references = Vec::new();
        collect_imports(&mut self.root.children, &mut references);
        references
    }
}

fn collect_imports<'a>(items: &'a mut [Element], references: &mut Vec<ImportReference<'a>>) {
    for item in items {
        match item.local.as_str() {
            "import" => {
                let mut resource = None;
                let mut kind = None;
                for attr in &mut item.attrs {
                    if !attr.space.is_empty() {
                        continue;
                    }
                    match attr.local.as_str() {
                        "resource" => resource = Some(&mut attr.value),
                        "type" => kind = Some(&mut attr.value),
                        _ => {}
                    }
                }
                if let Some(resource) = resource {
                    references.push(ImportReference { resource, kind });
                }
            }
            "when" => collect_imports(&mut item.children, references),
            _ => {}
        }
    }
}

/// Converts a parsed routes.xml file into the equivalent routes.yaml
/// content.
pub fn convert_routes_to_yaml(routes: &Routes) -> Result<String> {
    let mut root = Node::mapping();
    let mut used = HashSet::new();
    append_items(&mut root, &routes.root.children, &mut used, true)?;
    if root.content.is_empty() {
        return Ok(ROUTING_SCHEMA_COMMENT.to_owned());
    }
    let encoded = emit::encode(&root, 4);
    Ok(format!(
        "{ROUTING_SCHEMA_COMMENT}{}",
        insert_blank_lines(&encoded)
    ))
}

fn append_items(
    target: &mut Node,
    items: &[Element],
    used: &mut HashSet<String>,
    allow_when: bool,
) -> Result<()> {
    for item in items {
        match item.local.as_str() {
            "route" => {
                let id = item.attr("id");
                if id.is_empty() {
                    return err("<route> requires an id attribute");
                }
                let node = context(route_to_node(item), || format!("route {id:?}"))?;
                if !used.insert(id.to_owned()) {
                    return err(format!("route {id:?} is defined multiple times"));
                }
                map_put(target, id, node);
            }
            "import" => {
                let node = import_to_node(item)?;
                map_put(target, &import_key(item.attr("resource"), used), node);
            }
            "when" => {
                if !allow_when {
                    return err("<when> elements cannot be nested");
                }
                let env = item.attr("env");
                if env.is_empty() {
                    return err("<when> requires an env attribute");
                }
                let key = format!("when@{env}");
                if !used.insert(key.clone()) {
                    return err(format!("environment {env:?} is configured multiple times"));
                }
                let mut mapping = Node::mapping();
                append_items(&mut mapping, &item.children, &mut HashSet::new(), false)?;
                map_put(target, &key, mapping);
            }
            other => return err(format!("unsupported element <{other}> inside <routes>")),
        }
    }
    Ok(())
}

/// A readable, unique YAML key for an import: XML imports are anonymous,
/// YAML needs a mapping key.
fn import_key(resource: &str, used: &mut HashSet<String>) -> String {
    let mut sanitized = resource;
    if let Some(glob) = sanitized.find(['*', '?', '[', '{']) {
        sanitized = &sanitized[..glob];
    }
    // The extension of the last path segment, as `filepath.Ext` reads it.
    let name_start = sanitized.rfind(['/', '\\']).map_or(0, |index| index + 1);
    if let Some(dot) = sanitized[name_start..].rfind('.') {
        sanitized = &sanitized[..name_start + dot];
    }
    let parts: Vec<String> = sanitized
        .split(['/', '\\'])
        .filter(|part| !part.is_empty() && *part != "." && *part != "..")
        .filter_map(|part| {
            let mut key = String::new();
            for character in part.to_lowercase().chars() {
                if character.is_ascii_lowercase() || character.is_ascii_digit() {
                    key.push(character);
                } else if !key.ends_with('_') {
                    key.push('_');
                }
            }
            let key = key.trim_matches('_').to_owned();
            (!key.is_empty()).then_some(key)
        })
        .collect();
    let mut key = parts.join("_");
    if key.is_empty() {
        key = "imported_routes".to_owned();
    }
    if used.insert(key.clone()) {
        return key;
    }
    let mut number = 2;
    loop {
        let candidate = format!("{key}_{number}");
        if used.insert(candidate.clone()) {
            return candidate;
        }
        number += 1;
    }
}

/// Rejects attributes and child elements the conversion does not know.
fn check_known(element: &Element, attributes: &[&str], elements: &[&str]) -> Result<()> {
    let unknown_elements: Vec<String> = element
        .children
        .iter()
        .filter(|child| !elements.contains(&child.local.as_str()))
        .map(|child| child.local.clone())
        .collect();
    unknown_element_error(&element.local, &unknown_elements)?;
    let unknown_attributes: Vec<XmlAttr> = element
        .attrs
        .iter()
        .filter(|attr| !attr.space.is_empty() || !attributes.contains(&attr.local.as_str()))
        .cloned()
        .collect();
    unknown_attribute_error(&element.local, &unknown_attributes)
}

fn children<'a>(element: &'a Element, name: &'a str) -> impl Iterator<Item = &'a Element> {
    element
        .children
        .iter()
        .filter(move |child| child.local == name)
}

fn route_to_node(route: &Element) -> Result<Node> {
    check_known(route, ROUTE_ATTRIBUTES, ROUTE_ELEMENTS)?;
    let mut mapping = Node::mapping();
    let path = route.attr("path");
    let paths: Vec<&Element> = children(route, "path").collect();
    match (path.is_empty(), paths.is_empty()) {
        (false, false) => {
            return err("must not have both a path attribute and <path> child elements");
        }
        (false, true) => map_put(&mut mapping, "path", Node::string(path)),
        (true, false) => map_put(&mut mapping, "path", localized_to_node(&paths, "path")?),
        (true, true) => return err("requires a path attribute or <path> child elements"),
    }
    let controller = route.attr("controller");
    if !controller.is_empty() {
        if children(route, "default").any(|default| default.attr("key") == "_controller") {
            return err(
                "must not specify both the controller attribute and the _controller default",
            );
        }
        map_put(&mut mapping, "controller", Node::string(controller));
    }
    append_shared(&mut mapping, route)?;
    let condition: String = children(route, "condition")
        .map(|condition| condition.chardata.as_str())
        .collect();
    let condition = condition.trim();
    if !condition.is_empty() {
        map_put(&mut mapping, "condition", Node::string(condition));
    }
    Ok(mapping)
}

fn import_to_node(import: &Element) -> Result<Node> {
    check_known(import, IMPORT_ATTRIBUTES, IMPORT_ELEMENTS)?;
    let resource = import.attr("resource");
    if resource.is_empty() {
        return err("<import> requires a resource attribute");
    }
    let named = |result: Result<Node>| context(result, || format!("import {resource:?}"));
    let mut mapping = Node::mapping();
    map_put(&mut mapping, "resource", Node::string(resource));
    let kind = import.attr("type");
    if !kind.is_empty() {
        map_put(&mut mapping, "type", Node::string(kind));
    }
    let prefix = import.attr("prefix");
    let prefixes: Vec<&Element> = children(import, "prefix").collect();
    match (prefix.is_empty(), prefixes.is_empty()) {
        (false, false) => {
            return named(err(
                "must not have both a prefix attribute and <prefix> child elements",
            ));
        }
        (false, true) => map_put(&mut mapping, "prefix", Node::string(prefix)),
        (true, false) => map_put(
            &mut mapping,
            "prefix",
            named(localized_to_node(&prefixes, "prefix"))?,
        ),
        (true, true) => {}
    }
    let name_prefix = import.attr("name-prefix");
    if !name_prefix.is_empty() {
        map_put(&mut mapping, "name_prefix", Node::string(name_prefix));
    }
    let exclude = import.attr("exclude");
    let excludes: Vec<&Element> = children(import, "exclude").collect();
    if !exclude.is_empty() && !excludes.is_empty() {
        return named(err("mixes the exclude attribute with <exclude> elements"));
    }
    if !exclude.is_empty() {
        map_put(&mut mapping, "exclude", Node::string(exclude));
    } else if let [single] = excludes.as_slice() {
        map_put(&mut mapping, "exclude", Node::string(&single.chardata));
    } else if !excludes.is_empty() {
        let mut sequence = Node::sequence();
        for exclude in excludes {
            sequence.content.push(Node::string(&exclude.chardata));
        }
        map_put(&mut mapping, "exclude", sequence);
    }
    let trailing_slash = import.attr("trailing-slash-on-root");
    if !trailing_slash.is_empty() {
        map_put(
            &mut mapping,
            "trailing_slash_on_root",
            scalar_value(phpize(trailing_slash)),
        );
    }
    context(append_shared(&mut mapping, import), || {
        format!("import {resource:?}")
    })?;
    Ok(mapping)
}

/// The configuration `<route>` and `<import>` have in common.
fn append_shared(mapping: &mut Node, element: &Element) -> Result<()> {
    let host = element.attr("host");
    let hosts: Vec<&Element> = children(element, "host").collect();
    match (host.is_empty(), hosts.is_empty()) {
        (false, false) => {
            return err("must not have both a host attribute and <host> child elements");
        }
        (false, true) => map_put(mapping, "host", Node::string(host)),
        (true, false) => map_put(mapping, "host", localized_to_node(&hosts, "host")?),
        (true, true) => {}
    }
    for list in ["schemes", "methods"] {
        let value = element.attr(list);
        if !value.is_empty() {
            map_put(mapping, list, list_to_node(value));
        }
    }
    let defaults: Vec<&Element> = children(element, "default").collect();
    if !defaults.is_empty() {
        let mut node = Node::mapping();
        for default in defaults {
            let key = default.attr("key");
            if key.is_empty() {
                return err("<default> requires a key attribute");
            }
            let value = context(default_to_node(default), || format!("default {key:?}"))?;
            map_put(&mut node, key, value);
        }
        map_put(mapping, "defaults", node);
    }
    let requirements: Vec<&Element> = children(element, "requirement").collect();
    if !requirements.is_empty() {
        let node = keyed_to_node(&requirements, "requirement", |value| {
            Node::string(value.trim())
        })?;
        map_put(mapping, "requirements", node);
    }
    let options: Vec<&Element> = children(element, "option").collect();
    if !options.is_empty() {
        let node = keyed_to_node(&options, "option", |value| scalar_value(phpize(value)))?;
        map_put(mapping, "options", node);
    }
    for text in ["locale", "format"] {
        let value = element.attr(text);
        if !value.is_empty() {
            map_put(mapping, text, Node::string(value));
        }
    }
    for flag in ["utf8", "stateless"] {
        let value = element.attr(flag);
        if !value.is_empty() {
            map_put(mapping, flag, scalar_value(phpize(value)));
        }
    }
    Ok(())
}

/// Splits a schemes or methods list such as `GET|POST` or `GET, POST`.
fn list_to_node(value: &str) -> Node {
    let mut sequence = Node::sequence();
    sequence.flow = true;
    for entry in value
        .split(|character: char| character.is_whitespace() || character == ',' || character == '|')
    {
        if !entry.is_empty() {
            sequence.content.push(Node::string(entry));
        }
    }
    sequence
}

fn localized_to_node(values: &[&Element], element: &str) -> Result<Node> {
    let mut mapping = Node::mapping();
    for value in values {
        let locale = value.attr("locale");
        if locale.is_empty() {
            return err(format!(
                "<{element}> child elements require a locale attribute"
            ));
        }
        map_put(&mut mapping, locale, Node::string(value.chardata.trim()));
    }
    Ok(mapping)
}

fn keyed_to_node(
    values: &[&Element],
    element: &str,
    convert: impl Fn(&str) -> Node,
) -> Result<Node> {
    let mut mapping = Node::mapping();
    for value in values {
        let key = value.attr("key");
        if key.is_empty() {
            return err(format!("<{element}> requires a key attribute"));
        }
        map_put(&mut mapping, key, convert(&value.chardata));
    }
    Ok(mapping)
}

fn default_to_node(default: &Element) -> Result<Node> {
    if default.is_nil() {
        return Ok(Node::null("null"));
    }
    match default.children.as_slice() {
        // Symfony reads the plain text of a <default> as a string.
        [] => Ok(Node::string(default.chardata.trim())),
        [typed] => typed_to_node(typed),
        _ => err("only one typed value element is allowed"),
    }
}

fn typed_to_node(value: &Element) -> Result<Node> {
    if value.is_nil() {
        return Ok(Node::null("null"));
    }
    let trimmed = value.chardata.trim();
    match value.local.as_str() {
        "bool" => Ok(scalar_value(PhpValue::Bool(matches!(
            trimmed,
            "true" | "1"
        )))),
        "int" => match trimmed.parse::<i64>() {
            Ok(parsed) => Ok(scalar_value(PhpValue::Int(parsed))),
            Err(_) => err(format!("invalid <int> value {:?}", value.chardata)),
        },
        "float" => match emit::parse_finite_float(trimmed) {
            Some(parsed) => Ok(scalar_value(PhpValue::Float(parsed))),
            None => err(format!("invalid <float> value {:?}", value.chardata)),
        },
        "string" => Ok(Node::string(trimmed)),
        "list" => {
            let mut sequence = Node::sequence();
            for child in &value.children {
                sequence.content.push(typed_to_node(child)?);
            }
            Ok(sequence)
        }
        "map" => {
            let mut mapping = Node::mapping();
            for child in &value.children {
                let key = child.attr("key");
                if key.is_empty() {
                    return err("<map> entries require a key attribute");
                }
                map_put(&mut mapping, key, typed_to_node(child)?);
            }
            Ok(mapping)
        }
        other => err(format!("unsupported typed value element <{other}>")),
    }
}

#[cfg(test)]
#[path = "tests_yamlconv/routes_yaml_convert.rs"]
mod tests;
