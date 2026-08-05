package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type RouteHoverProvider struct {
	routeIndex *symfony.RouteIndexer
}

func NewRouteHoverProvider(routeIndex *symfony.RouteIndexer) *RouteHoverProvider {
	return &RouteHoverProvider{routeIndex: routeIndex}
}

func (p *RouteHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.routeIndex == nil || request == nil || request.Node == nil {
		return nil, nil
	}

	var literal *cst.Node
	var routeName string
	var routes []symfony.Route
	var err error
	htmlURL := false
	switch strings.ToLower(filepath.Ext(request.TextDocument.URI)) {
	case ".php":
		if !symfony.IsPHPRouteNameInContext(ctx, request.Node) {
			return nil, nil
		}
		literal = phpquery.StringAt(request.Node)
		routeName = phpquery.StringValue(literal)
		routes, err = p.routeIndex.GetRoute(routeName)
	case ".twig":
		if symfony.IsTwigRouteName(request.Node) {
			literal = twigquery.LiteralStringAt(request.Node)
			routeName = twigquery.StringValue(literal)
			routes, err = p.routeIndex.GetRoute(routeName)
		} else if reference, found := symfony.TwigHTMLRouteReferenceAt(
			request.Node,
		); found {
			literal = reference.Node
			routeName = reference.Value
			routes, err = p.routeIndex.FindRoutesByPath(routeName)
			htmlURL = true
		} else {
			return nil, nil
		}
	default:
		return nil, nil
	}
	if err != nil || len(routes) == 0 {
		return nil, err
	}
	slices.SortFunc(routes, func(left, right symfony.Route) int {
		return strings.Compare(left.FilePath, right.FilePath)
	})

	var markdown strings.Builder
	title := "Symfony route"
	if htmlURL {
		title = "Symfony route URL"
	}
	fmt.Fprintf(&markdown, "**%s** `%s`\n\n", title, routeName)
	for index, route := range routes {
		if index != 0 {
			markdown.WriteString("\n---\n\n")
		}
		if route.Path != "" {
			fmt.Fprintf(&markdown, "- Path: `%s`\n", escapeRouteMarkdown(route.Path))
		}
		if htmlURL {
			fmt.Fprintf(
				&markdown,
				"- Name: `%s`\n",
				escapeRouteMarkdown(route.Name),
			)
		}
		if route.Controller != "" {
			fmt.Fprintf(
				&markdown,
				"- Controller: `%s`\n",
				escapeRouteMarkdown(route.Controller),
			)
		}
		if len(route.Methods) != 0 {
			fmt.Fprintf(&markdown, "- Methods: `%s`\n", strings.Join(route.Methods, "`, `"))
		}
		if parameters := route.Parameters(); len(parameters) != 0 {
			fmt.Fprintf(&markdown, "- Parameters: `%s`\n", strings.Join(parameters, "`, `"))
		}
		fmt.Fprintf(
			&markdown,
			"- Defined at: `%s:%d`\n",
			escapeRouteMarkdown(route.FilePath),
			route.Line,
		)
	}

	rng := literal.RangeTrimmedTrivia()
	startLine, startColumn := request.LineIndex.PositionUTF16(rng.Start)
	endLine, endColumn := request.LineIndex.PositionUTF16(rng.End)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startColumn),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endColumn),
			},
		},
	}, nil
}

func escapeRouteMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
