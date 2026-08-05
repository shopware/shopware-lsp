package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type ContainerConstantDefinitionProvider struct {
	phpIndex *php.PHPIndex
}

func NewContainerConstantDefinitionProvider(
	phpIndex *php.PHPIndex,
) *ContainerConstantDefinitionProvider {
	return &ContainerConstantDefinitionProvider{phpIndex: phpIndex}
}

func (p *ContainerConstantDefinitionProvider) GetDefinition(
	_ context.Context,
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
	for _, reference := range constantDefinitionReferences(request) {
		if !reference.Range.Contains(offset) {
			continue
		}
		symbols := symfony.ResolveContainerConstant(
			p.phpIndex,
			reference.Name,
		)
		locations := make([]protocol.Location, 0, len(symbols))
		seen := make(map[string]struct{}, len(symbols))
		for _, symbol := range symbols {
			key := string(symbol.ID)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			locations = append(locations, phpSymbolLocation(symbol))
		}
		return locations
	}
	return nil
}

func constantDefinitionReferences(
	request *lsp.DefinitionRequest,
) []symfony.ContainerConstantReference {
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	switch extension {
	case ".yaml", ".yml":
		return symfony.YAMLContainerConstantReferences(
			request.DocumentContent,
		)
	case ".xml":
		if request.Root == nil {
			return nil
		}
		return symfony.XMLContainerConstantReferences(request.Root)
	default:
		return nil
	}
}
