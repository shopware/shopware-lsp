//! The single ownership catalog of every diagnostic the application emits
//! (port of Go `internal/app/inspections.go`). Stable IDs and language
//! routing are declared here; analyzers report byte-oriented problems.

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

/// Adapts the PHP semantic provider to the analyzer interface (Go passes
/// the provider itself as a `ProblemAnalyzer`).
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
}

#[cfg(feature = "diagnostics")]
fn register_inspections(server: &Registrar<'_>, php_features: &Arc<PhpFeatures>, s: &Services) {
    use swlsp_language::LanguageId;
    use swlsp_lsp_diagnostics::{self as diagnostics, ProblemAnalyzer, StoreComplianceResolver};
    use swlsp_lsp_inspections as inspections;

    let problem = |id: &str,
                   languages: &[LanguageId],
                   source: &str,
                   codes: &[&str],
                   analyzer: Arc<dyn ProblemAnalyzer>| {
        server.inspection(inspections::new_analyzer_inspection(
            id,
            languages,
            source,
            codes,
            vec![analyzer],
        ));
    };
    let php = || Some(s.php.clone());
    let php_only = [LanguageId::Php];
    let json_only = [LanguageId::Json];
    let twig_only = [LanguageId::Twig];
    let php_twig = [LanguageId::Php, LanguageId::Twig];
    let xml_yaml = [LanguageId::Xml, LanguageId::Yaml];
    let php_xml_yaml = [LanguageId::Php, LanguageId::Xml, LanguageId::Yaml];

    problem(
        "php.semantic",
        &php_only,
        swlsp_lsp_phpsemantic::SOURCE,
        swlsp_lsp_phpsemantic::DIAGNOSTIC_IDS,
        Arc::new(PhpSemanticAnalyzer(php_features.clone())),
    );
    server.inspection(inspections::new_php_imports(Some(s.php.clone())));
    server.inspection(inspections::new_shopware_php_local());
    server.inspection(inspections::new_shopware_php_semantic(php()));
    server.inspection(inspections::new_shopware_migration(
        php(),
        s.shopware_version,
    ));
    problem(
        "symfony.embedded_language",
        &php_only,
        "symfony",
        &[
            "symfony.php.embedded_css.invalid",
            "symfony.php.embedded_json.invalid",
            "symfony.php.embedded_xpath.invalid",
        ],
        Arc::new(diagnostics::EmbeddedLanguageAnalyzer::new(php())),
    );
    problem(
        "symfony.console",
        &php_only,
        "symfony",
        &[
            "symfony.console.argument.missing",
            "symfony.console.command.missing",
            "symfony.console.option.missing",
        ],
        Arc::new(diagnostics::ConsoleAnalyzer::new(
            Some(s.console.clone()),
            php(),
        )),
    );
    server.inspection(inspections::new_invokable_command(php()));
    problem(
        "symfony.doctrine",
        &php_only,
        "symfony",
        &[
            "symfony.doctrine.column.missing",
            "symfony.doctrine.table.missing",
            "symfony.doctrine.type_class.invalid",
            "symfony.doctrine.type_class.missing",
        ],
        Arc::new(diagnostics::DoctrineAnalyzer::new(
            Some(s.doctrine.clone()),
            php(),
            Some(s.dal.clone()),
        )),
    );
    problem(
        "symfony.asset",
        &php_twig,
        "symfony",
        &[
            "symfony.asset.missing",
            "symfony.asset.package.missing",
            "symfony.asset_mapper.entrypoint.missing",
            "symfony.encore.entry.missing",
            "symfony.vite.entry.missing",
        ],
        Arc::new(diagnostics::AssetAnalyzer::new(
            Some(s.assets.clone()),
            php(),
        )),
    );
    server.inspection(inspections::new_event(
        Some(s.events.clone()),
        php(),
        Some(s.services.clone()),
    ));
    problem(
        "symfony.messenger",
        &php_xml_yaml,
        "symfony",
        &[
            "symfony.messenger.handler_method.missing",
            "symfony.messenger.message.missing",
        ],
        Arc::new(diagnostics::MessengerAnalyzer::new(php())),
    );
    problem(
        "symfony.serializer",
        &php_only,
        "symfony",
        &["symfony.serializer.class.missing"],
        Arc::new(diagnostics::SerializerAnalyzer::new(
            Some(s.serializer.clone()),
            php(),
        )),
    );
    problem(
        "symfony.validation",
        &php_only,
        "symfony",
        &["symfony.validation.option.missing"],
        Arc::new(diagnostics::ValidationAnalyzer::new(php())),
    );
    problem(
        "twig.enum",
        &twig_only,
        "twig",
        &["twig.enum.invalid", "twig.enum.missing"],
        Arc::new(diagnostics::TwigEnumAnalyzer::new(php())),
    );
    problem(
        "twig.deprecation",
        &twig_only,
        "twig",
        &[
            "twig.filter.deprecated",
            "twig.function.deprecated",
            "twig.tag.deprecated",
            "twig.test.deprecated",
        ],
        Arc::new(diagnostics::TwigDeprecationAnalyzer::new(Some(
            s.twig.clone(),
        ))),
    );
    problem(
        "twig.member.missing",
        &twig_only,
        "twig",
        &["twig.member.missing"],
        Arc::new(diagnostics::TwigMemberMissingAnalyzer::new(
            Some(s.twig.clone()),
            php(),
        )),
    );
    problem(
        "twig.member.deprecation",
        &twig_only,
        "twig",
        &["twig.member.deprecated"],
        Arc::new(diagnostics::TwigMemberDeprecationAnalyzer::new(
            Some(s.twig.clone()),
            php(),
        )),
    );
    problem(
        "symfony.duplicate",
        &php_xml_yaml,
        "symfony",
        &[
            "symfony.parameter.duplicate",
            "symfony.route.duplicate",
            "symfony.service.duplicate",
        ],
        Arc::new(diagnostics::DuplicateAnalyzer::new()),
    );
    server.inspection(inspections::new_services_xml_migration());
    server.inspection(inspections::new_scss_variable(
        s.styles.clone(),
        Some(s.symbols.clone()),
    ));
    server.inspection(inspections::new_controller(
        Some(s.routes.clone()),
        Some(s.services.clone()),
        php(),
    ));
    problem(
        "symfony.service",
        &php_xml_yaml,
        "symfony",
        &[
            "symfony.class.deprecated",
            "symfony.class.missing",
            "symfony.parameter.missing",
            "symfony.service.deprecated",
            "symfony.service.missing",
        ],
        Arc::new(diagnostics::ServiceAnalyzer::new(
            Some(s.services.clone()),
            php(),
        )),
    );
    problem(
        "symfony.service.tag_type",
        &xml_yaml,
        "symfony",
        &["symfony.service.tag_type"],
        Arc::new(diagnostics::TaggedServiceAnalyzer::new(php())),
    );
    problem(
        "symfony.container_constant",
        &xml_yaml,
        "symfony",
        &["symfony.constant.missing"],
        Arc::new(diagnostics::ContainerConstantAnalyzer::new(php())),
    );
    server.inspection(inspections::new_service_argument(
        Some(s.services.clone()),
        php(),
    ));
    server.inspection(inspections::new_translation(
        Some(s.translations.clone()),
        php(),
        Some(s.snippets.clone()),
    ));
    server.inspection(inspections::new_template(
        &s.root,
        Some(s.twig.clone()),
        php(),
    ));
    problem(
        "twig.render_block",
        &php_only,
        "twig",
        &["twig.template.block.missing"],
        Arc::new(diagnostics::TwigRenderBlockAnalyzer::new(
            Some(s.twig.clone()),
            php(),
        )),
    );
    problem(
        "twig.macro",
        &twig_only,
        "twig",
        &["twig.macro.missing"],
        Arc::new(diagnostics::TwigMacroAnalyzer::new(Some(s.twig.clone()))),
    );
    problem(
        "twig.component",
        &twig_only,
        "twig",
        &[
            "twig.component.block.missing",
            "twig.component.live_action.missing",
            "twig.component.live_argument.missing",
            "twig.component.missing",
            "twig.component.mixed_syntax",
            "twig.component.self_macro_import",
        ],
        Arc::new(diagnostics::TwigComponentAnalyzer::new(Some(
            s.twig_components.clone(),
        ))),
    );
    problem(
        "twig.component.live_event",
        &php_twig,
        "twig",
        &[
            "twig.component.live_event.missing",
            "twig.component.live_event_argument.missing",
        ],
        Arc::new(diagnostics::LiveComponentEventAnalyzer::new(Some(
            s.twig_components.clone(),
        ))),
    );
    server.inspection(inspections::new_snippet(
        Some(s.snippets.clone()),
        Some(s.translations.clone()),
    ));
    problem(
        "shopware.theme",
        &twig_only,
        "shopware-lsp",
        &["theme.icon.missing"],
        Arc::new(diagnostics::ThemeAnalyzer::new(
            &s.root,
            s.extensions.clone(),
        )),
    );
    server.inspection(inspections::new_twig_versioning(Some(
        s.twig_versioning.clone(),
    )));
    server.inspection(inspections::new_admin(s.admin.clone(), s.shopware_version));
    server.inspection(inspections::new_dal_entity(Some(s.dal.clone())));
    server.inspection(inspections::new_shopware_criteria());
    problem(
        "shopware.decoration",
        &php_only,
        "shopware-lsp",
        &["shopware.decoration.abstraction"],
        Arc::new(diagnostics::ShopwareDecorationAnalyzer::new(php())),
    );
    let compliance: Arc<dyn StoreComplianceResolver> = s.extension_config.clone();
    problem(
        "shopware.store_composer",
        &json_only,
        "shopware-lsp",
        &[
            "shopware.store.description",
            "shopware.store.label",
            "shopware.store.manufacturer-link",
            "shopware.store.require-core",
            "shopware.store.support-link",
        ],
        Arc::new(diagnostics::ShopwareStoreComposerAnalyzer::new(Some(
            compliance,
        ))),
    );
    problem(
        "shopware.entity_snapshot",
        &json_only,
        "shopware-lsp",
        &[
            "shopware.entity_snapshot.invalid",
            "shopware.entity_snapshot.graph",
            "shopware.entity_snapshot.parent_missing",
            "shopware.entity_snapshot.reconcile",
            "shopware.entity_snapshot.migration_missing",
            "shopware.entity_snapshot.migration_changed",
            "shopware.entity_snapshot.schema_drift",
        ],
        Arc::new(diagnostics::EntitySnapshotAnalyzer::new(Some(Arc::new(
            crate::providers_actions::entity_catalog(s),
        )))),
    );
    server.inspection(inspections::new_app_script(
        Some(s.app_scripts.clone()),
        Some(s.extensions.clone()),
    ));
}
