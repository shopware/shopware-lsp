//! Inspections of the Twig domain: each declares its languages and
//! codes, runs analyzers of [`crate::diagnostics`] and binds quick fixes.

mod help;
mod twig;

use swlsp_ide::inspections::analyzer_inspection as analyzer;
use swlsp_ide::{Registrar, Services};
pub use twig::*;
// The shared inspection contract, so the inspections find it beside themselves.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::inspections::*;

use crate::diagnostics;

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    let twig = || Some(s.twig.clone());
    let add = |inspection| server.inspection(inspection);

    add(analyzer(diagnostics::TwigEnumAnalyzer::new(php())));
    add(analyzer(diagnostics::TwigDeprecationAnalyzer::new(twig())));
    server.rule_help("twig.markup", help::MARKUP);
    server.rule_help("twig", help::TWIG);
    add(analyzer(diagnostics::TwigMarkupAnalyzer::new()));
    add(analyzer(diagnostics::TwigMemberMissingAnalyzer::new(
        twig(),
        php(),
    )));
    add(analyzer(diagnostics::TwigMemberDeprecationAnalyzer::new(
        twig(),
        php(),
    )));
    add(analyzer(diagnostics::TwigMemberCallAnalyzer::new(
        twig(),
        php(),
    )));
    add(new_twig_null_access(twig(), php()));
    add(new_twig_undefined_variable(twig(), php()));
    add(new_twig_dump_type(twig(), php()));
    add(new_template(&s.root, twig(), php()));
    add(analyzer(diagnostics::TwigRenderBlockAnalyzer::new(
        twig(),
        php(),
    )));
    add(analyzer(diagnostics::TwigMacroAnalyzer::new(twig())));
    add(analyzer(diagnostics::TwigComponentAnalyzer::new(Some(
        s.twig_components.clone(),
    ))));
    add(analyzer(diagnostics::LiveComponentEventAnalyzer::new(
        Some(s.twig_components.clone()),
    )));
    add(new_twig_versioning(Some(s.twig_versioning.clone())));
    add(new_snippet(
        Some(s.snippets.clone()),
        Some(s.translations.clone()),
    ));
    add(new_translation(
        Some(s.translations.clone()),
        php(),
        Some(s.snippets.clone()),
    ));
}
