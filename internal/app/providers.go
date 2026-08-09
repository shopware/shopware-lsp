package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
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
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/shopware"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/style"
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
	styles          *style.Index
	php             *php.PHPIndex
	twig            *twig.TwigIndexer
	twigVersioning  *twig.VersioningService
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
	if server.DomainEnabled("administration") {
		registerAdministrationDocumentObserver(server, services.admin)
	}
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

	registerSymbolAndDocumentProviders(server, services)
	registerCompletionProviders(server, root, phpFeatures, services)
	registerDefinitionProviders(server, root, phpFeatures, services)
	registerCodeLensProviders(server, root, services)
	registerReferenceProviders(server, phpFeatures, services)
	registerDiagnosticInspections(server, root, phpFeatures, services)
	versioning := registerHoverProviders(server, root, phpFeatures, services)
	registerEditorProviders(server, phpFeatures, services)
	registerActionAndCommandProviders(server, root, versioning, services)
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
