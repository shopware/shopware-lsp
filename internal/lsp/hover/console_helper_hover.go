package hover

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type ConsoleHelperHoverProvider struct {
	phpIndex *php.PHPIndex
	catalog  *console.HelperCatalog
}

func NewConsoleHelperHoverProvider(
	phpIndex *php.PHPIndex,
) *ConsoleHelperHoverProvider {
	return &ConsoleHelperHoverProvider{
		phpIndex: phpIndex,
		catalog:  console.NewHelperCatalog(phpIndex),
	}
}

func (p *ConsoleHelperHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	reference, found := console.HelperReferenceAt(request.Node)
	if !found || reference.Name == "" || !console.ValidateHelperReference(
		ctx,
		p.phpIndex,
		reference,
		request.DocumentContent,
	) {
		return nil, nil
	}
	for _, helper := range p.catalog.Helpers() {
		if helper.Name != reference.Name {
			continue
		}
		value := fmt.Sprintf(
			"**Symfony Console helper** `%s`\n\nClass: `%s`",
			helper.Name,
			helper.Class,
		)
		if helper.Summary != "" {
			value += "\n\n" + helper.Summary
		}
		startLine, startCharacter := request.LineIndex.PositionUTF16(
			reference.Range.Start,
		)
		endLine, endCharacter := request.LineIndex.PositionUTF16(
			reference.Range.End,
		)
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: value,
			},
			Range: &protocol.Range{
				Start: protocol.Position{
					Line:      int(startLine),
					Character: int(startCharacter),
				},
				End: protocol.Position{
					Line:      int(endLine),
					Character: int(endCharacter),
				},
			},
		}, nil
	}
	return nil, nil
}

var _ lsp.HoverProvider = (*ConsoleHelperHoverProvider)(nil)
