//! Shopware's staged signature changes (`#[NewOptionalParameter]`,
//! `#[NewRequiredParameter]`).

use crate::fast::Fast;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Flags, Parameter, Str, Symbol, SymbolKind, new_symbol_id};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_textutil::eq_ignore_case;

use crate::builder::Builder;

const NEW_OPTIONAL_PARAMETER_ATTRIBUTE: &str =
    "Shopware\\Core\\Framework\\Deprecation\\BCChange\\NewOptionalParameter";
const NEW_REQUIRED_PARAMETER_ATTRIBUTE: &str =
    "Shopware\\Core\\Framework\\Deprecation\\BCChange\\NewRequiredParameter";

const PARAMETER_DEFAULT_VALUE_CHANGE_ATTRIBUTE: &str =
    "Shopware\\Core\\Framework\\Deprecation\\BCChange\\ParameterDefaultValueChange";

/// The parameters of `function` whose default value is announced to change
/// (`#[ParameterDefaultValueChange]`), without the leading `$`.
pub(crate) fn changing_defaults(function: &Symbol) -> Vec<Str> {
    function
        .attributes()
        .iter()
        .filter(|attribute| {
            eq_ignore_case(
                attribute.name.trim_matches('\\'),
                PARAMETER_DEFAULT_VALUE_CHANGE_ATTRIBUTE,
            )
        })
        .filter_map(|attribute| attribute.argument("parameterName", 1))
        .map(|name| Str::from(name.value.trim_start_matches('$')))
        .collect()
}

impl Builder<'_> {
    /// Exposes parameters that live in repeatable attributes during a
    /// compatibility window (read through `func_get_args()`), so PHP's
    /// native parameter list alone is intentionally incomplete.
    pub(crate) fn bind_planned_parameters(
        &self,
        node: &SyntaxNode,
        context: &NameContext,
    ) -> Vec<Parameter> {
        let mut result = Vec::new();
        for attribute in query::attributes(node) {
            let resolved = context.resolve_class(&query::attribute_name(&attribute));
            let optional = if eq_ignore_case(&resolved, NEW_OPTIONAL_PARAMETER_ATTRIBUTE) {
                true
            } else if eq_ignore_case(&resolved, NEW_REQUIRED_PARAMETER_ATTRIBUTE) {
                false
            } else {
                continue;
            };
            let name_node = named_attribute_argument(&attribute, "parameterName");
            let type_node = named_attribute_argument(&attribute, "parameterType");
            let name = name_node
                .as_ref()
                .map(query::string_value)
                .unwrap_or_default();
            let type_source = planned_parameter_type_source(type_node.as_ref());
            let (Some(name_node), Some(type_source)) =
                (name_node.filter(|_| !name.is_empty()), type_source)
            else {
                continue;
            };
            let mut parameter_type = self.bind_native_type(&type_source, context);
            if parameter_type.is_unknown() || parameter_type.kind() == TypeKind::Error {
                parameter_type = Type::mixed();
            }
            let name = format!("${}", name.strip_prefix('$').unwrap_or(&name));
            let attribute_range = attribute.trimmed();
            result.push(Parameter {
                id: new_symbol_id(
                    SymbolKind::Parameter,
                    &format!("planned:{name}"),
                    &self.path,
                    attribute_range.start().into(),
                ),
                name: Str::from(name),
                ty: parameter_type.clone(),
                native_type: parameter_type,
                range: attribute_range,
                selection_range: name_node.trimmed(),
                flags: Flags::SYNTHETIC,
                optional,
                ..Parameter::default()
            });
        }
        result
    }
}

fn named_attribute_argument(attribute: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    let index = query::arguments(attribute)
        .position(|argument| eq_ignore_case(&query::argument_name(&argument), name))?;
    query::argument_expression(attribute, index)
}

fn planned_parameter_type_source(node: Option<&SyntaxNode>) -> Option<String> {
    let node = node?;
    match node.kind() {
        PHP_STRING => Some(query::string_value(node)),
        PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS => {
            let name = query::class_constant_name(node);
            (!name.is_empty()).then_some(name)
        }
        PHP_PARENTHESIZED => planned_parameter_type_source(node.children().next().as_ref()),
        PHP_BINARY_EXPRESSION => {
            let mut builder = String::new();
            let mut operands = 0;
            for child in node.children_with_tokens() {
                match child {
                    NodeOrToken::Node(child) => {
                        builder.push_str(&planned_parameter_type_source(Some(&child))?);
                        operands += 1;
                    }
                    NodeOrToken::Token(token) => {
                        if token.kind().is_trivia() {
                            continue;
                        }
                        if token.kind() != TK_OPERATOR || token.text().trim() != "." {
                            return None;
                        }
                    }
                }
            }
            (operands >= 2).then_some(builder)
        }
        _ => None,
    }
}
