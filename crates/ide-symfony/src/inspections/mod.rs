//! Inspections of the Symfony domain: each declares its languages and
//! codes, runs analyzers of [`crate::diagnostics`] and binds quick fixes.

mod services;

pub use services::*;
use swlsp_ide::inspections::analyzer_inspection as analyzer;
use swlsp_ide::{Registrar, Services};
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;

use crate::diagnostics;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    let add = |inspection| server.inspection(inspection);

    add(analyzer(diagnostics::EmbeddedLanguageAnalyzer::new(php())));
    add(analyzer(diagnostics::ConsoleAnalyzer::new(
        Some(s.console.clone()),
        php(),
    )));
    add(new_invokable_command(php()));
    add(analyzer(diagnostics::DoctrineAnalyzer::new(
        Some(s.doctrine.clone()),
        php(),
        Some(s.dal.clone()),
    )));
    add(analyzer(diagnostics::AssetAnalyzer::new(
        Some(s.assets.clone()),
        php(),
    )));
    add(new_event(
        Some(s.events.clone()),
        php(),
        Some(s.services.clone()),
    ));
    add(analyzer(diagnostics::MessengerAnalyzer::new(php())));
    add(analyzer(diagnostics::SerializerAnalyzer::new(
        Some(s.serializer.clone()),
        php(),
    )));
    add(analyzer(diagnostics::ValidationAnalyzer::new(php())));
    add(analyzer(diagnostics::DuplicateAnalyzer::new()));
    add(analyzer(diagnostics::Project::new(
        diagnostics::RouteRedefinedAnalysis::new(Some(s.routes.clone())),
    )));
    add(new_controller(
        Some(s.routes.clone()),
        Some(s.services.clone()),
        php(),
    ));
    let client = server.server.downgrade();
    add(analyzer(
        diagnostics::ServiceAnalyzer::new(Some(s.services.clone()), php())
            .with_notice(move |message| client.notice(message)),
    ));
    add(analyzer(diagnostics::TaggedServiceAnalyzer::new(php())));
    add(analyzer(diagnostics::ContainerConstantAnalyzer::new(php())));
    add(new_service_argument(Some(s.services.clone()), php()));
}
