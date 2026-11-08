//! Ports of the Go `*_test.go` suites, one module per Go file.
#![allow(clippy::all)]

mod association_autoload;
mod behavior;
mod bulk_extension;
mod context;
mod definition_behavior;
mod definition_metadata;
mod enum_field;
mod extension;
mod field_modification;
mod hierarchy;
mod import;
mod import_association;
mod indexed_catalog;
mod inheritance;
mod matrix;
mod metadata;
mod protection;
mod render;
mod rewrite;
mod service;
mod snapshot;
mod source_index;
mod specialized_fields;
mod validate;
mod version;
mod write_protected;
