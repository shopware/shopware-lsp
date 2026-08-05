package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/analytics"
	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/feature"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/callhierarchy"
	"github.com/shopware/shopware-lsp/internal/lsp/codeaction"
	"github.com/shopware/shopware-lsp/internal/lsp/codelens"
	"github.com/shopware/shopware-lsp/internal/lsp/color"
	"github.com/shopware/shopware-lsp/internal/lsp/completion"
	"github.com/shopware/shopware-lsp/internal/lsp/definition"
	"github.com/shopware/shopware-lsp/internal/lsp/documentlink"
	"github.com/shopware/shopware-lsp/internal/lsp/folding"
	"github.com/shopware/shopware-lsp/internal/lsp/highlight"
	"github.com/shopware/shopware-lsp/internal/lsp/hover"
	"github.com/shopware/shopware-lsp/internal/lsp/inlay"
	"github.com/shopware/shopware-lsp/internal/lsp/linkedediting"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/lsp/refactor"
	"github.com/shopware/shopware-lsp/internal/lsp/reference"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/lsp/selection"
	lspsemantic "github.com/shopware/shopware-lsp/internal/lsp/semantic"
	"github.com/shopware/shopware-lsp/internal/lsp/signature"
	"github.com/shopware/shopware-lsp/internal/lsp/symbol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/shopware"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/systemconfig"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type workspaceServices struct {
	symbols         *indexer.WorkspaceSymbolCatalog
	services        *symfony.ServiceIndex
	routes          *symfony.RouteIndexer
	routeUsage      *symfony.RouteUsageIndexer
	console         *console.Index
	doctrine        *doctrine.Index
	assets          *asset.Index
	events          *event.Index
	messenger       *messenger.Index
	environment     *environment.Index
	forms           *form.Index
	security        *security.Index
	configuration   *symfonyconfig.Index
	serializer      *serializer.Index
	stimulus        *stimulus.Index
	php             *php.PHPIndex
	twig            *twig.TwigIndexer
	twigComponents  *twigcomponent.Index
	snippets        *snippet.SnippetIndexer
	translations    *translation.Index
	features        *feature.FeatureIndexer
	systemConfig    *systemconfig.SystemConfigIndexer
	theme           *theme.ThemeConfigIndexer
	extensions      *extension.ExtensionIndexer
	admin           *admin.AdminComponentIndexer
	dal             *shopwaredal.Index
	appScripts      *appscript.Index
	shopwareVersion shopware.ResolvedVersion
}

// registerFeatures is the adapter layer from domain repositories to LSP
// capabilities. Construction stays in workspace.go; protocol wiring stays here.
func registerFeatures(server *lsp.Server, root string, services workspaceServices) {
	registerAdministrationDocumentObserver(server, services.admin)
	server.RegisterContextEnricher(language.PHP, func(ctx context.Context, syntax lsp.SyntaxContext) context.Context {
		path := ""
		version := 0
		if syntax.Document != nil {
			path, _ = uriutil.Path(syntax.Document.URI)
			version = syntax.Document.Version
		}
		return services.php.AddDocumentContext(ctx, path, version, syntax.Node, syntax.Root)
	})
	phpFeatures := phpsemantic.New(services.php)

	symfonySymbols := symbol.NewSymfonyWorkspaceSymbolProvider(
		services.services,
		services.routes,
		services.console,
		services.twig,
		services.doctrine,
		services.twigComponents,
		services.translations,
		services.php,
	)
	adminSymbols := symbol.NewAdminWorkspaceSymbolProvider(services.admin)
	dalSymbols := symbol.NewDALWorkspaceSymbolProvider(services.dal)
	server.RegisterWorkspaceSymbolProvider(
		symbol.NewCatalogWorkspaceSymbolProvider(
			services.symbols,
			symfonySymbols,
			adminSymbols,
			dalSymbols,
		),
	)
	server.RegisterDocumentSymbolProvider(
		symbol.NewAdminDocumentSymbolProvider(services.admin),
	)
	server.RegisterDocumentHighlightProvider(
		highlight.NewAdminDocumentHighlightProvider(services.admin),
	)
	server.RegisterCallHierarchyProvider(
		callhierarchy.NewAdminCallHierarchyProvider(services.admin),
	)
	server.RegisterLinkedEditingRangeProvider(
		linkedediting.NewAdminLinkedEditingProvider(),
	)
	server.RegisterFoldingRangeProvider(folding.NewAdminFoldingProvider())
	server.RegisterSelectionRangeProvider(selection.NewAdminSelectionRangeProvider())
	server.RegisterDocumentColorProvider(color.NewAdminSCSSColorProvider())
	server.RegisterDocumentLinkProvider(documentlink.NewRelatedProvider(
		services.twig,
		services.configuration,
		services.php,
	))

	server.RegisterCompletionProvider(phpFeatures)
	server.RegisterCompletionProvider(
		completion.NewPHPAttributeCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(
		completion.NewResponseConstantCompletionProvider(
			services.php,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewHttpClientCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(
		completion.NewContainerConstantCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(
		completion.NewConsoleHelperCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(completion.NewConsoleCompletionProvider(
		services.console,
	))
	server.RegisterCompletionProvider(completion.NewDoctrineCompletionProvider(
		services.doctrine,
		services.php,
	))
	server.RegisterCompletionProvider(completion.NewAssetCompletionProvider(
		services.assets,
		services.php,
	))
	server.RegisterCompletionProvider(
		completion.NewStimulusCompletionProvider(services.stimulus),
	)
	server.RegisterCompletionProvider(completion.NewEventCompletionProvider(
		services.events,
		services.php,
		services.services,
	))
	server.RegisterCompletionProvider(
		completion.NewMessengerCompletionProvider(
			services.php,
			services.messenger,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewEnvironmentCompletionProvider(
			services.environment,
		),
	)
	server.RegisterCompletionProvider(completion.NewFormCompletionProvider(
		services.forms,
		services.php,
	))
	server.RegisterCompletionProvider(completion.NewSecurityCompletionProvider(
		services.security,
	))
	server.RegisterCompletionProvider(
		completion.NewSymfonyConfigCompletionProvider(
			services.configuration,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewYAMLServiceAuthoringCompletionProvider(
			services.php.Project(),
		),
	)
	server.RegisterCompletionProvider(
		completion.NewYAMLRouteAuthoringCompletionProvider(services.routes),
	)
	server.RegisterCompletionProvider(completion.NewValidationCompletionProvider())
	server.RegisterCompletionProvider(completion.NewServiceCompletionProvider(services.services, services.php))
	server.RegisterCompletionProvider(completion.NewTranslationCompletionProvider(services.translations, services.php))
	server.RegisterCompletionProvider(completion.NewTwigMacroCompletionProvider(
		services.twig,
	))
	server.RegisterCompletionProvider(
		completion.NewTwigEnumCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigConstantCompletionProvider(
			services.php,
			services.twig,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigComponentCompletionProvider(
			services.twigComponents,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigComponentVariableCompletionProvider(
			services.twigComponents,
			services.php,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigComponentPHPCompletionProvider(),
	)
	server.RegisterCompletionProvider(
		completion.NewLiveComponentEventCompletionProvider(
			services.twigComponents,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigIncludeParameterCompletionProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterCompletionProvider(
		completion.NewTwigRenderBlockCompletionProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterCompletionProvider(completion.NewTwigCompletionProvider(
		root,
		services.twig,
		services.extensions,
		services.php,
	))
	server.RegisterCompletionProvider(
		completion.NewControllerCompletionProvider(
			services.php,
			services.services,
			services.routes,
		),
	)
	server.RegisterCompletionProvider(completion.NewRouteCompletionProvider(services.routes))
	server.RegisterCompletionProvider(
		completion.NewBundleResourceCompletionProvider(services.php),
	)
	server.RegisterCompletionProvider(completion.NewSnippetCompletionProvider(services.snippets))
	server.RegisterCompletionProvider(completion.NewFeatureCompletionProvider(services.features))
	server.RegisterCompletionProvider(completion.NewDALCompletionProvider(services.dal))
	server.RegisterCompletionProvider(completion.NewSystemConfigCompletion(services.systemConfig, services.php))
	server.RegisterCompletionProvider(completion.NewThemeCompletionProvider(services.theme))
	server.RegisterCompletionProvider(completion.NewAdminCompletionProvider(services.admin))

	server.RegisterDefinitionProvider(phpFeatures)
	server.RegisterImplementationProvider(phpFeatures)
	server.RegisterTypeHierarchyProvider(phpFeatures)
	server.RegisterDefinitionProvider(
		definition.NewContainerConstantDefinitionProvider(services.php),
	)
	server.RegisterDefinitionProvider(
		definition.NewHttpClientDefinitionProvider(services.php),
	)
	server.RegisterDefinitionProvider(
		definition.NewConsoleHelperDefinitionProvider(services.php),
	)
	server.RegisterDefinitionProvider(definition.NewConsoleDefinitionProvider(
		services.console,
	))
	server.RegisterDefinitionProvider(definition.NewDoctrineDefinitionProvider(
		services.doctrine,
		services.php,
	))
	server.RegisterDefinitionProvider(definition.NewAssetDefinitionProvider(
		services.assets,
		services.php,
	))
	server.RegisterDefinitionProvider(
		definition.NewStimulusDefinitionProvider(services.stimulus),
	)
	server.RegisterDefinitionProvider(definition.NewEventDefinitionProvider(
		services.events,
		services.php,
		services.services,
	))
	server.RegisterDefinitionProvider(
		definition.NewMessengerDefinitionProvider(
			services.php,
			services.messenger,
		),
	)
	server.RegisterDefinitionProvider(
		definition.NewEnvironmentDefinitionProvider(
			services.environment,
		),
	)
	server.RegisterDefinitionProvider(definition.NewFormDefinitionProvider(
		services.forms,
		services.php,
	))
	server.RegisterDefinitionProvider(definition.NewSecurityDefinitionProvider(
		services.security,
	))
	server.RegisterDefinitionProvider(
		definition.NewSymfonyConfigDefinitionProvider(
			services.configuration,
		),
	)
	server.RegisterDefinitionProvider(definition.NewSerializerDefinitionProvider(
		services.serializer,
		services.php,
	))
	server.RegisterDefinitionProvider(definition.NewValidationDefinitionProvider())
	server.RegisterDefinitionProvider(
		definition.NewTwigEnumDefinitionProvider(services.php),
	)
	server.RegisterDefinitionProvider(
		definition.NewTwigConstantDefinitionProvider(
			services.php,
			services.twig,
		),
	)
	server.RegisterDefinitionProvider(definition.NewServiceXMLDefinitionProvider(services.services, services.php))
	server.RegisterDefinitionProvider(definition.NewTwigMacroDefinitionProvider(
		services.twig,
	))
	server.RegisterDefinitionProvider(
		definition.NewTwigComponentDefinitionProvider(
			services.twigComponents,
			services.php,
		),
	)
	server.RegisterDefinitionProvider(
		definition.NewLiveComponentEventDefinitionProvider(
			services.twigComponents,
		),
	)
	server.RegisterDefinitionProvider(
		definition.NewTwigIncludeParameterDefinitionProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterDefinitionProvider(
		definition.NewTwigRenderBlockDefinitionProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterDefinitionProvider(definition.NewTwigDefinitionProvider(
		root,
		services.twig,
		services.extensions,
		services.php,
	))
	server.RegisterDefinitionProvider(definition.NewRouteDefinitionProvider(
		services.routes,
		services.php,
	))
	server.RegisterDefinitionProvider(definition.NewControllerDefinitionProvider(services.services, services.php))
	server.RegisterDefinitionProvider(definition.NewTranslationDefinitionProvider(services.translations, services.php))
	server.RegisterDefinitionProvider(definition.NewSnippetDefinitionProvider(services.snippets))
	server.RegisterDefinitionProvider(definition.NewFeatureDefinitionProvider(services.features))
	server.RegisterDefinitionProvider(definition.NewDALDefinitionProvider(services.dal))
	server.RegisterDefinitionProvider(definition.NewSystemConfigDefinitionProvider(services.systemConfig, services.php))
	server.RegisterDefinitionProvider(definition.NewThemeDefinitionProvider(services.theme))
	server.RegisterDefinitionProvider(definition.NewAdminDefinitionProvider(services.admin))

	server.RegisterCodeLensProvider(codelens.NewPHPCodeLensProvider(services.php, services.services))
	server.RegisterCodeLensProvider(
		codelens.NewAdminComponentCodeLensProvider(services.admin),
	)
	server.RegisterCodeLensProvider(
		codelens.NewSymfonyConfigCodeLensProvider(
			services.configuration,
		),
	)
	server.RegisterCodeLensProvider(
		codelens.NewRouteResourceCodeLensProvider(
			services.routes,
			services.php,
		),
	)
	server.RegisterCodeLensProvider(
		codelens.NewRouteEndpointCodeLensProvider(
			services.services,
			services.php,
		),
	)
	server.RegisterCodeLensProvider(
		codelens.NewConsoleCommandCodeLensProvider(root),
	)
	server.RegisterCodeLensProvider(codelens.NewSerializerCodeLensProvider(
		services.serializer,
		services.php,
	))
	server.RegisterCodeLensProvider(codelens.NewValidationCodeLensProvider(
		services.php,
		services.translations,
	))
	server.RegisterCodeLensProvider(codelens.NewTwigCodeLensProvider(services.twig))
	server.RegisterCodeLensProvider(
		codelens.NewTwigComponentRelatedCodeLensProvider(
			services.twigComponents,
			services.php,
		),
	)
	server.RegisterCodeLensProvider(
		codelens.NewRelatedNavigationCodeLensProvider(
			services.twig,
			services.php,
			services.routes,
			services.services,
		),
	)
	server.RegisterCodeLensProvider(
		codelens.NewControllerRelatedCodeLensProvider(
			services.routeUsage,
			services.services,
			services.php,
		),
	)
	server.RegisterCodeLensProvider(codelens.NewFormRelatedCodeLensProvider(
		services.forms,
		services.php,
	))
	server.RegisterCodeLensProvider(codelens.NewDoctrineRelatedCodeLensProvider(
		services.doctrine,
		services.php,
	))
	server.RegisterCodeLensProvider(
		codelens.NewViteCodeLensProvider(services.assets),
	)
	server.RegisterCodeLensProvider(
		codelens.NewMessengerCodeLensProvider(
			services.messenger,
			services.php,
		),
	)
	server.RegisterCodeLensProvider(codelens.NewServiceRelatedCodeLensProvider(
		services.services,
		services.php,
	))
	server.RegisterReferencesProvider(phpFeatures)
	server.RegisterReferencesProvider(reference.NewDoctrineReferenceProvider(
		services.doctrine,
		services.php,
	))
	server.RegisterReferencesProvider(
		reference.NewControllerReferenceProvider(
			services.routeUsage,
			services.services,
			services.php,
		),
	)
	server.RegisterReferencesProvider(reference.NewRouteReferenceProvider(services.routes, services.routeUsage))
	server.RegisterReferencesProvider(reference.NewEventReferenceProvider(
		services.events,
	))
	server.RegisterReferencesProvider(
		reference.NewMessengerReferenceProvider(
			services.messenger,
			services.php,
		),
	)
	server.RegisterReferencesProvider(
		reference.NewEnvironmentReferenceProvider(
			services.environment,
		),
	)
	server.RegisterReferencesProvider(reference.NewSecurityReferenceProvider(
		services.security,
	))
	server.RegisterReferencesProvider(reference.NewSerializerReferenceProvider(
		services.serializer,
		services.php,
	))
	server.RegisterReferencesProvider(reference.NewAssetReferenceProvider(
		services.assets,
		services.php,
	))
	server.RegisterReferencesProvider(
		reference.NewStimulusReferenceProvider(services.stimulus),
	)
	server.RegisterReferencesProvider(reference.NewTwigMacroReferenceProvider(
		services.twig,
	))
	server.RegisterReferencesProvider(reference.NewTwigTemplateReferenceProvider(
		services.twig,
	))
	server.RegisterReferencesProvider(
		reference.NewTwigConstantReferenceProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterReferencesProvider(
		reference.NewTwigPHPReferenceProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterReferencesProvider(
		reference.NewTwigComponentReferenceProvider(
			services.twigComponents,
			services.php,
		),
	)
	server.RegisterReferencesProvider(
		reference.NewLiveComponentEventReferenceProvider(
			services.twigComponents,
		),
	)
	server.RegisterReferencesProvider(
		reference.NewAdminReferenceProvider(services.admin),
	)
	server.RegisterFileRenameProvider(
		refactor.NewTwigTemplateRenameProvider(services.twig),
	)

	registerDiagnosticInspections(server, root, phpFeatures, services)

	server.RegisterHoverProvider(phpFeatures)
	server.RegisterHoverProvider(
		hover.NewHttpClientHoverProvider(services.php),
	)
	server.RegisterHoverProvider(
		hover.NewConsoleHelperHoverProvider(services.php),
	)
	server.RegisterHoverProvider(hover.NewConsoleHoverProvider(
		root,
		services.console,
	))
	server.RegisterHoverProvider(hover.NewDoctrineHoverProvider(
		services.doctrine,
		services.php,
	))
	server.RegisterHoverProvider(hover.NewAssetHoverProvider(
		root,
		services.assets,
		services.php,
	))
	server.RegisterHoverProvider(
		hover.NewStimulusHoverProvider(root, services.stimulus),
	)
	server.RegisterHoverProvider(hover.NewEventHoverProvider(
		root,
		services.events,
		services.php,
		services.services,
	))
	server.RegisterHoverProvider(
		hover.NewMessengerHoverProvider(
			services.php,
			services.messenger,
		),
	)
	server.RegisterHoverProvider(
		hover.NewEnvironmentHoverProvider(
			root,
			services.environment,
		),
	)
	server.RegisterHoverProvider(hover.NewFormHoverProvider(
		root,
		services.forms,
		services.php,
	))
	server.RegisterHoverProvider(hover.NewSecurityHoverProvider(
		root,
		services.security,
	))
	server.RegisterHoverProvider(hover.NewSerializerHoverProvider(
		services.serializer,
		services.php,
	))
	server.RegisterHoverProvider(hover.NewValidationHoverProvider())
	server.RegisterHoverProvider(
		hover.NewTwigEnumHoverProvider(services.php),
	)
	server.RegisterHoverProvider(
		hover.NewTwigConstantHoverProvider(
			services.php,
			services.twig,
		),
	)
	server.RegisterHoverProvider(hover.NewRouteHoverProvider(services.routes))
	server.RegisterHoverProvider(hover.NewControllerHoverProvider(
		services.services,
		services.php,
	))
	server.RegisterHoverProvider(hover.NewTranslationHoverProvider(root, services.translations, services.php))
	server.RegisterHoverProvider(hover.NewTwigMacroHoverProvider(
		root,
		services.twig,
	))
	server.RegisterHoverProvider(hover.NewTwigComponentHoverProvider(
		root,
		services.twigComponents,
	))
	server.RegisterHoverProvider(hover.NewLiveComponentEventHoverProvider(
		services.twigComponents,
	))
	server.RegisterHoverProvider(
		hover.NewTwigIncludeParameterHoverProvider(
			root,
			services.twig,
			services.php,
		),
	)
	server.RegisterHoverProvider(
		hover.NewTwigRenderBlockHoverProvider(
			root,
			services.twig,
			services.php,
		),
	)
	server.RegisterHoverProvider(hover.NewTwigHoverProvider(
		root,
		services.extensions,
		services.php,
		services.twig,
	))
	server.RegisterHoverProvider(hover.NewSnippetHoverProvider(root, services.snippets))
	server.RegisterHoverProvider(hover.NewDALHoverProvider(services.dal))
	server.RegisterHoverProvider(hover.NewTwigVersioningHoverProvider(services.twig))
	server.RegisterHoverProvider(hover.NewAdminHoverProvider(root, services.admin))
	server.RegisterSignatureHelpProvider(signature.NewTwigMacroSignatureProvider(
		services.twig,
	))
	server.RegisterSignatureHelpProvider(
		signature.NewAdminSignatureProvider(services.admin),
	)
	server.RegisterSignatureHelpProvider(phpFeatures)
	server.RegisterRenameProvider(refactor.NewPHPTwigRenameProvider(
		phpFeatures,
		services.twig,
		services.php,
	))
	server.RegisterRenameProvider(
		refactor.NewAdminRenameProvider(services.admin),
	)
	server.RegisterInlayHintProvider(inlay.NewServiceArgumentProvider(
		services.services,
		services.php,
	))
	server.RegisterInlayHintProvider(inlay.NewRouteControllerProvider(
		services.services,
		services.php,
	))
	server.RegisterInlayHintProvider(inlay.NewRoutePathProvider(
		services.routes,
		services.php,
	))
	server.RegisterInlayHintProvider(inlay.NewTwigVariableProvider(
		services.php,
	))
	server.RegisterInlayHintProvider(inlay.NewSnippetPreviewProvider(
		services.snippets,
	))
	server.RegisterInlayHintProvider(inlay.NewPHPUnitProviderProvider(
		services.php,
	))
	server.RegisterInlayHintProvider(inlay.NewAdminParameterProvider(
		services.admin,
	))
	server.RegisterSemanticTokensProvider(
		lspsemantic.NewTwigUXToolkitProvider(),
	)
	server.RegisterSemanticTokensProvider(
		lspsemantic.NewAdminMarkupProvider(services.admin),
	)
	server.RegisterSemanticTokensProvider(
		lspsemantic.NewEmbeddedLanguageProvider(services.php),
	)

	server.RegisterActionProvider(codeaction.NewSnippetCodeActionProvider(services.snippets))
	server.RegisterActionProvider(codeaction.NewSnippetCopyCodeActionProvider())
	server.RegisterActionProvider(codeaction.NewTwigCodeActionProvider(root, services.twig))
	server.RegisterActionProvider(codeaction.NewAdminContextProvider())
	server.RegisterActionProvider(
		codeaction.NewEventListenerContextProvider(services.php),
	)
	server.RegisterActionProvider(
		codeaction.NewServiceTagCodeActionProvider(services.php),
	)
	server.RegisterActionProvider(
		codeaction.NewXMLServiceSuggestionCodeActionProvider(
			services.services,
			services.php,
		),
	)
	server.RegisterActionProvider(
		codeaction.NewPropertyServiceCodeActionProvider(
			services.php,
			services.services,
		),
	)
	symfonyGenerators := codeaction.NewSymfonyGeneratorProvider(
		services.php,
		services.services,
	)
	server.RegisterActionProvider(symfonyGenerators)
	formFieldGenerator := codeaction.NewFormFieldGeneratorProvider(
		services.forms,
		services.php,
		services.doctrine,
	)
	server.RegisterActionProvider(formFieldGenerator)
	twigFormFieldGenerator := codeaction.NewTwigFormFieldGeneratorProvider(
		services.forms,
		services.php,
	)
	server.RegisterActionProvider(twigFormFieldGenerator)
	twigTemplateGenerator := codeaction.NewTwigTemplateGeneratorProvider(
		services.twig,
	)
	server.RegisterActionProvider(twigTemplateGenerator)
	server.RegisterActionProvider(
		codeaction.NewInvokableCommandMigrationCodeActionProvider(
			services.php,
		),
	)
	server.RegisterActionProvider(
		codeaction.NewCommandInvokeParameterCodeActionProvider(services.php),
	)
	server.RegisterActionProvider(
		codeaction.NewRouteAttributeCodeActionProvider(services.php),
	)
	server.RegisterActionProvider(
		codeaction.NewRouteActionParameterCodeActionProvider(),
	)
	server.RegisterActionProvider(
		codeaction.NewTwigExtensionAttributeCodeActionProvider(services.php),
	)
	server.RegisterActionProvider(
		codeaction.NewDoctrineClassConstantCodeActionProvider(
			services.doctrine,
			services.php,
		),
	)
	twigTranslationExtractor := codeaction.NewTwigTranslationExtractProvider(
		services.translations,
	)
	server.RegisterActionProvider(twigTranslationExtractor)

	server.RegisterCommandProvider(snippet.NewSnippetCommandProvider(services.snippets, server))
	server.RegisterCommandProvider(extension.NewExtensionCommandProvider(services.extensions))
	server.RegisterCommandProvider(twig.NewTwigCommandProvider(root, services.extensions, services.twig))
	server.RegisterCommandProvider(symfonyGenerators)
	server.RegisterCommandProvider(formFieldGenerator)
	server.RegisterCommandProvider(twigFormFieldGenerator)
	server.RegisterCommandProvider(twigTemplateGenerator)
	server.RegisterCommandProvider(twigTranslationExtractor)
	server.RegisterCommandProvider(console.NewCatalogProvider(
		services.console,
		root,
	))
	server.RegisterCommandProvider(analytics.NewRouteCatalogProvider(
		root,
		services.routes,
		services.services,
		services.php,
		services.twig,
	))
	server.RegisterCommandProvider(analytics.NewDoctrineCatalogProvider(
		root,
		services.doctrine,
	))
	server.RegisterCommandProvider(analytics.NewFormCatalogProvider(
		root,
		services.forms,
		services.php,
	))
	server.RegisterCommandProvider(analytics.NewServiceLocatorProvider(
		services.services,
		services.php,
	))
	server.RegisterCommandProvider(
		analytics.NewTwigExtensionCatalogProvider(
			services.twig,
			services.php,
		),
	)
	server.RegisterCommandProvider(
		analytics.NewTwigTemplateUsageCatalogProvider(
			root,
			services.twig,
			services.php,
			services.routes,
			services.services,
			services.twigComponents,
		),
	)
	server.RegisterCommandProvider(
		analytics.NewTwigComponentCatalogProvider(
			services.twigComponents,
		),
	)
	server.RegisterCommandProvider(
		analytics.NewTwigTemplateVariableCatalogProvider(
			root,
			services.twig,
			services.php,
			services.twigComponents,
		),
	)
	server.RegisterCommandProvider(
		analytics.NewProfilerCatalogProvider(
			root,
			services.php,
			services.twig,
		),
	)
	server.RegisterCommandProvider(scaffold.NewProvider(
		root,
		services.php,
		services.console,
	))
}

func registerAdministrationDocumentObserver(
	server *lsp.Server,
	adminIndex *admin.AdminComponentIndexer,
) {
	if server == nil || adminIndex == nil {
		return
	}
	isAdministrationPath := func(path string) bool {
		normalized := filepath.ToSlash(filepath.Clean(path))
		return strings.Contains(
			normalized,
			"/Resources/app/administration/src/",
		)
	}
	refreshAdministrationDiagnostics := func() {
		server.RefreshOpenDocumentDiagnostics(
			func(document *lsp.TextDocument) bool {
				if document == nil {
					return false
				}
				path, err := uriutil.Path(document.URI)
				if err != nil {
					return false
				}
				return isAdministrationPath(path)
			},
		)
	}
	server.RegisterDocumentObserver(lsp.DocumentObserver{
		DidOpenOrChange: func(document *lsp.TextDocument) {
			// CLI checks run only after the scanner has brought the on-disk index
			// current. Publishing identical editor overlays for every checked file
			// invalidates Administration caches and makes concurrent checks copy the
			// entire component registry repeatedly.
			if document == nil || server.InitializationOptions().CLIMode {
				return
			}
			path, err := uriutil.Path(document.URI)
			if err != nil || !isAdministrationPath(path) {
				return
			}
			var root *cst.Node
			if document.SyntaxTree != nil {
				root = document.SyntaxTree.Root
			}
			adminIndex.UpdateLiveDocument(
				path, root, document.Source, document.LineIndex,
			)
			refreshAdministrationDiagnostics()
		},
		DidClose: func(uri string) {
			if server.InitializationOptions().CLIMode {
				return
			}
			path, err := uriutil.Path(uri)
			if err == nil && isAdministrationPath(path) {
				adminIndex.RemoveLiveDocument(path)
				refreshAdministrationDiagnostics()
			}
		},
	})
}
