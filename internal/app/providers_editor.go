package app

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	lspformatting "github.com/shopware/shopware-lsp/internal/lsp/formatting"
	"github.com/shopware/shopware-lsp/internal/lsp/inlay"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/lsp/refactor"
	lspsemantic "github.com/shopware/shopware-lsp/internal/lsp/semantic"
	"github.com/shopware/shopware-lsp/internal/lsp/signature"
)

func registerEditorProviders(server *lsp.Server, phpFeatures *phpsemantic.Provider, services workspaceServices) {
	server.RegisterDocumentFormattingProvider(lspformatting.NewTwigProvider())
	server.RegisterSignatureHelpProvider(signature.NewTwigMacroSignatureProvider(
		services.twig,
	))
	if server.DomainEnabled("administration") {
		server.RegisterSignatureHelpProvider(
			signature.NewAdminSignatureProvider(services.admin),
		)
	}
	if !server.FrameworkPresentation() {
		server.RegisterSignatureHelpProvider(phpFeatures)
		server.RegisterRenameProvider(refactor.NewPHPTwigRenameProvider(
			phpFeatures,
			services.twig,
			services.php,
		))
	}
	if server.DomainEnabled("administration") {
		server.RegisterRenameProvider(
			refactor.NewAdminRenameProvider(services.admin),
		)
	}
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
	if server.DomainEnabled("administration") {
		server.RegisterInlayHintProvider(inlay.NewAdminParameterProvider(
			services.admin,
		))
	}
	server.RegisterSemanticTokensProvider(
		lspsemantic.NewTwigUXToolkitProvider(),
	)
	if server.DomainEnabled("administration") {
		server.RegisterSemanticTokensProvider(
			lspsemantic.NewAdminMarkupProvider(services.admin),
		)
	}
	if !server.FrameworkPresentation() {
		server.RegisterSemanticTokensProvider(
			lspsemantic.NewEmbeddedLanguageProvider(services.php),
		)
	}
}
