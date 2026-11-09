//! Constructs every diagnostic inspection the application emits. Inspections declare their IDs, codes,
//! languages and configuration domains themselves; this module only supplies
//! their services.

use std::sync::Arc;

use swlsp_lsp_phpsemantic::Provider as PhpFeatures;

use crate::registrar::Registrar;
use crate::workspace::Services;

pub(crate) fn register_diagnostic_inspections(
    server: &Registrar<'_>,
    php_features: &Arc<PhpFeatures>,
    services: &Services,
) {
    if server.domain_enabled("shopware") {
        crate::providers_extension_config::register_extension_config_document_observer(
            server,
            &services.extension_config,
        );
    }
    #[cfg(feature = "diagnostics")]
    register_inspections(server, php_features, services);
    #[cfg(not(feature = "diagnostics"))]
    let _ = php_features;
}

/// Adapts the PHP semantic provider to the analyzer interface.
#[cfg(feature = "diagnostics")]
struct PhpSemanticAnalyzer(Arc<PhpFeatures>);

#[cfg(feature = "diagnostics")]
impl swlsp_lsp_diagnostics::ProblemAnalyzer for PhpSemanticAnalyzer {
    fn analyze(
        &self,
        ctx: &swlsp_lsp::Context,
        document: &Arc<swlsp_lsp::TextDocument>,
    ) -> Result<Vec<swlsp_lsp::Problem>, swlsp_lsp::BoxError> {
        self.0.analyze(ctx, document)
    }

    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(swlsp_lsp_diagnostics::inputs(&self.0.index()))
    }
}

#[cfg(feature = "diagnostics")]
impl swlsp_lsp_diagnostics::DeclaredAnalyzer for PhpSemanticAnalyzer {
    const INSPECTION: swlsp_lsp::InspectionSpec = swlsp_lsp_phpsemantic::INSPECTION;
}

/// Constructs every inspection with its services. Each inspection declares
/// its ID, configuration domain, languages and codes beside its analyzer.
#[cfg(feature = "diagnostics")]
fn register_inspections(server: &Registrar<'_>, php_features: &Arc<PhpFeatures>, s: &Services) {
    use swlsp_lsp_diagnostics::{self as diagnostics, StoreComplianceResolver};
    use swlsp_lsp_inspections::{self as inspections, analyzer_inspection as analyzer};

    register_suppressions(server);

    let php = || Some(s.php.clone());
    let add = |inspection| server.inspection(inspection);

    add(analyzer(PhpSemanticAnalyzer(php_features.clone())));
    add(inspections::new_php_imports(php()));
    add(analyzer(diagnostics::ShopwarePhpLocalAnalyzer::new()));
    add(analyzer(diagnostics::ShopwarePhpSemanticAnalyzer::new(
        php(),
    )));
    for inspection in inspections::new_shopware_migration_inspections(php(), s.shopware_version) {
        add(inspection);
    }
    add(analyzer(diagnostics::EmbeddedLanguageAnalyzer::new(php())));
    add(analyzer(diagnostics::ConsoleAnalyzer::new(
        Some(s.console.clone()),
        php(),
    )));
    add(inspections::new_invokable_command(php()));
    add(analyzer(diagnostics::DoctrineAnalyzer::new(
        Some(s.doctrine.clone()),
        php(),
        Some(s.dal.clone()),
    )));
    add(analyzer(diagnostics::AssetAnalyzer::new(
        Some(s.assets.clone()),
        php(),
    )));
    add(inspections::new_event(
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
    add(analyzer(diagnostics::TwigEnumAnalyzer::new(php())));
    add(analyzer(diagnostics::TwigDeprecationAnalyzer::new(Some(
        s.twig.clone(),
    ))));
    add(analyzer(diagnostics::TwigMemberMissingAnalyzer::new(
        Some(s.twig.clone()),
        php(),
    )));
    add(analyzer(diagnostics::TwigMemberDeprecationAnalyzer::new(
        Some(s.twig.clone()),
        php(),
    )));
    add(analyzer(diagnostics::DuplicateAnalyzer::new()));
    add(analyzer(diagnostics::Project::new(
        diagnostics::RouteRedefinedAnalysis::new(Some(s.routes.clone())),
    )));
    add(inspections::new_services_xml_migration());
    add(inspections::new_scss_variable(
        s.styles.clone(),
        Some(s.symbols.clone()),
    ));
    add(inspections::new_controller(
        Some(s.routes.clone()),
        Some(s.services.clone()),
        php(),
    ));
    add(analyzer(diagnostics::ServiceAnalyzer::new(
        Some(s.services.clone()),
        php(),
    )));
    add(analyzer(diagnostics::TaggedServiceAnalyzer::new(php())));
    add(analyzer(diagnostics::ContainerConstantAnalyzer::new(php())));
    add(inspections::new_service_argument(
        Some(s.services.clone()),
        php(),
    ));
    add(inspections::new_translation(
        Some(s.translations.clone()),
        php(),
        Some(s.snippets.clone()),
    ));
    add(inspections::new_template(
        &s.root,
        Some(s.twig.clone()),
        php(),
    ));
    add(analyzer(diagnostics::TwigRenderBlockAnalyzer::new(
        Some(s.twig.clone()),
        php(),
    )));
    add(analyzer(diagnostics::TwigMacroAnalyzer::new(Some(
        s.twig.clone(),
    ))));
    add(analyzer(diagnostics::TwigComponentAnalyzer::new(Some(
        s.twig_components.clone(),
    ))));
    add(analyzer(diagnostics::LiveComponentEventAnalyzer::new(
        Some(s.twig_components.clone()),
    )));
    add(inspections::new_snippet(
        Some(s.snippets.clone()),
        Some(s.translations.clone()),
    ));
    add(analyzer(diagnostics::ThemeAnalyzer::new(
        &s.root,
        s.extensions.clone(),
    )));
    add(inspections::new_twig_versioning(Some(
        s.twig_versioning.clone(),
    )));
    for inspection in inspections::new_admin_inspections(s.admin.clone(), s.shopware_version) {
        add(inspection);
    }
    add(analyzer(diagnostics::DalEntityAnalyzer::new(Some(
        s.dal.clone(),
    ))));
    add(inspections::new_shopware_criteria());
    add(analyzer(
        diagnostics::ShopwareDecorationAnalyzer::new(php()),
    ));
    let compliance: Arc<dyn StoreComplianceResolver> = s.extension_config.clone();
    add(analyzer(diagnostics::ShopwareStoreComposerAnalyzer::new(
        Some(compliance),
    )));
    add(analyzer(diagnostics::EntitySnapshotAnalyzer::new(Some(
        Arc::new(crate::providers_actions::entity_catalog(s)),
    ))));
    add(inspections::new_app_script(
        Some(s.app_scripts.clone()),
        Some(s.extensions.clone()),
    ));
}

/// `@noinspection` comments: PHP's reader also understands PHPStan's forms;
/// the other languages share the CST-based reader.
#[cfg(feature = "diagnostics")]
fn register_suppressions(server: &Registrar<'_>) {
    use swlsp_language::LanguageId;
    use swlsp_lsp::comment_suppression_reader;

    server.suppressions(LanguageId::Php, swlsp_lsp_phpanalysis::suppression_reader());
    server.suppressions(
        LanguageId::Twig,
        comment_suppression_reader(&[
            swlsp_parser_twig::syntax::TWIG_COMMENT,
            swlsp_parser_twig::syntax::HTML_COMMENT,
        ]),
    );
    server.suppressions(
        LanguageId::Yaml,
        comment_suppression_reader(&[swlsp_parser_yaml::syntax::TK_COMMENT]),
    );
    server.suppressions(
        LanguageId::Xml,
        comment_suppression_reader(&[
            swlsp_parser_xml::syntax::XML_COMMENT,
            swlsp_parser_xml::syntax::TK_COMMENT,
        ]),
    );
    server.suppressions(
        LanguageId::JavaScript,
        comment_suppression_reader(&[
            swlsp_parser_js::syntax::TK_LINE_COMMENT,
            swlsp_parser_js::syntax::TK_BLOCK_COMMENT,
        ]),
    );
    server.suppressions(
        LanguageId::Scss,
        comment_suppression_reader(&[
            swlsp_parser_scss::syntax::TK_LINE_COMMENT,
            swlsp_parser_scss::syntax::TK_BLOCK_COMMENT,
        ]),
    );
}
