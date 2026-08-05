package definition

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func (p *TwigDefinitionProvider) twigTypesTagDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.DefinitionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.TypesTagClassReferenceAt(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	symbol, found := p.phpIndex.FindClass(reference.Name)
	if !found {
		return []protocol.Location{}
	}
	return []protocol.Location{phpSymbolLocation(symbol)}
}
