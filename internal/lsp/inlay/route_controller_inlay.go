package inlay

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// RouteControllerProvider is the LSP equivalent of the reference plugin's
// route-controller gutter target. It makes service and invokable controller
// indirection visible while retaining ordinary go-to-definition on the source.
type RouteControllerProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewRouteControllerProvider(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *RouteControllerProvider {
	return &RouteControllerProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

type routeControllerHintCandidate struct {
	reference symfony.ControllerReference
	routeName string
	position  uint32
}

func (p *RouteControllerProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.InlayHintParams == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil {
		return nil, nil
	}

	candidates := routeControllerHintCandidates(request.Document)
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
		key := fmt.Sprintf(
			"%d\x00%s",
			candidate.position,
			symfony.ControllerReferenceKey(candidate.reference),
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}

		resolution, err := symfony.ResolveControllerReference(
			candidate.reference,
			p.serviceIndex,
			p.phpIndex,
		)
		if err != nil {
			return nil, err
		}
		if !resolution.MethodFound {
			continue
		}
		className := strings.TrimLeft(
			resolution.Class.FullyQualified,
			`\`,
		)
		methodName := resolution.Method.Name
		line, character := request.Document.LineIndex.PositionUTF16(
			candidate.position,
		)
		label := "→ " + shortControllerClass(className) +
			"::" + methodName
		location := routeControllerMethodLocation(resolution.Method)
		result = append(result, protocol.InlayHint{
			Position: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			Label: []protocol.InlayHintLabelPart{{
				Value:    label,
				Tooltip:  "Open " + className + "::" + methodName,
				Location: &location,
			}},
			Kind: protocol.InlayHintKindType,
			Tooltip: routeControllerHintTooltip(
				candidate,
				className,
				methodName,
			),
			PaddingLeft: true,
		})
	}
	return result, nil
}

func routeControllerHintCandidates(
	document *lsp.TextDocument,
) []routeControllerHintCandidate {
	root := document.SyntaxTree.Root
	switch document.SyntaxLanguage {
	case language.YAML:
		var result []routeControllerHintCandidate
		for _, scalar := range yamlquery.Nodes(root, yamlsyntax.YamlScalar) {
			reference, routeName, ok :=
				symfony.YAMLControllerReference(scalar)
			if !ok {
				continue
			}
			result = append(result, routeControllerHintCandidate{
				reference: reference,
				routeName: routeName,
				position:  scalar.RangeTrimmedTrivia().End,
			})
		}
		return result
	case language.XML:
		var result []routeControllerHintCandidate
		for _, node := range xmlquery.Nodes(
			root,
			xmlsyntax.XmlAttribute,
			xmlsyntax.XmlText,
		) {
			reference, routeName, ok :=
				symfony.XMLControllerReference(node)
			if !ok {
				continue
			}
			result = append(result, routeControllerHintCandidate{
				reference: reference,
				routeName: routeName,
				position:  node.RangeTrimmedTrivia().End,
			})
		}
		return result
	case language.PHP:
		path, err := uriutil.Path(document.URI)
		if err != nil {
			return nil
		}
		routes := symfony.ParsePHPRoutesTree(
			path,
			document.SyntaxTree,
			document.LineIndex,
		)
		result := make([]routeControllerHintCandidate, 0, len(routes))
		for _, route := range routes {
			if route.Line <= 0 || route.Controller == "" {
				continue
			}
			reference, ok := symfony.ParseControllerReference(
				route.Controller,
			)
			if !ok {
				continue
			}
			result = append(result, routeControllerHintCandidate{
				reference: reference,
				routeName: route.Name,
				position: document.LineIndex.LineEnd(
					uint32(route.Line - 1),
				),
			})
		}
		return result
	default:
		return nil
	}
}

func inlayHintByteRange(request *lsp.InlayHintRequest) (uint32, uint32) {
	start := request.Document.LineIndex.OffsetUTF16(
		uint32(max(request.Range.Start.Line, 0)),
		uint32(max(request.Range.Start.Character, 0)),
	)
	end := request.Document.LineIndex.OffsetUTF16(
		uint32(max(request.Range.End.Line, 0)),
		uint32(max(request.Range.End.Character, 0)),
	)
	if end < start {
		start, end = end, start
	}
	return start, end
}

func shortControllerClass(className string) string {
	if separator := strings.LastIndexByte(className, '\\'); separator >= 0 {
		return className[separator+1:]
	}
	return className
}

func routeControllerMethodLocation(
	method semantic.Symbol,
) protocol.Location {
	location := protocol.Location{URI: uriutil.FileURI(method.Path)}
	source, err := os.ReadFile(method.Path)
	if err != nil {
		return location
	}
	lineIndex := cst.NewLineIndex(string(source))
	rng := method.SelectionRange
	if rng.Len() == 0 {
		rng = method.Range
	}
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	location.Range = protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
	return location
}

func routeControllerHintTooltip(
	candidate routeControllerHintCandidate,
	className,
	methodName string,
) string {
	resolved := className + "::" + methodName
	detail := candidate.reference.Value
	if detail != resolved {
		detail += " → " + resolved
	}
	if candidate.routeName == "" {
		return detail
	}
	return fmt.Sprintf("Route %q\n%s", candidate.routeName, detail)
}

var _ lsp.InlayHintProvider = (*RouteControllerProvider)(nil)
