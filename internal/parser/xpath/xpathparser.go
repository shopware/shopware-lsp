// Package xpathparser provides the lossless, error-tolerant XPath parser used
// for Symfony DomCrawler embedded expressions.
package xpathparser

import "github.com/shopware/shopware-lsp/internal/parser/xpath/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
