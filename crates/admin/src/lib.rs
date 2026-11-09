//! Shopware Administration intelligence.
//!
//! Every module is re-exported at the crate root, so modules share one flat
//! namespace through `use crate::*`. Crate-private helpers use `pub(crate)`.
//!
//! - Component, mixin, module, directive, filter, CMS, service, store,
//!   privilege, usage and TypeScript-type repositories are persisted by
//!   [`AdminComponentIndexer`] in the shared workspace store.
//! - Twig/Vue template analysis (scoped slots, v-for and event bindings,
//!   member chains, calls) works on the current document's syntax tree.
#![allow(clippy::too_many_arguments)]

// Shared crate-internal prelude: every module starts with `use crate::*;`,
// which brings these into scope next to the flat crate namespace.
#[allow(unused_imports)]
pub(crate) use serde::{Deserialize, Serialize};
#[allow(unused_imports)]
pub(crate) use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};
#[allow(unused_imports)]
pub(crate) use swlsp_indexer::{DataIndexer, Mutation, ParsedFile, Result as IndexResult};
#[allow(unused_imports)]
pub(crate) use swlsp_parser_js::{query as jsquery, syntax as jssyntax};
#[allow(unused_imports)]
pub(crate) use swlsp_parser_twig::{ast as twigast, query as twigquery, syntax as twigsyntax};
#[allow(unused_imports)]
pub(crate) use swlsp_syntax::{
    Kind, LineIndex, SyntaxElement, SyntaxNode, SyntaxNodeExt, SyntaxToken, SyntaxTokenExt,
    TextRange, TextSize,
};

/// Declares an enumeration that is stored and displayed as a string. The
/// `#[default]` `Unset` variant stands for an absent value and renders as `""`.
macro_rules! string_enum {
    ($(#[$meta:meta])* $vis:vis enum $name:ident { $($variant:ident => $text:literal),+ $(,)? }) => {
        $(#[$meta])*
        #[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
        $vis enum $name {
            #[default]
            Unset,
            $($variant),+
        }

        impl $name {
            pub fn as_str(self) -> &'static str {
                match self {
                    Self::Unset => "",
                    $(Self::$variant => $text),+
                }
            }

            /// Parses the string form; unknown values are `None`.
            #[allow(dead_code)]
            pub fn from_text(value: &str) -> Option<Self> {
                match value {
                    "" => Some(Self::Unset),
                    $($text => Some(Self::$variant),)+
                    _ => None,
                }
            }

            pub fn is_unset(self) -> bool {
                self == Self::Unset
            }
        }

        /// Persisted as the string form; unknown strings read back as `Unset`.
        impl serde::Serialize for $name {
            fn serialize<S: serde::Serializer>(&self, serializer: S) -> std::result::Result<S::Ok, S::Error> {
                serializer.serialize_str(self.as_str())
            }
        }

        impl<'de> serde::Deserialize<'de> for $name {
            fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> std::result::Result<Self, D::Error> {
                let value = <std::borrow::Cow<'de, str> as serde::Deserialize>::deserialize(deserializer)?;
                Ok(Self::from_text(&value).unwrap_or_default())
            }
        }

        impl std::fmt::Display for $name {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str(self.as_str())
            }
        }
    };
}

mod application_container;
mod component;
mod component_type_inference;
mod definition_parser_component;
mod definition_parser_events;
mod definition_parser_members;
mod definition_parser_props;
mod definition_parser_registration;
mod definition_parser_setup;
mod dynamic_component;
mod dynamic_component_resolution;
use swlsp_uriutil::filepath;
mod indexer;
mod indexer_catalog;
mod indexer_components;
mod indexer_effective;
mod indexer_javascript;
mod indexer_twig_vue;
mod indexer_usage;
mod indexer_usage_symbol;
mod javascript_analysis;
mod live_vue;
mod meteor_declaration;
mod object_binding;
mod patterns;
mod privilege;
mod runtime_context;
mod runtime_registry;
mod script_setup;
mod script_setup_contract;
mod shopware_context;
mod shopware_utils;
mod shopware_utils_index;
mod slot_parser;
mod slot_scope;
mod static_member;
mod twig_reference;
mod type_index;
mod type_resolver;
mod usage_collector;
mod usage_javascript;
mod usage_symbol_javascript;
mod usage_symbol_registration;
mod usage_symbol_twig;
mod usage_symbol_twig_base;
mod usage_twig;
mod usage_twig_helpers;
mod usage_types;
mod value_type;
mod vue_scope_bindings;
mod vue_scope_calls;
mod vue_scope_inference;
mod vue_scope_members;
mod vue_scope_types;
mod workspace_symbols;

#[allow(unused_imports)]
pub use application_container::*;
#[allow(unused_imports)]
pub use component::*;
#[allow(unused_imports)]
pub use component_type_inference::*;
#[allow(unused_imports)]
pub use definition_parser_component::*;
#[allow(unused_imports)]
pub use definition_parser_events::*;
#[allow(unused_imports)]
pub use definition_parser_members::*;
#[allow(unused_imports)]
pub use definition_parser_props::*;
#[allow(unused_imports)]
pub use definition_parser_registration::*;
#[allow(unused_imports)]
pub use definition_parser_setup::*;
#[allow(unused_imports)]
pub use dynamic_component::*;
#[allow(unused_imports)]
pub use dynamic_component_resolution::*;
#[allow(unused_imports)]
pub use indexer::*;
#[allow(unused_imports)]
pub use indexer_catalog::*;
#[allow(unused_imports)]
pub use indexer_components::*;
#[allow(unused_imports)]
pub use indexer_effective::*;
#[allow(unused_imports)]
pub use indexer_javascript::*;
#[allow(unused_imports)]
pub use indexer_twig_vue::*;
#[allow(unused_imports)]
pub use indexer_usage::*;
#[allow(unused_imports)]
pub use indexer_usage_symbol::*;
#[allow(unused_imports)]
pub use javascript_analysis::*;
#[allow(unused_imports)]
pub use live_vue::*;
#[allow(unused_imports)]
pub use meteor_declaration::*;
#[allow(unused_imports)]
pub use object_binding::*;
#[allow(unused_imports)]
pub use patterns::*;
#[allow(unused_imports)]
pub use privilege::*;
#[allow(unused_imports)]
pub use runtime_context::*;
#[allow(unused_imports)]
pub use runtime_registry::*;
#[allow(unused_imports)]
pub use script_setup::*;
#[allow(unused_imports)]
pub use script_setup_contract::*;
#[allow(unused_imports)]
pub use shopware_context::*;
#[allow(unused_imports)]
pub use shopware_utils::*;
#[allow(unused_imports)]
pub use shopware_utils_index::*;
#[allow(unused_imports)]
pub use slot_parser::*;
#[allow(unused_imports)]
pub use slot_scope::*;
#[allow(unused_imports)]
pub use static_member::*;
#[allow(unused_imports)]
pub(crate) use swlsp_twig::block_deprecation;
#[allow(unused_imports)]
pub use twig_reference::*;
#[allow(unused_imports)]
pub use type_index::*;
#[allow(unused_imports)]
pub use type_resolver::*;
#[allow(unused_imports)]
pub use usage_collector::*;
#[allow(unused_imports)]
pub use usage_javascript::*;
#[allow(unused_imports)]
pub use usage_symbol_javascript::*;
#[allow(unused_imports)]
pub use usage_symbol_registration::*;
#[allow(unused_imports)]
pub use usage_symbol_twig::*;
#[allow(unused_imports)]
pub use usage_symbol_twig_base::*;
#[allow(unused_imports)]
pub use usage_twig::*;
#[allow(unused_imports)]
pub use usage_twig_helpers::*;
#[allow(unused_imports)]
pub use usage_types::*;
#[allow(unused_imports)]
pub use value_type::*;
#[allow(unused_imports)]
pub use vue_scope_bindings::*;
#[allow(unused_imports)]
pub use vue_scope_calls::*;
#[allow(unused_imports)]
pub use vue_scope_inference::*;
#[allow(unused_imports)]
pub use vue_scope_members::*;
#[allow(unused_imports)]
pub use vue_scope_types::*;
#[allow(unused_imports)]
pub use workspace_symbols::*;

#[cfg(test)]
mod tests;
