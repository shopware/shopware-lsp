package theme

import (
	"fmt"

	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

// ParseThemeConfig parses the config.fields section from a theme.json file.
func ParseThemeConfig(document []byte, filePath string) ([]ThemeConfigField, error) {
	result := jsonparser.Parse(string(document))
	return ParseThemeConfigTree(result.Tree, jsonsyntax.NewLineIndex(result.Tree.Source), filePath)
}

func ParseThemeConfigTree(tree *jsonsyntax.Tree, lineIndex *jsonsyntax.LineIndex, filePath string) ([]ThemeConfigField, error) {
	root := jsonquery.RootValue(tree.Root)
	if root == nil || root.Kind() != jsonsyntax.JsonObject {
		return nil, fmt.Errorf("JSON root is not an object")
	}

	config := jsonquery.Property(root, "config")
	if config == nil || config.Kind() != jsonsyntax.JsonObject {
		return []ThemeConfigField{}, nil
	}
	fieldsNode := jsonquery.Property(config, "fields")
	if fieldsNode == nil || fieldsNode.Kind() != jsonsyntax.JsonObject {
		return []ThemeConfigField{}, nil
	}

	fields := make([]ThemeConfigField, 0, len(jsonquery.Pairs(fieldsNode)))
	for _, pair := range jsonquery.Pairs(fieldsNode) {
		key := jsonquery.PairKey(pair)
		value := jsonquery.PairValue(pair)
		if key == nil || value == nil || value.Kind() != jsonsyntax.JsonObject {
			continue
		}

		line, _ := lineIndex.Position(key.RangeTrimmedTrivia().Start)
		field := ThemeConfigField{
			Key:   jsonquery.StringValue(key),
			Label: make(map[string]string),
			Scss:  true,
			Path:  filePath,
			Line:  int(line) + 1,
		}

		if labels := jsonquery.Property(value, "label"); labels != nil && labels.Kind() == jsonsyntax.JsonObject {
			for _, labelPair := range jsonquery.Pairs(labels) {
				labelKey := jsonquery.PairKey(labelPair)
				labelValue := jsonquery.PairValue(labelPair)
				if labelKey != nil && labelValue != nil && labelValue.Kind() == jsonsyntax.JsonString {
					field.Label[jsonquery.StringValue(labelKey)] = jsonquery.StringValue(labelValue)
				}
			}
		}
		if property := jsonquery.Property(value, "type"); property != nil {
			field.Type = jsonquery.StringValue(property)
		}
		if property := jsonquery.Property(value, "value"); property != nil {
			field.Value = jsonquery.StringValue(property)
		}
		if property := jsonquery.Property(value, "editable"); property != nil {
			if editable, ok := jsonquery.BooleanValue(property); ok {
				field.Editable = editable
			}
		}
		if property := jsonquery.Property(value, "scss"); property != nil {
			if scss, ok := jsonquery.BooleanValue(property); ok {
				field.Scss = scss
			}
		}
		if property := jsonquery.Property(value, "order"); property != nil {
			if order, ok := jsonquery.IntegerValue(property); ok {
				field.Order = order
			}
		}
		if property := jsonquery.Property(value, "block"); property != nil {
			field.Block = jsonquery.StringValue(property)
		}

		fields = append(fields, field)
	}

	return fields, nil
}
