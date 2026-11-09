//! Symfony public assets, asset metadata (manifest, Encore entrypoints,
//! AssetMapper importmaps, webpack and Vite configs), asset packages,
//! Assetic named formulas, and editor-independent asset references in PHP
//! and Twig.
//!
//! Request-time PHP analysis is passed explicitly as a
//! [`swlsp_php::PhpContext`].

mod assetic;
pub(crate) mod index;
mod model;
mod package;
mod parser;
mod paths;
mod reference;
pub(crate) mod vite;

pub use index::{Index, NameCatalog};
pub use model::{Kind, Package, ReferenceKind, Resource, Usage};
pub use package::{packages_in_php, packages_in_xml, packages_in_yaml};
pub use reference::{
    HtmlAssetType, Reference, matches_html_asset_type, reference_at, reference_range, references,
    validate_php_reference,
};

#[cfg(test)]
mod tests;
