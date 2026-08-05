package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/twig/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// Result is the outcome of Parse: a lossless syntax tree plus a flat list of
// parse errors. Parse is total — for ANY input it returns a usable tree
// (possibly containing ERROR nodes and TK_UNKNOWN tokens); Tree.Root.Text()
// always equals source.
type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

// Parse lexes source, runs the grammar over the trivia-skipping engine producing
// events + errors, then replays the events through the sink into a lossless CST.
func Parse(source string) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/4),
	)
	p := newOwnedParser(tokens)
	root(p)
	tree, errs := p.Finish(source)
	return Result{Tree: tree, Errors: errs}
}
