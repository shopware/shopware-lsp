package twig

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// RedundantBlockOverride returns the nearest resolved parent when the current
// block is an exact copy. Fallback candidates are deliberately excluded: they
// may share a view path without participating in the template's extends chain.
func RedundantBlockOverride(
	block TwigBlock,
	resolution UpstreamResolution,
) (TwigBlockHash, bool) {
	if !resolution.ParentResolved || len(resolution.Candidates) == 0 ||
		block.Hash == "" || block.Text == "" {
		return TwigBlockHash{}, false
	}
	parent := resolution.Candidates[0]
	if parent.Hash != block.Hash || parent.Text != block.Text {
		return TwigBlockHash{}, false
	}
	for _, path := range resolution.ChainPaths {
		if samePath(parent.AbsolutePath, path) {
			return parent, true
		}
	}
	return TwigBlockHash{}, false
}

// BlockBodySnapshot identifies the exact source owned by a block's CST body.
type BlockBodySnapshot struct {
	Text  string
	Range cst.TextRange
}

// BlockBodies collects block bodies in one tree traversal, keyed by each
// declaration's trimmed range.
func BlockBodies(
	root *twigsyntax.Node,
	source string,
) map[cst.TextRange]BlockBodySnapshot {
	bodies := make(map[cst.TextRange]BlockBodySnapshot)
	if root == nil {
		return bodies
	}
	for element := range root.Descendants() {
		node, ok := element.(*twigsyntax.Node)
		if !ok {
			continue
		}
		block, ok := twigast.CastTwigBlock(node)
		if !ok {
			continue
		}
		body, ok := block.Body()
		if !ok {
			continue
		}
		rng := body.Syntax().Range()
		if rng.Start > rng.End || rng.End > uint32(len(source)) {
			continue
		}
		bodies[node.RangeTrimmedTrivia()] = BlockBodySnapshot{
			Text: source[rng.Start:rng.End], Range: rng,
		}
	}
	return bodies
}

// ParseBlockBody returns the body range relative to one persisted block.
func ParseBlockBody(blockText string) (string, cst.TextRange, bool) {
	result := twigparser.Parse(blockText)
	body, ok := BlockBodies(
		result.Tree.Root,
		blockText,
	)[cst.TextRange{End: uint32(len(blockText))}]
	return body.Text, body.Range, ok
}

// IsParentDelegation reports whether the body is the canonical delegation
// emitted by the redundant-block quick fix.
func IsParentDelegation(body string) bool {
	return strings.TrimSpace(body) == "{{ parent() }}"
}
