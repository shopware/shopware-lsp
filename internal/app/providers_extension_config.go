package app

import (
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func registerExtensionConfigDocumentObserver(server *lsp.Server, compliance *extension.ConfigIndex) {
	if compliance == nil {
		return
	}
	compliance.SetOnChange(func(configuration string) {
		server.RefreshOpenDocumentDiagnostics(func(document *lsp.TextDocument) bool {
			path, err := uriutil.Path(document.URI)
			root, _ := extension.ConfigurationRoot(configuration)
			return err == nil && filepath.Base(path) == "composer.json" && (configuration == "" || filepath.Dir(path) == root)
		})
	})
	server.RegisterDocumentObserver(lsp.DocumentObserver{
		DidOpenOrChange: func(document *lsp.TextDocument) {
			if server.InitializationOptions().CLIMode {
				return
			}
			path, err := uriutil.Path(document.URI)
			if err != nil || !compliance.IsConfiguration(path) {
				return
			}
			var root *cst.Node
			if document.SyntaxTree != nil {
				root = document.SyntaxTree.Root
			}
			compliance.UpdateLiveDocument(path, root)
		},
		DidClose: func(uri string) {
			path, err := uriutil.Path(uri)
			if err == nil && compliance.IsConfiguration(path) {
				compliance.RemoveLiveDocument(path)
			}
		},
	})
}
