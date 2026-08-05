// Package twigparser provides the in-process Twig parser used by the language
// server. It is lossless, error-tolerant, and implemented entirely in Go.
//
// The language-independent building blocks live in cst and parsekit. New
// languages should register their own kind range, provide a lexer that emits
// parsekit.Token values, and drive parsekit.Parser from their grammar.
package twigparser

import "github.com/shopware/shopware-lsp/internal/parser/twig/parser"

// Parse parses Twig and HTML source into a lossless concrete syntax tree.
// Parsing is total: malformed input is represented by error nodes and
// diagnostics while the returned tree still reproduces the full source.
func Parse(source string) parser.Result {
	return parser.Parse(source)
}
