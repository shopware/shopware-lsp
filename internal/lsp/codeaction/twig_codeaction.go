package codeaction

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigCodeActionProvider struct {
	twigIndexer *twig.TwigIndexer
	projectRoot string
}

func NewTwigCodeActionProvider(projectRoot string, twigIndexer *twig.TwigIndexer) *TwigCodeActionProvider {
	return &TwigCodeActionProvider{twigIndexer: twigIndexer, projectRoot: projectRoot}
}

func (p *TwigCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{
		protocol.CodeActionRefactorExtract,
		protocol.CodeActionQuickFix,
	}
}

func (p *TwigCodeActionProvider) GetCodeActions(ctx context.Context, params *lsp.CodeActionRequest) []protocol.CodeAction {
	if params.Node == nil && len(params.DocumentContent) > 0 {
		result := twigparser.Parse(string(params.DocumentContent))
		lineIndex := twigsyntax.NewLineIndex(result.Tree.Source)
		params.DocumentTree = result.Tree
		params.LineIndex = lineIndex
		offset := lineIndex.OffsetUTF16(uint32(params.Range.Start.Line), uint32(params.Range.Start.Character))
		params.Root = result.Tree.Root
		params.Token = result.Tree.Root.TokenAtOffset(offset)
		params.Node = result.Tree.Root.NodeAtOffset(offset)
	}
	if params.Node == nil {
		return nil
	}

	var codeActions []protocol.CodeAction

	blockNode := twigquery.BlockAt(params.Node)
	blockName := twigquery.BlockName(blockNode)
	if blockNode != nil && params.Token != nil && params.Token.Text() == blockName {
		if strings.Contains(params.TextDocument.URI, "Resources/views/storefront") {
			codeActions = append(codeActions, protocol.CodeAction{
				Title: "Overwrite this block in Extension",
				Kind:  protocol.CodeActionRefactorExtract,
				Command: &protocol.CommandAction{
					Title:     "Overwrite Block",
					Command:   "shopware.twig.extendBlock",
					Arguments: []any{params.TextDocument.URI, blockName},
				},
			})
		}

		if action := p.getVersioningHashAction(params); action != nil {
			codeActions = append(codeActions, *action)
		}

		if action := p.getShowDiffAction(params); action != nil {
			codeActions = append(codeActions, *action)
		}
	}

	if action := p.getShowDiffActionFromComment(params); action != nil {
		codeActions = append(codeActions, *action)
	}

	return codeActions
}

func (p *TwigCodeActionProvider) getVersioningHashAction(params *lsp.CodeActionRequest) *protocol.CodeAction {
	if p.twigIndexer == nil {
		return nil
	}

	if twig.IsStorefrontTemplate(params.TextDocument.URI) {
		return nil
	}

	blockNode := twigquery.BlockAt(params.Node)
	if blockNode == nil {
		return nil
	}

	blockName := twigquery.BlockName(blockNode)

	if p.hasVersioningComment(blockNode, params.DocumentContent, codeActionLineIndex(params)) {
		return nil
	}

	allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(blockName)
	if err != nil || len(allBlockHashes) == 0 {
		return nil
	}

	originalHash := twig.FindOriginalStorefrontHash(allBlockHashes)
	if originalHash == nil {
		return nil
	}

	lineIndex := codeActionLineIndex(params)
	blockLine, _ := lineIndex.Position(blockNode.RangeTrimmedTrivia().Start)
	versionComment := twig.FormatVersionComment(originalHash.Hash, twig.DetectShopwareVersion(p.projectRoot))

	edit := &protocol.WorkspaceEdit{
		Changes: map[string][]protocol.TextEdit{
			params.TextDocument.URI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: int(blockLine), Character: 0},
						End:   protocol.Position{Line: int(blockLine), Character: 0},
					},
					NewText: versionComment,
				},
			},
		},
	}

	return &protocol.CodeAction{
		Title: "Add twig versioning hash",
		Kind:  protocol.CodeActionQuickFix,
		Edit:  edit,
	}
}

func (p *TwigCodeActionProvider) getShowDiffAction(params *lsp.CodeActionRequest) *protocol.CodeAction {
	if p.twigIndexer == nil {
		return nil
	}

	if twig.IsStorefrontTemplate(params.TextDocument.URI) {
		return nil
	}

	blockNode := twigquery.BlockAt(params.Node)
	if blockNode == nil {
		return nil
	}

	blockName := twigquery.BlockName(blockNode)

	twigFile, err := parseTwigCodeActionDocument(params)
	if err != nil {
		return nil
	}

	block, exists := twigFile.Blocks[blockName]
	if !exists || block.VersionComment == nil {
		return nil
	}

	allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(blockName)
	if err != nil || len(allBlockHashes) == 0 {
		return nil
	}

	originalHash := twig.FindOriginalStorefrontHash(allBlockHashes)
	if originalHash == nil {
		return nil
	}

	if block.VersionComment.Hash == originalHash.Hash {
		return nil
	}

	return &protocol.CodeAction{
		Title: "Show block difference",
		Kind:  protocol.CodeActionQuickFix,
		Command: &protocol.CommandAction{
			Title:     "Show Block Difference",
			Command:   "shopware.twig.showBlockDiff",
			Arguments: []any{params.TextDocument.URI, blockName},
		},
	}
}

func (p *TwigCodeActionProvider) getShowDiffActionFromComment(params *lsp.CodeActionRequest) *protocol.CodeAction {
	if p.twigIndexer == nil {
		return nil
	}

	if twig.IsStorefrontTemplate(params.TextDocument.URI) {
		return nil
	}

	commentNode := twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigComment)
	if commentNode == nil {
		return nil
	}

	commentRange := commentNode.RangeTrimmedTrivia()
	commentText := string(params.DocumentContent[commentRange.Start:commentRange.End])
	if !strings.Contains(commentText, twig.VersionCommentPrefix) {
		return nil
	}

	lineIndex := codeActionLineIndex(params)
	commentLineZero, _ := lineIndex.Position(commentRange.Start)
	versionComment := twig.ParseVersionComment(commentText, int(commentLineZero)+1)
	if versionComment == nil {
		return nil
	}

	commentLine := int(commentLineZero) + 1

	twigFile, err := parseTwigCodeActionDocument(params)
	if err != nil {
		return nil
	}

	var blockName string
	for _, block := range twigFile.Blocks {
		if block.VersionComment != nil && block.VersionComment.Line == commentLine {
			blockName = block.Name
			break
		}
	}

	if blockName == "" {
		return nil
	}

	allBlockHashes, err := p.twigIndexer.GetTwigBlockHashes(blockName)
	if err != nil || len(allBlockHashes) == 0 {
		return nil
	}

	originalHash := twig.FindOriginalStorefrontHash(allBlockHashes)
	if originalHash == nil {
		return nil
	}

	if versionComment.Hash == originalHash.Hash {
		return nil
	}

	return &protocol.CodeAction{
		Title: "Show block difference",
		Kind:  protocol.CodeActionQuickFix,
		Command: &protocol.CommandAction{
			Title:     "Show Block Difference",
			Command:   "shopware.twig.showBlockDiff",
			Arguments: []any{params.TextDocument.URI, blockName},
		},
	}
}

func (p *TwigCodeActionProvider) hasVersioningComment(
	blockNode *twigsyntax.Node,
	content []byte,
	lineIndex *cst.LineIndex,
) bool {
	blockStartLine, _ := lineIndex.Position(blockNode.RangeTrimmedTrivia().Start)
	for sibling := blockNode.PrevSibling(); sibling != nil; {
		switch previous := sibling.(type) {
		case *twigsyntax.Token:
			sibling = previous.PrevSibling()
		case *twigsyntax.Node:
			if previous.Kind() == twigsyntax.TwigComment {
				commentRange := previous.RangeTrimmedTrivia()
				commentEndLine, _ := lineIndex.Position(commentRange.End)
				return blockStartLine-commentEndLine <= 1 &&
					strings.Contains(string(content[commentRange.Start:commentRange.End]), twig.VersionCommentPrefix)
			}
			if previous.Kind() == twigsyntax.TwigBlock {
				return false
			}
			sibling = previous.PrevSibling()
		}
	}
	return false
}

func codeActionLineIndex(params *lsp.CodeActionRequest) *cst.LineIndex {
	if params.LineIndex != nil {
		return params.LineIndex
	}
	params.LineIndex = cst.NewLineIndex(string(params.DocumentContent))
	return params.LineIndex
}

func parseTwigCodeActionDocument(params *lsp.CodeActionRequest) (*twig.TwigFile, error) {
	if params.DocumentTree != nil {
		filePath, err := uriutil.Path(params.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		return twig.ParseTwigTree(
			filePath,
			params.DocumentTree,
			codeActionLineIndex(params),
		)
	}
	return twig.ParseTwig(params.TextDocument.URI, params.DocumentContent)
}
