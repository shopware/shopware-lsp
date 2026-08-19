package app

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/inspections"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
)

// registerDiagnosticInspections is the single ownership catalog for every
// diagnostic emitted by the application. Stable IDs and language routing are
// declared here; analyzers report byte-oriented Problems directly.
func registerDiagnosticInspections(
	server *lsp.Server,
	root string,
	phpFeatures *phpsemantic.Provider,
	services workspaceServices,
) {
	phpOnly := []language.ID{language.PHP}
	jsonOnly := []language.ID{language.JSON}
	twigOnly := []language.ID{language.Twig}
	phpTwig := []language.ID{language.PHP, language.Twig}
	xmlYAML := []language.ID{language.XML, language.YAML}
	phpXMLYAML := []language.ID{language.PHP, language.XML, language.YAML}

	registerProblemInspection(server, "php.semantic", phpOnly, "shopware-php", []string{
		"php.abstract",
		"php.arguments",
		"php.deprecated",
		"php.duplicate",
		"php.extension",
		"php.inheritance",
		"php.override",
		"php.parse",
		"php.returnType",
		"php.undefined",
		"php.version",
		"php.visibility",
	}, phpFeatures)
	server.RegisterInspection(inspections.NewShopwarePHPLocal())
	server.RegisterInspection(inspections.NewShopwarePHPSemantic(services.php))
	server.RegisterInspection(inspections.NewShopwareMigration(
		services.php,
		services.shopwareVersion,
	))
	registerProblemInspection(server, "symfony.embedded_language", phpOnly, "symfony", []string{
		"symfony.php.embedded_css.invalid",
		"symfony.php.embedded_json.invalid",
		"symfony.php.embedded_xpath.invalid",
	}, diagnostics.NewEmbeddedLanguageAnalyzer(services.php))
	registerProblemInspection(server, "symfony.console", phpOnly, "symfony", []string{
		"symfony.console.argument.missing",
		"symfony.console.command.missing",
		"symfony.console.option.missing",
	}, diagnostics.NewConsoleAnalyzer(services.console, services.php))
	server.RegisterInspection(inspections.NewInvokableCommand(services.php))
	registerProblemInspection(server, "symfony.doctrine", phpOnly, "symfony", []string{
		"symfony.doctrine.column.missing",
		"symfony.doctrine.constraint_column.missing",
		"symfony.doctrine.constraint_field.missing",
		"symfony.doctrine.discriminator_class.invalid",
		"symfony.doctrine.entity.missing",
		"symfony.doctrine.field.missing",
		"symfony.doctrine.lifecycle_method.missing",
		"symfony.doctrine.magic_field.missing",
		"symfony.doctrine.mapping_class.missing",
		"symfony.doctrine.mapping_property.missing",
		"symfony.doctrine.table.missing",
		"symfony.doctrine.type.unknown",
		"symfony.doctrine.type_class.invalid",
		"symfony.doctrine.type_class.missing",
	}, diagnostics.NewDoctrineAnalyzer(
		services.doctrine,
		services.php,
		services.dal,
	))
	registerProblemInspection(server, "symfony.asset", phpTwig, "symfony", []string{
		"symfony.asset.missing",
		"symfony.asset.package.missing",
		"symfony.asset_mapper.entrypoint.missing",
		"symfony.encore.entry.missing",
		"symfony.vite.entry.missing",
	}, diagnostics.NewAssetAnalyzer(services.assets, services.php))
	registerProblemInspection(server, "symfony.stimulus", twigOnly, "symfony", []string{
		"symfony.stimulus.controller.missing",
	}, diagnostics.NewStimulusAnalyzer(services.stimulus))
	server.RegisterInspection(inspections.NewEvent(
		services.events,
		services.php,
		services.services,
	))
	registerProblemInspection(server, "symfony.messenger", phpXMLYAML, "symfony", []string{
		"symfony.messenger.handler_method.missing",
		"symfony.messenger.message.missing",
	}, diagnostics.NewMessengerAnalyzer(services.php))
	server.RegisterInspection(inspections.NewForm(services.forms, services.php))
	registerProblemInspection(server, "symfony.security", []language.ID{
		language.PHP, language.Twig, language.XML, language.YAML,
	}, "symfony", []string{
		"symfony.security.attribute.missing",
		"symfony.security.provider.missing",
	}, diagnostics.NewSecurityAnalyzer(services.security, services.php))
	registerProblemInspection(server, "symfony.serializer", phpOnly, "symfony", []string{
		"symfony.serializer.class.missing",
	}, diagnostics.NewSerializerAnalyzer(services.serializer, services.php))
	registerProblemInspection(server, "symfony.validation", phpOnly, "symfony", []string{
		"symfony.validation.option.missing",
	}, diagnostics.NewValidationAnalyzer(services.php))
	registerProblemInspection(server, "twig.enum", twigOnly, "twig", []string{
		"twig.enum.invalid",
		"twig.enum.missing",
	}, diagnostics.NewTwigEnumAnalyzer(services.php))
	registerProblemInspection(server, "twig.deprecation", twigOnly, "twig", []string{
		"twig.filter.deprecated",
		"twig.function.deprecated",
		"twig.tag.deprecated",
		"twig.test.deprecated",
	}, diagnostics.NewTwigDeprecationAnalyzer(services.twig))
	registerProblemInspection(server, "twig.member.missing", twigOnly, "twig", []string{
		"twig.member.missing",
	}, diagnostics.NewTwigMemberMissingAnalyzer(services.twig, services.php))
	registerProblemInspection(server, "twig.member.deprecation", twigOnly, "twig", []string{
		"twig.member.deprecated",
	}, diagnostics.NewTwigMemberDeprecationAnalyzer(services.twig, services.php))
	registerProblemInspection(server, "symfony.duplicate", phpXMLYAML, "symfony", []string{
		"symfony.parameter.duplicate",
		"symfony.route.duplicate",
		"symfony.service.duplicate",
	}, diagnostics.NewDuplicateAnalyzer())
	registerProblemInspection(server, "symfony.legacy_configuration", xmlYAML, "symfony", []string{
		"symfony.route.pattern.deprecated",
		"symfony.route.requirement.deprecated",
		"symfony.service.factory.deprecated",
	}, diagnostics.NewLegacyConfigurationAnalyzer())
	server.RegisterInspection(inspections.NewServicesXMLMigration())
	server.RegisterInspection(inspections.NewSCSSVariable(
		services.styles,
		services.symbols,
	))

	server.RegisterInspection(inspections.NewYAMLCompatibility(services.php.Project()))
	server.RegisterInspection(inspections.NewController(
		services.routes,
		services.services,
		services.php,
	))

	registerProblemInspection(server, "symfony.service", phpXMLYAML, "symfony", []string{
		"symfony.class.deprecated",
		"symfony.class.missing",
		"symfony.parameter.missing",
		"symfony.service.deprecated",
		"symfony.service.missing",
	}, diagnostics.NewServiceAnalyzer(services.services, services.php))
	registerProblemInspection(server, "symfony.service.tag_type", xmlYAML, "symfony", []string{
		"symfony.service.tag_type",
	}, diagnostics.NewTaggedServiceAnalyzer(services.php))
	registerProblemInspection(server, "symfony.container_constant", xmlYAML, "symfony", []string{
		"symfony.constant.missing",
	}, diagnostics.NewContainerConstantAnalyzer(services.php))
	server.RegisterInspection(inspections.NewServiceArgument(services.services, services.php))
	server.RegisterInspection(inspections.NewTranslation(
		services.translations,
		services.php,
		services.snippets,
	))
	server.RegisterInspection(inspections.NewTemplate(
		root,
		services.twig,
		services.php,
	))
	registerProblemInspection(server, "twig.render_block", phpOnly, "twig", []string{
		"twig.template.block.missing",
	}, diagnostics.NewTwigRenderBlockAnalyzer(services.twig, services.php))
	registerProblemInspection(server, "twig.macro", twigOnly, "twig", []string{
		"twig.macro.missing",
	}, diagnostics.NewTwigMacroAnalyzer(services.twig))
	registerProblemInspection(server, "twig.component", twigOnly, "twig", []string{
		"twig.component.block.missing",
		"twig.component.live_action.missing",
		"twig.component.live_argument.missing",
		"twig.component.missing",
		"twig.component.mixed_syntax",
		"twig.component.self_macro_import",
	}, diagnostics.NewTwigComponentAnalyzer(services.twigComponents))
	registerProblemInspection(server, "twig.component.live_event", phpTwig, "twig", []string{
		"twig.component.live_event.missing",
		"twig.component.live_event_argument.missing",
	}, diagnostics.NewLiveComponentEventAnalyzer(services.twigComponents))
	server.RegisterInspection(inspections.NewSnippet(
		services.snippets,
		services.translations,
	))
	registerProblemInspection(server, "shopware.theme", twigOnly, "shopware-lsp", []string{
		"theme.icon.missing",
	}, diagnostics.NewThemeAnalyzer(root, services.extensions))
	server.RegisterInspection(inspections.NewTwigVersioning(services.twigVersioning))
	server.RegisterInspection(inspections.NewAdmin(
		services.admin,
		services.shopwareVersion,
	))
	server.RegisterInspection(inspections.NewDALEntity(services.dal))
	server.RegisterInspection(inspections.NewShopwareCriteria())
	registerProblemInspection(server, "shopware.decoration", phpOnly, "shopware-lsp", []string{
		"shopware.decoration.abstraction",
	}, diagnostics.NewShopwareDecorationAnalyzer(services.php))
	registerProblemInspection(server, "shopware.store_composer", jsonOnly, "shopware-lsp", []string{
		"shopware.store.description",
		"shopware.store.label",
		"shopware.store.manufacturer-link",
		"shopware.store.require-core",
		"shopware.store.support-link",
	}, diagnostics.NewShopwareStoreComposerAnalyzer())
	registerProblemInspection(server, "shopware.entity_snapshot", jsonOnly, "shopware-lsp", []string{
		"shopware.entity_snapshot.invalid",
		"shopware.entity_snapshot.graph",
		"shopware.entity_snapshot.parent_missing",
		"shopware.entity_snapshot.reconcile",
		"shopware.entity_snapshot.migration_missing",
		"shopware.entity_snapshot.migration_changed",
		"shopware.entity_snapshot.schema_drift",
	}, diagnostics.NewEntitySnapshotAnalyzer(entityschema.NewIndexedCatalog(
		services.php,
		services.entitySchemaSources,
		services.symbols,
	)))
	server.RegisterInspection(inspections.NewAppScript(
		services.appScripts,
		services.extensions,
	))
}

func registerProblemInspection(
	server *lsp.Server,
	id string,
	languages []language.ID,
	source string,
	codes []string,
	analyzers ...inspections.ProblemAnalyzer,
) {
	server.RegisterInspection(inspections.NewAnalyzerInspection(
		id,
		languages,
		source,
		codes,
		analyzers...,
	))
}
