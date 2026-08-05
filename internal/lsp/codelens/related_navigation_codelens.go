package codelens

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type RelatedNavigationCodeLensProvider struct {
	twig     *twig.TwigIndexer
	php      *php.PHPIndex
	routes   *symfony.RouteIndexer
	services *symfony.ServiceIndex
}

func NewRelatedNavigationCodeLensProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
	routes *symfony.RouteIndexer,
	services *symfony.ServiceIndex,
) *RelatedNavigationCodeLensProvider {
	return &RelatedNavigationCodeLensProvider{
		twig:     twigIndex,
		php:      phpIndex,
		routes:   routes,
		services: services,
	}
}

func (p *RelatedNavigationCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || request == nil || request.CodeLensParams == nil ||
		request.Document == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return p.phpCodeLenses(ctx, path, request)
	case ".twig":
		return p.twigCodeLenses(ctx, path)
	default:
		return nil, nil
	}
}

func (p *RelatedNavigationCodeLensProvider) phpCodeLenses(
	ctx context.Context,
	path string,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p.twig == nil || p.php == nil || p.routes == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil {
		return nil, nil
	}
	var result []protocol.CodeLens
	for _, reference := range twig.PHPTemplateReferences(
		path,
		request.Document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		files, err := p.twig.GetTwigFilesByRelPath(reference.Template)
		if err != nil {
			return nil, err
		}
		var targets []string
		for _, file := range files {
			targets = append(targets, relatedTarget(file.Path, 1))
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open related template"
		if len(targets) > 1 {
			title = fmt.Sprintf("Open %d related templates", len(targets))
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				reference.Range,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	routeTargets, err := p.routeTargetsByMethod(ctx)
	if err != nil {
		return nil, err
	}
	for _, symbol := range p.php.SemanticSnapshot().SymbolsIn(path) {
		if symbol.Kind != semantic.MethodSymbol {
			continue
		}
		targets := routeTargets[strings.ToLower(symbol.FullyQualified)]
		if len(targets) == 0 {
			continue
		}
		title := "Open route definition"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d route definitions",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				symbol.SelectionRange,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		return result[left].Command.Title < result[right].Command.Title
	})
	return result, nil
}

func (p *RelatedNavigationCodeLensProvider) twigCodeLenses(
	ctx context.Context,
	path string,
) ([]protocol.CodeLens, error) {
	if p.twig == nil || p.php == nil || p.routes == nil {
		return nil, nil
	}
	references, err := p.twig.GetTemplateReferences(
		twig.TemplateNames(path)...,
	)
	if err != nil {
		return nil, err
	}
	var phpReferences []twig.TemplateReference
	var controllerTargets []string
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !strings.EqualFold(filepath.Ext(reference.FilePath), ".php") {
			continue
		}
		switch reference.Kind {
		case twig.TemplateRenderReference,
			twig.TemplateAttributeReference,
			twig.TemplateAnnotationReference:
		default:
			continue
		}
		phpReferences = append(phpReferences, reference)
		controllerTargets = append(controllerTargets, relatedTarget(
			reference.FilePath,
			relatedSourceLine(reference.FilePath, reference.Range.Start),
		))
	}
	controllerTargets = uniqueRelatedTargets(controllerTargets)
	routeTargetsByMethod, err := p.routeTargetsByMethod(ctx)
	if err != nil {
		return nil, err
	}
	var routeTargets []string
	for _, reference := range phpReferences {
		for _, symbol := range p.php.SemanticSnapshot().SymbolsIn(
			reference.FilePath,
		) {
			if symbol.Kind != semantic.MethodSymbol ||
				!symbol.BodyRange.Contains(reference.Range.Start) {
				continue
			}
			routeTargets = append(
				routeTargets,
				routeTargetsByMethod[strings.ToLower(
					symbol.FullyQualified,
				)]...,
			)
		}
	}
	routeTargets = uniqueRelatedTargets(routeTargets)
	var result []protocol.CodeLens
	top := protocol.Range{}
	if len(controllerTargets) != 0 {
		title := "Open rendering PHP location"
		if len(controllerTargets) > 1 {
			title = fmt.Sprintf(
				"Open %d rendering PHP locations",
				len(controllerTargets),
			)
		}
		result = append(result, relatedLens(
			top,
			title,
			controllerTargets,
		))
	}
	if len(routeTargets) != 0 {
		title := "Open related route"
		if len(routeTargets) > 1 {
			title = fmt.Sprintf(
				"Open %d related routes",
				len(routeTargets),
			)
		}
		result = append(result, relatedLens(
			top,
			title,
			routeTargets,
		))
	}
	return result, nil
}

func (p *RelatedNavigationCodeLensProvider) routeTargetsByMethod(
	ctx context.Context,
) (map[string][]string, error) {
	result := make(map[string][]string)
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reference, found := symfony.ParseControllerReference(route.Controller)
		if !found {
			continue
		}
		method := strings.TrimPrefix(reference.Target, "\\") +
			"::" + reference.Method
		if !strings.Contains(reference.Target, "\\") &&
			p.services != nil {
			resolution, resolveErr := symfony.ResolveControllerReference(
				reference,
				p.services,
				p.php,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if resolution.MethodFound {
				method = resolution.Method.FullyQualified
			}
		}
		key := strings.ToLower(strings.TrimPrefix(method, "\\"))
		result[key] = append(
			result[key],
			relatedTarget(route.FilePath, route.Line),
		)
	}
	for key, targets := range result {
		result[key] = uniqueRelatedTargets(targets)
	}
	return result, nil
}

func relatedLens(
	rng protocol.Range,
	title string,
	targets []string,
) protocol.CodeLens {
	return protocol.CodeLens{
		Range: rng,
		Command: &protocol.Command{
			Title:     title,
			Command:   "shopware.openReferences",
			Arguments: []any{targets},
		},
	}
}

func relatedProtocolRange(
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

func relatedTarget(path string, line int) string {
	if path == "" {
		return ""
	}
	if line < 1 {
		line = 1
	}
	return uriutil.FileURIWithFragment(path, strconv.Itoa(line))
}

func relatedSourceLine(path string, offset uint32) int {
	source, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	line, _ := cst.NewLineIndex(string(source)).PositionUTF16(offset)
	return int(line) + 1
}

func uniqueRelatedTargets(targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == "" {
			continue
		}
		if _, duplicate := seen[target]; duplicate {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	sort.Strings(result)
	return result
}

func (p *RelatedNavigationCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
