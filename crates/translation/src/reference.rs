//! Translation references in PHP and Twig sources.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_FUNCTION_CALL, PHP_OBJECT_CREATION, PHP_PROPERTY_DECLARATION, PHP_STRING,
};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{
    HTML_TEXT, TWIG_ARGUMENTS, TWIG_EXPRESSION, TWIG_FILTER, TWIG_LITERAL_NAME, TWIG_LITERAL_STRING,
};
use swlsp_php::{NameResolver, PhpContext, PhpIndex};
use swlsp_php_types::Type;
use swlsp_syntax::{Kind, SyntaxNode};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::parser::last_name_part;
use crate::{lower_extension, normalize_domain};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum ReferenceRole {
    #[default]
    None,
    Key,
    Domain,
    Placeholder,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum PhpReferenceKind {
    #[default]
    None,
    Method,
    Function,
    Message,
    ValidatorMessage,
    ValidatorDomain,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Reference {
    pub role: ReferenceRole,
    pub key: String,
    pub domain: String,
    pub node: Option<SyntaxNode>,
    pub container: Option<SyntaxNode>,
    pub php_kind: PhpReferenceKind,
    pub class: String,
}

/// The translation reference at `node` of a PHP or Twig document.
pub fn reference_at(path: &str, node: &SyntaxNode, source: &str) -> Option<Reference> {
    match lower_extension(path).as_str() {
        ".php" => php_reference_at(node).or_else(|| php_placeholder_reference_at(node)),
        ".twig" => twig_reference_at(node, source),
        _ => None,
    }
}

/// Every translation reference of a PHP or Twig document.
pub fn references(path: &str, root: &SyntaxNode, source: &str) -> Vec<Reference> {
    match lower_extension(path).as_str() {
        ".php" => php_references(root),
        ".twig" => twig_references(root, source),
        _ => Vec::new(),
    }
}

pub fn php_reference_at(node: &SyntaxNode) -> Option<Reference> {
    php_reference_at_with(node, None)
}

fn php_reference_at_with(
    node: &SyntaxNode,
    name_resolver: Option<&NameResolver>,
) -> Option<Reference> {
    let literal = phpquery::string_at(node)?;
    let Some((container, kind, key_index, domain_index)) = php_translation_container(&literal)
    else {
        return php_validator_reference_at(&literal, name_resolver);
    };
    let argument = php_argument_containing(&container, &literal)?;
    let index = phpquery::argument_index(&container, &literal);
    if index.and_then(|index| phpquery::argument_expression(&container, index))
        != Some(literal.clone())
    {
        return None;
    }
    let argument_name = phpquery::argument_name(&argument);

    let role = match argument_name.as_str() {
        "id" => ReferenceRole::Key,
        "domain" => ReferenceRole::Domain,
        "" if index == Some(key_index) => ReferenceRole::Key,
        "" if index == Some(domain_index) => ReferenceRole::Domain,
        _ => ReferenceRole::None,
    };
    if role == ReferenceRole::None {
        return None;
    }

    let key = php_string_argument(&container, "id", Some(key_index));
    let mut domain = php_string_argument(&container, "domain", Some(domain_index));
    if domain.is_empty() && role == ReferenceRole::Key {
        domain = "messages".to_owned();
    }
    Some(Reference {
        role,
        key,
        domain: normalize_domain(&domain).to_owned(),
        node: Some(literal),
        container: Some(container),
        php_kind: kind,
        class: String::new(),
    })
}

pub fn php_placeholder_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let literal = phpquery::string_at(node)?;
    let array = phpquery::array_at(&literal)?;
    let (container, kind, key_index, domain_index) = php_translation_container(&array)?;
    let parameter_index = if kind == PhpReferenceKind::Method
        && eq_ignore_case(&phpquery::call_method_name(&container), "transChoice")
    {
        2
    } else {
        1
    };
    if phpquery::argument_expression(&container, parameter_index) != Some(array) {
        return None;
    }
    let key = php_string_argument(&container, "id", Some(key_index));
    if key.is_empty() {
        return None;
    }
    let mut domain = php_string_argument(&container, "domain", Some(domain_index));
    if domain.is_empty() {
        domain = "messages".to_owned();
    }
    Some(Reference {
        role: ReferenceRole::Placeholder,
        key,
        domain: normalize_domain(&domain).to_owned(),
        node: Some(literal),
        container: Some(container),
        php_kind: kind,
        class: String::new(),
    })
}

/// Every translation key and domain reference of a PHP tree.
pub fn php_references(root: &SyntaxNode) -> Vec<Reference> {
    // One resolver serves every literal of the tree; building it per literal
    // re-walks the whole tree for each inspected string.
    let name_resolver = NameResolver::new(root);
    phpquery::nodes(root, &[PHP_STRING])
        .iter()
        .filter_map(|literal| php_reference_at_with(literal, Some(&name_resolver)))
        .collect()
}

/// Whether the receiver or callee of a PHP reference is really a translator,
/// validator or translatable message. References without a PHP kind, and
/// requests without a PHP index, are accepted.
pub fn validate_php_reference(
    context: Option<&PhpContext>,
    reference: &Reference,
    php_index: Option<&PhpIndex>,
) -> bool {
    if reference.php_kind == PhpReferenceKind::None {
        return true;
    }
    let Some(php_index) = php_index else {
        return true;
    };
    let called_on_any = |classes: &[&str]| {
        reference.container.as_ref().is_some_and(|container| {
            classes
                .iter()
                .any(|class| php_index.is_method_called_on_class(context, container, class))
        })
    };
    match reference.php_kind {
        PhpReferenceKind::Method => called_on_any(&[
            "Symfony\\Component\\Translation\\TranslatorInterface",
            "Symfony\\Contracts\\Translation\\TranslatorInterface",
            "Symfony\\Bundle\\FrameworkBundle\\Templating\\Helper\\TranslatorHelper",
        ]),
        PhpReferenceKind::Function => {
            let name = reference
                .container
                .as_ref()
                .map(phpquery::call_name)
                .unwrap_or_default();
            let name = name.strip_prefix('\\').unwrap_or(&name);
            name == "Symfony\\Component\\Translation\\t" || name == "t"
        }
        PhpReferenceKind::Message => reference.container.as_ref().is_some_and(|container| {
            eq_ignore_case(
                last_name_part(&phpquery::object_class_name(container)),
                "TranslatableMessage",
            )
        }),
        PhpReferenceKind::ValidatorMessage => {
            if !reference.class.is_empty() {
                return context.is_some_and(|context| {
                    context.snapshot.relations().is_subtype(
                        &Type::named(&reference.class, Vec::new()),
                        &Type::named("Symfony\\Component\\Validator\\Constraint", Vec::new()),
                    )
                });
            }
            called_on_any(&[
                "Symfony\\Component\\Validator\\Context\\ExecutionContextInterface",
                "Symfony\\Component\\Validator\\Context\\ExecutionContext",
            ])
        }
        PhpReferenceKind::ValidatorDomain => called_on_any(&[
            "Symfony\\Component\\Validator\\Violation\\ConstraintViolationBuilderInterface",
            "Symfony\\Component\\Validator\\Violation\\ConstraintViolationBuilder",
        ]),
        PhpReferenceKind::None => false,
    }
}

fn php_validator_reference_at(
    literal: &SyntaxNode,
    name_resolver: Option<&NameResolver>,
) -> Option<Reference> {
    if let Some(call) = phpquery::call_at(literal)
        && phpquery::argument_index(&call, literal) == Some(0)
        && phpquery::argument_expression(&call, 0).as_ref() == Some(literal)
    {
        match lowercase(&phpquery::call_method_name(&call)).as_str() {
            "addviolation" | "buildviolation" => {
                return Some(Reference {
                    role: ReferenceRole::Key,
                    key: phpquery::string_value(literal),
                    domain: "validators".to_owned(),
                    node: Some(literal.clone()),
                    container: Some(call),
                    php_kind: PhpReferenceKind::ValidatorMessage,
                    class: String::new(),
                });
            }
            "settranslationdomain" => {
                return Some(Reference {
                    role: ReferenceRole::Domain,
                    key: String::new(),
                    domain: normalize_domain(&phpquery::string_value(literal)).to_owned(),
                    node: Some(literal.clone()),
                    container: Some(call),
                    php_kind: PhpReferenceKind::ValidatorDomain,
                    class: String::new(),
                });
            }
            _ => {}
        }
    }

    let owned_resolver;
    let name_resolver = match name_resolver {
        Some(resolver) => resolver,
        None => {
            let root = literal
                .ancestors()
                .last()
                .unwrap_or_else(|| literal.clone());
            owned_resolver = NameResolver::new(&root);
            &owned_resolver
        }
    };
    let validator_message = |container: SyntaxNode, class_name: String| Reference {
        role: ReferenceRole::Key,
        key: phpquery::string_value(literal),
        domain: "validators".to_owned(),
        node: Some(literal.clone()),
        container: Some(container),
        php_kind: PhpReferenceKind::ValidatorMessage,
        class: class_name,
    };
    if let Some(object) = php_ancestor(literal, PHP_OBJECT_CREATION)
        && let Some(property) = constraint_message_property(&object, literal)
    {
        let resolved = name_resolver.resolve(&phpquery::object_class_name(&object));
        let class_name = resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned();
        return is_constraint_message_property(&property)
            .then(|| validator_message(object, class_name));
    }
    if let Some(attribute) = phpquery::attribute_at(literal)
        && let Some(property) = constraint_message_property(&attribute, literal)
    {
        let resolved = name_resolver.resolve(&phpquery::attribute_name(&attribute));
        let class_name = resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned();
        return is_constraint_message_property(&property)
            .then(|| validator_message(attribute, class_name));
    }
    if let Some(property) = php_ancestor(literal, PHP_PROPERTY_DECLARATION) {
        for variable in phpquery::property_variables(&property) {
            if !is_constraint_message_property(&phpquery::variable_name(&variable)) {
                continue;
            }
            let class_name = phpquery::class_at(&property)
                .map(|class| phpquery::class_name(&class))
                .unwrap_or_default();
            let resolved = name_resolver.resolve(&class_name);
            let class_name = resolved.strip_prefix('\\').unwrap_or(&resolved).to_owned();
            return Some(validator_message(property, class_name));
        }
    }
    None
}

fn constraint_message_property(container: &SyntaxNode, literal: &SyntaxNode) -> Option<String> {
    let literal_range = literal.text_range();
    for (index, argument) in phpquery::arguments(container).enumerate() {
        let range = argument.text_range();
        if range.start() > literal_range.start() || literal_range.end() > range.end() {
            continue;
        }
        let name = phpquery::argument_name(&argument);
        let expression = phpquery::argument_expression(container, index);
        if !name.is_empty() && expression.as_ref() == Some(literal) {
            return Some(name);
        }
        let array = expression.as_ref().and_then(phpquery::array_at);
        let item = phpquery::array_item_at(literal);
        let (Some(array), Some(item)) = (array, item) else {
            continue;
        };
        if index != 0
            || item.parent().as_ref() != Some(&array)
            || phpquery::array_item_value(&item).as_ref() != Some(literal)
        {
            continue;
        }
        let Some(key) = phpquery::array_item_key(&item) else {
            continue;
        };
        if key.kind() != PHP_STRING {
            continue;
        }
        return Some(phpquery::string_value(&key));
    }
    None
}

fn is_constraint_message_property(name: &str) -> bool {
    let lower = lowercase(name);
    lower.starts_with("message") || lower.ends_with("message")
}

fn php_ancestor(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn php_translation_container(
    node: &SyntaxNode,
) -> Option<(SyntaxNode, PhpReferenceKind, usize, usize)> {
    if let Some(call) = phpquery::call_at(node) {
        let is_function = call.kind() == PHP_FUNCTION_CALL;
        match lowercase(&phpquery::call_method_name(&call)).as_str() {
            "trans" => {
                let kind = if is_function && last_name_part(&phpquery::call_name(&call)) == "t" {
                    PhpReferenceKind::Function
                } else {
                    PhpReferenceKind::Method
                };
                return Some((call, kind, 0, 2));
            }
            "transchoice" => return Some((call, PhpReferenceKind::Method, 0, 3)),
            "t" if is_function => return Some((call, PhpReferenceKind::Function, 0, 2)),
            _ => {}
        }
    }
    let object = php_ancestor(node, PHP_OBJECT_CREATION)?;
    eq_ignore_case(
        last_name_part(&phpquery::object_class_name(&object)),
        "TranslatableMessage",
    )
    .then_some((object, PhpReferenceKind::Message, 0, 2))
}

fn php_argument_containing(container: &SyntaxNode, node: &SyntaxNode) -> Option<SyntaxNode> {
    let range = node.text_range();
    phpquery::arguments(container).find(|argument| {
        let argument_range = argument.text_range();
        argument_range.start() <= range.start() && range.end() <= argument_range.end()
    })
}

fn php_string_argument(container: &SyntaxNode, name: &str, fallback: Option<usize>) -> String {
    for (index, argument) in phpquery::arguments(container).enumerate() {
        if phpquery::argument_name(&argument) != name {
            continue;
        }
        return match phpquery::argument_expression(container, index) {
            Some(expression) if expression.kind() == PHP_STRING => {
                phpquery::string_value(&expression)
            }
            _ => String::new(),
        };
    }
    match fallback.and_then(|index| phpquery::argument_expression(container, index)) {
        Some(expression) if expression.kind() == PHP_STRING => phpquery::string_value(&expression),
        _ => String::new(),
    }
}

fn twig_string_value(node: &SyntaxNode) -> String {
    twigquery::string_value(node).unwrap_or_default()
}

fn twig_filter_name(node: &SyntaxNode) -> String {
    twigquery::filter_name(node).unwrap_or_default()
}

pub fn twig_reference_at(node: &SyntaxNode, source: &str) -> Option<Reference> {
    if let Some(literal) = twigquery::literal_string_at(node) {
        if twigquery::string_in_filter(&literal, &["trans", "transchoice"]) {
            let mut domain = twig_filter_domain(Some(&literal));
            if domain.is_empty() {
                domain = twig_default_domain_before(source, literal.text_range().start().into());
            }
            if domain.is_empty() {
                domain = "messages".to_owned();
            }
            return Some(Reference {
                role: ReferenceRole::Key,
                key: twig_string_value(&literal),
                domain: normalize_domain(&domain).to_owned(),
                node: Some(literal),
                ..Reference::default()
            });
        }

        if let Some(filter) = twigquery::closest_node_of_kind(&literal, &[TWIG_FILTER]) {
            let (parameter_index, expected_index) =
                match lowercase(&twig_filter_name(&filter)).as_str() {
                    "trans" => (Some(0), Some(1)),
                    "transchoice" => (Some(1), Some(2)),
                    _ => (None, None),
                };
            let argument_index = twig_filter_argument_index(&filter, &literal);
            if parameter_index.is_some()
                && argument_index == parameter_index
                && twigquery::hash_at(&literal).is_some()
            {
                let key = twig_filter_key(&filter);
                let mut domain = twig_filter_domain(key_node_in_filter(&filter).as_ref());
                if domain.is_empty() {
                    domain =
                        twig_default_domain_before(source, literal.text_range().start().into());
                }
                if domain.is_empty() {
                    domain = "messages".to_owned();
                }
                if key.is_empty() {
                    return None;
                }
                return Some(Reference {
                    role: ReferenceRole::Placeholder,
                    key,
                    domain: normalize_domain(&domain).to_owned(),
                    node: Some(literal),
                    ..Reference::default()
                });
            }
            if expected_index.is_some() && argument_index == expected_index {
                let key = twig_filter_key(&filter);
                return Some(Reference {
                    role: ReferenceRole::Domain,
                    key,
                    domain: normalize_domain(&twig_string_value(&literal)).to_owned(),
                    node: Some(literal),
                    ..Reference::default()
                });
            }
        }
    }

    let (domain, domain_node) = twig_default_domain_at(node, source)?;
    Some(Reference {
        role: ReferenceRole::Domain,
        domain: normalize_domain(&domain).to_owned(),
        node: Some(domain_node),
        ..Reference::default()
    })
}

/// Every translation reference of a Twig tree, without duplicates.
pub fn twig_references(root: &SyntaxNode, source: &str) -> Vec<Reference> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for node in twigquery::iterate_nodes(root, &[TWIG_LITERAL_STRING, TWIG_LITERAL_NAME, HTML_TEXT])
    {
        let Some(reference) = twig_reference_at(&node, source) else {
            continue;
        };
        if seen.insert(reference_identity(&reference)) {
            result.push(reference);
        }
    }
    result
}

fn twig_filter_domain(key: Option<&SyntaxNode>) -> String {
    let Some(filter) = key.and_then(|key| twigquery::closest_node_of_kind(key, &[TWIG_FILTER]))
    else {
        return String::new();
    };
    let index = match lowercase(&twig_filter_name(&filter)).as_str() {
        "trans" => 1,
        "transchoice" => 2,
        _ => return String::new(),
    };
    twig_filter_string_argument(&filter, index)
        .map(|literal| twig_string_value(&literal))
        .unwrap_or_default()
}

fn key_node_in_filter(filter: &SyntaxNode) -> Option<SyntaxNode> {
    twigquery::iterate_nodes(filter, &[TWIG_LITERAL_STRING])
        .find(|literal| twigquery::string_in_filter(literal, &["trans", "transchoice"]))
}

fn twig_filter_key(filter: &SyntaxNode) -> String {
    key_node_in_filter(filter)
        .map(|literal| twig_string_value(&literal))
        .unwrap_or_default()
}

fn twig_filter_string_argument(filter: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    let arguments = twigquery::iterate_nodes(filter, &[TWIG_ARGUMENTS]).next()?;
    let expression = arguments
        .children()
        .filter(|child| child.kind() == TWIG_EXPRESSION)
        .nth(index)?;
    twigquery::iterate_nodes(&expression, &[TWIG_LITERAL_STRING]).next()
}

fn twig_filter_argument_index(filter: &SyntaxNode, node: &SyntaxNode) -> Option<usize> {
    let arguments = twigquery::iterate_nodes(filter, &[TWIG_ARGUMENTS]).next()?;
    let range = node.text_range();
    arguments
        .children()
        .filter(|child| child.kind() == TWIG_EXPRESSION)
        .position(|child| {
            let child_range = child.text_range();
            child_range.start() <= range.start() && range.end() <= child_range.end()
        })
}

fn rfind_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .rposition(|window| window == needle)
}

fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// The active `trans_default_domain` declaration before `offset`. Callers
/// should use the conventional `messages` domain when this returns an empty
/// string.
pub fn twig_default_domain_before(source: &str, offset: u32) -> String {
    let source = source.as_bytes();
    let offset = (offset as usize).min(source.len());
    let mut prefix = &source[..offset];
    loop {
        let Some(index) = rfind_bytes(prefix, b"trans_default_domain") else {
            return String::new();
        };
        let open = rfind_bytes(&prefix[..index], b"{%");
        let close = find_bytes(&prefix[index..], b"%}");
        if let (Some(open), Some(close)) = (open, close) {
            let value = parse_default_domain_tag(&String::from_utf8_lossy(
                &prefix[open..index + close + 2],
            ));
            if !value.is_empty() {
                return value;
            }
        }
        prefix = &prefix[..index];
    }
}

fn twig_default_domain_at(node: &SyntaxNode, source: &str) -> Option<(String, SyntaxNode)> {
    let bytes = source.as_bytes();
    if bytes.is_empty() {
        return None;
    }
    let offset = (u32::from(node.text_range().start()) as usize).min(bytes.len());
    let open = rfind_bytes(&bytes[..offset], b"{%")
        .or_else(|| rfind_bytes(&bytes[..(offset + 1).min(bytes.len())], b"{%"))?;
    let close = offset + find_bytes(&bytes[offset..], b"%}")? + 2;
    if close > bytes.len() {
        return None;
    }
    let value = default_domain_tag_value(&String::from_utf8_lossy(&bytes[open..close]))?;
    let domain_node = twigquery::literal_string_at(node).unwrap_or_else(|| node.clone());
    Some((value, domain_node))
}

fn parse_default_domain_tag(tag: &str) -> String {
    default_domain_tag_value(tag).unwrap_or_default()
}

fn default_domain_tag_value(tag: &str) -> Option<String> {
    let tag = tag.trim();
    let tag = tag.strip_prefix("{%").unwrap_or(tag);
    let tag = tag.strip_suffix("%}").unwrap_or(tag);
    let tag = tag.trim();
    const NAME: &str = "trans_default_domain";
    let value = tag.strip_prefix(NAME)?.trim();
    if value.is_empty() {
        return Some(String::new());
    }
    let value = match value.find([' ', '\t', '\r', '\n']) {
        Some(index) => &value[..index],
        None => value,
    };
    Some(value.trim().trim_matches(['"', '\'']).to_owned())
}

fn reference_identity(reference: &Reference) -> String {
    let Some(node) = &reference.node else {
        return String::new();
    };
    let range = node.text_range();
    format!(
        "{}:{}:{}",
        reference.role as u8,
        u32::from(range.start()),
        u32::from(range.end())
    )
}
