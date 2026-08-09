package app

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/callhierarchy"
	"github.com/shopware/shopware-lsp/internal/lsp/color"
	"github.com/shopware/shopware-lsp/internal/lsp/documentlink"
	"github.com/shopware/shopware-lsp/internal/lsp/folding"
	"github.com/shopware/shopware-lsp/internal/lsp/highlight"
	"github.com/shopware/shopware-lsp/internal/lsp/linkedediting"
	"github.com/shopware/shopware-lsp/internal/lsp/selection"
	"github.com/shopware/shopware-lsp/internal/lsp/symbol"
)

func registerSymbolAndDocumentProviders(server *lsp.Server, services workspaceServices) {
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
	dalSymbols := symbol.NewDALWorkspaceSymbolProvider(services.dal)
	if server.DomainEnabled("administration") {
		adminSymbols := symbol.NewAdminWorkspaceSymbolProvider(services.admin)
		server.RegisterWorkspaceSymbolProvider(
			symbol.NewCatalogWorkspaceSymbolProvider(
				services.symbols, symfonySymbols, adminSymbols, dalSymbols,
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
	} else {
		server.RegisterWorkspaceSymbolProvider(
			symbol.NewCatalogWorkspaceSymbolProvider(
				services.symbols, symfonySymbols, dalSymbols,
			),
		)
	}
	if server.DomainEnabled("scss") {
		server.RegisterDocumentColorProvider(color.NewAdminSCSSColorProvider())
	}
	server.RegisterDocumentLinkProvider(documentlink.NewRelatedProvider(
		services.twig,
		services.configuration,
		services.php,
	))
}
