package twig

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type GlobalSource uint8

const (
	YAMLGlobalSource GlobalSource = iota
	PHPExtensionGlobalSource
	ContainerGlobalSource
)

func (source GlobalSource) String() string {
	switch source {
	case YAMLGlobalSource:
		return "Twig configuration"
	case PHPExtensionGlobalSource:
		return "Twig extension"
	case ContainerGlobalSource:
		return "compiled container"
	default:
		return "Twig global"
	}
}

// Global is one workspace-wide variable available in every Twig template.
type Global struct {
	Name      string
	Type      string
	Value     string
	ServiceID string
	File      string
	Range     cst.TextRange
	Source    GlobalSource
}

type GlobalCatalog struct {
	File    string
	Globals []Global
}

func GlobalsInYAML(
	path string,
	root *yamlsyntax.Node,
) []Global {
	if root == nil {
		return nil
	}
	value := yamlquery.RootValue(root)
	if !yamlquery.IsMapping(value) {
		return nil
	}
	var result []Global
	for _, twigConfig := range twigYAMLConfigurations(value) {
		globals := yamlquery.Property(twigConfig, "globals")
		if !yamlquery.IsMapping(globals) {
			continue
		}
		for _, pair := range yamlquery.Pairs(globals) {
			key := yamlquery.PairKey(pair)
			name := yamlquery.ScalarValue(key)
			if name == "" {
				continue
			}
			valueNode := yamlquery.PairValue(pair)
			global := Global{
				Name:   name,
				Type:   yamlGlobalType(valueNode),
				Value:  yamlquery.ScalarValue(valueNode),
				File:   path,
				Range:  yamlGlobalScalarRange(key),
				Source: YAMLGlobalSource,
			}
			if service, found := yamlGlobalService(valueNode); found {
				global.ServiceID = service
				global.Type = ""
			}
			result = append(result, global)
		}
	}
	return uniqueGlobals(result)
}

func twigYAMLConfigurations(root *yamlsyntax.Node) []*yamlsyntax.Node {
	var result []*yamlsyntax.Node
	if twig := yamlquery.Property(root, "twig"); yamlquery.IsMapping(twig) {
		result = append(result, twig)
	}
	for _, pair := range yamlquery.Pairs(root) {
		key := yamlquery.ScalarValue(yamlquery.PairKey(pair))
		value := yamlquery.PairValue(pair)
		if !strings.HasPrefix(key, "when@") || !yamlquery.IsMapping(value) {
			continue
		}
		if twig := yamlquery.Property(value, "twig"); yamlquery.IsMapping(twig) {
			result = append(result, twig)
		}
	}
	return result
}

func yamlGlobalService(node *yamlsyntax.Node) (string, bool) {
	if node == nil || node.Kind() != yamlsyntax.YamlScalar {
		return "", false
	}
	value := strings.TrimSpace(yamlquery.ScalarValue(node))
	if strings.HasPrefix(value, "@@") || !strings.HasPrefix(value, "@") {
		return "", false
	}
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimPrefix(value, "?")
	return value, value != ""
}

func yamlGlobalType(node *yamlsyntax.Node) string {
	if node == nil || yamlquery.IsNull(node) {
		return "null"
	}
	if yamlquery.IsMapping(node) || yamlquery.IsSequence(node) {
		return "array"
	}
	raw := strings.TrimSpace(yamlquery.RawText(node))
	if raw == "" {
		return ""
	}
	if raw[0] == '\'' || raw[0] == '"' ||
		strings.HasPrefix(raw, "@@") {
		return "string"
	}
	switch strings.ToLower(raw) {
	case "true", "false":
		return "bool"
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return "int"
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return "float"
	}
	return "string"
}

func yamlGlobalScalarRange(node *yamlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 1 && (text[0] == '\'' || text[0] == '"') {
		rng.Start++
		if len(text) >= 2 && text[len(text)-1] == text[0] {
			rng.End--
		}
	}
	return rng
}

func GlobalsInPHPExtension(
	path string,
	root *phpsyntax.Node,
	document *semantic.Document,
) []Global {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Global
	for _, class := range phpquery.Classes(root) {
		if !isTwigExtensionClass(class, resolver) {
			continue
		}
		method := methodNamed(class, "getGlobals")
		if method == nil {
			continue
		}
		for _, statement := range phpquery.Nodes(
			method,
			phpsyntax.PhpReturnStatement,
		) {
			if phpquery.FunctionLikeAt(statement) != method {
				continue
			}
			expression := firstPHPChild(statement)
			array := phpquery.ArrayAt(expression)
			if array == nil {
				continue
			}
			for _, item := range phpquery.ArrayItems(array) {
				key := phpquery.ArrayItemKey(item)
				value := phpquery.ArrayItemValue(item)
				name := phpquery.StringValue(key)
				if name == "" || value == nil {
					continue
				}
				result = append(result, Global{
					Name:   name,
					Type:   phpGlobalType(value, document, resolver),
					Value:  strings.TrimSpace(value.Text()),
					File:   path,
					Range:  phpGlobalStringRange(key),
					Source: PHPExtensionGlobalSource,
				})
			}
		}
	}
	return uniqueGlobals(result)
}

func isTwigExtensionClass(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	for _, name := range append(
		phpquery.ClassExtends(class),
		phpquery.ClassImplements(class)...,
	) {
		resolved := strings.TrimPrefix(resolver.Resolve(name), "\\")
		switch strings.ToLower(resolved) {
		case "twig\\extension\\abstractextension",
			"twig\\extension\\extensioninterface",
			"twig\\extension\\globalsinterface",
			"twig_extension",
			"twig_extensioninterface":
			return true
		}
	}
	return false
}

func firstPHPChild(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func phpGlobalType(
	node *phpsyntax.Node,
	document *semantic.Document,
	resolver *php.NameResolver,
) string {
	if document != nil {
		value := document.TypeOf(node).Type
		if !value.IsUnknown() {
			return value.String()
		}
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		return "string"
	case phpsyntax.PhpNumber:
		if strings.ContainsAny(node.Text(), ".eE") {
			return "float"
		}
		return "int"
	case phpsyntax.PhpBoolean:
		return "bool"
	case phpsyntax.PhpNull:
		return "null"
	case phpsyntax.PhpArray:
		return "array"
	case phpsyntax.PhpObjectCreation:
		return strings.TrimPrefix(
			resolver.Resolve(phpquery.ObjectClassName(node)),
			"\\",
		)
	default:
		return ""
	}
}

func phpGlobalStringRange(node *phpsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func uniqueGlobals(values []Global) []Global {
	seen := make(map[string]struct{}, len(values))
	result := make([]Global, 0, len(values))
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		key := strings.ToLower(value.Name) + "\x00" +
			value.File + "\x00" + value.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
