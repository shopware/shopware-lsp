package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

const (
	missingControllerTargetCode lsp.DiagnosticID = "symfony.controller.target.missing"
	missingControllerMethodCode lsp.DiagnosticID = "symfony.controller.method.missing"
	deprecatedControllerCode    lsp.DiagnosticID = "symfony.controller.deprecated"
)

type ControllerAnalyzer struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewControllerAnalyzer(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ControllerAnalyzer {
	return &ControllerAnalyzer{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

type controllerReferenceNode struct {
	node       *cst.Node
	reference  symfony.ControllerReference
	routeName  string
	valueRange cst.TextRange
}

func (p *ControllerAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	var references []controllerReferenceNode
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		for _, scalar := range yamlquery.Nodes(
			document.SyntaxTree.Root,
			yamlsyntax.YamlScalar,
		) {
			reference, routeName, ok := symfony.YAMLControllerReference(scalar)
			if ok {
				references = append(references, controllerReferenceNode{
					node:      scalar,
					reference: reference,
					routeName: routeName,
				})
			}
		}
	case ".xml":
		for _, node := range xmlquery.Nodes(
			document.SyntaxTree.Root,
			xmlsyntax.XmlAttribute,
			xmlsyntax.XmlText,
		) {
			reference, routeName, ok := symfony.XMLControllerReference(node)
			if ok {
				references = append(references, controllerReferenceNode{
					node:      node,
					reference: reference,
					routeName: routeName,
				})
			}
		}
	case ".twig":
		for _, reference := range symfony.TwigControllerReferences(
			document.SyntaxTree.Root,
		) {
			references = append(references, controllerReferenceNode{
				node:       reference.Node,
				reference:  reference.ControllerReference,
				valueRange: reference.Range,
			})
		}
	default:
		return nil, nil
	}

	var result []lsp.Problem
	for _, item := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		resolution, err := symfony.ResolveControllerReference(
			item.reference,
			p.serviceIndex,
			p.phpIndex,
		)
		if err != nil {
			return nil, err
		}
		switch {
		case !resolution.TargetExists:
			result = append(result, lsp.Problem{
				Range: controllerReferenceTextRange(
					item,
					item.reference.Target,
				),
				Message: fmt.Sprintf(
					"Controller target '%s' not found",
					item.reference.Target,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       missingControllerTargetCode,
				Payload: map[string]any{
					"controller": item.reference.Value,
					"routeName":  item.routeName,
				},
			})
		case resolution.ClassFound && !resolution.MethodDeclared:
			data := map[string]any{
				"controller": item.reference.Value,
				"className":  resolution.Class.FullyQualified,
				"methodName": item.reference.Method,
				"routeName":  item.routeName,
			}
			data["routeParameters"] = localRouteParameters(document, item.routeName)
			result = append(result, lsp.Problem{
				Range: controllerReferenceTextRange(
					item,
					item.reference.Method,
				),
				Message: fmt.Sprintf(
					"Controller method '%s::%s' not found",
					resolution.Class.FullyQualified,
					item.reference.Method,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       missingControllerMethodCode,
				Payload:  data,
			})
		case resolution.Deprecated():
			result = append(result, lsp.Problem{
				Range: controllerReferenceTextRange(
					item,
					item.reference.Value,
				),
				Message: fmt.Sprintf(
					"Controller action '%s' is deprecated",
					item.reference.Value,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityHint,
				ID:       deprecatedControllerCode,
				Tags: []protocol.DiagnosticTag{
					protocol.DiagnosticTagDeprecated,
				},
			})
		}
	}
	return result, nil
}

func controllerReferenceTextRange(
	item controllerReferenceNode,
	value string,
) cst.TextRange {
	if item.node != nil && strings.Contains(item.node.Text(), value) {
		return valueNodeTextRange(item.node, value)
	}
	if item.valueRange.Len() == 0 {
		return valueNodeTextRange(item.node, value)
	}
	return item.valueRange
}

func localRouteParameters(
	document *lsp.TextDocument,
	routeName string,
) []string {
	if routeName == "" {
		return nil
	}
	var routes []symfony.Route
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		routes, _ = symfony.ParseYAMLRoutesTree(
			"",
			document.SyntaxTree,
			document.LineIndex,
		)
	case ".xml":
		routes, _ = symfony.ParseXMLRoutesTree(
			"",
			document.SyntaxTree,
			document.LineIndex,
		)
	}
	for _, route := range routes {
		if route.Name == routeName {
			return route.Parameters()
		}
	}
	return nil
}
