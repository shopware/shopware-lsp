package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type RouteAnalyzer struct {
	routeIndex   *symfony.RouteIndexer
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewRouteAnalyzer(
	routeIndex *symfony.RouteIndexer,
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *RouteAnalyzer {
	return &RouteAnalyzer{
		routeIndex:   routeIndex,
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

func (p *RouteAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.routeIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".php":
		var literals []*cst.Node
		seen := make(map[cst.TextRange]struct{})
		validationContext := ctx
		if p.phpIndex != nil {
			path, _ := uriutil.Path(document.URI)
			validationContext = p.phpIndex.AddDocumentContext(
				ctx,
				path,
				document.Version,
				document.SyntaxTree.Root,
				document.SyntaxTree.Root,
			)
		}
		for _, call := range phpquery.Calls(document.SyntaxTree.Root) {
			literal := phpquery.StringArgument(call, 0)
			if symfony.IsPHPRouteNameInContext(
				validationContext,
				literal,
			) {
				literals = append(literals, literal)
				seen[literal.Range()] = struct{}{}
			}
		}
		if p.phpIndex != nil {
			for _, literal := range phpquery.Nodes(
				document.SyntaxTree.Root,
				phpsyntax.PhpString,
			) {
				if _, exists := seen[literal.Range()]; exists {
					continue
				}
				if _, tags := php.AssistantArgumentTags(
					validationContext,
					literal,
					"Route",
				); len(tags) != 0 {
					literals = append(literals, literal)
				}
			}
		}
		return p.missingRoutes(ctx, document, literals)
	case ".twig":
		literals := twigquery.StringArgumentsInFunctions(
			document.SyntaxTree.Root,
			"seoUrl",
			"url",
			"path",
		)
		for _, reference := range symfony.TwigRouteComparisonReferences(
			document.SyntaxTree.Root,
		) {
			literals = append(literals, reference.Node)
		}
		return p.missingRoutes(ctx, document, literals)
	default:
		return nil, nil
	}
}

func (p *RouteAnalyzer) missingRoutes(
	ctx context.Context,
	document *lsp.TextDocument,
	literals []*cst.Node,
) ([]lsp.Problem, error) {
	var result []lsp.Problem
	var candidateNames []string
	candidatesLoaded := false
	for _, literal := range literals {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		name := routeStringValue(document, literal)
		if !symfony.IsStaticRouteName(name) {
			continue
		}
		routes, err := p.routeIndex.GetRoute(name)
		if err != nil {
			return nil, fmt.Errorf("query Symfony route %q: %w", name, err)
		}
		if len(routes) != 0 {
			deprecated, resolveErr := p.routeControllerDeprecated(routes)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if deprecated {
				result = append(result, lsp.Problem{
					Range: valueNodeTextRange(literal, name),
					Message: "Controller action for route '" +
						name + "' is deprecated",
					Source:   "symfony",
					Severity: protocol.DiagnosticSeverityHint,
					ID:       deprecatedControllerCode,
					Tags: []protocol.DiagnosticTag{
						protocol.DiagnosticTagDeprecated,
					},
				})
			}
			continue
		}
		if !candidatesLoaded {
			allRoutes, queryErr := p.routeIndex.GetRoutes()
			if queryErr != nil {
				return nil, fmt.Errorf("query Symfony routes: %w", queryErr)
			}
			seen := make(map[string]struct{}, len(allRoutes))
			for _, route := range allRoutes {
				if _, exists := seen[route.Name]; route.Name == "" || exists {
					continue
				}
				seen[route.Name] = struct{}{}
				candidateNames = append(candidateNames, route.Name)
			}
			candidatesLoaded = true
		}
		result = append(result, lsp.Problem{
			Range:    valueNodeTextRange(literal, name),
			Message:  fmt.Sprintf("Route '%s' not found", name),
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityError,
			ID:       "symfony.route.missing",
			Payload: map[string]any{
				"routeName":   name,
				"suggestions": suggestion.Similar(name, candidateNames),
			},
		})
	}
	return result, nil
}

func (p *RouteAnalyzer) routeControllerDeprecated(
	routes []symfony.Route,
) (bool, error) {
	if p.serviceIndex == nil || p.phpIndex == nil {
		return false, nil
	}
	for _, route := range routes {
		reference, ok := symfony.ParseControllerReference(route.Controller)
		if !ok {
			continue
		}
		resolution, err := symfony.ResolveControllerReference(
			reference,
			p.serviceIndex,
			p.phpIndex,
		)
		if err != nil {
			return false, fmt.Errorf(
				"resolve controller for route %q: %w",
				route.Name,
				err,
			)
		}
		if resolution.Deprecated() {
			return true, nil
		}
	}
	return false, nil
}

func valueNodeTextRange(node *cst.Node, value string) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	if value == "" {
		return node.RangeTrimmedTrivia()
	}
	nodeRange := node.Range()
	trimmedRange := node.RangeTrimmedTrivia()
	text := node.Text()
	start := int(trimmedRange.Start - nodeRange.Start)
	end := int(trimmedRange.End - nodeRange.Start)
	if start < 0 || end < start || end > len(text) {
		return trimmedRange
	}
	if index := strings.Index(text[start:end], value); index >= 0 {
		return cst.TextRange{
			Start: trimmedRange.Start + uint32(index),
			End:   trimmedRange.Start + uint32(index+len(value)),
		}
	}
	return trimmedRange
}

func routeStringValue(document *lsp.TextDocument, node *cst.Node) string {
	if strings.EqualFold(filepath.Ext(document.URI), ".php") {
		return phpquery.StringValue(node)
	}
	return twigquery.StringValue(node)
}
