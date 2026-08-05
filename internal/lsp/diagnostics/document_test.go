package diagnostics

import "github.com/shopware/shopware-lsp/internal/lsp"

func diagnosticsDocument(uri string, content []byte) *lsp.TextDocument {
	return lsp.NewTextDocument(uri, string(content), 1)
}
