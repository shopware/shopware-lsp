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
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

const (
	duplicateRouteCode     lsp.DiagnosticID = "symfony.route.duplicate"
	duplicateServiceCode   lsp.DiagnosticID = "symfony.service.duplicate"
	duplicateParameterCode lsp.DiagnosticID = "symfony.parameter.duplicate"
)

type DuplicateAnalyzer struct{}

func NewDuplicateAnalyzer() *DuplicateAnalyzer {
	return &DuplicateAnalyzer{}
}

func (p *DuplicateAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	var entries []duplicateDefinition
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		entries = yamlDuplicateDefinitions(document.SyntaxTree.Root)
	case ".xml":
		entries = xmlDuplicateDefinitions(document.SyntaxTree.Root)
	case ".php":
		entries = phpDuplicateRouteDefinitions(document.SyntaxTree.Root)
	default:
		return nil, nil
	}

	counts := make(map[string]int, len(entries))
	for _, entry := range entries {
		counts[entry.kind+"\x00"+entry.name]++
	}
	var result []lsp.Problem
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, nil
		}
		if counts[entry.kind+"\x00"+entry.name] < 2 {
			continue
		}
		code, label := duplicateCodeAndLabel(entry.kind)
		result = append(result, lsp.Problem{
			Range: valueNodeTextRange(entry.node, entry.name),
			Message: fmt.Sprintf(
				"Duplicate %s name '%s'",
				label,
				entry.name,
			),
			Source:   "symfony",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       code,
			Payload: map[string]any{
				"definitionKind": entry.kind,
				"definitionName": entry.name,
			},
		})
	}
	return result, nil
}

type duplicateDefinition struct {
	kind string
	name string
	node *cst.Node
}

func yamlDuplicateDefinitions(root *cst.Node) []duplicateDefinition {
	mapping := yamlquery.RootValue(root)
	if !yamlquery.IsMapping(mapping) {
		return nil
	}
	var result []duplicateDefinition
	result = append(
		result,
		yamlMappingDefinitions(yamlquery.Property(mapping, "services"), "service", true)...,
	)
	result = append(
		result,
		yamlMappingDefinitions(yamlquery.Property(mapping, "parameters"), "parameter", false)...,
	)

	routing := false
	for _, pair := range yamlquery.Pairs(mapping) {
		value := yamlquery.PairValue(pair)
		if !yamlquery.IsMapping(value) {
			continue
		}
		if yamlquery.Property(value, "path") != nil ||
			yamlquery.Property(value, "resource") != nil ||
			yamlquery.Property(value, "controller") != nil {
			routing = true
			break
		}
	}
	if routing {
		for _, pair := range yamlquery.Pairs(mapping) {
			key := yamlquery.PairKey(pair)
			name := yamlquery.ScalarValue(key)
			if name == "" || strings.HasPrefix(name, "_") ||
				!yamlquery.IsMapping(yamlquery.PairValue(pair)) {
				continue
			}
			result = append(result, duplicateDefinition{
				kind: "route",
				name: name,
				node: key,
			})
		}
	}
	return result
}

func yamlMappingDefinitions(
	mapping *cst.Node,
	kind string,
	skipDefaults bool,
) []duplicateDefinition {
	if !yamlquery.IsMapping(mapping) {
		return nil
	}
	var result []duplicateDefinition
	for _, pair := range yamlquery.Pairs(mapping) {
		key := yamlquery.PairKey(pair)
		name := yamlquery.ScalarValue(key)
		if name == "" || skipDefaults && strings.HasPrefix(name, "_") {
			continue
		}
		result = append(result, duplicateDefinition{
			kind: kind,
			name: name,
			node: key,
		})
	}
	return result
}

func xmlDuplicateDefinitions(root *cst.Node) []duplicateDefinition {
	var result []duplicateDefinition
	for _, services := range xmlquery.Elements(root, "services") {
		for _, child := range xmlquery.ChildElements(services, "service", "alias") {
			attribute := xmlquery.Attribute(child, "id")
			if name := xmlquery.AttributeValue(attribute); name != "" {
				result = append(result, duplicateDefinition{
					kind: "service",
					name: name,
					node: attribute,
				})
			}
		}
	}
	for _, parameters := range xmlquery.Elements(root, "parameters") {
		for _, child := range xmlquery.ChildElements(parameters, "parameter") {
			attribute := xmlquery.Attribute(child, "key")
			if name := xmlquery.AttributeValue(attribute); name != "" {
				result = append(result, duplicateDefinition{
					kind: "parameter",
					name: name,
					node: attribute,
				})
			}
		}
	}
	for _, routes := range xmlquery.Elements(root, "routes") {
		for _, child := range xmlquery.ChildElements(routes, "route") {
			attribute := xmlquery.Attribute(child, "id")
			if name := xmlquery.AttributeValue(attribute); name != "" {
				result = append(result, duplicateDefinition{
					kind: "route",
					name: name,
					node: attribute,
				})
			}
		}
	}
	return result
}

func phpDuplicateRouteDefinitions(root *cst.Node) []duplicateDefinition {
	var result []duplicateDefinition
	for _, attribute := range phpquery.Nodes(root, phpsyntax.PhpAttribute) {
		if phpquery.ClassAt(attribute) != nil &&
			phpquery.MethodAt(attribute) == nil {
			// A class-level route name is a prefix, not a concrete route name.
			continue
		}
		name := strings.TrimPrefix(phpquery.AttributeName(attribute), "\\")
		if index := strings.LastIndexByte(name, '\\'); index >= 0 {
			name = name[index+1:]
		}
		if name != "Route" {
			continue
		}
		positional := 0
		for _, argument := range phpquery.Arguments(attribute) {
			argumentName := phpquery.ArgumentName(argument)
			literal := firstPHPString(argument)
			switch {
			case argumentName == "name" && literal != nil:
				if value := phpquery.StringValue(literal); value != "" {
					result = append(result, duplicateDefinition{
						kind: "route",
						name: value,
						node: literal,
					})
				}
			case argumentName == "" && positional == 1 && literal != nil:
				if value := phpquery.StringValue(literal); value != "" {
					result = append(result, duplicateDefinition{
						kind: "route",
						name: value,
						node: literal,
					})
				}
			}
			if argumentName == "" {
				positional++
			}
		}
	}
	return result
}

func firstPHPString(node *cst.Node) *cst.Node {
	strings := phpquery.Nodes(node, phpsyntax.PhpString)
	if len(strings) == 0 {
		return nil
	}
	return strings[0]
}

func duplicateCodeAndLabel(kind string) (lsp.DiagnosticID, string) {
	switch kind {
	case "service":
		return duplicateServiceCode, "service"
	case "parameter":
		return duplicateParameterCode, "parameter"
	default:
		return duplicateRouteCode, "route"
	}
}
