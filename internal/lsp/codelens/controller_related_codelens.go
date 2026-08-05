package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// ControllerRelatedCodeLensProvider ports the reference plugin's
// TwigControllerUsageControllerRelatedGotoCollector in both directions.
type ControllerRelatedCodeLensProvider struct {
	usages   *symfony.RouteUsageIndexer
	services *symfony.ServiceIndex
	php      *php.PHPIndex
}

func NewControllerRelatedCodeLensProvider(
	usages *symfony.RouteUsageIndexer,
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ControllerRelatedCodeLensProvider {
	return &ControllerRelatedCodeLensProvider{
		usages: usages, services: services, php: phpIndex,
	}
}

func (p *ControllerRelatedCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.usages == nil || p.php == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil {
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
		return p.twigCodeLenses(ctx, request)
	default:
		return nil, nil
	}
}

func (p *ControllerRelatedCodeLensProvider) phpCodeLenses(
	ctx context.Context,
	path string,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	targetsByMethod, err := p.twigTargetsByMethod(ctx)
	if err != nil {
		return nil, err
	}
	var result []protocol.CodeLens
	snapshot := p.php.SemanticSnapshot()
	for _, method := range snapshot.SymbolsIn(path) {
		if method.Kind != semantic.MethodSymbol ||
			method.Visibility != semantic.Public {
			continue
		}
		class, found := snapshot.Symbol(method.Container)
		if !found {
			continue
		}
		key := controllerMethodKey(class.FullyQualified, method.Name)
		targets := uniqueRelatedTargets(targetsByMethod[key])
		if len(targets) == 0 {
			continue
		}
		title := "Open Twig controller usage"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d Twig controller usages",
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
	sort.Slice(result, func(left, right int) bool {
		return result[left].Range.Start.Line < result[right].Range.Start.Line
	})
	return result, nil
}

func (p *ControllerRelatedCodeLensProvider) twigCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	var result []protocol.CodeLens
	for _, reference := range symfony.TwigControllerReferences(
		request.Document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resolution, err := symfony.ResolveControllerReference(
			reference.ControllerReference,
			p.services,
			p.php,
		)
		if err != nil {
			return nil, err
		}
		var symbol semantic.Symbol
		switch {
		case resolution.MethodDeclared:
			symbol = resolution.Method
		case resolution.ClassFound:
			symbol = resolution.Class
		default:
			continue
		}
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		title := "Open controller method"
		if symbol.Kind != semantic.MethodSymbol {
			title = "Open controller class"
		}
		result = append(result, relatedLens(
			relatedProtocolRange(reference.Range, request.Document.LineIndex),
			title,
			[]string{relatedTarget(
				symbol.Path,
				relatedSourceLine(symbol.Path, rng.Start),
			)},
		))
	}
	return result, nil
}

func (p *ControllerRelatedCodeLensProvider) twigTargetsByMethod(
	ctx context.Context,
) (map[string][]string, error) {
	usages, err := p.usages.GetAllControllerUsages()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, usage := range usages {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reference := symfony.ControllerReference{
			Value:  usage.Controller,
			Target: usage.Target,
			Method: usage.Method,
		}
		resolution, err := symfony.ResolveControllerReference(
			reference,
			p.services,
			p.php,
		)
		if err != nil {
			return nil, err
		}
		if !resolution.MethodDeclared {
			continue
		}
		key := controllerMethodKey(
			resolution.Class.FullyQualified,
			resolution.Method.Name,
		)
		result[key] = append(result[key], relatedTarget(
			usage.File,
			relatedSourceLine(usage.File, usage.Range.Start),
		))
	}
	return result, nil
}

func controllerMethodKey(className, methodName string) string {
	return strings.ToLower(
		strings.TrimLeft(className, `\`) + "::" + methodName,
	)
}

func (p *ControllerRelatedCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	_ *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return nil, nil
}
