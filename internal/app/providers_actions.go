package app

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/analytics"
	"github.com/shopware/shopware-lsp/internal/lsp/codeaction"
	"github.com/shopware/shopware-lsp/internal/lsp/commands"
	lspintegration "github.com/shopware/shopware-lsp/internal/lsp/integration"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func registerActionAndCommandProviders(server *lsp.Server, root string, versioning *twig.VersioningService, services workspaceServices) {
	if !server.FrameworkPresentation() && server.DomainEnabled("php") {
		server.RegisterActionProvider(codeaction.NewPHPImportsProvider(services.php))
		server.RegisterActionProvider(codeaction.NewPHPExtractVariableProvider(services.php))
		server.RegisterActionProvider(codeaction.NewPHPExtractMethodProvider(services.php))
	}
	server.RegisterActionProvider(codeaction.NewSnippetCodeActionProvider(services.snippets))
	server.RegisterActionProvider(codeaction.NewSnippetCopyCodeActionProvider())
	server.RegisterActionProvider(codeaction.NewTwigCodeActionProvider(versioning))
	var adminTwigOverride *codeaction.AdminTwigOverrideProvider
	if server.DomainEnabled("administration") {
		server.RegisterActionProvider(codeaction.NewAdminContextProvider())
		adminTwigOverride = codeaction.NewAdminTwigOverrideProvider(
			services.admin,
			services.extensions,
			server,
		)
		server.RegisterActionProvider(adminTwigOverride)
	}
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
	twigTranslationExtractor := codeaction.NewTwigTranslationExtractProvider(
		services.translations,
		server,
	)
	server.RegisterActionProvider(twigTranslationExtractor)

	server.RegisterCommandProvider(commands.NewSnippetCommandProvider(services.snippets, server))
	server.RegisterCommandProvider(lspintegration.NewProvider())
	server.RegisterCommandProvider(commands.NewExtensionCommandProvider(services.extensions))
	server.RegisterCommandProvider(commands.NewTwigCommandProvider(root, services.extensions, versioning, server))
	server.RegisterCommandProvider(symfonyGenerators)
	server.RegisterCommandProvider(twigTemplateGenerator)
	server.RegisterCommandProvider(twigTranslationExtractor)
	if adminTwigOverride != nil {
		server.RegisterCommandProvider(adminTwigOverride)
	}
	server.RegisterCommandProvider(commands.NewConsoleCatalogProvider(
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
	entityScaffolds := scaffold.NewProvider(
		root,
		services.php,
		services.console,
		services.dal,
	)
	entityScaffolds.SetEntityCatalog(entityschema.NewIndexedCatalog(
		services.php,
		services.entitySchemaSources,
		services.symbols,
	))
	server.RegisterCommandProvider(entityScaffolds)
}
