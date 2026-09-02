package inlay

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// RoutePathProvider is the portable counterpart of the reference plugin's
// inline route-name folding. It leaves source text visible and adds the
// resolved path as a clickable inlay instead.
type RoutePathProvider struct {
	routes   *symfony.RouteIndexer
	phpIndex *php.PHPIndex
}

func NewRoutePathProvider(
	routes *symfony.RouteIndexer,
	phpIndexes ...*php.PHPIndex,
) *RoutePathProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &RoutePathProvider{
		routes:   routes,
		phpIndex: phpIndex,
	}
}

type routePathHintCandidate struct {
	name     string
	position uint32
}

func (p *RoutePathProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.routes == nil ||
		request == nil || request.InlayHintParams == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	candidates := routePathHintCandidates(
		ctx,
		request.Document,
		p.phpIndex,
	)
	if len(candidates) == 0 {
		return nil, nil
	}
	rangeStart, rangeEnd := inlayHintByteRange(request)
	seen := make(map[string]struct{}, len(candidates))
	result := make([]protocol.InlayHint, 0, len(candidates))
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return result, nil
		}
		if candidate.position < rangeStart || candidate.position > rangeEnd {
			continue
		}
		key := fmt.Sprintf("%d\x00%s", candidate.position, candidate.name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		routes, err := p.routes.GetRoute(candidate.name)
		if err != nil {
			return nil, err
		}
		routes = routePathHintTargets(routes)
		if len(routes) == 0 {
			continue
		}
		primary := routes[0]
		line, character := request.Document.LineIndex.PositionUTF16(
			candidate.position,
		)
		part := protocol.InlayHintLabelPart{
			Value:   routePathHintLabel(routes),
			Tooltip: "Open route " + candidate.name,
		}
		if primary.FilePath != "" && primary.Line > 0 {
			part.Location = &protocol.Location{
				URI: uriutil.FileURI(primary.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line: primary.Line - 1,
					},
					End: protocol.Position{
						Line: primary.Line - 1,
					},
				},
			}
		}
		result = append(result, protocol.InlayHint{
			Position: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			Label:       []protocol.InlayHintLabelPart{part},
			Kind:        protocol.InlayHintKindType,
			Tooltip:     routePathHintTooltip(candidate.name, routes),
			PaddingLeft: true,
		})
	}
	return result, nil
}

func routePathHintCandidates(
	ctx context.Context,
	document *lsp.TextDocument,
	phpIndex *php.PHPIndex,
) []routePathHintCandidate {
	root := document.SyntaxTree.Root
	switch document.SyntaxLanguage {
	case language.PHP:
		validationContext := ctx
		if phpIndex != nil {
			path, _ := uriutil.Path(document.URI)
			validationContext = phpIndex.AddDocumentContext(
				ctx,
				path,
				document.Version,
				root,
				root,
			)
		}
		var result []routePathHintCandidate
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if !symfony.IsPHPRouteNameInContext(
				validationContext,
				literal,
			) {
				continue
			}
			name := phpquery.StringValue(literal)
			if !symfony.IsStaticRouteName(name) {
				continue
			}
			result = append(result, routePathHintCandidate{
				name:     name,
				position: literal.RangeTrimmedTrivia().End,
			})
		}
		return result
	case language.Twig:
		var result []routePathHintCandidate
		for literal := range twigquery.IterateNodes(
			root,
			twigsyntax.TwigLiteralString,
		) {
			if !symfony.IsTwigRouteName(literal) ||
				!twigquery.StringIsStatic(literal) {
				continue
			}
			name := twigquery.StringValue(literal)
			if !symfony.IsStaticRouteName(name) {
				continue
			}
			result = append(result, routePathHintCandidate{
				name:     name,
				position: literal.RangeTrimmedTrivia().End,
			})
		}
		return result
	default:
		return nil
	}
}

func routePathHintTargets(routes []symfony.Route) []symfony.Route {
	result := make([]symfony.Route, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.Path == "" {
			continue
		}
		key := strings.ToLower(route.Path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, route)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Path != result[right].Path {
			return result[left].Path < result[right].Path
		}
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Line < result[right].Line
	})
	return result
}

func routePathHintLabel(routes []symfony.Route) string {
	label := "→ " + routes[0].Path
	if len(routes) > 1 {
		label += fmt.Sprintf(" (+%d)", len(routes)-1)
	}
	return label
}

func routePathHintTooltip(
	name string,
	routes []symfony.Route,
) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Route %q", name))
	for _, route := range routes {
		detail := route.Path
		if len(route.Methods) != 0 {
			detail = strings.Join(route.Methods, "|") + " " + detail
		}
		if route.Controller != "" {
			detail += " → " + route.Controller
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

var _ lsp.InlayHintProvider = (*RoutePathProvider)(nil)
