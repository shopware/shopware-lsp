package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type ControllerDefinitionProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewControllerDefinitionProvider(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ControllerDefinitionProvider {
	return &ControllerDefinitionProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

func (p *ControllerDefinitionProvider) GetDefinition(
	ctx context.Context,
	params *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || params == nil {
		return nil
	}
	var reference symfony.ControllerReference
	var ok bool
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".yaml", ".yml":
		if params.Node == nil {
			return nil
		}
		reference, _, ok = symfony.YAMLControllerReference(params.Node)
	case ".xml":
		if params.Node == nil {
			return nil
		}
		reference, _, ok = symfony.XMLControllerReference(params.Node)
	case ".twig":
		if params.Root != nil && params.LineIndex != nil {
			offset := params.LineIndex.OffsetUTF16(
				uint32(params.Position.Line),
				uint32(params.Position.Character),
			)
			if see, found := twig.SeeReferenceAt(
				params.Root,
				offset,
			); found {
				reference, ok = symfony.ParseTwigControllerReference(
					see.Target,
				)
				if ok {
					if _, classFound := p.phpIndex.FindClass(
						reference.Target,
					); classFound {
						return nil
					}
				}
			}
		}
		if !ok {
			if params.Node == nil {
				return nil
			}
			twigReference, found :=
				symfony.TwigControllerReferenceAt(params.Node)
			reference, ok =
				twigReference.ControllerReference, found
		}
	default:
		return nil
	}
	if !ok || ctx.Err() != nil {
		return nil
	}
	resolution, err := symfony.ResolveControllerReference(
		reference,
		p.serviceIndex,
		p.phpIndex,
	)
	if err != nil {
		return nil
	}
	if resolution.MethodDeclared {
		return []protocol.Location{phpSymbolLocation(resolution.Method)}
	}
	if resolution.ClassFound {
		return []protocol.Location{phpSymbolLocation(resolution.Class)}
	}
	return nil
}
