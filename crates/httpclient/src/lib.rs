//! Symfony HttpClient option declarations and references resolved from the
//! shared PHP semantic index.
//!
//! Request-time lookups take the request's PHP analysis as an explicit
//! [`PhpContext`].

use std::collections::BTreeSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, TextRange};

pub const CLIENT_INTERFACE: &str = "Symfony\\Contracts\\HttpClient\\HttpClientInterface";

/// One entry of `HttpClientInterface::OPTIONS_DEFAULTS`.
#[derive(Clone, Debug, PartialEq)]
pub struct HttpOption {
    pub name: String,
    pub ty: Type,
    pub default: String,
    pub file: String,
    pub range: TextRange,
}

/// An option key inside the options array of `request()` or `withOptions()`.
#[derive(Clone, Debug)]
pub struct Reference {
    pub name: String,
    pub range: TextRange,
    pub call: SyntaxNode,
    pub array: SyntaxNode,
}

/// The option key at `node`, accepted only as a top-level key of the
/// options argument (`request` argument 3, `withOptions` argument 1, or the
/// named `options:` argument).
pub fn reference_at(node: &SyntaxNode) -> Option<Reference> {
    let string = query::string_at(node)?;
    let item = query::array_item_at(&string)?;
    if query::array_item_key(&item).as_ref() != Some(&string) {
        return None;
    }
    let array = query::array_at(&item)?;
    let call = query::call_at(&array)?;
    let index = query::argument_index(&call, &array)?;
    if query::argument_expression(&call, index).as_ref() != Some(&array) {
        return None;
    }
    let method = query::call_method_name(&call).to_lowercase();
    let argument_name = query::argument_name(&array).to_lowercase();
    let expected = match method.as_str() {
        "request" => 2,
        "withoptions" => 0,
        _ => return None,
    };
    if argument_name != "options" && (!argument_name.is_empty() || index != expected) {
        return None;
    }
    Some(Reference {
        name: query::string_value(&string),
        range: query::string_content_range(&string),
        call,
        array,
    })
}

/// Whether the reference's call receiver is an `HttpClientInterface`.
pub fn validate(index: &PhpIndex, context: Option<&PhpContext>, reference: &Reference) -> bool {
    index.is_method_called_on_class(context, &reference.call, CLIENT_INTERFACE)
}

/// Every declared option, sorted by name, file and start offset.
pub fn options(index: &PhpIndex) -> Vec<HttpOption> {
    let mut result = Vec::new();
    for constant in index.find_constants(CLIENT_INTERFACE, "OPTIONS_DEFAULTS") {
        for item in constant.constant_array() {
            result.push(HttpOption {
                name: item.key.to_string(),
                ty: item.ty.clone(),
                default: item.value.to_string(),
                file: constant.path.to_string(),
                range: item.key_range,
            });
        }
    }
    result.sort_by(|left, right| {
        left.name
            .cmp(&right.name)
            .then_with(|| left.file.cmp(&right.file))
            .then_with(|| left.range.start().cmp(&right.range.start()))
    });
    result
}

/// Option names already present in the reference's array, excluding the key
/// being edited (a separate duplicate of it still counts).
pub fn used_option_names(reference: &Reference) -> BTreeSet<String> {
    let mut result = BTreeSet::new();
    for item in query::array_items(&reference.array) {
        let Some(key) = query::array_item_key(&item) else {
            continue;
        };
        if key.kind() != PHP_STRING || query::string_content_range(&key) == reference.range {
            continue;
        }
        let name = query::string_value(&key);
        if !name.is_empty() {
            result.insert(name);
        }
    }
    result
}

#[cfg(test)]
mod tests;
