// Package formatter formats Twig/HTML documents from the native lossless Twig
// CST. Its rendering rules are adapted from shopware-cli's internal HTML
// formatter, while parsing remains owned by internal/parser/twig.
package formatter

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
)

// Options controls indentation and the Shopware template dialect.
type Options struct {
	InsertSpaces            bool
	TabSize                 int
	TwigBlockIndentChildren bool
}

// Format parses source with the native Twig parser and formats its lossless
// CST. LSP callers should prefer FormatTree so the current editor snapshot is
// not parsed twice.
func Format(source string, options Options) (string, error) {
	result := twigparser.Parse(source)
	return FormatTree(result.Tree, options)
}

// FormatTree formats an immutable native Twig CST.
func FormatTree(tree *cst.Tree, options Options) (string, error) {
	if tree == nil || tree.Root == nil {
		return "", fmt.Errorf("format twig: missing syntax tree")
	}

	config := defaultIndentConfig()
	config.SpaceIndent = options.InsertSpaces
	if options.TabSize > 0 {
		config.IndentSize = options.TabSize
	}
	config.TwigBlockIndentChildren = options.TwigBlockIndentChildren

	nodes := converter{}.nodeList(tree.Root)
	return (&renderer{config: config}).render(nodes), nil
}
