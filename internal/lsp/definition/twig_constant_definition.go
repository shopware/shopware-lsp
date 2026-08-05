package definition

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigConstantDefinitionProvider struct {
	phpIndex  *php.PHPIndex
	twigIndex *twig.TwigIndexer
}

func NewTwigConstantDefinitionProvider(
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *TwigConstantDefinitionProvider {
	return &TwigConstantDefinitionProvider{
		phpIndex:  phpIndex,
		twigIndex: twigIndex,
	}
}

func (p *TwigConstantDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	references := twig.ConstantReferencesAt(
		path,
		request.Root,
		request.Node,
		twig.PHPAccessResolver{
			PHP:  p.phpIndex,
			Twig: p.twigIndex,
		},
	)
	seen := make(map[semantic.SymbolID]struct{})
	var locations []protocol.Location
	for _, reference := range references {
		for _, symbol := range twigConstantSymbols(
			p.phpIndex,
			reference,
		) {
			if symbol.Visibility != semantic.Public &&
				symbol.Kind != semantic.GlobalConstantSymbol {
				continue
			}
			if _, duplicate := seen[symbol.ID]; duplicate {
				continue
			}
			seen[symbol.ID] = struct{}{}
			locations = append(locations, phpSymbolLocation(symbol))
		}
	}
	return locations
}

func twigConstantSymbols(
	index *php.PHPIndex,
	reference twig.ConstantReference,
) []semantic.Symbol {
	if index == nil {
		return nil
	}
	if reference.Class != "" {
		return index.FindConstants(reference.Class, reference.Name)
	}
	return index.FindGlobalConstants(reference.Name)
}
