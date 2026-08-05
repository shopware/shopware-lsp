package definition

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type RouteDefinitionProvider struct {
	routeIndex       *symfony.RouteIndexer
	resourceResolver *symfony.RouteResourceResolver
}

func NewRouteDefinitionProvider(
	routeIndexer *symfony.RouteIndexer,
	phpIndexes ...*php.PHPIndex,
) *RouteDefinitionProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &RouteDefinitionProvider{
		routeIndex: routeIndexer,
		resourceResolver: symfony.NewRouteResourceResolver(
			phpIndex,
		),
	}
}

func (p *RouteDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".php":
		if params.Node == nil {
			return []protocol.Location{}
		}
		if locations := routeResourceDefinitions(
			params,
			p.resourceResolver,
		); len(locations) != 0 {
			return locations
		}
		return p.phpDefinition(ctx, params)
	case ".yaml", ".yml", ".xml":
		return routeResourceDefinitions(params, p.resourceResolver)
	case ".twig":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.twigDefinition(ctx, params)
	case ".js", ".ts":
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.jsDefinition(ctx, params)
	default:
		return []protocol.Location{}
	}
}

func routeResourceDefinitions(
	request *lsp.DefinitionRequest,
	resolver *symfony.RouteResourceResolver,
) []protocol.Location {
	if request == nil || request.Node == nil {
		return nil
	}
	reference, found := symfony.RouteResourceReferenceAt(request.Node)
	if !found {
		return nil
	}
	currentPath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	for _, path := range resolver.Files(currentPath, reference) {
		locations = append(locations, protocol.Location{
			URI:   uriutil.FileURI(path),
			Range: protocol.Range{},
		})
	}
	return locations
}

func (p *RouteDefinitionProvider) phpDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if _, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Route",
	); found && p != nil && p.routeIndex != nil {
		routes, _ := p.routeIndex.GetRoute(phpquery.StringValue(params.Node))
		return routeLocations(routes)
	}
	if reference, found := symfony.PHPRoutePathReferenceAt(params.Node); found &&
		params.LineIndex != nil && params.DefinitionParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if prefix, ok := reference.PrefixAt(offset); ok {
			routes, _ := p.routeIndex.FindRoutesByPathPrefix(
				prefix,
				reference.Value,
			)
			return routeLocations(routes)
		}
	}
	if symfony.IsPHPRouteNameInContext(ctx, params.Node) {
		currentText := phpquery.StringValue(params.Node)

		locations, _ := p.routeIndex.GetRoute(currentText)

		var result []protocol.Location
		for _, location := range locations {
			result = append(result, protocol.Location{
				URI: uriutil.FileURI(location.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
					End: protocol.Position{
						Line:      location.Line - 1,
						Character: 0,
					},
				},
			})
		}

		return result
	}
	if routeName, parameter, found :=
		symfony.PHPRouteParameterReferenceAt(params.Node); found {
		routes, _ := p.routeIndex.GetRoute(routeName)
		return routeParameterLocations(routes, parameter)
	}

	return []protocol.Location{}
}

func (p *RouteDefinitionProvider) twigDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	if symfony.IsTwigRouteName(params.Node) {
		routes, _ := p.routeIndex.GetRoute(twigquery.StringValue(twigquery.LiteralStringAt(params.Node)))
		return routeLocations(routes)
	}
	if routeName, parameter, found :=
		symfony.TwigRouteParameterReferenceAt(params.Node); found {
		routes, _ := p.routeIndex.GetRoute(routeName)
		return routeParameterLocations(routes, parameter)
	}
	if reference, found := symfony.TwigHTMLRouteReferenceAt(
		params.Node,
	); found {
		routes, _ := p.routeIndex.FindRoutesByPath(reference.Value)
		return routeLocations(routes)
	}

	return []protocol.Location{}
}

func routeParameterLocations(
	routes []symfony.Route,
	parameter string,
) []protocol.Location {
	matched := make([]symfony.Route, 0, len(routes))
	for _, route := range routes {
		if slices.Contains(route.Parameters(), parameter) {
			matched = append(matched, route)
		}
	}
	return routeLocations(matched)
}

func (p *RouteDefinitionProvider) jsDefinition(
	_ context.Context,
	params *lsp.DefinitionRequest,
) []protocol.Location {
	reference, found := symfony.JSRouteURLReferenceAt(params.Node)
	if !found || reference.Value == "" {
		return []protocol.Location{}
	}
	routes, _ := p.routeIndex.FindRoutesByPath(reference.Value)
	return routeLocations(routes)
}

func routeLocations(routes []symfony.Route) []protocol.Location {
	var locations []protocol.Location
	for _, route := range routes {
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(route.FilePath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      route.Line - 1,
					Character: 0,
				},
				End: protocol.Position{
					Line:      route.Line - 1,
					Character: 0,
				},
			},
		})
	}
	return locations
}
