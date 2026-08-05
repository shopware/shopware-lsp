package hover

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
)

type SerializerHoverProvider struct {
	index    *serializer.Index
	phpIndex *php.PHPIndex
}

func NewSerializerHoverProvider(
	index *serializer.Index,
	phpIndex *php.PHPIndex,
) *SerializerHoverProvider {
	return &SerializerHoverProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SerializerHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".php",
		) {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	usage, found := serializer.UsageAt(request.Root, offset)
	if !found {
		return nil, nil
	}
	usages, err := p.index.Usages(usage.Class)
	if err != nil {
		return nil, err
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony Serializer target** `%s`",
		strings.ReplaceAll(usage.Class, "`", "\\`"),
	)
	if p.phpIndex != nil {
		if symbol, exists := p.phpIndex.FindClass(usage.Class); exists &&
			symbol.DocSummary != "" {
			fmt.Fprintf(&markdown, "\n\n%s", symbol.DocSummary)
		}
	}
	fmt.Fprintf(&markdown, "\n\n%d indexed deserialize use(s)", len(usages))
	rng := serializerHoverRange(usage.Range, request.LineIndex)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &rng,
	}, nil
}

func serializerHoverRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}
