//! Static CSS classes (SCSS declarations and markup usages) and SCSS
//! variables (port of Go `internal/style`).

mod classes;
mod index;
mod model;
mod variables;

pub use classes::{class_at, class_declarations, class_usages};
pub use index::Index;
pub use model::{ByteRange, ClassCatalog, ClassOccurrence, ClassOccurrenceKind, SourcePosition};
pub use variables::{
    VariableAnalysis, VariableCatalog, VariableOccurrence, VariableOccurrenceKind,
    analyze_variables, normalize_variable_name, theme_variables,
};
