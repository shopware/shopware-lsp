// Package twigfmt formats Twig/HTML documents from the native lossless Twig
// CST. Its rendering rules are adapted from shopware-cli's internal HTML
// formatter, while parsing remains owned by internal/parser/twig.
package twigfmt

import (
	"fmt"
	"sync"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
)

// Options controls indentation and the Shopware template dialect.
type Options struct {
	InsertSpaces            bool
	TabSize                 int
	TwigBlockIndentChildren bool
}

var formatMu sync.Mutex

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

	config := DefaultIndentConfig()
	config.SpaceIndent = options.InsertSpaces
	if options.TabSize > 0 {
		config.IndentSize = options.TabSize
	}
	config.TwigBlockIndentChildren = options.TwigBlockIndentChildren

	nodes := converter{}.nodeList(tree.Root)
	formatMu.Lock()
	defer formatMu.Unlock()
	oldConfig := indentConfig
	defer func() { indentConfig = oldConfig }()
	indentConfig = config
	return nodes.Dump(0), nil
}
