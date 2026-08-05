package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// DoctrineRelatedCodeLensProvider ports the reference plugin's entity metadata
// gutter and controller-model related navigation to LSP code lenses.
type DoctrineRelatedCodeLensProvider struct {
	doctrine *doctrine.Index
	php      *php.PHPIndex
}

func NewDoctrineRelatedCodeLensProvider(
	doctrineIndex *doctrine.Index,
	phpIndex *php.PHPIndex,
) *DoctrineRelatedCodeLensProvider {
	return &DoctrineRelatedCodeLensProvider{
		doctrine: doctrineIndex,
		php:      phpIndex,
	}
}

func (p *DoctrineRelatedCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.doctrine == nil || p.php == nil ||
		request == nil || request.CodeLensParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return p.phpCodeLenses(ctx, path, request)
	case ".xml", ".yaml", ".yml":
		return p.mappingCodeLenses(ctx, path, request)
	default:
		return nil, nil
	}
}

func (p *DoctrineRelatedCodeLensProvider) phpCodeLenses(
	ctx context.Context,
	path string,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	root := request.Document.SyntaxTree.Root
	phpContext := p.php.AddDocumentContext(
		ctx,
		path,
		request.Document.Version,
		root,
		root,
	)
	document := php.GetPHPContext(phpContext).Document
	var classes []semantic.Symbol
	var methods []semantic.Symbol
	for _, symbol := range document.Symbols {
		switch {
		case symbol.Kind == semantic.ClassSymbol:
			classes = append(classes, symbol)
		case symbol.Kind == semantic.MethodSymbol &&
			symbol.Visibility == semantic.Public:
			methods = append(methods, symbol)
		}
	}

	var result []protocol.CodeLens
	for _, class := range classes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		targets, targetErr := p.externalMappingTargets(
			class.FullyQualified,
			path,
		)
		if targetErr != nil {
			return nil, targetErr
		}
		if len(targets) == 0 {
			continue
		}
		title := "Open related Doctrine mapping"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d related Doctrine mappings",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				class.SelectionRange,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}

	targetsByMethod := make(map[semantic.SymbolID][]string)
	for _, reference := range p.doctrine.EntityReferencesInDocument(
		phpContext,
		root,
	) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		targets, targetErr := p.modelTargets(reference.Name)
		if targetErr != nil {
			return nil, targetErr
		}
		if len(targets) == 0 {
			continue
		}
		offset := doctrine.ReferenceRange(reference).Start
		for _, method := range methods {
			if !method.BodyRange.Contains(offset) {
				continue
			}
			targetsByMethod[method.ID] = append(
				targetsByMethod[method.ID],
				targets...,
			)
			break
		}
	}
	for _, method := range methods {
		targets := uniqueRelatedTargets(targetsByMethod[method.ID])
		if len(targets) == 0 {
			continue
		}
		title := "Open related Doctrine model"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d related Doctrine declarations",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				method.SelectionRange,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	sortRelatedCodeLenses(result)
	return result, nil
}

func (p *DoctrineRelatedCodeLensProvider) mappingCodeLenses(
	ctx context.Context,
	path string,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	var result []protocol.CodeLens
	for _, model := range doctrine.ModelsInDocument(
		path,
		request.Document.SyntaxTree.Root,
		request.Document.Source,
	) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if model.Source != doctrine.XMLSource &&
			model.Source != doctrine.YAMLSource {
			continue
		}
		symbol, found := p.php.FindClass(model.Class)
		if !found {
			continue
		}
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				model.NameRange,
				request.Document.LineIndex,
			),
			"Open mapped PHP class",
			[]string{relatedTarget(
				symbol.Path,
				relatedSourceLine(symbol.Path, rng.Start),
			)},
		))
	}
	sortRelatedCodeLenses(result)
	return result, nil
}

func (p *DoctrineRelatedCodeLensProvider) modelTargets(
	className string,
) ([]string, error) {
	var result []string
	if symbol, found := p.php.FindClass(className); found {
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		result = append(result, relatedTarget(
			symbol.Path,
			relatedSourceLine(symbol.Path, rng.Start),
		))
	}
	mappings, err := p.externalMappingTargets(className, "")
	if err != nil {
		return nil, err
	}
	return uniqueRelatedTargets(append(result, mappings...)), nil
}

func (p *DoctrineRelatedCodeLensProvider) externalMappingTargets(
	className,
	excludePath string,
) ([]string, error) {
	declarations, err := p.doctrine.ModelDeclarations(className)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, declaration := range declarations {
		if declaration.File == "" ||
			filepath.Clean(declaration.File) == filepath.Clean(excludePath) ||
			declaration.Source != doctrine.XMLSource &&
				declaration.Source != doctrine.YAMLSource {
			continue
		}
		result = append(result, relatedTarget(
			declaration.File,
			relatedSourceLine(
				declaration.File,
				declaration.NameRange.Start,
			),
		))
	}
	return uniqueRelatedTargets(result), nil
}

func sortRelatedCodeLenses(result []protocol.CodeLens) {
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
}

func (p *DoctrineRelatedCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
