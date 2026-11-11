//! Inspections of the Shopware domain: each declares its languages and
//! codes, runs analyzers of [`crate::diagnostics`] and binds quick fixes.

mod migration;
mod shopware;

pub use migration::*;
pub use shopware::*;
use swlsp_ide::inspections::analyzer_inspection as analyzer;
use swlsp_ide::{Registrar, Services};
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;

use crate::diagnostics;

use std::sync::Arc;

use crate::diagnostics::StoreComplianceResolver;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    let add = |inspection| server.inspection(inspection);

    add(analyzer(diagnostics::ShopwarePhpLocalAnalyzer::new()));
    add(analyzer(diagnostics::ShopwarePhpSemanticAnalyzer::new(
        php(),
    )));
    for inspection in new_shopware_migration_inspections(php(), s.shopware_version) {
        add(inspection);
    }
    add(analyzer(diagnostics::ThemeAnalyzer::new(
        &s.root,
        s.extensions.clone(),
    )));
    add(analyzer(diagnostics::DalEntityAnalyzer::new(Some(
        s.dal.clone(),
    ))));
    add(analyzer(diagnostics::DalDefinitionAnalyzer::new(
        php(),
        Some(s.dal.clone()),
    )));
    add(new_shopware_criteria());
    add(analyzer(
        diagnostics::ShopwareDecorationAnalyzer::new(php()),
    ));
    let compliance: Arc<dyn StoreComplianceResolver> = s.extension_config.clone();
    add(analyzer(diagnostics::ShopwareStoreComposerAnalyzer::new(
        Some(compliance),
    )));
    add(analyzer(diagnostics::EntitySnapshotAnalyzer::new(Some(
        Arc::new(s.entity_catalog()),
    ))));
    add(new_app_script(
        Some(s.app_scripts.clone()),
        Some(s.extensions.clone()),
    ));
    add(new_services_xml_migration());
}
