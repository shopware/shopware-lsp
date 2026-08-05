package hover

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
)

type MessengerHoverProvider struct {
	phpIndex *php.PHPIndex
	index    *messenger.Index
}

func NewMessengerHoverProvider(
	phpIndex *php.PHPIndex,
	indexes ...*messenger.Index,
) *MessengerHoverProvider {
	var index *messenger.Index
	if len(indexes) != 0 {
		index = indexes[0]
	}
	return &MessengerHoverProvider{
		phpIndex: phpIndex,
		index:    index,
	}
}

func (p *MessengerHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Root == nil || request.Node == nil {
		return nil, nil
	}
	reference, found := messenger.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	)
	if !found || reference.Name == "" {
		return nil, nil
	}
	var markdown strings.Builder
	switch reference.Role {
	case messenger.ReferenceMessage:
		fmt.Fprintf(
			&markdown,
			"**Symfony Messenger message** `%s`",
			escapeMessengerMarkdown(reference.Name),
		)
		if p.index != nil {
			message, exists, err := p.index.GetMessage(reference.Name)
			if err != nil {
				return nil, err
			}
			if exists {
				fmt.Fprintf(
					&markdown,
					"\n\n%d handler(s) · %d dispatch site(s)",
					len(message.Handlers()),
					len(message.Dispatches()),
				)
			}
		}
	case messenger.ReferenceHandlerMethod:
		fmt.Fprintf(
			&markdown,
			"**Symfony Messenger handler method** `%s::%s()`",
			escapeMessengerMarkdown(reference.Class),
			escapeMessengerMarkdown(reference.Name),
		)
		methods := p.phpIndex.FindMethods(reference.Class, reference.Name)
		if len(methods) != 0 && methods[0].DocSummary != "" {
			fmt.Fprintf(&markdown, "\n\n%s", methods[0].DocSummary)
		}
	default:
		return nil, nil
	}
	rng := reference.Node.RangeTrimmedTrivia()
	startLine, startCharacter := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
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

func escapeMessengerMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
