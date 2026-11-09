//! Environment-variable declarations (`.env`, Dockerfile, Docker Compose) and
//! Symfony `%env(...)%` / PHP `env()` references.

mod index;
mod model;
mod parser;
mod reference;

pub use index::{Index, should_index_path};
pub use model::{Occurrence, OccurrenceKind, Reference, SourceKind, Variable};
pub use reference::{
    completion_reference_at, occurrence_at, php_completion_reference_at, php_occurrence_at,
    php_reference_at, php_references, reference_at, references,
};
