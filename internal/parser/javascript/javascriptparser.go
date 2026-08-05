// Package javascriptparser provides the shared lossless JavaScript/TypeScript
// parser used by the language server.
package javascriptparser

import "github.com/shopware/shopware-lsp/internal/parser/javascript/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
