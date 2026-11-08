//! Lossless, composable PHP source rewrites on top of the immutable PHP CST
//! (port of `internal/php/rewrite`).
//!
//! An [`Editor`] collects related edits against one source snapshot: class
//! references with automatic imports, argument/parameter list edits, class
//! hierarchy and member edits, native type edits, and PHPDoc annotation
//! edits. [`Editor::finish`] validates the complete set.

mod class;
mod editor;
mod list;
mod phpdoc;
mod types;

#[cfg(test)]
mod tests;

pub use editor::{Editor, Error};
pub use phpdoc::{PhpDocAnnotation, find_phpdoc_annotation};
