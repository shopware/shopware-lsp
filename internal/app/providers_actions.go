package app

import (
	"github.com/shopware/shopware-lsp/internal/analytics"
	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/codeaction"
	lspintegration "github.com/shopware/shopware-lsp/internal/lsp/integration"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func registerActionAndCommandProviders(server *lsp.Server, root string, versioning *twig.VersioningService, services workspaceServices) {
	server.RegisterActionProvider(codeaction.NewSnippetCodeActionProvider(services.snippets))
	server.RegisterActionProvider(codeaction.NewSnippetCopyCodeActionProvider())
	server.RegisterActionProvider(codeaction.NewTwigCodeActionProvider(versioning))
	var adminTwigOverride *codeaction.AdminTwigOverrideProvider
	if server.DomainEnabled("administration") {
		server.RegisterActionProvider(codeaction.NewAdminContextProvider())
		adminTwigOverride = codeaction.NewAdminTwigOverrideProvider(
			services.admin,
			services.extensions,
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
	server.RegisterCommandProvider(lspintegration.NewProvider())
	server.RegisterCommandProvider(extension.NewExtensionCommandProvider(services.extensions))
	server.RegisterCommandProvider(twig.NewTwigCommandProvider(root, services.extensions, versioning))
	server.RegisterCommandProvider(symfonyGenerators)
	server.RegisterCommandProvider(formFieldGenerator)
	server.RegisterCommandProvider(twigFormFieldGenerator)
	server.RegisterCommandProvider(twigTemplateGenerator)
	server.RegisterCommandProvider(twigTranslationExtractor)
	if adminTwigOverride != nil {
		server.RegisterCommandProvider(adminTwigOverride)
	}
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
		services.dal,
	))
}
