//! Shopware DAL entity schema.
//!
//! The normalized [`EntitySpec`]/[`Schema`] model drives entity-designer
//! previews, generated PHP classes, source-preserving rewrites, MySQL
//! migrations, committed snapshots and drift detection. Class-based DAL
//! definitions are imported losslessly: anything the typed model cannot prove
//! stays a locked raw expression.

pub mod canonical_json;
mod context;
mod definition_behavior;
mod definition_metadata;
mod diff;
mod field_modification;
mod import;
mod indexed_catalog;
mod model;
mod render;
mod rewrite;
mod service;
mod snapshot;
mod source_index;
mod specialized_fields;
mod sql;
mod util;
mod validate_spec;
mod version;

pub use context::*;
pub use diff::*;
pub use import::{
    attach_translation, import_bulk_extension, import_class_based_definition, import_definition,
    import_extension, import_translation_definition,
};
pub use indexed_catalog::*;
pub use model::*;
pub use render::{
    complete_spec, render_collection, render_definition, render_entity, render_migration,
    render_translation_collection, render_translation_definition, render_translation_entity,
};
pub use rewrite::*;
pub use service::*;
pub use snapshot::*;
pub use source_index::*;
pub use specialized_fields::{
    SpecializedFieldTemplate, specialized_field_supported, specialized_field_templates,
};
pub use sql::*;
pub use validate_spec::{ValidationIssue, valid_spec, validate_migration, validate_spec};
pub use version::*;

#[cfg(test)]
mod tests;
