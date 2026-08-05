package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigConstantReferenceProvider struct {
	index    *twig.TwigIndexer
	phpIndex *php.PHPIndex
}

func NewTwigConstantReferenceProvider(
	index *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigConstantReferenceProvider {
	return &TwigConstantReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TwigConstantReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		request == nil || request.Node == nil ||
		request.Root == nil || request.LineIndex == nil ||
		request.Document == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	targets, fromTwig := p.targetsAt(
		ctx,
		request,
		path,
		offset,
	)
	if len(targets) == 0 {
		return nil, nil
	}

	var current []twig.ConstantReference
	if fromTwig {
		current = twig.ConstantReferencesInDocument(
			path,
			request.Root,
			twig.PHPAccessResolver{
				PHP:  p.phpIndex,
				Twig: p.index,
			},
		)
	}
	var result []protocol.Location
	seenTargets := make(map[string]struct{})
	for _, target := range targets {
		key := twig.ConstantReferenceKey(target)
		if key == "" {
			continue
		}
		if _, duplicate := seenTargets[key]; duplicate {
			continue
		}
		seenTargets[key] = struct{}{}
		indexed, queryErr := p.index.GetConstantReferences(target)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, reference := range indexed {
			if fromTwig && filepath.Clean(reference.FilePath) ==
				filepath.Clean(path) {
				continue
			}
			if location, found := twigConstantReferenceLocation(
				reference,
				"",
				nil,
			); found {
				result = append(result, location)
			}
		}
		for _, reference := range current {
			if twig.ConstantReferenceKey(reference) != key {
				continue
			}
			if location, found := twigConstantReferenceLocation(
				reference,
				path,
				request.LineIndex,
			); found {
				result = append(result, location)
			}
		}
		if fromTwig && request.Context.IncludeDeclaration {
			for _, symbol := range referenceConstantSymbols(
				p.phpIndex,
				target,
			) {
				if location, found := twigConstantSymbolLocation(
					symbol,
				); found {
					result = append(result, location)
				}
			}
		}
	}
	return uniqueTwigConstantLocations(result), nil
}

func (p *TwigConstantReferenceProvider) targetsAt(
	ctx context.Context,
	request *lsp.ReferenceRequest,
	path string,
	offset uint32,
) ([]twig.ConstantReference, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".twig":
		return twig.ConstantReferencesAt(
			path,
			request.Root,
			request.Node,
			twig.PHPAccessResolver{
				PHP:  p.phpIndex,
				Twig: p.index,
			},
		), true
	case ".php":
		document, snapshot := p.phpSemanticState(
			ctx,
			request,
			path,
		)
		if document == nil || snapshot == nil {
			return nil, false
		}
		symbol, found := php.SymbolAt(document, snapshot, offset)
		if !found {
			return nil, false
		}
		target, found := twigConstantTargetFromSymbol(
			snapshot,
			symbol,
		)
		if !found {
			return nil, false
		}
		return []twig.ConstantReference{target}, false
	default:
		return nil, false
	}
}

func (p *TwigConstantReferenceProvider) phpSemanticState(
	ctx context.Context,
	request *lsp.ReferenceRequest,
	path string,
) (*semantic.Document, *semantic.Snapshot) {
	if phpContext := php.GetPHPContext(ctx); phpContext != nil &&
		phpContext.Document != nil && phpContext.Snapshot != nil {
		return phpContext.Document, phpContext.Snapshot
	}
	if document, found := p.phpIndex.SemanticDocument(path); found {
		return document, p.phpIndex.SemanticSnapshot()
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		(*phpsyntax.Node)(request.Root),
	)
	return document, p.phpIndex.SemanticSnapshot().WithDocument(document)
}

func twigConstantTargetFromSymbol(
	snapshot *semantic.Snapshot,
	symbol semantic.Symbol,
) (twig.ConstantReference, bool) {
	switch symbol.Kind {
	case semantic.GlobalConstantSymbol:
		return twig.ConstantReference{
			Name: strings.TrimPrefix(symbol.FullyQualified, "\\"),
		}, true
	case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
		container, found := snapshot.Symbol(symbol.Container)
		if !found || !container.IsClassLike() {
			return twig.ConstantReference{}, false
		}
		return twig.ConstantReference{
			Class: container.FullyQualified,
			Name:  symbol.Name,
		}, true
	default:
		return twig.ConstantReference{}, false
	}
}

func referenceConstantSymbols(
	index *php.PHPIndex,
	target twig.ConstantReference,
) []semantic.Symbol {
	if target.Class != "" {
		return index.FindConstants(target.Class, target.Name)
	}
	return index.FindGlobalConstants(target.Name)
}

func twigConstantReferenceLocation(
	reference twig.ConstantReference,
	currentPath string,
	currentLineIndex *cst.LineIndex,
) (protocol.Location, bool) {
	lineIndex := currentLineIndex
	if lineIndex == nil ||
		filepath.Clean(reference.FilePath) != filepath.Clean(currentPath) {
		content, err := os.ReadFile(reference.FilePath)
		if err != nil {
			return protocol.Location{}, false
		}
		lineIndex = cst.NewLineIndex(string(content))
	}
	return protocol.Location{
		URI: uriutil.FileURI(reference.FilePath),
		Range: templateReferenceProtocolRange(
			reference.Range,
			lineIndex,
		),
	}, true
}

func twigConstantSymbolLocation(
	symbol semantic.Symbol,
) (protocol.Location, bool) {
	content, err := os.ReadFile(symbol.Path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(symbol.Path),
		Range: templateReferenceProtocolRange(
			symbol.SelectionRange,
			cst.NewLineIndex(string(content)),
		),
	}, true
}

func uniqueTwigConstantLocations(
	locations []protocol.Location,
) []protocol.Location {
	seen := make(map[string]struct{}, len(locations))
	result := make([]protocol.Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}
