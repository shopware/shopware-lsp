// Package jsonparser provides the lossless, error-tolerant JSON parser used by
// the language server.
package jsonparser

import "github.com/shopware/shopware-lsp/internal/parser/json/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
