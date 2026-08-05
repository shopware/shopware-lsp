package completion

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type RouteCompletionProvider struct {
	routeIndex *symfony.RouteIndexer
}

func NewRouteCompletionProvider(routeIndexer *symfony.RouteIndexer) *RouteCompletionProvider {
	return &RouteCompletionProvider{routeIndex: routeIndexer}
}

func (p *RouteCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".php":
		return p.phpCompletions(ctx, params)
	case ".twig":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.twigCompletions(ctx, params)
	case ".js", ".ts":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.jsCompletions(ctx, params)
	default:
		return []protocol.CompletionItem{}
	}
}

func (p *RouteCompletionProvider) phpCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if reference, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Route",
	); found {
		return p.routeReferenceCompletions(
			ctx,
			reference,
			params.LineIndex,
		)
	}
	if params.LineIndex != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if reference, found := symfony.PHPRouteAnnotationPathReferenceAt(
			string(params.DocumentContent),
			offset,
		); found {
			return p.routePathCompletions(
				ctx,
				reference.Range,
				params.LineIndex,
			)
		}
		if reference, found := symfony.PHPRouteAnnotationNameReferenceAt(
			string(params.DocumentContent),
			offset,
		); found {
			return phpRouteNameSuggestionCompletion(
				params,
				reference.Range,
				offset,
			)
		}
	}
	if params.Node == nil {
		return []protocol.CompletionItem{}
	}
	if reference, found := symfony.PHPRoutePathReferenceAt(
		params.Node,
	); found {
		return p.routePathCompletions(
			ctx,
			reference.Range,
			params.LineIndex,
		)
	}
	if reference, found := symfony.PHPRouteNameReferenceAt(
		params.Node,
	); found {
		offset := reference.Range.Start
		if params.LineIndex != nil {
			offset = params.LineIndex.OffsetUTF16(
				uint32(params.Position.Line),
				uint32(params.Position.Character),
			)
		}
		return phpRouteNameSuggestionCompletion(
			params,
			reference.Range,
			offset,
		)
	}
	if symfony.IsPHPRouteNameInContext(ctx, params.Node) {
		return p.routeCompletions(ctx)
	}
	if routeName, ok := symfony.PHPRouteParameterContext(params.Node); ok {
		return p.routeParameterCompletions(ctx, routeName)
	}

	return []protocol.CompletionItem{}
}

func (p *RouteCompletionProvider) routeReferenceCompletions(
	ctx context.Context,
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	items := p.routeCompletions(ctx)
	editRange := routeHTMLCompletionRange(rng, lineIndex)
	for index := range items {
		items[index].TextEdit = protocol.TextEdit{
			Range:   editRange,
			NewText: items[index].Label,
		}
	}
	return items
}

func phpRouteNameSuggestionCompletion(
	request *lsp.CompletionRequest,
	rng cst.TextRange,
	offset uint32,
) []protocol.CompletionItem {
	name := symfony.PHPRouteNameSuggestion(
		request.Root,
		request.Node,
		offset,
	)
	if name == "" {
		return nil
	}
	method := phpquery.MethodAt(request.Node)
	detail := "Generated Symfony route name"
	if method != nil {
		className := phpquery.ClassName(method)
		methodName := phpquery.MethodName(method)
		if className != "" && methodName != "" {
			detail += " · " + className + "::" + methodName
		}
	}
	return []protocol.CompletionItem{{
		Label:  name,
		Kind:   int(protocol.ReferenceCompletion),
		Detail: detail,
		TextEdit: protocol.TextEdit{
			Range:   routeHTMLCompletionRange(rng, request.LineIndex),
			NewText: name,
		},
	}}
}

func (p *RouteCompletionProvider) routePathCompletions(
	ctx context.Context,
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	routes, err := p.routeIndex.GetRoutes()
	if err != nil {
		return nil
	}
	slices.SortFunc(routes, func(left, right symfony.Route) int {
		if left.Path == right.Path {
			return strings.Compare(left.Name, right.Name)
		}
		return strings.Compare(left.Path, right.Path)
	})
	editRange := routeHTMLCompletionRange(rng, lineIndex)
	seen := make(map[string]struct{}, len(routes))
	result := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil
		}
		if route.Path == "" || route.Name == "" ||
			strings.HasPrefix(route.Name, "_") {
			continue
		}
		if _, duplicate := seen[route.Path]; duplicate {
			continue
		}
		seen[route.Path] = struct{}{}
		result = append(result, protocol.CompletionItem{
			Label:      route.Path,
			FilterText: route.Path + " " + route.Name,
			Kind:       int(protocol.ReferenceCompletion),
			Detail:     route.Name,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: route.Path,
			},
		})
	}
	return result
}

func (p *RouteCompletionProvider) twigCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if symfony.IsTwigRouteName(params.Node) {
		return p.routeCompletions(ctx)
	}
	if routeName, ok := symfony.TwigRouteParameterContext(params.Node); ok {
		return p.routeParameterCompletions(ctx, routeName)
	}
	if reference, found := symfony.TwigHTMLRouteReferenceAt(
		params.Node,
	); found {
		return p.htmlRouteCompletions(
			ctx,
			reference,
			params.LineIndex,
		)
	}

	return []protocol.CompletionItem{}
}

func (p *RouteCompletionProvider) jsCompletions(
	ctx context.Context,
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	reference, found := symfony.JSRouteURLReferenceAt(params.Node)
	if !found {
		return []protocol.CompletionItem{}
	}
	routes, err := p.routeIndex.GetRoutes()
	if err != nil {
		return nil
	}
	slices.SortFunc(routes, func(left, right symfony.Route) int {
		return strings.Compare(left.Name, right.Name)
	})
	seen := make(map[string]struct{}, len(routes))
	result := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil
		}
		if route.Name == "" || route.Path == "" {
			continue
		}
		if _, duplicate := seen[route.Name]; duplicate {
			continue
		}
		seen[route.Name] = struct{}{}
		detail := route.Path
		if len(route.Methods) != 0 {
			detail += " [" + strings.Join(route.Methods, "|") + "]"
		}
		edit := protocol.TextEdit{
			Range: routeHTMLCompletionRange(
				reference.Range,
				params.LineIndex,
			),
			NewText: route.Path,
		}
		if !strings.HasPrefix(route.Name, "_") {
			result = append(result, protocol.CompletionItem{
				Label:      route.Path,
				FilterText: route.Path + " " + route.Name,
				Kind:       int(protocol.ReferenceCompletion),
				Detail:     route.Name,
				TextEdit:   edit,
			})
		}
		result = append(result, protocol.CompletionItem{
			Label:      route.Name,
			FilterText: route.Name + " " + route.Path,
			Kind:       int(protocol.ReferenceCompletion),
			Detail:     detail,
			TextEdit:   edit,
		})
	}
	return result
}

func (p *RouteCompletionProvider) htmlRouteCompletions(
	ctx context.Context,
	reference symfony.TwigHTMLRouteReference,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	routes, err := p.routeIndex.GetRoutes()
	if err != nil {
		return nil
	}
	slices.SortFunc(routes, func(left, right symfony.Route) int {
		return strings.Compare(left.Name, right.Name)
	})
	seen := make(map[string]struct{}, len(routes))
	result := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil
		}
		if route.Name == "" {
			continue
		}
		if _, duplicate := seen[route.Name]; duplicate {
			continue
		}
		seen[route.Name] = struct{}{}
		detail := route.Path
		if len(route.Methods) != 0 {
			detail += " [" + strings.Join(route.Methods, "|") + "]"
		}
		result = append(result, protocol.CompletionItem{
			Label:      route.Name,
			FilterText: route.Name + " " + route.Path,
			Kind:       int(protocol.ReferenceCompletion),
			Detail:     detail,
			TextEdit: protocol.TextEdit{
				Range: routeHTMLCompletionRange(
					reference.Node.RangeTrimmedTrivia(),
					lineIndex,
				),
				NewText: twigPathExpression(reference, route),
			},
		})
	}
	return result
}

func routeHTMLCompletionRange(
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

func twigPathExpression(
	reference symfony.TwigHTMLRouteReference,
	route symfony.Route,
) string {
	quote := byte('\'')
	if reference.Container != nil {
		text := strings.TrimSpace(reference.Container.Text())
		if len(text) != 0 && text[0] == '\'' {
			quote = '"'
		}
	}
	quoted := func(value string) string {
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		if quote == '\'' {
			escaped = strings.ReplaceAll(escaped, `'`, `\'`)
		} else {
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		}
		return string(quote) + escaped + string(quote)
	}
	var result strings.Builder
	result.WriteString("{{ path(")
	result.WriteString(quoted(route.Name))
	if parameters := route.Parameters(); len(parameters) != 0 {
		result.WriteString(", {")
		for index, parameter := range parameters {
			if index != 0 {
				result.WriteString(", ")
			}
			result.WriteString(quoted(parameter))
			result.WriteString(": ")
			result.WriteString(quoted("x"))
		}
		result.WriteByte('}')
	}
	result.WriteString(") }}")
	return result.String()
}

func (p *RouteCompletionProvider) routeCompletions(ctx context.Context) []protocol.CompletionItem {
	routes, err := p.routeIndex.GetRoutes()
	if err != nil {
		return nil
	}
	slices.SortFunc(routes, func(left, right symfony.Route) int {
		return strings.Compare(left.Name, right.Name)
	})
	seen := make(map[string]struct{}, len(routes))
	completions := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil
		}
		if _, exists := seen[route.Name]; exists {
			continue
		}
		seen[route.Name] = struct{}{}
		detail := route.Path
		if len(route.Methods) != 0 {
			detail += " [" + strings.Join(route.Methods, "|") + "]"
		}
		if parameters := route.Parameters(); len(parameters) != 0 {
			detail += " (" + strings.Join(parameters, ", ") + ")"
		}
		completions = append(completions, protocol.CompletionItem{
			Label:  route.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: detail,
		})
	}
	return completions
}

func (p *RouteCompletionProvider) routeParameterCompletions(
	ctx context.Context,
	routeName string,
) []protocol.CompletionItem {
	routes, err := p.routeIndex.GetRoute(routeName)
	if err != nil {
		return nil
	}
	var names []string
	seen := make(map[string]struct{})
	for _, route := range routes {
		for _, name := range route.Parameters() {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	if _, exists := seen["_fragment"]; !exists {
		names = append(names, "_fragment")
	}
	slices.Sort(names)
	completions := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return nil
		}
		completions = append(completions, protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.FieldCompletion),
			Detail: "Route parameter for " + routeName,
		})
	}
	return completions
}

func (p *RouteCompletionProvider) GetTriggerCharacters() []string {
	return []string{}
}
