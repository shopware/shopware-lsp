package reference

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type RouteReferenceProvider struct {
	routeIndex      *symfony.RouteIndexer
	routeUsageIndex *symfony.RouteUsageIndexer
}

func NewRouteReferenceProvider(routeIndex *symfony.RouteIndexer, routeUsageIndex *symfony.RouteUsageIndexer) *RouteReferenceProvider {
	return &RouteReferenceProvider{
		routeIndex:      routeIndex,
		routeUsageIndex: routeUsageIndex,
	}
}

func (r *RouteReferenceProvider) GetReferences(ctx context.Context, params *lsp.ReferenceRequest) ([]protocol.Location, error) {
	if params.Node == nil {
		return nil, nil
	}

	switch filepath.Ext(params.TextDocument.URI) {
	case ".php":
		if symfony.IsPHPRouteNameInContext(ctx, params.Node) {
			name := phpquery.StringValue(params.Node)
			routes, err := r.routeIndex.GetRoute(name)
			if err != nil {
				return nil, err
			}
			return r.referencesForRoutes(routes, params.Context.IncludeDeclaration)
		}
		return r.getReferencesForPHP(ctx, params)
	case ".twig":
		if symfony.IsTwigRouteName(params.Node) {
			name := twigquery.StringValue(
				twigquery.LiteralStringAt(params.Node),
			)
			routes, err := r.routeIndex.GetRoute(name)
			if err != nil {
				return nil, err
			}
			return r.referencesForRoutes(routes, params.Context.IncludeDeclaration)
		}
		if reference, found := symfony.TwigHTMLRouteReferenceAt(
			params.Node,
		); found {
			routes, err := r.routeIndex.FindRoutesByPath(reference.Value)
			if err != nil {
				return nil, err
			}
			return r.referencesForRoutes(routes, params.Context.IncludeDeclaration)
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func (r *RouteReferenceProvider) getReferencesForPHP(ctx context.Context, params *lsp.ReferenceRequest) ([]protocol.Location, error) {
	methodName := phpquery.MethodName(params.Node)
	className := phpquery.ClassName(params.Node)
	methodFQCN := ""
	if methodName != "" && className != "" {
		if namespace := phpquery.Namespace(params.Root); namespace != "" {
			className = namespace + "\\" + className
		}
		methodFQCN = className + "::" + methodName
	}

	if methodFQCN != "" {
		routes, err := r.routeIndex.GetRoutes()
		if err != nil {
			return nil, err
		}

		route := routes.GetByController(methodFQCN)

		if route != nil {
			return r.referencesForRoutes(
				[]symfony.Route{*route},
				params.Context.IncludeDeclaration,
			)
		}
	}

	return nil, nil
}

func (r *RouteReferenceProvider) referencesForRoutes(
	routes []symfony.Route,
	includeDeclaration bool,
) ([]protocol.Location, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	htmlUsages, err := r.routeUsageIndex.GetHTMLRouteUsages()
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	seen := make(map[string]struct{})
	add := func(location protocol.Location) {
		key := fmt.Sprintf(
			"%s:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	routeNames := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.Name == "" {
			continue
		}
		key := strings.ToLower(route.Name)
		if _, duplicate := routeNames[key]; duplicate {
			continue
		}
		routeNames[key] = struct{}{}
		usages, usageErr := r.routeUsageIndex.GetRoute(route.Name)
		if usageErr != nil {
			return nil, usageErr
		}
		for _, usage := range usages {
			add(routeUsageLocation(usage))
		}
		if includeDeclaration {
			add(protocol.Location{
				URI: uriutil.FileURI(route.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line: route.Line - 1,
					},
					End: protocol.Position{
						Line: route.Line - 1,
					},
				},
			})
		}
	}
	for _, usage := range htmlUsages {
		for _, route := range routes {
			if symfony.RoutePathMatches(route.Path, usage.Path) {
				add(routeUsageLocation(usage))
				break
			}
		}
	}
	return result, nil
}

func routeUsageLocation(usage symfony.RouteUsage) protocol.Location {
	line := usage.Line - 1
	if line < 0 {
		line = 0
	}
	return protocol.Location{
		URI: uriutil.FileURI(usage.File),
		Range: protocol.Range{
			Start: protocol.Position{Line: line},
			End:   protocol.Position{Line: line},
		},
	}
}
