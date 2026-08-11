package app

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/hover"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func registerHoverProviders(server *lsp.Server, root string, phpFeatures *phpsemantic.Provider, services workspaceServices) *twig.VersioningService {
	if !server.FrameworkPresentation() {
		server.RegisterHoverProvider(phpFeatures)
	}
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
	versioning := services.twigVersioning
	if !server.DomainEnabled("shopware.twigVersioning") {
		versioning = nil
	} else {
		server.RegisterHoverProvider(hover.NewTwigVersioningHoverProvider(versioning))
	}
	if server.DomainEnabled("administration") {
		server.RegisterHoverProvider(hover.NewAdminHoverProvider(root, services.admin))
	}
	return versioning
}
