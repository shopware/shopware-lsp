package definition

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
)

type SerializerDefinitionProvider struct {
	index    *serializer.Index
	phpIndex *php.PHPIndex
}

func NewSerializerDefinitionProvider(
	index *serializer.Index,
	phpIndex *php.PHPIndex,
) *SerializerDefinitionProvider {
	return &SerializerDefinitionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SerializerDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		request == nil || request.Node == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".php",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	usage, found := serializer.UsageAt(request.Root, offset)
	if !found || usage.Kind != serializer.StringTarget {
		return nil
	}
	symbol, found := p.phpIndex.FindClass(usage.Class)
	if !found {
		return nil
	}
	return []protocol.Location{phpSymbolLocation(symbol)}
}
