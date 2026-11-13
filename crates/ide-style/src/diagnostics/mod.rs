//! Analyzers of the SCSS domain.

mod scss_import;
mod scss_variable;

pub use scss_import::{SCSS_IMPORT_EXTENSION_CODE, ScssImportExtensionAnalyzer};
pub use scss_variable::{
    SCSS_VARIABLE_UNKNOWN_CODE, ScssVariableAnalyzer, ScssVariableDeclarationProvider,
    ScssVariableIndexReadiness,
};
// The shared analyzer contract, so the analyzers find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::diagnostics::*;
