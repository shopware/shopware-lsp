//! Twig template domain: template files, blocks, inheritance, references,
//! macros, globals and extension callables.

mod builtins;
mod context;
mod document_memo;
mod documentation;
mod dump_type;
mod enum_reference;
mod extension_parser;
mod extension_usage;
mod global;
mod guard;
mod indexer;
mod inference;
mod macros;
mod narrowing;
mod parser;
mod path;
mod php_access;
mod php_call;
mod php_usage;
mod reference;
mod render_block;
mod security;
mod see;
mod symfony_types;
mod template_variable;
mod test_expression;
mod token_parser;
mod types_tag;
mod undefined;
mod versioning_service;

pub use context::{ABSTRACT_TEMPLATE_MARKER, template_file_is_abstract, template_is_abstract};
pub use document_memo::DocumentMemo;
pub use documentation::*;
pub use dump_type::{DUMP_TYPE_TAG, DumpedType};
pub use enum_reference::*;
pub use extension_parser::*;
pub use extension_usage::*;
pub use global::*;
pub use guard::*;
pub use indexer::TwigIndexer;
pub use inference::is_nullable;
pub use macros::*;
pub use parser::*;
pub use path::{
    VERSION_COMMENT_PREFIX, convert_to_relative_path, format_version_comment,
    is_excluded_twig_path, is_storefront_dist_directory, is_storefront_template,
    is_template_asset_path, is_upstream_template, template_names,
};
pub use php_access::*;
pub use php_call::*;
pub use php_usage::*;
pub use reference::*;
pub use render_block::*;
pub use security::{AccessRule, access_rules_in_yaml};
pub use see::*;
pub use template_variable::*;
pub use test_expression::*;
pub use token_parser::{TwigTag, TwigTagUsage, parse_twig_token_parsers, twig_tag_usages};
pub use types_tag::*;
pub use undefined::UndefinedVariable;
pub use versioning_service::*;

use std::cmp::Ordering;

/// Case-insensitive ordering with an ASCII fast path.
pub fn compare_fold(left: &str, right: &str) -> Ordering {
    if left.is_ascii() && right.is_ascii() {
        let l = left.bytes().map(|b| b.to_ascii_lowercase());
        let r = right.bytes().map(|b| b.to_ascii_lowercase());
        return l.cmp(r);
    }
    swlsp_textutil::lowercase(left).cmp(&swlsp_textutil::lowercase(right))
}

#[cfg(test)]
mod tests;
