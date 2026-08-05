package linkedediting

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

const adminComponentTagWordPattern = "[-A-Za-z0-9_]+"

// AdminLinkedEditingProvider links the opening and closing names of Vue-style
// component tags in Administration Twig markup. The parser's HtmlTag node is
// the pairing authority, so nested components cannot accidentally link to a
// same-named sibling and malformed markup remains conservative.
type AdminLinkedEditingProvider struct{}

func NewAdminLinkedEditingProvider() *AdminLinkedEditingProvider {
	return &AdminLinkedEditingProvider{}
}

func (p *AdminLinkedEditingProvider) GetLinkedEditingRanges(
	ctx context.Context,
	request *lsp.LinkedEditingRangeRequest,
) (*protocol.LinkedEditingRanges, error) {
	if ctx.Err() != nil || p == nil || request == nil ||
		request.LinkedEditingRangeParams == nil || request.Document == nil ||
		lsp.EffectiveSyntaxLanguage(request.Language, request.Node) != language.Twig ||
		request.Root == nil ||
		request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	if tag := adminLinkedEditingAncestorTag(request.Node); tag != nil {
		if ranges := adminLinkedEditingTagRanges(*tag, offset, request.LineIndex); ranges != nil {
			return ranges, nil
		}
	}
	// At the half-open end of a name NodeAtOffset can select the following
	// token. A narrow fallback over complete HtmlTag nodes preserves the editor
	// experience at that valid word boundary without guessing tag pairing.
	for _, node := range twigquery.Nodes(request.Root, twigsyntax.HtmlTag) {
		tag, ok := twigast.CastHtmlTag(node)
		if !ok {
			continue
		}
		if ranges := adminLinkedEditingTagRanges(tag, offset, request.LineIndex); ranges != nil {
			return ranges, nil
		}
	}
	return nil, nil
}

func adminLinkedEditingAncestorTag(node *twigsyntax.Node) *twigast.HtmlTag {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != twigsyntax.HtmlTag {
			continue
		}
		tag, ok := twigast.CastHtmlTag(current)
		if ok {
			return &tag
		}
	}
	return nil
}

func adminLinkedEditingTagRanges(
	tag twigast.HtmlTag,
	offset uint32,
	lineIndex *cst.LineIndex,
) *protocol.LinkedEditingRanges {
	starting, startFound := tag.StartingTag()
	ending, endFound := tag.EndingTag()
	if !startFound || !endFound || starting.Name() == nil || ending.Name() == nil {
		return nil
	}
	name := starting.Name().Text()
	if name == "" || name != ending.Name().Text() ||
		!adminLinkedEditingComponentTag(name) {
		return nil
	}
	startRange := starting.Name().Range()
	endRange := ending.Name().Range()
	if !adminLinkedEditingRangeContainsCursor(startRange, offset) &&
		!adminLinkedEditingRangeContainsCursor(endRange, offset) {
		return nil
	}
	return &protocol.LinkedEditingRanges{
		Ranges: []protocol.Range{
			adminLinkedEditingProtocolRange(startRange, lineIndex),
			adminLinkedEditingProtocolRange(endRange, lineIndex),
		},
		WordPattern: adminComponentTagWordPattern,
	}
}

func adminLinkedEditingComponentTag(name string) bool {
	return admin.IsComponentTag(name) || strings.EqualFold(name, "component")
}

func adminLinkedEditingRangeContainsCursor(
	rangeValue cst.TextRange,
	offset uint32,
) bool {
	return offset >= rangeValue.Start && offset <= rangeValue.End
}

func adminLinkedEditingProtocolRange(
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

var _ lsp.LinkedEditingRangeProvider = (*AdminLinkedEditingProvider)(nil)
