//! Inspections of the SCSS domain.

mod scss_css_variable;
mod scss_import;
mod scss_variable;

pub use scss_css_variable::new_scss_css_variable;
pub use scss_import::new_scss_import;
pub use scss_variable::new_scss_variable;
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;
