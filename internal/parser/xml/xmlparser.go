// Package xmlparser provides the lossless, error-tolerant XML parser used by
// the language server.
package xmlparser

import "github.com/shopware/shopware-lsp/internal/parser/xml/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
