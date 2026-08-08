package hover

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigVersioningHoverProvider struct {
	versioning *twig.VersioningService
}

func NewTwigVersioningHoverProvider(
	versioning *twig.VersioningService,
) *TwigVersioningHoverProvider {
	return &TwigVersioningHoverProvider{versioning: versioning}
}

func (p *TwigVersioningHoverProvider) GetHover(
	ctx context.Context,
	params *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if params == nil || !strings.HasSuffix(strings.ToLower(params.TextDocument.URI), ".twig") ||
		params.Node == nil || p == nil || p.versioning == nil {
		return nil, nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	file, err := twig.ParseTwigTree(path, params.DocumentTree, params.LineIndex)
	if err != nil {
		return nil, err
	}
	if block := p.blockAtIdentifier(params.Node, params.Token); block != nil {
		return p.hoverBlock(*file, twigquery.BlockName(block))
	}
	comment := p.versionCommentAt(params.Node)
	if comment == nil {
		return nil, nil
	}
	blockName := p.findBlockNameAfterComment(comment)
	if blockName == "" {
		return nil, nil
	}
	return p.hoverBlock(*file, blockName)
}

func (p *TwigVersioningHoverProvider) blockAtIdentifier(
	node *twigsyntax.Node,
	token *twigsyntax.Token,
) *twigsyntax.Node {
	block := twigquery.BlockAt(node)
	if block == nil || token == nil || token.Text() != twigquery.BlockName(block) {
		return nil
	}
	return block
}

func (p *TwigVersioningHoverProvider) versionCommentAt(
	node *twigsyntax.Node,
) *twigsyntax.Node {
	comment := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigComment)
	if comment == nil || !strings.Contains(comment.Text(), twig.VersionCommentPrefix) {
		return nil
	}
	return comment
}

func (p *TwigVersioningHoverProvider) hoverBlock(
	file twig.TwigFile,
	blockName string,
) (*protocol.Hover, error) {
	block, found := file.Blocks[blockName]
	if !found {
		return nil, nil
	}
	resolution, err := p.versioning.Resolve(file, blockName)
	if err != nil {
		return nil, err
	}
	var markdown strings.Builder
	fmt.Fprintf(&markdown, "**Block:** `%s`\n\n", blockName)
	if len(resolution.Candidates) == 0 {
		if block.HasVersioningComment && resolution.ParentResolved {
			markdown.WriteString("**Status:** Upstream block was removed\n\n")
		} else {
			markdown.WriteString("**Status:** No resolvable upstream block\n\n")
		}
		return twigVersioningHover(markdown.String()), nil
	}
	upstream := resolution.Candidates[0]
	fmt.Fprintf(&markdown, "**Upstream:** `%s`\n\n", upstream.AbsolutePath)
	fmt.Fprintf(&markdown, "**Upstream hash:** `%s`\n\n", upstream.Hash)
	if version := p.versioning.VersionForPath(upstream.AbsolutePath); version != "" {
		fmt.Fprintf(&markdown, "**Upstream version:** `%s`\n\n", version)
	}
	if len(resolution.Candidates) > 1 {
		fmt.Fprintf(&markdown, "**Candidates:** %d compatible upstream blocks\n\n", len(resolution.Candidates))
	}
	if block.VersionComment == nil {
		if block.HasVersioningComment {
			markdown.WriteString("**Status:** Version comment is malformed\n\n")
		} else {
			markdown.WriteString("**Status:** No version comment\n\n")
		}
		return twigVersioningHover(markdown.String()), nil
	}
	fmt.Fprintf(&markdown, "**Recorded hash:** `%s`\n\n", block.VersionComment.Hash)
	if block.VersionComment.Version != "" {
		fmt.Fprintf(&markdown, "**Recorded version:** `%s`\n\n", block.VersionComment.Version)
	}
	for _, candidate := range resolution.Candidates {
		if candidate.Hash == block.VersionComment.Hash {
			markdown.WriteString("**Status:** Version comment is up to date\n\n")
			return twigVersioningHover(markdown.String()), nil
		}
	}
	markdown.WriteString("**Status:** Upstream block has changed\n\n")
	return twigVersioningHover(markdown.String()), nil
}

func twigVersioningHover(value string) *protocol.Hover {
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}
}

func (p *TwigVersioningHoverProvider) findBlockNameAfterComment(
	commentNode *twigsyntax.Node,
) string {
	for sibling := commentNode.NextSibling(); sibling != nil; {
		switch next := sibling.(type) {
		case *twigsyntax.Token:
			sibling = next.NextSibling()
		case *twigsyntax.Node:
			if next.Kind() == twigsyntax.TwigBlock {
				return twigquery.BlockName(next)
			}
			sibling = next.NextSibling()
		}
	}
	return ""
}
