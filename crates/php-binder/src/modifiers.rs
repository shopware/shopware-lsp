//! Modifiers, attributes, and attribute-driven type semantics.

use crate::fast::Fast;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{
    Attribute, AttributeArgument, AttributeArrayItem, AttributeValue, AttributeValueKind, Flags,
    Str, Symbol, SymbolKind, Visibility, attribute_named,
};
use swlsp_php_types::{Kind as TypeKind, ShapeField, Type};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::builder::{constant_literal_type, numeric_literal_is_float};

pub(crate) fn text<'s>(source: &'s str, node: &SyntaxNode) -> &'s str {
    &source[node.text_range()]
}

fn visibility_of(text: &str) -> Option<Visibility> {
    if eq_ignore_case(text, "private") {
        Some(Visibility::Private)
    } else if eq_ignore_case(text, "protected") {
        Some(Visibility::Protected)
    } else if eq_ignore_case(text, "public") {
        Some(Visibility::Public)
    } else {
        None
    }
}

pub(crate) fn declaration_visibility(node: &SyntaxNode) -> Visibility {
    for child in node.green().children() {
        if let NodeOrToken::Token(token) = child
            && !Kind(token.kind().0).is_trivia()
            && let Some(visibility) = visibility_of(token.text())
        {
            return visibility;
        }
    }
    Visibility::Public
}

pub(crate) fn declaration_write_visibility(node: &SyntaxNode) -> (Visibility, bool) {
    let children: Vec<_> = node.children_with_tokens().collect();
    for (index, child) in children.iter().enumerate() {
        let NodeOrToken::Token(token) = child else {
            continue;
        };
        let Some(visibility) = visibility_of(token.text()) else {
            continue;
        };
        if has_set_suffix(&children[index + 1..]) {
            return (visibility, true);
        }
    }
    (Visibility::Public, false)
}

fn has_set_suffix(children: &[swlsp_syntax::SyntaxElement]) -> bool {
    const EXPECTED: [&str; 3] = ["(", "set", ")"];
    let mut matched = 0;
    for child in children {
        let NodeOrToken::Token(token) = child else {
            continue;
        };
        if token.kind().is_trivia() {
            continue;
        }
        if !eq_ignore_case(token.text(), EXPECTED[matched]) {
            return false;
        }
        matched += 1;
        if matched == EXPECTED.len() {
            return true;
        }
    }
    false
}

pub(crate) fn declaration_flags(node: &SyntaxNode) -> Flags {
    let mut flags = Flags::empty();
    let mut promoted = false;
    let parameter = node.kind() == PHP_PARAMETER;
    for child in node.green().children() {
        let NodeOrToken::Token(token) = child else {
            continue;
        };
        let kind = Kind(token.kind().0);
        if kind.is_trivia() {
            continue;
        }
        let text = token.text();
        if eq_ignore_case(text, "static") {
            flags |= Flags::STATIC;
        } else if eq_ignore_case(text, "final") {
            flags |= Flags::FINAL;
        } else if eq_ignore_case(text, "abstract") {
            flags |= Flags::ABSTRACT;
        } else if eq_ignore_case(text, "readonly") {
            flags |= Flags::READONLY;
        } else if kind == TK_AMPERSAND {
            flags |= Flags::BY_REFERENCE;
        }
        if parameter
            && kind == TK_KEYWORD
            && (eq_ignore_case(text, "public")
                || eq_ignore_case(text, "protected")
                || eq_ignore_case(text, "private"))
        {
            promoted = true;
        }
    }
    if promoted {
        flags |= Flags::PROMOTED;
    }
    flags
}

/// Whether a direct child token has `kind` and, when `text` is non-empty,
/// matches it case-insensitively.
pub(crate) fn has_token(node: &SyntaxNode, kind: Kind, text: &str) -> bool {
    node.children_with_tokens().any(|child| match child {
        NodeOrToken::Token(token) => {
            token.kind() == kind && (text.is_empty() || eq_ignore_case(token.text(), text))
        }
        NodeOrToken::Node(_) => false,
    })
}

pub(crate) fn compact_name(source: &str) -> String {
    swlsp_textutil::compact_whitespace(source).into_owned()
}

pub(crate) fn bind_attributes(
    node: &SyntaxNode,
    context: &NameContext,
    source: &str,
) -> Vec<Attribute> {
    let mut result = Vec::new();
    for group in node
        .children()
        .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
    {
        for attribute in group
            .children()
            .filter(|child| child.kind() == PHP_ATTRIBUTE)
        {
            let Some(name_node) = attribute.child_node_of_kind(PHP_NAME) else {
                continue;
            };
            let name = context.resolve_class(&compact_name(&source[name_node.trimmed()]));
            let arguments = if retain_attribute_arguments(&name) {
                bind_attribute_arguments(&attribute, context, source)
            } else {
                Vec::new()
            };
            result.push(Attribute {
                name: Str::from(name),
                arguments,
                range: attribute.trimmed(),
            });
        }
    }
    result
}

/// Shopware announces signature changes through these attributes; the
/// migration inspections read their arguments.
const BC_CHANGE_NAMESPACE: &str = "shopware\\core\\framework\\deprecation\\bcchange\\";

fn retain_attribute_arguments(name: &str) -> bool {
    let name = lowercase(name.trim_matches('\\'));
    if name.starts_with(BC_CHANGE_NAMESPACE) {
        return true;
    }
    [
        "arrayshape",
        "deprecated",
        "expectedvalues",
        "noreturn",
        "objectshape",
        "returntypecontract",
    ]
    .iter()
    .any(|suffix| {
        name == *suffix
            || (name.len() > suffix.len()
                && name.ends_with(suffix)
                && name.as_bytes()[name.len() - suffix.len() - 1] == b'\\')
    })
}

fn bind_attribute_arguments(
    attribute: &SyntaxNode,
    context: &NameContext,
    source: &str,
) -> Vec<AttributeArgument> {
    let arguments: Vec<SyntaxNode> = query::arguments(attribute).collect();
    let mut result = Vec::with_capacity(arguments.len());
    for argument in &arguments {
        // Index by the number of bound arguments, not the iteration index.
        let Some(expression) = query::argument_expression(attribute, result.len()) else {
            continue;
        };
        result.push(AttributeArgument {
            name: Str::from(query::argument_name(argument)),
            value: bind_attribute_value(&expression, context, 0, source),
            range: argument.trimmed(),
        });
    }
    result
}

const MAX_ATTRIBUTE_VALUE_DEPTH: usize = 16;

pub(crate) fn bind_attribute_value(
    node: &SyntaxNode,
    context: &NameContext,
    depth: usize,
    source: &str,
) -> AttributeValue {
    let expression = text(source, node).trim();
    let mut result = AttributeValue {
        kind: AttributeValueKind::Expression,
        value: Str::from(expression),
        expression: Str::from(expression),
        items: Vec::new(),
    };
    if depth >= MAX_ATTRIBUTE_VALUE_DEPTH {
        return result;
    }
    match node.kind() {
        PHP_STRING => {
            result.kind = AttributeValueKind::String;
            result.value = Str::from(query::string_value(node));
        }
        PHP_NUMBER => {
            result.kind = if numeric_literal_is_float(expression) {
                AttributeValueKind::Float
            } else {
                AttributeValueKind::Integer
            };
        }
        PHP_UNARY_EXPRESSION => match constant_literal_type(Some(node), source).kind() {
            TypeKind::LiteralInt => result.kind = AttributeValueKind::Integer,
            TypeKind::LiteralFloat => result.kind = AttributeValueKind::Float,
            _ => {}
        },
        PHP_BOOLEAN => {
            result.kind = AttributeValueKind::Bool;
            result.value = Str::from(lowercase(expression));
        }
        PHP_NULL => {
            result.kind = AttributeValueKind::Null;
            result.value = Str::from("null");
        }
        PHP_NAME => {
            result.kind = AttributeValueKind::Constant;
            let name = compact_name(text(source, node));
            let resolved = context.resolve_constant(&name);
            // Unqualified constants in a namespace have a global fallback.
            // The last candidate is therefore the stable identity used by
            // stubs.
            result.value = match resolved.last() {
                Some(last) => Str::from(last.as_str()),
                None => Str::from(name.strip_prefix('\\').unwrap_or(&name)),
            };
        }
        PHP_SCOPED_ACCESS => {
            result.kind = AttributeValueKind::ClassConstant;
            result.value = Str::from(resolve_attribute_class_constant(expression, context));
        }
        PHP_ARRAY => {
            result.kind = AttributeValueKind::Array;
            result.value = Str::EMPTY;
            let items = query::array_items(node);
            result.items.reserve_exact(items.len());
            for item in &items {
                let Some(value_node) = query::array_item_value(item) else {
                    continue;
                };
                let mut bound = AttributeArrayItem {
                    value: bind_attribute_value(&value_node, context, depth + 1, source),
                    ..AttributeArrayItem::default()
                };
                if let Some(key_node) = query::array_item_key(item) {
                    bound.has_key = true;
                    bound.key = bind_attribute_value(&key_node, context, depth + 1, source);
                }
                result.items.push(bound);
            }
        }
        PHP_BINARY_EXPRESSION => {
            if let Some(value) = concatenated_string(node, context, depth, source) {
                result.kind = AttributeValueKind::String;
                result.value = Str::from(value);
            }
        }
        _ => {
            if expression.contains("::")
                && !expression.contains(|c: char| "|&.+-*/%?()[]{}'\"".contains(c))
            {
                result.kind = AttributeValueKind::ClassConstant;
                result.value = Str::from(resolve_attribute_class_constant(expression, context));
            }
        }
    }
    result
}

/// The value of `'a' . Foo::class . 'b'`: strings and resolved class names
/// joined by the concatenation operator.
fn concatenated_string(
    node: &SyntaxNode,
    context: &NameContext,
    depth: usize,
    source: &str,
) -> Option<String> {
    let mut result = String::new();
    let mut operands = 0;
    for child in node.children_with_tokens() {
        match child {
            NodeOrToken::Node(child) => {
                let value = bind_attribute_value(&child, context, depth + 1, source);
                match value.kind {
                    AttributeValueKind::String => result.push_str(&value.value),
                    AttributeValueKind::ClassConstant => {
                        let (class, member) = value.value.rsplit_once("::")?;
                        if !eq_ignore_case(member, "class") {
                            return None;
                        }
                        result.push_str(class);
                    }
                    _ => return None,
                }
                operands += 1;
            }
            NodeOrToken::Token(token) => {
                if !token.kind().is_trivia() && token.text().trim() != "." {
                    return None;
                }
            }
        }
    }
    (operands >= 2).then_some(result)
}

fn resolve_attribute_class_constant(expression: &str, context: &NameContext) -> String {
    let Some(separator) = expression.rfind("::") else {
        return expression
            .strip_prefix('\\')
            .unwrap_or(expression)
            .to_owned();
    };
    let receiver = expression[..separator].trim();
    let member = expression[separator + 2..].trim();
    if receiver.is_empty() || member.is_empty() {
        return expression
            .strip_prefix('\\')
            .unwrap_or(expression)
            .to_owned();
    }
    match lowercase(receiver).as_str() {
        "self" | "static" | "parent" => format!("{receiver}::{member}"),
        _ => format!("{}::{member}", context.resolve_class(receiver)),
    }
}

pub(crate) fn apply_attribute_type_semantics(symbol: &mut Symbol, context: &NameContext) {
    if let Some(shape) = attribute_shape_type(symbol.attributes(), context) {
        match symbol.kind {
            SymbolKind::Function | SymbolKind::Method => {
                symbol.return_type = refine_type_with_shape(&symbol.return_type, shape);
            }
            _ => symbol.ty = refine_type_with_shape(&symbol.ty, shape),
        }
    }
    if let Some(no_return) = attribute_named(symbol.attributes(), "NoReturn")
        && no_return.arguments.is_empty()
        && matches!(symbol.kind, SymbolKind::Function | SymbolKind::Method)
    {
        symbol.return_type = Type::never();
    }
}

pub(crate) fn attribute_refined_type(
    fallback: Type,
    attributes: &[Attribute],
    context: &NameContext,
) -> Type {
    match attribute_shape_type(attributes, context) {
        Some(shape) => refine_type_with_shape(&fallback, shape),
        None => fallback,
    }
}

fn refine_type_with_shape(fallback: &Type, shape: Type) -> Type {
    if fallback.is_unknown() || fallback.kind() == TypeKind::Mixed {
        return shape;
    }
    if shape_replaces_type(fallback, shape.kind()) {
        return shape;
    }
    if fallback.kind() != TypeKind::Union {
        return shape;
    }
    let mut members = fallback.arguments().to_vec();
    let mut replaced = false;
    for member in &mut members {
        if shape_replaces_type(member, shape.kind()) {
            *member = shape.clone();
            replaced = true;
        }
    }
    if !replaced {
        return shape;
    }
    Type::union(members)
}

fn shape_replaces_type(value: &Type, shape_kind: TypeKind) -> bool {
    match shape_kind {
        TypeKind::ArrayShape => matches!(
            value.kind(),
            TypeKind::Array
                | TypeKind::NonEmptyArray
                | TypeKind::List
                | TypeKind::NonEmptyList
                | TypeKind::ArrayShape
        ),
        TypeKind::ObjectShape => {
            value.kind() == TypeKind::ObjectShape
                || (value.kind() == TypeKind::Object && value.name().is_empty())
        }
        _ => false,
    }
}

fn attribute_shape_type(attributes: &[Attribute], context: &NameContext) -> Option<Type> {
    let (attribute, object) = match attribute_named(attributes, "ArrayShape") {
        Some(attribute) => (attribute, false),
        None => (attribute_named(attributes, "ObjectShape")?, true),
    };
    let shape = attribute.argument("shape", 0)?;
    if shape.kind != AttributeValueKind::Array {
        return None;
    }
    let mut fields = Vec::with_capacity(shape.items.len());
    let mut next_numeric_key: i64 = 0;
    for item in &shape.items {
        let mut name = String::new();
        let mut optional = false;
        if item.has_key {
            match item.key.kind {
                AttributeValueKind::String => {
                    name = item.key.value.to_string();
                    if let Some(stripped) = name.strip_suffix('?') {
                        name = stripped.to_owned();
                        optional = true;
                    }
                }
                AttributeValueKind::Integer => {
                    name = item.key.expression.replace('_', "");
                    if let Ok(parsed) = name.parse::<i64>()
                        && parsed >= next_numeric_key
                    {
                        next_numeric_key = parsed + 1;
                    }
                }
                _ => {}
            }
        } else if !object {
            name = next_numeric_key.to_string();
            next_numeric_key += 1;
        }
        if name.is_empty() || item.value.kind != AttributeValueKind::String {
            continue;
        }
        let field_type = match swlsp_php_types::parse(&item.value.value) {
            Ok(value) if !value.is_unknown() && value.kind() != TypeKind::Error => {
                context.resolve_phpdoc_type(&value, &[] as &[&str])
            }
            _ => Type::mixed(),
        };
        fields.push(ShapeField {
            name: name.as_str().into(),
            ty: field_type,
            optional,
        });
    }
    if fields.is_empty() && !shape.items.is_empty() {
        return None;
    }
    Some(if object {
        Type::object_shape(fields, false)
    } else {
        Type::array_shape(fields, false)
    })
}
