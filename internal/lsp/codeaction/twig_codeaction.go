package codeaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigCodeActionProvider struct {
	versioning *twig.VersioningService
}

func NewTwigCodeActionProvider(
	versioning *twig.VersioningService,
) *TwigCodeActionProvider {
	return &TwigCodeActionProvider{versioning: versioning}
}

func (p *TwigCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{
		protocol.CodeActionRefactorExtract,
		protocol.CodeActionQuickFix,
	}
}

func (p *TwigCodeActionProvider) GetCodeActions(
	ctx context.Context,
	params *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if params == nil || params.CodeActionParams == nil {
		return nil
	}
	if documentPath, err := uriutil.Path(params.TextDocument.URI); err == nil &&
		isAdministrationTwigPath(documentPath) {
		return nil
	}
	if params.Node == nil && len(params.DocumentContent) > 0 {
		result := twigparser.Parse(string(params.DocumentContent))
		lineIndex := twigsyntax.NewLineIndex(result.Tree.Source)
		params.DocumentTree = result.Tree
		params.LineIndex = lineIndex
		offset := lineIndex.OffsetUTF16(
			uint32(params.Range.Start.Line),
			uint32(params.Range.Start.Character),
		)
		params.Root = result.Tree.Root
		params.Token = result.Tree.Root.TokenAtOffset(offset)
		params.Node = result.Tree.Root.NodeAtOffset(offset)
	}
	if params.Node == nil {
		return nil
	}

	var actions []protocol.CodeAction
	blockNode := twigquery.BlockAt(params.Node)
	blockName := twigquery.BlockName(blockNode)
	if blockNode != nil && params.Token != nil && params.Token.Text() == blockName {
		if strings.Contains(params.TextDocument.URI, "Resources/views/storefront") {
			actions = append(actions, protocol.CodeAction{
				Title: "Overwrite this block in Extension",
				Kind:  protocol.CodeActionRefactorExtract,
				Command: &protocol.CommandAction{
					Title: "Overwrite Block", Command: "shopware.twig.extendBlock",
					Arguments: []any{params.TextDocument.URI, blockName},
				},
			})
		}
		if !hasDiagnosticCode(
			params,
			"twig.versioning.comment_missing",
			"twig.versioning.outdated",
		) {
			if action := p.getVersionCommentAction(params, blockName); action != nil {
				actions = append(actions, *action)
			}
		}
		if !hasDiagnosticCode(params, "twig.versioning.outdated") {
			if action := p.getShowDiffAction(params, blockName); action != nil {
				actions = append(actions, *action)
			}
		}
	}

	if comment := twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigComment); comment != nil &&
		strings.Contains(comment.Text(), twig.VersionCommentPrefix) {
		if name := versionedBlockAtComment(params, comment); name != "" {
			if !hasDiagnosticCode(
				params,
				"twig.versioning.comment_missing",
				"twig.versioning.outdated",
			) {
				if action := p.getVersionCommentAction(params, name); action != nil {
					actions = append(actions, *action)
				}
			}
			if !hasDiagnosticCode(params, "twig.versioning.outdated") {
				if action := p.getShowDiffAction(params, name); action != nil {
					actions = append(actions, *action)
				}
			}
		}
	}
	return actions
}

func (p *TwigCodeActionProvider) getVersionCommentAction(
	params *lsp.CodeActionRequest,
	blockName string,
) *protocol.CodeAction {
	if p == nil || p.versioning == nil || blockName == "" {
		return nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil || twig.IsUpstreamTemplate(path) {
		return nil
	}
	_, block, resolution, err := p.versioning.ResolveDocument(
		path,
		string(params.DocumentContent),
		blockName,
	)
	if err != nil || len(resolution.Candidates) == 0 {
		return nil
	}
	update := block.VersionComment != nil
	if update {
		for _, candidate := range resolution.Candidates {
			if candidate.Hash == block.VersionComment.Hash {
				return nil
			}
		}
	}
	rng, replacement, err := p.versioning.VersionCommentEdit(
		path,
		string(params.DocumentContent),
		blockName,
	)
	if err != nil {
		return nil
	}
	title := "Shopware: Add Twig block version comment"
	if update {
		title = "Shopware: Update Twig block version comment"
	}
	lineIndex := codeActionLineIndex(params)
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return &protocol.CodeAction{
		Title: title,
		Kind:  protocol.CodeActionQuickFix,
		Edit: &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
			params.TextDocument.URI: {{
				Range: protocol.Range{
					Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
					End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
				},
				NewText: replacement,
			}},
		}},
	}
}

func (p *TwigCodeActionProvider) getShowDiffAction(
	params *lsp.CodeActionRequest,
	blockName string,
) *protocol.CodeAction {
	if p == nil || p.versioning == nil || blockName == "" {
		return nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil || twig.IsUpstreamTemplate(path) {
		return nil
	}
	_, block, resolution, err := p.versioning.ResolveDocument(
		path,
		string(params.DocumentContent),
		blockName,
	)
	if err != nil || block.VersionComment == nil ||
		block.VersionComment.Version == "" || len(resolution.Candidates) == 0 ||
		!twig.IsStorefrontTemplate(resolution.Candidates[0].AbsolutePath) {
		return nil
	}
	for _, candidate := range resolution.Candidates {
		if candidate.Hash == block.VersionComment.Hash {
			return nil
		}
	}
	return &protocol.CodeAction{
		Title: "Shopware: Show Twig block difference",
		Kind:  protocol.CodeActionQuickFix,
		Command: &protocol.CommandAction{
			Title: "Show Twig Block Difference", Command: "shopware.twig.showBlockDiff",
			Arguments: []any{params.TextDocument.URI, blockName},
		},
	}
}

func versionedBlockAtComment(
	params *lsp.CodeActionRequest,
	comment *twigsyntax.Node,
) string {
	file, err := parseTwigCodeActionDocument(params)
	if err != nil {
		return ""
	}
	commentRange := comment.RangeTrimmedTrivia()
	for _, block := range file.Blocks {
		if block.VersionCommentRange != nil &&
			block.VersionCommentRange.Start == commentRange.Start {
			return block.Name
		}
	}
	return ""
}

func hasDiagnosticCode(params *lsp.CodeActionRequest, codes ...string) bool {
	for _, diagnostic := range params.Context.Diagnostics {
		current := fmt.Sprint(diagnostic.Code)
		for _, code := range codes {
			if current == code {
				return true
			}
		}
	}
	return false
}

func codeActionLineIndex(params *lsp.CodeActionRequest) *twigsyntax.LineIndex {
	if params.LineIndex != nil {
		return params.LineIndex
	}
	params.LineIndex = twigsyntax.NewLineIndex(string(params.DocumentContent))
	return params.LineIndex
}

func parseTwigCodeActionDocument(
	params *lsp.CodeActionRequest,
) (*twig.TwigFile, error) {
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	if params.DocumentTree != nil {
		return twig.ParseTwigTree(path, params.DocumentTree, codeActionLineIndex(params))
	}
	return twig.ParseTwig(path, params.DocumentContent)
}
