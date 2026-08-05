package feature

import (
	"fmt"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type Feature struct {
	Name string
	File string
	Line int
}

func ParseFeatureFile(document []byte, filePath string) ([]Feature, error) {
	result := yamlparser.Parse(string(document))
	return ParseFeatureTree(result.Tree, yamlsyntax.NewLineIndex(result.Tree.Source), filePath)
}

func ParseFeatureTree(tree *yamlsyntax.Tree, lineIndex *yamlsyntax.LineIndex, filePath string) ([]Feature, error) {
	features := make([]Feature, 0)

	for _, pair := range yamlquery.Nodes(tree.Root, yamlsyntax.YamlPair) {
		if yamlquery.ScalarValue(yamlquery.PairKey(pair)) != "name" {
			continue
		}
		value := yamlquery.PairValue(pair)
		if value == nil || value.Kind() != yamlsyntax.YamlScalar || yamlquery.IsNull(value) {
			continue
		}
		name := yamlquery.ScalarValue(value)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(value.RangeTrimmedTrivia().Start)
		features = append(features, Feature{
			Name: name,
			File: filePath,
			Line: int(line) + 1,
		})
	}

	if len(features) == 0 {
		return features, fmt.Errorf("could not find flags node in file: %s", filePath)
	}
	return features, nil
}
