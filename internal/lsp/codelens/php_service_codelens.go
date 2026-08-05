package codelens

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type PHPServiceCodelensProvider struct {
	phpIndex     *php.PHPIndex
	serviceIndex *symfony.ServiceIndex
}

func NewPHPCodeLensProvider(phpIndex *php.PHPIndex, serviceIndex *symfony.ServiceIndex) *PHPServiceCodelensProvider {
	return &PHPServiceCodelensProvider{
		phpIndex:     phpIndex,
		serviceIndex: serviceIndex,
	}
}

func (p *PHPServiceCodelensProvider) GetCodeLenses(ctx context.Context, params *lsp.CodeLensRequest) ([]protocol.CodeLens, error) {
	if p == nil || p.phpIndex == nil || p.serviceIndex == nil ||
		params == nil || params.CodeLensParams == nil ||
		params.Document == nil ||
		params.Document.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(params.TextDocument.URI),
			".php",
		) {
		return []protocol.CodeLens{}, nil
	}

	filePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	var symbols []semantic.Symbol
	if params.Document.SyntaxTree != nil &&
		params.Document.SyntaxTree.Root != nil {
		symbols = p.phpIndex.AnalyzeDocument(
			filePath,
			params.Document.Version,
			params.Document.SyntaxTree.Root,
		).Symbols
	} else if document, found := p.phpIndex.SemanticDocument(filePath); found {
		symbols = document.Symbols
	}
	if len(symbols) == 0 {
		return []protocol.CodeLens{}, nil
	}

	var lenses []protocol.CodeLens
	classes := make(map[semantic.SymbolID]semantic.Symbol)
	for _, phpClass := range symbols {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if phpClass.Kind != semantic.ClassSymbol ||
			phpClass.Flags.Has(semantic.AbstractFlag) {
			continue
		}
		classes[phpClass.ID] = phpClass
		locations, err := p.serviceIndex.GetServicesUsageByClassName(phpClass.FullyQualified)
		if err != nil {
			return nil, err
		}
		if len(locations) == 0 {
			continue
		}
		title := "Open Service Definition"
		if len(locations) > 1 {
			title = fmt.Sprintf(
				"Open %d Service Definitions",
				len(locations),
			)
		}
		lenses = append(lenses, relatedLens(
			relatedProtocolRange(
				phpClass.SelectionRange,
				params.Document.LineIndex,
			),
			title,
			serviceLocationTargets(locations),
		))
	}

	for _, method := range symbols {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		phpClass, found := classes[method.Container]
		if !found || method.Kind != semantic.MethodSymbol ||
			!strings.EqualFold(method.Name, "__construct") ||
			method.Visibility != semantic.Public {
			continue
		}
		locations, err := p.serviceIndex.
			GetAutowiredServicesUsageByClassName(
				phpClass.FullyQualified,
			)
		if err != nil {
			return nil, err
		}
		if len(locations) == 0 {
			continue
		}
		title := "Open autowired service definition"
		if len(locations) > 1 {
			title = fmt.Sprintf(
				"Open %d autowired service definitions",
				len(locations),
			)
		}
		lenses = append(lenses, relatedLens(
			relatedProtocolRange(
				method.SelectionRange,
				params.Document.LineIndex,
			),
			title,
			serviceLocationTargets(locations),
		))
	}
	sort.Slice(lenses, func(left, right int) bool {
		if lenses[left].Range.Start.Line !=
			lenses[right].Range.Start.Line {
			return lenses[left].Range.Start.Line <
				lenses[right].Range.Start.Line
		}
		if lenses[left].Range.Start.Character !=
			lenses[right].Range.Start.Character {
			return lenses[left].Range.Start.Character <
				lenses[right].Range.Start.Character
		}
		return lenses[left].Command.Title <
			lenses[right].Command.Title
	})
	return lenses, nil
}

func serviceLocationTargets(locations []symfony.Location) []string {
	targets := make([]string, 0, len(locations))
	for _, location := range locations {
		targets = append(
			targets,
			relatedTarget(location.Path, location.Line),
		)
	}
	return uniqueRelatedTargets(targets)
}

func (p *PHPServiceCodelensProvider) ResolveCodeLens(ctx context.Context, params *protocol.CodeLens) (*protocol.CodeLens, error) {
	return params, nil
}
