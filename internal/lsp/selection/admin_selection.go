package selection

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// AdminSelectionRangeProvider exposes lossless native-CST ancestry for
// Administration JavaScript/TypeScript and Twig documents. It deliberately
// starts with the exact non-trivia token before expanding through syntax nodes
// and finally the whole document.
type AdminSelectionRangeProvider struct{}

func NewAdminSelectionRangeProvider() *AdminSelectionRangeProvider {
	return &AdminSelectionRangeProvider{}
}

func (p *AdminSelectionRangeProvider) GetSelectionRanges(
	ctx context.Context,
	request *lsp.SelectionRangeRequest,
) ([]protocol.SelectionRange, error) {
	if ctx.Err() != nil || p == nil || request == nil ||
		request.SelectionRangeParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	if request.Document.SyntaxLanguage != language.JavaScript &&
		request.Document.SyntaxLanguage != language.Twig &&
		request.Document.SyntaxLanguage != language.Vue {
		return nil, nil
	}
	result := make([]protocol.SelectionRange, 0, len(request.Positions))
	for _, position := range request.Positions {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result = append(result, adminSelectionRangeAt(
			request.Document.SyntaxTree.Root,
			request.Document.LineIndex,
			position,
		))
	}
	return result, nil
}

func adminSelectionRangeAt(
	root *cst.Node,
	lineIndex *cst.LineIndex,
	position protocol.Position,
) protocol.SelectionRange {
	offset := lineIndex.OffsetUTF16(
		uint32(max(position.Line, 0)),
		uint32(max(position.Character, 0)),
	)
	token := root.TokenAtOffset(offset)
	node := root.NodeAtOffset(offset)
	if token == nil && offset > root.Range().Start {
		offset--
		token = root.TokenAtOffset(offset)
		node = root.NodeAtOffset(offset)
	}
	var ranges []cst.TextRange
	add := func(candidate cst.TextRange, allowEmpty bool) {
		if candidate.End < candidate.Start ||
			(!allowEmpty && candidate.End == candidate.Start) {
			return
		}
		if len(ranges) == 0 {
			ranges = append(ranges, candidate)
			return
		}
		child := ranges[len(ranges)-1]
		if candidate.Start > child.Start || candidate.End < child.End ||
			candidate == child {
			return
		}
		ranges = append(ranges, candidate)
	}
	if token != nil && !token.Kind().IsTrivia() {
		add(token.Range(), false)
	}
	for current := node; current != nil; current = current.Parent() {
		add(current.RangeTrimmedTrivia(), false)
	}
	add(root.Range(), true)
	if len(ranges) == 0 {
		ranges = append(ranges, root.Range())
	}
	var parent *protocol.SelectionRange
	for index := len(ranges) - 1; index >= 0; index-- {
		current := protocol.SelectionRange{
			Range:  adminSelectionProtocolRange(ranges[index], lineIndex),
			Parent: parent,
		}
		parent = &current
	}
	return *parent
}

func adminSelectionProtocolRange(
	rangeValue cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
	return protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

var _ lsp.SelectionRangeProvider = (*AdminSelectionRangeProvider)(nil)
