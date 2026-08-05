package twigcomponent

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func configurationInYAML(
	path string,
	root *yamlsyntax.Node,
) ([]Namespace, []string) {
	if root == nil {
		return nil, nil
	}
	var namespaces []Namespace
	var anonymous []string
	for _, pair := range yamlquery.Nodes(root, yamlsyntax.YamlPair) {
		if yamlquery.ScalarValue(yamlquery.PairKey(pair)) !=
			"twig_component" {
			continue
		}
		config := yamlquery.PairValue(pair)
		if !yamlquery.IsMapping(config) {
			continue
		}
		if value := yamlquery.Property(
			config,
			"anonymous_template_directory",
		); value != nil {
			if directory := normalizeDirectory(
				yamlquery.ScalarValue(value),
			); directory != "" {
				anonymous = append(anonymous, directory)
			}
		}
		defaults := yamlquery.Property(config, "defaults")
		if !yamlquery.IsMapping(defaults) {
			continue
		}
		for _, entry := range yamlquery.Pairs(defaults) {
			classPrefix := normalizeClass(
				yamlquery.ScalarValue(yamlquery.PairKey(entry)),
			)
			value := yamlquery.PairValue(entry)
			directory := "components"
			namePrefix := ""
			switch {
			case yamlquery.IsMapping(value):
				if configured := yamlquery.Property(
					value,
					"template_directory",
				); configured != nil {
					directory = yamlquery.ScalarValue(configured)
				}
				namePrefix = yamlquery.ScalarValue(
					yamlquery.Property(value, "name_prefix"),
				)
			case value != nil:
				directory = yamlquery.ScalarValue(value)
			}
			if classPrefix == "" ||
				normalizeDirectory(directory) == "" {
				continue
			}
			namespaces = append(namespaces, Namespace{
				ClassPrefix:       classPrefix,
				TemplateDirectory: normalizeDirectory(directory),
				NamePrefix:        normalizeComponentName(namePrefix),
				File:              path,
				Range:             scalarValueRange(yamlquery.PairKey(entry)),
			})
		}
	}
	return uniqueNamespaces(namespaces), uniqueStrings(anonymous)
}

func scalarValueRange(node *yamlsyntax.Node) cst.TextRange {
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

func uniqueNamespaces(values []Namespace) []Namespace {
	seen := make(map[string]struct{}, len(values))
	result := make([]Namespace, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value.ClassPrefix) + "\x00" +
			value.TemplateDirectory + "\x00" + value.NamePrefix
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
