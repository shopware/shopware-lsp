package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

const (
	deprecatedFactorySettingCode lsp.DiagnosticID = "symfony.service.factory.deprecated"
	deprecatedRoutePatternCode   lsp.DiagnosticID = "symfony.route.pattern.deprecated"
	deprecatedRequirementCode    lsp.DiagnosticID = "symfony.route.requirement.deprecated"
)

// LegacyConfigurationAnalyzer ports Symfony's structural
// deprecation inspections without depending on an editor-specific schema API.
type LegacyConfigurationAnalyzer struct{}

func NewLegacyConfigurationAnalyzer() *LegacyConfigurationAnalyzer {
	return &LegacyConfigurationAnalyzer{}
}

func (p *LegacyConfigurationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".yaml", ".yml":
		return legacyYAMLDiagnostics(ctx, document), nil
	case ".xml":
		return legacyXMLDiagnostics(ctx, document), nil
	default:
		return nil, nil
	}
}

func legacyYAMLDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) []lsp.Problem {
	var result []lsp.Problem
	routingFile := likelyRoutingYAML(document)
	for _, pair := range yamlquery.Nodes(
		document.SyntaxTree.Root,
		yamlsyntax.YamlPair,
	) {
		if ctx.Err() != nil {
			return nil
		}
		key := yamlquery.PairKey(pair)
		name := yamlquery.ScalarValue(key)
		path := yamlquery.PairPath(pair)
		switch {
		case len(path) == 3 && path[0] == "services" &&
			isDeprecatedFactorySetting(name):
			result = append(result, deprecatedConfigurationDiagnostic(
				key,
				document.LineIndex,
				name,
				deprecatedFactorySettingCode,
				"Symfony: this factory pattern is deprecated; use 'factory' instead",
			))
		case routingFile && len(path) == 2 && name == "pattern":
			result = append(result, deprecatedConfigurationDiagnostic(
				key,
				document.LineIndex,
				name,
				deprecatedRoutePatternCode,
				"Pattern is deprecated; use path instead",
			))
		case routingFile && len(path) == 3 && path[1] == "requirements" &&
			(name == "_method" || name == "_scheme"):
			result = append(result, deprecatedConfigurationDiagnostic(
				key,
				document.LineIndex,
				name,
				deprecatedRequirementCode,
				"The '"+name+"' requirement is deprecated",
			))
		}
	}
	return result
}

func likelyRoutingYAML(document *lsp.TextDocument) bool {
	path := strings.ToLower(strings.ReplaceAll(document.URI, `\`, "/"))
	base := filepath.Base(path)
	if strings.Contains(base, "routing") || strings.Contains(base, "routes") ||
		strings.Contains(path, "/routing/") ||
		strings.Contains(path, "/routes/") {
		return true
	}
	root := yamlquery.RootValue(document.SyntaxTree.Root)
	for _, pair := range yamlquery.Pairs(root) {
		definition := yamlquery.PairValue(pair)
		if !yamlquery.IsMapping(definition) {
			continue
		}
		for _, marker := range []string{
			"path",
			"controller",
			"methods",
			"defaults",
			"requirements",
			"options",
			"host",
			"schemes",
			"condition",
			"resource",
			"type",
			"prefix",
		} {
			if yamlquery.PropertyPair(definition, marker) != nil {
				return true
			}
		}
	}
	return false
}

func legacyXMLDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) []lsp.Problem {
	var result []lsp.Problem
	for _, attribute := range xmlquery.Nodes(
		document.SyntaxTree.Root,
		xmlsyntax.XmlAttribute,
	) {
		if ctx.Err() != nil {
			return nil
		}
		element := xmlquery.ElementAt(attribute)
		name := xmlquery.AttributeName(attribute)
		switch {
		case element != nil && xmlquery.ElementName(element) == "service" &&
			isDeprecatedXMLFactorySetting(name):
			result = append(result, deprecatedConfigurationDiagnostic(
				attribute,
				document.LineIndex,
				name,
				deprecatedFactorySettingCode,
				"Symfony: this factory pattern is deprecated; use 'factory' instead",
			))
		case element != nil && xmlquery.ElementName(element) == "route" &&
			name == "pattern":
			result = append(result, deprecatedConfigurationDiagnostic(
				attribute,
				document.LineIndex,
				name,
				deprecatedRoutePatternCode,
				"Pattern is deprecated; use path instead",
			))
		case element != nil &&
			xmlquery.ElementName(element) == "requirement" &&
			name == "key":
			value := xmlquery.AttributeValue(attribute)
			parent := xmlquery.ParentElement(element)
			if parent == nil || xmlquery.ElementName(parent) != "route" ||
				value != "_method" && value != "_scheme" {
				continue
			}
			result = append(result, deprecatedConfigurationDiagnostic(
				attribute,
				document.LineIndex,
				value,
				deprecatedRequirementCode,
				"The '"+value+"' requirement is deprecated",
			))
		}
	}
	return result
}

func deprecatedConfigurationDiagnostic(
	node *cst.Node,
	_ *cst.LineIndex,
	rangeValue string,
	code lsp.DiagnosticID,
	message string,
) lsp.Problem {
	return lsp.Problem{
		Range:    valueNodeTextRange(node, rangeValue),
		Message:  message,
		Source:   "symfony",
		Severity: protocol.DiagnosticSeverityHint,
		ID:       code,
		Tags: []protocol.DiagnosticTag{
			protocol.DiagnosticTagDeprecated,
		},
	}
}

func isDeprecatedFactorySetting(name string) bool {
	return name == "factory_class" ||
		name == "factory_method" ||
		name == "factory_service"
}

func isDeprecatedXMLFactorySetting(name string) bool {
	return name == "factory-class" ||
		name == "factory-method" ||
		name == "factory-service"
}
