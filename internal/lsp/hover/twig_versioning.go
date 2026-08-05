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
)

type TwigVersioningHoverProvider struct {
	twigIndexer *twig.TwigIndexer
}

func NewTwigVersioningHoverProvider(twigIndexer *twig.TwigIndexer) *TwigVersioningHoverProvider {
	return &TwigVersioningHoverProvider{twigIndexer: twigIndexer}
}

func (p *TwigVersioningHoverProvider) GetHover(ctx context.Context, params *lsp.HoverRequest) (*protocol.Hover, error) {
	if !strings.HasSuffix(strings.ToLower(params.TextDocument.URI), ".twig") {
		return nil, nil
	}

	if params.Node == nil {
		return nil, nil
	}

	if p.twigIndexer == nil {
		return nil, nil
	}

	if block := p.blockAtIdentifier(params.Node, params.Token); block != nil {
		return p.hoverBlockIdentifier(twigquery.BlockName(block), params.TextDocument.URI)
	}

	if comment := p.versionCommentAt(params.Node); comment != nil {
		return p.hoverVersionComment(
			comment,
			string(params.DocumentContent),
			params.TextDocument.URI,
			params.LineIndex,
		)
	}

	return nil, nil
}

func (p *TwigVersioningHoverProvider) blockAtIdentifier(node *twigsyntax.Node, token *twigsyntax.Token) *twigsyntax.Node {
	block := twigquery.BlockAt(node)
	if block == nil || token == nil || token.Text() != twigquery.BlockName(block) {
		return nil
	}
	return block
}

func (p *TwigVersioningHoverProvider) versionCommentAt(node *twigsyntax.Node) *twigsyntax.Node {
	comment := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigComment)
	if comment == nil || !strings.Contains(comment.Text(), twig.VersionCommentPrefix) {
		return nil
	}
	return comment
}

func (p *TwigVersioningHoverProvider) hoverBlockIdentifier(blockName string, uri string) (*protocol.Hover, error) {
	allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(blockName)
	if err != nil {
		return nil, err
	}

	originalHash := twig.FindOriginalStorefrontHash(allBlockHashes)

	var hoverText strings.Builder
	fmt.Fprintf(&hoverText, "**Block:** `%s`\n\n", blockName)

	if originalHash != nil {
		fmt.Fprintf(&hoverText, "**Original Hash:** `%s`\n\n", originalHash.Hash)
		fmt.Fprintf(&hoverText, "**Template Path:** `%s`\n\n", originalHash.RelativePath)

		twigFiles, err := p.twigIndexer.GetTwigFilesByRelPath(twig.ConvertToRelativePath(uri))
		if err == nil && len(twigFiles) > 0 {
			if block, exists := twigFiles[0].Blocks[blockName]; exists && block.VersionComment != nil {
				if block.VersionComment.Hash == originalHash.Hash {
					hoverText.WriteString("**Status:** Block version is up to date\n\n")
				} else {
					hoverText.WriteString("**Status:** Block version is outdated\n\n")
					fmt.Fprintf(&hoverText, "**Current Version:** `%s`\n\n", block.VersionComment.Version)
				}
			} else {
				hoverText.WriteString("❗ **Status:** No version comment found\n\n")
			}
		}
	} else {
		hoverText.WriteString("ℹ️ **Status:** No original block found in Storefront templates\n\n")
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: hoverText.String(),
		},
	}, nil
}

func (p *TwigVersioningHoverProvider) hoverVersionComment(
	node *twigsyntax.Node,
	content string,
	uri string,
	lineIndex *twigsyntax.LineIndex,
) (*protocol.Hover, error) {
	commentRange := node.RangeTrimmedTrivia()
	commentText := content[commentRange.Start:commentRange.End]

	if lineIndex == nil {
		lineIndex = twigsyntax.NewLineIndex(content)
	}
	line, _ := lineIndex.Position(commentRange.Start)
	versionComment := twig.ParseVersionComment(commentText, int(line)+1)
	if versionComment == nil {
		return nil, nil
	}

	var hoverText strings.Builder
	hoverText.WriteString("**Shopware Block Version Comment**\n\n")
	fmt.Fprintf(&hoverText, "**Hash:** `%s`\n\n", versionComment.Hash)
	fmt.Fprintf(&hoverText, "**Version:** `%s`\n\n", versionComment.Version)

	blockName := p.findBlockNameAfterComment(node, content)
	if blockName != "" {
		allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(blockName)
		if err == nil {
			originalHash := twig.FindOriginalStorefrontHash(allBlockHashes)
			if originalHash != nil {
				if versionComment.Hash == originalHash.Hash {
					hoverText.WriteString("**Status:** Version comment matches original block\n\n")
				} else {
					hoverText.WriteString("**Status:** Version comment is outdated\n\n")
					fmt.Fprintf(&hoverText, "**Expected Hash:** `%s`\n\n", originalHash.Hash)
				}
			}
		}
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: hoverText.String(),
		},
	}, nil
}

func (p *TwigVersioningHoverProvider) findBlockNameAfterComment(commentNode *twigsyntax.Node, content string) string {
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
