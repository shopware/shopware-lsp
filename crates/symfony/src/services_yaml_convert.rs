//! Lossless conversion of a parsed `services.xml` container into the
//! equivalent `services.yaml`, rendered by the emitter in `services_yaml_emit`.

use std::collections::HashSet;

use crate::services_yaml_model::{
    Container, XmlArgument, XmlAttr, XmlCall, XmlCallable, XmlDefaults, XmlDeprecated, XmlImports,
    XmlParameter, XmlParameters, XmlProperty, XmlService, XmlServices, XmlTag, XmlTagAttribute,
    unknown_attribute_error, unknown_element_error,
};
use crate::services_yaml_phpize::{PhpValue, phpize};

#[path = "services_yaml_emit.rs"]
pub(crate) mod emit;

use emit::{Node, NodeKind};

pub(crate) use emit::parse_finite_float;

/// Points the YAML language server of editors to the JSON schema Symfony
/// 7.4+ ships for the services.yaml format.
const SERVICES_SCHEMA_COMMENT: &str = "# yaml-language-server: $schema=https://raw.githubusercontent.com/symfony/dependency-injection/7.4/Loader/schema/services.schema.json\n";

/// A conversion that cannot be expressed losslessly, or invalid input.
/// The message names the offending element, prefixed by the service,
/// parameter or call it belongs to.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[error("{0}")]
pub struct ConvertError(pub String);

pub(crate) type Result<T> = std::result::Result<T, ConvertError>;

pub(crate) fn err<T>(message: impl Into<String>) -> Result<T> {
    Err(ConvertError(message.into()))
}

pub(crate) fn context<T>(result: Result<T>, prefix: impl FnOnce() -> String) -> Result<T> {
    result.map_err(|error| ConvertError(format!("{}: {}", prefix(), error.0)))
}

/// Converts a parsed services.xml container into the equivalent
/// services.yaml content. Fails whenever the XML contains something that
/// cannot be expressed safely in YAML, so a conversion never silently drops
/// configuration.
pub fn convert_container_to_yaml(container: &Container) -> Result<String> {
    unknown_element_error("container", &container.unknown)?;
    let mut root = Node::mapping();
    append_container_sections(
        &mut root,
        container.imports.as_ref(),
        container.parameters.as_ref(),
        container.services.as_ref(),
    )?;
    for when in &container.when {
        if when.env.is_empty() {
            return err("<when> requires an env attribute");
        }
        unknown_element_error("when", &when.unknown)?;
        let mut mapping = Node::mapping();
        append_container_sections(
            &mut mapping,
            when.imports.as_ref(),
            when.parameters.as_ref(),
            when.services.as_ref(),
        )?;
        map_put(&mut root, &format!("when@{}", when.env), mapping);
    }
    if root.content.is_empty() {
        return Ok(SERVICES_SCHEMA_COMMENT.to_owned());
    }
    let encoded = emit::encode(&root, 4);
    Ok(format!(
        "{SERVICES_SCHEMA_COMMENT}{}",
        insert_blank_lines(&encoded)
    ))
}

fn append_container_sections(
    target: &mut Node,
    imports: Option<&XmlImports>,
    parameters: Option<&XmlParameters>,
    services: Option<&XmlServices>,
) -> Result<()> {
    if let Some(imports) = imports {
        let node = imports_to_node(imports)?;
        if !node.content.is_empty() {
            map_put(target, "imports", node);
        }
    }
    if let Some(parameters) = parameters {
        let node = parameters_to_node(parameters)?;
        if !node.content.is_empty() {
            map_put(target, "parameters", node);
        }
    }
    if let Some(services) = services {
        let node = services_to_node(services)?;
        if !node.content.is_empty() {
            map_put(target, "services", node);
        }
    }
    Ok(())
}

fn imports_to_node(imports: &XmlImports) -> Result<Node> {
    unknown_element_error("imports", &imports.unknown)?;
    let mut sequence = Node::sequence();
    for import in &imports.imports {
        unknown_attribute_error("import", &import.unknown_attrs)?;
        if import.resource.is_empty() {
            return err("<import> requires a resource attribute");
        }
        let mut entry = Node::mapping();
        entry.flow = true;
        map_put(&mut entry, "resource", Node::string(&import.resource));
        if !import.kind.is_empty() {
            map_put(&mut entry, "type", Node::string(&import.kind));
        }
        if !import.ignore_errors.is_empty() {
            map_put(
                &mut entry,
                "ignore_errors",
                scalar_value(phpize(&import.ignore_errors)),
            );
        }
        sequence.content.push(entry);
    }
    Ok(sequence)
}

fn parameters_to_node(parameters: &XmlParameters) -> Result<Node> {
    unknown_element_error("parameters", &parameters.unknown)?;
    let mut mapping = Node::mapping();
    for parameter in &parameters.parameters {
        if parameter.key.is_empty() {
            return err("<parameter> requires a key attribute");
        }
        let value = context(parameter_value_to_node(parameter), || {
            format!("parameter {:?}", parameter.key)
        })?;
        map_put(&mut mapping, &parameter.key, value);
    }
    Ok(mapping)
}

fn parameter_value_to_node(parameter: &XmlParameter) -> Result<Node> {
    unknown_element_error("parameter", &parameter.unknown)?;
    unknown_attribute_error("parameter", &parameter.unknown_attrs)?;
    match parameter.kind.as_str() {
        "collection" => parameter_collection_to_node(&parameter.children),
        "constant" => Ok(Node::tagged("!php/const", parameter.value.trim())),
        "binary" => Ok(Node::tagged("!!binary", parameter.value.trim())),
        "string" => Ok(Node::string(&parameter.value)),
        "" => {
            if !parameter.children.is_empty() {
                return parameter_collection_to_node(&parameter.children);
            }
            Ok(scalar_value(phpize(&parameter.value)))
        }
        kind => err(format!("unsupported parameter type {kind:?}")),
    }
}

fn parameter_collection_to_node(children: &[XmlParameter]) -> Result<Node> {
    if children.iter().all(|child| child.key.is_empty()) {
        let mut sequence = Node::sequence();
        for child in children {
            sequence.content.push(parameter_value_to_node(child)?);
        }
        return Ok(sequence);
    }
    let mut mapping = Node::mapping();
    for child in children {
        if child.key.is_empty() {
            return err("collection mixes keyed and unkeyed <parameter> elements");
        }
        let value = parameter_value_to_node(child)?;
        map_put(&mut mapping, &child.key, value);
    }
    Ok(mapping)
}

fn services_to_node(services: &XmlServices) -> Result<Node> {
    let mut mapping = Node::mapping();
    if services.defaults.len() > 1 {
        return err("multiple <defaults> elements are not supported");
    }
    if let Some(defaults) = services.defaults.first() {
        let node = defaults_to_node(defaults)?;
        if !node.content.is_empty() {
            map_put(&mut mapping, "_defaults", node);
        }
    }
    let mut instanceof = Node::mapping();
    let mut seen_instanceof: HashSet<&str> = HashSet::new();
    for item in &services.items {
        if item.kind != "instanceof" {
            continue;
        }
        let Some(service) = item.service.as_ref() else {
            continue;
        };
        if service.id.is_empty() {
            return err("<instanceof> requires an id attribute");
        }
        if !service.class.is_empty() {
            return err(format!(
                "instanceof {:?}: the class attribute is not supported",
                service.id
            ));
        }
        reject_prototype_fields(service, &format!("instanceof {:?}", service.id))?;
        if !seen_instanceof.insert(&service.id) {
            return err(format!("duplicate <instanceof> for {:?}", service.id));
        }
        let body = context(service_body_to_node(service, false), || {
            format!("instanceof {:?}", service.id)
        })?;
        map_put(&mut instanceof, &service.id, body);
    }
    if !instanceof.content.is_empty() {
        map_put(&mut mapping, "_instanceof", instanceof);
    }
    let mut seen: HashSet<String> = HashSet::new();
    for item in &services.items {
        let (key, node) = match (item.kind.as_str(), item.service.as_ref()) {
            ("service", Some(service)) => service_to_node(service)?,
            ("prototype", Some(service)) => prototype_to_node(service)?,
            ("instanceof", _) => continue,
            (kind, _) => return err(format!("unsupported element <{kind}> inside <services>")),
        };
        if !seen.insert(key.clone()) {
            return err(format!("service {key:?} is defined multiple times"));
        }
        map_put(&mut mapping, &key, node);
    }
    Ok(mapping)
}

fn defaults_to_node(defaults: &XmlDefaults) -> Result<Node> {
    unknown_element_error("defaults", &defaults.unknown)?;
    unknown_attribute_error("defaults", &defaults.unknown_attrs)?;
    let mut mapping = Node::mapping();
    for (key, value) in [
        ("autowire", &defaults.autowire),
        ("autoconfigure", &defaults.autoconfigure),
        ("public", &defaults.public),
    ] {
        if !value.is_empty() {
            map_put(&mut mapping, key, scalar_value(phpize(value)));
        }
    }
    if !defaults.binds.is_empty() {
        let node = context(binds_to_node(&defaults.binds), || "defaults".to_owned())?;
        map_put(&mut mapping, "bind", node);
    }
    if !defaults.tags.is_empty() {
        let node = context(tags_to_node(&defaults.tags), || "defaults".to_owned())?;
        map_put(&mut mapping, "tags", node);
    }
    Ok(mapping)
}

fn service_to_node(service: &XmlService) -> Result<(String, Node)> {
    if service.id.is_empty() {
        return err("<service> requires an id attribute");
    }
    reject_prototype_fields(service, &format!("service {:?}", service.id))?;
    if !service.alias.is_empty() {
        let node = context(alias_to_node(service), || format!("alias {:?}", service.id))?;
        return Ok((service.id.clone(), node));
    }
    let omit_class = service.class == service.id && service.id.contains('\\');
    let body = context(service_body_to_node(service, omit_class), || {
        format!("service {:?}", service.id)
    })?;
    Ok((service.id.clone(), body))
}

fn alias_to_node(service: &XmlService) -> Result<Node> {
    unknown_element_error("service", &service.unknown)?;
    unknown_attribute_error("service", &service.unknown_attrs)?;
    if has_definition_config(service) {
        return err("aliases only support the public attribute and <deprecated>");
    }
    if service.public.is_empty() && service.deprecated.is_none() {
        return Ok(Node::string(&format!("@{}", service.alias)));
    }
    let mut mapping = Node::mapping();
    map_put(&mut mapping, "alias", Node::string(&service.alias));
    if !service.public.is_empty() {
        map_put(
            &mut mapping,
            "public",
            scalar_value(phpize(&service.public)),
        );
    }
    if let Some(deprecated) = &service.deprecated {
        map_put(&mut mapping, "deprecated", deprecated_to_node(deprecated));
    }
    Ok(mapping)
}

fn reject_prototype_fields(service: &XmlService, context: &str) -> Result<()> {
    if !service.namespace.is_empty()
        || !service.resource.is_empty()
        || !service.exclude_attr.is_empty()
        || !service.excludes.is_empty()
    {
        return err(format!(
            "{context}: namespace, resource and exclude are only supported on <prototype>"
        ));
    }
    Ok(())
}

fn has_definition_config(service: &XmlService) -> bool {
    [
        &service.class,
        &service.shared,
        &service.synthetic,
        &service.lazy,
        &service.is_abstract,
        &service.autowire,
        &service.autoconfigure,
        &service.parent,
        &service.constructor,
        &service.decorates,
        &service.decoration_inner_name,
        &service.decoration_priority,
        &service.decoration_on_invalid,
        &service.file,
    ]
    .iter()
    .any(|value| !value.is_empty())
        || !service.arguments.is_empty()
        || !service.properties.is_empty()
        || !service.calls.is_empty()
        || !service.tags.is_empty()
        || !service.binds.is_empty()
        || service.factory.is_some()
        || service.configurator.is_some()
}

fn prototype_to_node(prototype: &XmlService) -> Result<(String, Node)> {
    if prototype.namespace.is_empty() {
        return err("<prototype> requires a namespace attribute");
    }
    let namespace = format!("{:?}", prototype.namespace);
    if prototype.resource.is_empty() {
        return err(format!(
            "prototype {namespace}: requires a resource attribute"
        ));
    }
    if !prototype.id.is_empty() || !prototype.alias.is_empty() || !prototype.class.is_empty() {
        return err(format!(
            "prototype {namespace}: id, alias and class are not supported"
        ));
    }
    let mut mapping = Node::mapping();
    map_put(&mut mapping, "resource", Node::string(&prototype.resource));
    if !prototype.exclude_attr.is_empty() && !prototype.excludes.is_empty() {
        return err(format!(
            "prototype {namespace}: mixes the exclude attribute with <exclude> elements"
        ));
    }
    if !prototype.exclude_attr.is_empty() {
        map_put(
            &mut mapping,
            "exclude",
            Node::string(&prototype.exclude_attr),
        );
    } else if prototype.excludes.len() == 1 {
        map_put(
            &mut mapping,
            "exclude",
            Node::string(&prototype.excludes[0]),
        );
    } else if prototype.excludes.len() > 1 {
        let mut excludes = Node::sequence();
        excludes.content.extend(
            prototype
                .excludes
                .iter()
                .map(|exclude| Node::string(exclude)),
        );
        map_put(&mut mapping, "exclude", excludes);
    }
    let body = context(service_body_to_node(prototype, false), || {
        format!("prototype {namespace}")
    })?;
    if body.kind == NodeKind::Mapping {
        mapping.content.extend(body.content);
    }
    Ok((prototype.namespace.clone(), mapping))
}

/// Everything inside a service definition, shared between services,
/// prototypes, instanceof conditionals and inline services. An empty
/// definition is null (`~`).
fn service_body_to_node(service: &XmlService, omit_class: bool) -> Result<Node> {
    unknown_element_error("service", &service.unknown)?;
    unknown_attribute_error("service", &service.unknown_attrs)?;
    let mut mapping = Node::mapping();
    append_service_attributes(&mut mapping, service, omit_class);
    append_decoration(&mut mapping, service)?;
    append_service_children(&mut mapping, service)?;
    if mapping.content.is_empty() {
        return Ok(Node::null("~"));
    }
    Ok(mapping)
}

fn append_service_attributes(mapping: &mut Node, service: &XmlService, omit_class: bool) {
    if !service.class.is_empty() && !omit_class {
        map_put(mapping, "class", Node::string(&service.class));
    }
    if !service.file.is_empty() {
        map_put(mapping, "file", Node::string(&service.file));
    }
    if !service.parent.is_empty() {
        map_put(mapping, "parent", Node::string(&service.parent));
    }
    for (key, value) in [
        ("lazy", &service.lazy),
        ("shared", &service.shared),
        ("synthetic", &service.synthetic),
        ("abstract", &service.is_abstract),
        ("public", &service.public),
        ("autowire", &service.autowire),
        ("autoconfigure", &service.autoconfigure),
    ] {
        if !value.is_empty() {
            map_put(mapping, key, scalar_value(phpize(value)));
        }
    }
    if !service.constructor.is_empty() {
        map_put(mapping, "constructor", Node::string(&service.constructor));
    }
}

fn append_decoration(mapping: &mut Node, service: &XmlService) -> Result<()> {
    if service.decorates.is_empty() {
        if !service.decoration_inner_name.is_empty()
            || !service.decoration_priority.is_empty()
            || !service.decoration_on_invalid.is_empty()
        {
            return err("decoration attributes require the decorates attribute");
        }
        return Ok(());
    }
    map_put(mapping, "decorates", Node::string(&service.decorates));
    if !service.decoration_inner_name.is_empty() {
        map_put(
            mapping,
            "decoration_inner_name",
            Node::string(&service.decoration_inner_name),
        );
    }
    if !service.decoration_priority.is_empty() {
        map_put(
            mapping,
            "decoration_priority",
            scalar_value(phpize(&service.decoration_priority)),
        );
    }
    match service.decoration_on_invalid.as_str() {
        "" => {}
        "exception" | "ignore" => {
            map_put(
                mapping,
                "decoration_on_invalid",
                Node::string(&service.decoration_on_invalid),
            );
        }
        // XML uses the string "null" while YAML expects an actual null value.
        "null" => map_put(mapping, "decoration_on_invalid", Node::null("null")),
        other => {
            return err(format!("unsupported decoration-on-invalid value {other:?}"));
        }
    }
    Ok(())
}

fn append_service_children(mapping: &mut Node, service: &XmlService) -> Result<()> {
    if let Some(deprecated) = &service.deprecated {
        map_put(mapping, "deprecated", deprecated_to_node(deprecated));
    }
    if let Some(factory) = &service.factory {
        map_put(mapping, "factory", callable_to_node(factory, "factory")?);
    }
    if let Some(configurator) = &service.configurator {
        map_put(
            mapping,
            "configurator",
            callable_to_node(configurator, "configurator")?,
        );
    }
    if !service.arguments.is_empty() {
        map_put(
            mapping,
            "arguments",
            arguments_to_node(
                &service.arguments,
                !service.parent.is_empty(),
                Position::Constructor,
            )?,
        );
    }
    if !service.properties.is_empty() {
        map_put(
            mapping,
            "properties",
            properties_to_node(&service.properties)?,
        );
    }
    if !service.binds.is_empty() {
        map_put(mapping, "bind", binds_to_node(&service.binds)?);
    }
    if !service.calls.is_empty() {
        map_put(mapping, "calls", calls_to_node(&service.calls)?);
    }
    if !service.tags.is_empty() {
        map_put(mapping, "tags", tags_to_node(&service.tags)?);
    }
    Ok(())
}

fn deprecated_to_node(deprecated: &XmlDeprecated) -> Node {
    let mut mapping = Node::mapping();
    if !deprecated.package.is_empty() {
        map_put(&mut mapping, "package", Node::string(&deprecated.package));
    }
    if !deprecated.version.is_empty() {
        map_put(&mut mapping, "version", Node::string(&deprecated.version));
    }
    let message = deprecated.message.trim();
    if !message.is_empty() {
        map_put(&mut mapping, "message", Node::string(message));
    }
    mapping
}

fn callable_to_node(callable: &XmlCallable, element: &str) -> Result<Node> {
    unknown_element_error(element, &callable.unknown)?;
    unknown_attribute_error(element, &callable.unknown_attrs)?;
    if !callable.expression.is_empty() {
        return Ok(Node::string(&format!("@={}", callable.expression)));
    }
    if !callable.function.is_empty() {
        return Ok(Node::string(&callable.function));
    }
    let target = if !callable.service.is_empty() {
        Node::string(&format!("@{}", callable.service))
    } else if !callable.class.is_empty() {
        Node::string(&callable.class)
    } else if element == "factory" && !callable.method.is_empty() {
        // A factory with only a method refers to the service class itself.
        Node::null("null")
    } else {
        return err(format!(
            "<{element}> requires a class, service, function or expression attribute"
        ));
    };
    let method = if callable.method.is_empty() {
        "__invoke"
    } else {
        &callable.method
    };
    let mut pair = Node::sequence();
    pair.flow = true;
    pair.content.push(target);
    pair.content.push(Node::string(method));
    Ok(pair)
}

/// Where a list of arguments sits, which decides what a missing optional
/// service leaves behind.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Position {
    /// Directly in the constructor (or factory) arguments of a service.
    Constructor,
    /// In a method call, a property or a collection.
    Nested,
}

fn arguments_to_node(
    arguments: &[XmlArgument],
    is_child_definition: bool,
    position: Position,
) -> Result<Node> {
    let keyed = arguments
        .iter()
        .any(|argument| !argument.key.is_empty() || !argument.index.is_empty());
    if !keyed {
        let mut sequence = Node::sequence();
        for argument in arguments {
            sequence
                .content
                .push(argument_value_to_node(argument, position)?);
        }
        return Ok(sequence);
    }
    let mut mapping = Node::mapping();
    let mut next_index: i64 = 0;
    for argument in arguments {
        let key = if !argument.key.is_empty() {
            Node::string(&argument.key)
        } else if !argument.index.is_empty() {
            let PhpValue::Int(index) = phpize(&argument.index) else {
                return err(format!("invalid argument index {:?}", argument.index));
            };
            if index >= next_index {
                next_index = index + 1;
            }
            if is_child_definition {
                Node::string(&format!("index_{index}"))
            } else {
                Node::implicit(index.to_string())
            }
        } else {
            let key = Node::implicit(next_index.to_string());
            next_index += 1;
            key
        };
        let value = argument_value_to_node(argument, position)?;
        mapping.content.push(key);
        mapping.content.push(value);
    }
    Ok(mapping)
}

fn argument_value_to_node(argument: &XmlArgument, position: Position) -> Result<Node> {
    unknown_element_error("argument", &argument.unknown)?;
    unknown_attribute_error("argument", &argument.unknown_attrs)?;
    if !argument.inline_services.is_empty() && argument.kind != "service" {
        return err("inline <service> elements require type=\"service\"");
    }
    if !argument.excludes.is_empty()
        && !matches!(
            argument.kind.as_str(),
            "tagged" | "tagged_iterator" | "tagged_locator"
        )
    {
        return err("<exclude> elements are only supported on tagged iterator arguments");
    }
    match argument.kind.as_str() {
        "" => {
            if !argument.children.is_empty() {
                return arguments_to_node(&argument.children, false, Position::Nested);
            }
            Ok(phpized_service_value(&argument.value))
        }
        "collection" => arguments_to_node(&argument.children, false, Position::Nested),
        "service" => {
            if argument.inline_services.len() > 1 {
                return err("only one inline <service> is supported per argument");
            }
            if let Some(service) = argument.inline_services.first() {
                return inline_service_to_node(service);
            }
            if argument.id.is_empty() {
                return err("argument type=\"service\" requires an id attribute");
            }
            reference_node(&argument.id, &argument.on_invalid, position)
        }
        "expression" => Ok(Node::string(&format!("@={}", argument.value))),
        "string" => Ok(service_string(&argument.value)),
        "constant" => Ok(Node::tagged("!php/const", argument.value.trim())),
        "binary" => Ok(Node::tagged("!!binary", argument.value.trim())),
        "abstract" => Ok(Node::tagged("!abstract", &argument.value)),
        "iterator" => {
            let mut node = arguments_to_node(&argument.children, false, Position::Nested)?;
            node.tag = "!iterator".to_owned();
            Ok(node)
        }
        "service_locator" => {
            let mut node = arguments_to_node(&argument.children, false, Position::Nested)?;
            node.tag = "!service_locator".to_owned();
            Ok(node)
        }
        "service_closure" | "closure" => {
            if argument.id.is_empty() {
                return err(format!(
                    "argument type={:?} requires an id attribute",
                    argument.kind
                ));
            }
            // A closure is resolved like a collection entry.
            let mut node = reference_node(&argument.id, &argument.on_invalid, Position::Nested)?;
            node.tag = format!("!{}", argument.kind);
            Ok(node)
        }
        "tagged" | "tagged_iterator" | "tagged_locator" => tagged_iterator_to_node(argument),
        kind => err(format!("unsupported argument type {kind:?}")),
    }
}

fn tagged_iterator_to_node(argument: &XmlArgument) -> Result<Node> {
    if argument.tag.is_empty() {
        return err(format!(
            "argument type={:?} requires a tag attribute",
            argument.kind
        ));
    }
    let yaml_tag = if argument.kind == "tagged_locator" {
        "!tagged_locator"
    } else {
        "!tagged_iterator"
    };
    if argument.index_by.is_empty()
        && argument.default_index_method.is_empty()
        && argument.default_priority_method.is_empty()
        && argument.exclude_self.is_empty()
        && argument.excludes.is_empty()
    {
        return Ok(Node::tagged(yaml_tag, &argument.tag));
    }
    let mut mapping = Node::mapping();
    mapping.flow = true;
    mapping.tag = yaml_tag.to_owned();
    map_put(&mut mapping, "tag", Node::string(&argument.tag));
    for (key, value) in [
        ("index_by", &argument.index_by),
        ("default_index_method", &argument.default_index_method),
        ("default_priority_method", &argument.default_priority_method),
    ] {
        if !value.is_empty() {
            map_put(&mut mapping, key, Node::string(value));
        }
    }
    if !argument.excludes.is_empty() {
        let mut excludes = Node::sequence();
        excludes.flow = true;
        excludes.content.extend(
            argument
                .excludes
                .iter()
                .map(|exclude| Node::string(exclude)),
        );
        map_put(&mut mapping, "exclude", excludes);
    }
    if !argument.exclude_self.is_empty() {
        map_put(
            &mut mapping,
            "exclude_self",
            scalar_value(phpize(&argument.exclude_self)),
        );
    }
    Ok(mapping)
}

fn inline_service_to_node(service: &XmlService) -> Result<Node> {
    if !service.id.is_empty() || !service.alias.is_empty() {
        return err("inline services do not support id or alias attributes");
    }
    if service.class.is_empty() {
        return err("inline services require a class attribute");
    }
    reject_prototype_fields(service, "inline service")?;
    let mut node = service_body_to_node(service, false)?;
    node.tag = "!service".to_owned();
    Ok(node)
}

fn reference_node(id: &str, on_invalid: &str, position: Position) -> Result<Node> {
    let prefix = match on_invalid {
        "" | "exception" => "@",
        "ignore" => "@?",
        // YAML only knows "@?" (ignore). For a constructor argument Symfony
        // resolves a missing ignored service to null, exactly like the null
        // strategy (`ResolveInvalidReferencesPass`). Elsewhere ignore drops
        // the method call, the property or the collection entry instead of
        // passing null, so null cannot be expressed there.
        "null" if position == Position::Constructor => "@?",
        "null" => {
            return err(
                "on-invalid=\"null\" in a method call, a property or a collection cannot be expressed in YAML: \"@?\" would drop the call, property or entry instead of passing null",
            );
        }
        "ignore_uninitialized" => "@!",
        other => return err(format!("unsupported on-invalid value {other:?}")),
    };
    Ok(Node::string(&format!("{prefix}{id}")))
}

fn properties_to_node(properties: &[XmlProperty]) -> Result<Node> {
    let mut mapping = Node::mapping();
    for property in properties {
        if property.name.is_empty() {
            return err("<property> requires a name attribute");
        }
        let node = context(
            argument_value_to_node(&property.argument, Position::Nested),
            || format!("property {:?}", property.name),
        )?;
        map_put(&mut mapping, &property.name, node);
    }
    Ok(mapping)
}

fn binds_to_node(binds: &[XmlArgument]) -> Result<Node> {
    let mut mapping = Node::mapping();
    for bind in binds {
        if bind.key.is_empty() {
            return err("<bind> requires a key attribute");
        }
        let unkeyed = XmlArgument {
            key: String::new(),
            ..bind.clone()
        };
        let node = context(argument_value_to_node(&unkeyed, Position::Nested), || {
            format!("bind {:?}", bind.key)
        })?;
        map_put(&mut mapping, &bind.key, node);
    }
    Ok(mapping)
}

fn calls_to_node(calls: &[XmlCall]) -> Result<Node> {
    let mut sequence = Node::sequence();
    for call in calls {
        unknown_element_error("call", &call.unknown)?;
        unknown_attribute_error("call", &call.unknown_attrs)?;
        if call.method.is_empty() {
            return err("<call> requires a method attribute");
        }
        let mut entry = Node::sequence();
        entry.flow = true;
        entry.content.push(Node::string(&call.method));
        let returns_clone = phpize(&call.returns_clone) == PhpValue::Bool(true);
        if !call.arguments.is_empty() || returns_clone {
            let arguments = context(
                arguments_to_node(&call.arguments, false, Position::Nested),
                || format!("call {:?}", call.method),
            )?;
            entry.content.push(arguments);
        }
        if returns_clone {
            entry.content.push(scalar_value(PhpValue::Bool(true)));
        }
        sequence.content.push(entry);
    }
    Ok(sequence)
}

fn tags_to_node(tags: &[XmlTag]) -> Result<Node> {
    let mut sequence = Node::sequence();
    for tag in tags {
        sequence.content.push(tag_to_node(tag)?);
    }
    Ok(sequence)
}

fn tag_to_node(tag: &XmlTag) -> Result<Node> {
    let name = if tag.name_attr.is_empty() {
        tag.value.trim()
    } else {
        &tag.name_attr
    };
    if name.is_empty() {
        return err("<tag> requires a name");
    }
    if tag.other_attrs.is_empty() && tag.attributes.is_empty() {
        return Ok(Node::string(name));
    }
    let mut mapping = Node::mapping();
    mapping.flow = true;
    map_put(&mut mapping, "name", Node::string(name));
    for XmlAttr { local, value, .. } in &tag.other_attrs {
        put_tag_attribute(&mut mapping, local, scalar_value(phpize(value)));
    }
    for attribute in &tag.attributes {
        let node = tag_attribute_to_node(attribute)?;
        put_tag_attribute(&mut mapping, &attribute.name, node);
    }
    Ok(mapping)
}

fn tag_attribute_to_node(attribute: &XmlTagAttribute) -> Result<Node> {
    if attribute.name.is_empty() {
        return err("tag <attribute> requires a name attribute");
    }
    if attribute.children.is_empty() {
        return Ok(scalar_value(phpize(&attribute.value)));
    }
    let mut mapping = Node::mapping();
    mapping.flow = true;
    for child in &attribute.children {
        let node = tag_attribute_to_node(child)?;
        put_tag_attribute(&mut mapping, &child.name, node);
    }
    Ok(mapping)
}

/// Mirrors Symfony's XmlFileLoader, which stores tag attributes containing
/// dashes additionally under their underscore variant.
fn put_tag_attribute(mapping: &mut Node, name: &str, value: Node) {
    if name.contains('-') && !name.contains('_') {
        let normalized = name.replace('-', "_");
        if !mapping_has_key(mapping, &normalized) {
            map_put(mapping, &normalized, value.clone());
        }
    }
    map_put(mapping, name, value);
}

fn mapping_has_key(mapping: &Node, key: &str) -> bool {
    mapping
        .content
        .iter()
        .step_by(2)
        .any(|node| node.value == key)
}

fn phpized_service_value(value: &str) -> Node {
    match phpize(value) {
        PhpValue::Str(text) => service_string(&text),
        other => scalar_value(other),
    }
}

/// A string inside a service definition. Literal `@` prefixes are escaped as
/// `@@`, since `@` marks service references in YAML (unlike XML text).
fn service_string(value: &str) -> Node {
    if value.starts_with('@') {
        return Node::string(&format!("@{value}"));
    }
    Node::string(value)
}

pub(crate) fn scalar_value(value: PhpValue) -> Node {
    match value {
        PhpValue::Null => Node::null("null"),
        PhpValue::Str(text) => Node::string(&text),
        PhpValue::Bool(value) => Node::implicit(value.to_string()),
        PhpValue::Int(value) => Node::implicit(value.to_string()),
        PhpValue::Float(value) => Node::implicit(emit::format_float(value)),
    }
}

pub(crate) fn map_put(mapping: &mut Node, key: &str, value: Node) {
    mapping.content.push(Node::string(key));
    mapping.content.push(value);
}

/// Adds an empty line between top-level sections and between service
/// definitions to keep the generated YAML readable.
pub(crate) fn insert_blank_lines(content: &str) -> String {
    let mut out: Vec<&str> = Vec::new();
    let mut in_services = false;
    let mut first_service = true;
    for line in content.split('\n') {
        let bytes = line.as_bytes();
        if !bytes.is_empty() && bytes[0] != b' ' && bytes[0] != b'-' {
            if !out.is_empty() {
                out.push("");
            }
            in_services = line == "services:";
            first_service = true;
            out.push(line);
            continue;
        }
        if in_services && is_service_key_line(line) {
            if first_service {
                first_service = false;
            } else {
                out.push("");
            }
        }
        out.push(line);
    }
    out.join("\n")
}

fn is_service_key_line(line: &str) -> bool {
    line.len() > 4 && line.starts_with("    ") && line.as_bytes()[4] != b' '
}

#[cfg(test)]
#[path = "tests_yamlconv/services_yaml_convert.rs"]
mod tests;
