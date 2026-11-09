//! The parsed representation of a Symfony `services.xml` file.
//!
//! The native XML CST is decoded into a small element tree: names
//! match by local name, `,any` fields collect unmatched children, `,any,attr`
//! fields collect unmatched attributes with their namespace resolved,
//! `,chardata` concatenates direct character data with entities decoded and
//! line endings normalized, and repeated pointer elements decode into the
//! same value.

use std::collections::HashMap;

use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::*;
use swlsp_syntax::SyntaxNode;

use crate::services_yaml_convert::ConvertError;

const XSI_NAMESPACE: &str = "http://www.w3.org/2001/XMLSchema-instance";
const XML_NAMESPACE: &str = "http://www.w3.org/XML/1998/namespace";

/// An XML attribute name with its resolved namespace and value.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct XmlAttr {
    pub space: String,
    pub local: String,
    pub value: String,
}

/// The parsed representation of a Symfony services.xml file.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Container {
    pub(crate) imports: Option<XmlImports>,
    pub(crate) parameters: Option<XmlParameters>,
    pub(crate) services: Option<XmlServices>,
    pub(crate) when: Vec<XmlWhen>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlWhen {
    pub(crate) env: String,
    pub(crate) imports: Option<XmlImports>,
    pub(crate) parameters: Option<XmlParameters>,
    pub(crate) services: Option<XmlServices>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlImports {
    pub(crate) imports: Vec<XmlImport>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlImport {
    pub(crate) resource: String,
    pub(crate) kind: String,
    pub(crate) ignore_errors: String,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlParameters {
    pub(crate) parameters: Vec<XmlParameter>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlParameter {
    pub(crate) key: String,
    pub(crate) kind: String,
    pub(crate) value: String,
    pub(crate) children: Vec<XmlParameter>,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlServices {
    pub(crate) defaults: Vec<XmlDefaults>,
    pub(crate) items: Vec<XmlServiceItem>,
}

/// A `service`, `prototype`, or `instanceof` element (or any other element,
/// with no definition) in document order.
#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlServiceItem {
    pub(crate) kind: String,
    pub(crate) service: Option<XmlService>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlDefaults {
    pub(crate) public: String,
    pub(crate) autowire: String,
    pub(crate) autoconfigure: String,
    pub(crate) binds: Vec<XmlArgument>,
    pub(crate) tags: Vec<XmlTag>,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlService {
    pub(crate) id: String,
    pub(crate) class: String,
    pub(crate) alias: String,
    pub(crate) namespace: String,
    pub(crate) resource: String,
    pub(crate) public: String,
    pub(crate) shared: String,
    pub(crate) synthetic: String,
    pub(crate) lazy: String,
    pub(crate) is_abstract: String,
    pub(crate) autowire: String,
    pub(crate) autoconfigure: String,
    pub(crate) parent: String,
    pub(crate) constructor: String,
    pub(crate) decorates: String,
    pub(crate) decoration_inner_name: String,
    pub(crate) decoration_priority: String,
    pub(crate) decoration_on_invalid: String,
    pub(crate) exclude_attr: String,
    pub(crate) excludes: Vec<String>,
    pub(crate) arguments: Vec<XmlArgument>,
    pub(crate) properties: Vec<XmlProperty>,
    pub(crate) calls: Vec<XmlCall>,
    pub(crate) tags: Vec<XmlTag>,
    pub(crate) binds: Vec<XmlArgument>,
    pub(crate) factory: Option<XmlCallable>,
    pub(crate) configurator: Option<XmlCallable>,
    pub(crate) deprecated: Option<XmlDeprecated>,
    pub(crate) file: String,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlArgument {
    pub(crate) kind: String,
    pub(crate) id: String,
    pub(crate) key: String,
    pub(crate) index: String,
    pub(crate) on_invalid: String,
    pub(crate) tag: String,
    pub(crate) index_by: String,
    pub(crate) default_index_method: String,
    pub(crate) default_priority_method: String,
    pub(crate) exclude_self: String,
    pub(crate) value: String,
    pub(crate) children: Vec<XmlArgument>,
    pub(crate) excludes: Vec<String>,
    pub(crate) inline_services: Vec<XmlService>,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlProperty {
    pub(crate) name: String,
    pub(crate) argument: XmlArgument,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlCall {
    pub(crate) method: String,
    pub(crate) returns_clone: String,
    pub(crate) arguments: Vec<XmlArgument>,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlTag {
    pub(crate) name_attr: String,
    pub(crate) value: String,
    pub(crate) attributes: Vec<XmlTagAttribute>,
    pub(crate) other_attrs: Vec<XmlAttr>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlTagAttribute {
    pub(crate) name: String,
    pub(crate) value: String,
    pub(crate) children: Vec<XmlTagAttribute>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlCallable {
    pub(crate) class: String,
    pub(crate) service: String,
    pub(crate) method: String,
    pub(crate) function: String,
    pub(crate) expression: String,
    pub(crate) unknown_attrs: Vec<XmlAttr>,
    pub(crate) unknown: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct XmlDeprecated {
    pub(crate) package: String,
    pub(crate) version: String,
    pub(crate) message: String,
}

/// A decoded XML element: local name, attributes, direct character data,
/// and child elements.
#[derive(Debug, Default)]
struct Element {
    local: String,
    attrs: Vec<XmlAttr>,
    chardata: String,
    children: Vec<Element>,
}

fn error(message: impl Into<String>) -> ConvertError {
    ConvertError(format!("failed to parse services.xml: {}", message.into()))
}

/// Parses the content of a Symfony services.xml file.
pub fn parse_services_xml(content: &str) -> Result<Container, ConvertError> {
    let parse = swlsp_parser_xml::parse(content);
    if let Some(problem) = parse.errors.first() {
        return Err(error(format!("XML syntax error: {}", problem.message())));
    }
    let root = parse.root();
    let Some(element) = root.children().find(|child| child.kind() == XML_ELEMENT) else {
        return Err(error("EOF"));
    };
    let element = decode_element(&element, &HashMap::new())?;
    if element.local != "container" {
        return Err(error(format!(
            "expected element type <container> but have <{}>",
            element.local
        )));
    }
    Ok(container(element))
}

fn split_name(name: &str) -> (&str, &str) {
    match name.find(':') {
        Some(index) if index >= 1 && index + 1 < name.len() => (&name[..index], &name[index + 1..]),
        _ => ("", name),
    }
}

fn decode_element(
    node: &SyntaxNode,
    parent_scope: &HashMap<String, String>,
) -> Result<Element, ConvertError> {
    let name = xmlquery::element_name(node).unwrap_or_default();
    if let Some(end) = node.children().find(|child| child.kind() == XML_END_TAG) {
        let end_name = end
            .children()
            .find(|child| child.kind() == XML_NAME)
            .map(|name| name.text().to_string().trim().to_owned())
            .unwrap_or_default();
        if end_name != name {
            return Err(error(format!("element <{name}> closed by </{end_name}>")));
        }
    }
    let mut raw_attrs: Vec<(String, String)> = Vec::new();
    for attribute in xmlquery::attributes(node) {
        let attribute_name = xmlquery::attribute_name(&attribute).unwrap_or_default();
        let raw = xmlquery::attribute_value(&attribute).unwrap_or_default();
        raw_attrs.push((attribute_name, decode_text(&raw)?));
    }
    let mut scope = parent_scope.clone();
    for (name, value) in &raw_attrs {
        if let Some(prefix) = name.strip_prefix("xmlns:") {
            scope.insert(prefix.to_owned(), value.clone());
        }
    }
    let attrs = raw_attrs
        .into_iter()
        .map(|(name, value)| {
            let (prefix, local) = split_name(&name);
            let space = match prefix {
                "" => String::new(),
                "xmlns" => "xmlns".to_owned(),
                "xml" => XML_NAMESPACE.to_owned(),
                prefix => scope
                    .get(prefix)
                    .cloned()
                    .unwrap_or_else(|| prefix.to_owned()),
            };
            XmlAttr {
                space,
                local: local.to_owned(),
                value,
            }
        })
        .collect();
    let mut element = Element {
        local: split_name(&name).1.to_owned(),
        attrs,
        ..Element::default()
    };
    if let Some(content) = node.children().find(|child| child.kind() == XML_CONTENT) {
        for child in content.children() {
            match child.kind() {
                XML_ELEMENT => element.children.push(decode_element(&child, &scope)?),
                XML_TEXT => element
                    .chardata
                    .push_str(&normalize_newlines(&child.text().to_string())),
                XML_ENTITY_REFERENCE => element
                    .chardata
                    .push_str(&decode_text(&child.text().to_string())?),
                XML_CDATA => {
                    let text = child.text().to_string();
                    let text = text.strip_prefix("<![CDATA[").unwrap_or(&text);
                    let text = text.strip_suffix("]]>").unwrap_or(text);
                    element.chardata.push_str(&normalize_newlines(text));
                }
                _ => {}
            }
        }
    }
    Ok(element)
}

fn normalize_newlines(text: &str) -> String {
    if !text.contains('\r') {
        return text.to_owned();
    }
    text.replace("\r\n", "\n").replace('\r', "\n")
}

/// Decodes the predefined and numeric entities of strict XML.
fn decode_text(text: &str) -> Result<String, ConvertError> {
    let text = normalize_newlines(text);
    if !text.contains('&') {
        return Ok(text);
    }
    let mut out = String::with_capacity(text.len());
    let mut rest = text.as_str();
    while let Some(start) = rest.find('&') {
        out.push_str(&rest[..start]);
        let after = &rest[start + 1..];
        let Some(end) = after.find(';') else {
            return Err(error("invalid character entity &"));
        };
        let name = &after[..end];
        let decoded = match name {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "apos" => Some('\''),
            "quot" => Some('"'),
            _ => name.strip_prefix('#').and_then(|number| {
                let code = match number.strip_prefix('x') {
                    Some(hex) => u32::from_str_radix(hex, 16).ok(),
                    None => number.parse::<u32>().ok(),
                }?;
                char::from_u32(code)
            }),
        };
        let Some(decoded) = decoded else {
            return Err(error(format!("invalid character entity &{name};")));
        };
        out.push(decoded);
        rest = &after[end + 1..];
    }
    out.push_str(rest);
    Ok(out)
}

/// Assigns known attributes by local name; the rest are unknown.
fn assign_attrs(attrs: &[XmlAttr], mut assign: impl FnMut(&str, &str) -> bool) -> Vec<XmlAttr> {
    attrs
        .iter()
        .filter(|attr| !assign(&attr.local, &attr.value))
        .cloned()
        .collect()
}

fn container(element: Element) -> Container {
    let mut result = Container::default();
    for child in element.children {
        match child.local.as_str() {
            "imports" => imports(result.imports.get_or_insert_with(Default::default), child),
            "parameters" => parameters(
                result.parameters.get_or_insert_with(Default::default),
                child,
            ),
            "services" => services(result.services.get_or_insert_with(Default::default), child),
            "when" => result.when.push(when(child)),
            _ => result.unknown.push(child.local),
        }
    }
    result
}

fn when(element: Element) -> XmlWhen {
    let mut result = XmlWhen::default();
    for attr in &element.attrs {
        if attr.local == "env" {
            result.env = attr.value.clone();
        }
    }
    for child in element.children {
        match child.local.as_str() {
            "imports" => imports(result.imports.get_or_insert_with(Default::default), child),
            "parameters" => parameters(
                result.parameters.get_or_insert_with(Default::default),
                child,
            ),
            "services" => services(result.services.get_or_insert_with(Default::default), child),
            _ => result.unknown.push(child.local),
        }
    }
    result
}

fn imports(target: &mut XmlImports, element: Element) {
    for child in element.children {
        if child.local == "import" {
            let mut import = XmlImport::default();
            import.unknown_attrs = assign_attrs(&child.attrs, |name, value| {
                let field = match name {
                    "resource" => &mut import.resource,
                    "type" => &mut import.kind,
                    "ignore-errors" => &mut import.ignore_errors,
                    _ => return false,
                };
                *field = value.to_owned();
                true
            });
            target.imports.push(import);
        } else {
            target.unknown.push(child.local);
        }
    }
}

fn parameters(target: &mut XmlParameters, element: Element) {
    for child in element.children {
        if child.local == "parameter" {
            target.parameters.push(parameter(child));
        } else {
            target.unknown.push(child.local);
        }
    }
}

fn parameter(element: Element) -> XmlParameter {
    let mut result = XmlParameter {
        value: element.chardata,
        ..XmlParameter::default()
    };
    result.unknown_attrs = assign_attrs(&element.attrs, |name, value| {
        let field = match name {
            "key" => &mut result.key,
            "type" => &mut result.kind,
            _ => return false,
        };
        *field = value.to_owned();
        true
    });
    for child in element.children {
        if child.local == "parameter" {
            result.children.push(parameter(child));
        } else {
            result.unknown.push(child.local);
        }
    }
    result
}

fn services(target: &mut XmlServices, element: Element) {
    for child in element.children {
        match child.local.as_str() {
            "defaults" => target.defaults.push(defaults(child)),
            "service" | "prototype" | "instanceof" => target.items.push(XmlServiceItem {
                kind: child.local.clone(),
                service: Some(service(child)),
            }),
            _ => target.items.push(XmlServiceItem {
                kind: child.local,
                service: None,
            }),
        }
    }
}

fn defaults(element: Element) -> XmlDefaults {
    let mut result = XmlDefaults::default();
    result.unknown_attrs = assign_attrs(&element.attrs, |name, value| {
        let field = match name {
            "public" => &mut result.public,
            "autowire" => &mut result.autowire,
            "autoconfigure" => &mut result.autoconfigure,
            _ => return false,
        };
        *field = value.to_owned();
        true
    });
    for child in element.children {
        match child.local.as_str() {
            "bind" => result.binds.push(argument(child)),
            "tag" => result.tags.push(tag(child)),
            _ => result.unknown.push(child.local),
        }
    }
    result
}

fn service(element: Element) -> XmlService {
    let mut result = XmlService::default();
    result.unknown_attrs = assign_attrs(&element.attrs, |name, value| {
        let field = match name {
            "id" => &mut result.id,
            "class" => &mut result.class,
            "alias" => &mut result.alias,
            "namespace" => &mut result.namespace,
            "resource" => &mut result.resource,
            "public" => &mut result.public,
            "shared" => &mut result.shared,
            "synthetic" => &mut result.synthetic,
            "lazy" => &mut result.lazy,
            "abstract" => &mut result.is_abstract,
            "autowire" => &mut result.autowire,
            "autoconfigure" => &mut result.autoconfigure,
            "parent" => &mut result.parent,
            "constructor" => &mut result.constructor,
            "decorates" => &mut result.decorates,
            "decoration-inner-name" => &mut result.decoration_inner_name,
            "decoration-priority" => &mut result.decoration_priority,
            "decoration-on-invalid" => &mut result.decoration_on_invalid,
            "exclude" => &mut result.exclude_attr,
            _ => return false,
        };
        *field = value.to_owned();
        true
    });
    for child in element.children {
        match child.local.as_str() {
            "exclude" => result.excludes.push(child.chardata),
            "argument" => result.arguments.push(argument(child)),
            "property" => result.properties.push(property(child)),
            "call" => result.calls.push(call(child)),
            "tag" => result.tags.push(tag(child)),
            "bind" => result.binds.push(argument(child)),
            "factory" => callable(result.factory.get_or_insert_with(Default::default), child),
            "configurator" => callable(
                result.configurator.get_or_insert_with(Default::default),
                child,
            ),
            "deprecated" => deprecated(
                result.deprecated.get_or_insert_with(Default::default),
                child,
            ),
            "file" => result.file = child.chardata,
            _ => result.unknown.push(child.local),
        }
    }
    result
}

fn argument_attr(argument: &mut XmlArgument, name: &str, value: &str) -> bool {
    let field = match name {
        "type" => &mut argument.kind,
        "id" => &mut argument.id,
        "key" => &mut argument.key,
        "index" => &mut argument.index,
        "on-invalid" => &mut argument.on_invalid,
        "tag" => &mut argument.tag,
        "index-by" => &mut argument.index_by,
        "default-index-method" => &mut argument.default_index_method,
        "default-priority-method" => &mut argument.default_priority_method,
        "exclude-self" => &mut argument.exclude_self,
        _ => return false,
    };
    *field = value.to_owned();
    true
}

fn argument_children(argument: &mut XmlArgument, children: Vec<Element>) {
    for child in children {
        match child.local.as_str() {
            "argument" => argument.children.push(self::argument(child)),
            "exclude" => argument.excludes.push(child.chardata),
            "service" => argument.inline_services.push(service(child)),
            _ => argument.unknown.push(child.local),
        }
    }
}

fn argument(element: Element) -> XmlArgument {
    let mut result = XmlArgument {
        value: element.chardata,
        ..XmlArgument::default()
    };
    result.unknown_attrs = assign_attrs(&element.attrs, |name, value| {
        argument_attr(&mut result, name, value)
    });
    argument_children(&mut result, element.children);
    result
}

fn property(element: Element) -> XmlProperty {
    let mut name = String::new();
    let mut argument = XmlArgument {
        value: element.chardata,
        ..XmlArgument::default()
    };
    argument.unknown_attrs = assign_attrs(&element.attrs, |attr, value| {
        if attr == "name" {
            name = value.to_owned();
            return true;
        }
        argument_attr(&mut argument, attr, value)
    });
    argument_children(&mut argument, element.children);
    XmlProperty { name, argument }
}

fn call(element: Element) -> XmlCall {
    let mut result = XmlCall::default();
    result.unknown_attrs = assign_attrs(&element.attrs, |name, value| {
        let field = match name {
            "method" => &mut result.method,
            "returns-clone" => &mut result.returns_clone,
            _ => return false,
        };
        *field = value.to_owned();
        true
    });
    for child in element.children {
        if child.local == "argument" {
            result.arguments.push(argument(child));
        } else {
            result.unknown.push(child.local);
        }
    }
    result
}

fn tag(element: Element) -> XmlTag {
    let mut result = XmlTag {
        value: element.chardata,
        ..XmlTag::default()
    };
    result.other_attrs = assign_attrs(&element.attrs, |name, value| {
        if name == "name" {
            result.name_attr = value.to_owned();
            return true;
        }
        false
    });
    for child in element.children {
        if child.local == "attribute" {
            result.attributes.push(tag_attribute(child));
        }
    }
    result
}

fn tag_attribute(element: Element) -> XmlTagAttribute {
    let mut result = XmlTagAttribute {
        value: element.chardata,
        ..XmlTagAttribute::default()
    };
    for attr in &element.attrs {
        if attr.local == "name" {
            result.name = attr.value.clone();
        }
    }
    for child in element.children {
        if child.local == "attribute" {
            result.children.push(tag_attribute(child));
        }
    }
    result
}

fn callable(target: &mut XmlCallable, element: Element) {
    let unknown = assign_attrs(&element.attrs, |name, value| {
        let field = match name {
            "class" => &mut target.class,
            "service" => &mut target.service,
            "method" => &mut target.method,
            "function" => &mut target.function,
            "expression" => &mut target.expression,
            _ => return false,
        };
        *field = value.to_owned();
        true
    });
    target.unknown_attrs.extend(unknown);
    target
        .unknown
        .extend(element.children.into_iter().map(|child| child.local));
}

fn deprecated(target: &mut XmlDeprecated, element: Element) {
    for attr in &element.attrs {
        match attr.local.as_str() {
            "package" => target.package = attr.value.clone(),
            "version" => target.version = attr.value.clone(),
            _ => {}
        }
    }
    target.message = element.chardata;
}

/// Rejects the first child element the conversion does not understand.
pub(crate) fn unknown_element_error(parent: &str, unknown: &[String]) -> Result<(), ConvertError> {
    match unknown.first() {
        Some(name) => Err(ConvertError(format!(
            "unsupported element <{name}> inside <{parent}>"
        ))),
        None => Ok(()),
    }
}

/// Rejects the first attribute the conversion does not understand;
/// namespace declarations and schema locations carry no configuration.
pub(crate) fn unknown_attribute_error(parent: &str, attrs: &[XmlAttr]) -> Result<(), ConvertError> {
    for attr in attrs {
        if attr.space == "xmlns" || attr.local == "xmlns" || attr.space == XSI_NAMESPACE {
            continue;
        }
        return Err(ConvertError(format!(
            "unsupported attribute {:?} on <{parent}>",
            attr.local
        )));
    }
    Ok(())
}
