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

// RouteEndpointCodeLensProvider is the portable counterpart of the reference
// plugin's Symfony Endpoints provider. It presents HTTP method/path metadata at
// route declarations and navigates to the resolved controller action.
type RouteEndpointCodeLensProvider struct {
	services *symfony.ServiceIndex
	php      *php.PHPIndex
}

func NewRouteEndpointCodeLensProvider(
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *RouteEndpointCodeLensProvider {
	return &RouteEndpointCodeLensProvider{
		services: services,
		php:      phpIndex,
	}
}

func (p *RouteEndpointCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.php == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	routes, err := endpointRoutesInDocument(path, request.Document)
	if err != nil {
		return nil, err
	}
	var result []protocol.CodeLens
	seen := make(map[string]struct{})
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if route.Path == "" || strings.HasPrefix(route.Name, "_") {
			continue
		}
		reference, found := symfony.ParseControllerReference(route.Controller)
		if !found {
			continue
		}
		resolution, err := symfony.ResolveControllerReference(
			reference,
			p.services,
			p.php,
		)
		if err != nil {
			return nil, err
		}
		target, found := endpointControllerTarget(
			path,
			request.Document,
			resolution,
		)
		if !found {
			continue
		}
		line := route.Line - 1
		if line < 0 {
			line = 0
		}
		title := endpointCodeLensTitle(route)
		key := fmt.Sprintf("%d:%s:%s", line, title, target)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, relatedLens(
			protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			},
			title,
			[]string{target},
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

func endpointRoutesInDocument(
	path string,
	document *lsp.TextDocument,
) ([]symfony.Route, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return symfony.ParsePHPRoutesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		), nil
	case ".yaml", ".yml":
		return symfony.ParseYAMLRoutesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		)
	case ".xml":
		return symfony.ParseXMLRoutesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		)
	default:
		return nil, nil
	}
}

func endpointCodeLensTitle(route symfony.Route) string {
	methods := make([]string, 0, len(route.Methods))
	seen := make(map[string]struct{}, len(route.Methods))
	for _, method := range route.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}
		if _, duplicate := seen[method]; duplicate {
			continue
		}
		seen[method] = struct{}{}
		methods = append(methods, method)
	}
	methodLabel := "ANY"
	if len(methods) != 0 {
		methodLabel = strings.Join(methods, "|")
	}
	title := methodLabel + " " + route.Path
	if route.Name != "" {
		title += " · " + route.Name
	}
	return title
}

func endpointControllerTarget(
	documentPath string,
	document *lsp.TextDocument,
	resolution symfony.ControllerResolution,
) (string, bool) {
	var symbol semantic.Symbol
	switch {
	case resolution.MethodFound:
		symbol = resolution.Method
	case resolution.ClassFound:
		symbol = resolution.Class
	default:
		return "", false
	}
	rng := symbol.SelectionRange
	if rng.Len() == 0 {
		rng = symbol.Range
	}
	line := relatedSourceLine(symbol.Path, rng.Start)
	if filepath.Clean(symbol.Path) == filepath.Clean(documentPath) {
		currentLine, _ := document.LineIndex.PositionUTF16(rng.Start)
		line = int(currentLine) + 1
	}
	return relatedTarget(symbol.Path, line), true
}

func (p *RouteEndpointCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}

var _ lsp.CodeLensProvider = (*RouteEndpointCodeLensProvider)(nil)
