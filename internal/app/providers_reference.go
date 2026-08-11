package app

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/lsp/refactor"
	"github.com/shopware/shopware-lsp/internal/lsp/reference"
)

func registerReferenceProviders(server *lsp.Server, phpFeatures *phpsemantic.Provider, services workspaceServices) {
	if !server.FrameworkPresentation() {
		server.RegisterReferencesProvider(phpFeatures)
	}
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
	if server.DomainEnabled("scss") {
		server.RegisterReferencesProvider(
			reference.NewStyleClassReferenceProvider(services.styles),
		)
	}
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
	if server.DomainEnabled("administration") {
		server.RegisterReferencesProvider(
			reference.NewAdminReferenceProvider(services.admin),
		)
	}
	server.RegisterFileRenameProvider(
		refactor.NewTwigTemplateRenameProvider(services.twig),
	)
}
