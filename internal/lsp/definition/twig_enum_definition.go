package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type TwigEnumDefinitionProvider struct {
	phpIndex *php.PHPIndex
}

func NewTwigEnumDefinitionProvider(
	phpIndex *php.PHPIndex,
) *TwigEnumDefinitionProvider {
	return &TwigEnumDefinitionProvider{phpIndex: phpIndex}
}

func (p *TwigEnumDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil
	}
	reference, found := twig.EnumReferenceAt(request.Node)
	if !found {
		return nil
	}
	symbol, found := p.phpIndex.FindClass(reference.Name)
	if !found {
		return nil
	}
	return []protocol.Location{phpSymbolLocation(symbol)}
}
