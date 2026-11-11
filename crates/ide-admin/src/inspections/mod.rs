//! Inspections of the Administration domain: each declares its languages and
//! codes, runs analyzers of [`crate::diagnostics`] and binds quick fixes.

mod admin;
mod help;

pub use admin::*;
use swlsp_ide::{Registrar, Services};
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    server.rule_help("admin", help::ADMIN);
    for inspection in new_admin_inspections(s.admin.clone(), s.shopware_version) {
        server.inspection(inspection);
    }
}
