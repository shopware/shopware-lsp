// Package scssparser provides the lossless, error-tolerant SCSS parser used by
// the language server.
package scssparser

import "github.com/shopware/shopware-lsp/internal/parser/scss/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
