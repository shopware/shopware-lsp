package snippet

import (
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

type Snippet struct {
	Key  string
	Text string
	File string
	Line int
}

func parseSnippetFile(document []byte, filePath string) (map[string]Snippet, error) {
	parsed := jsonparser.Parse(string(document))
	return parseSnippetTree(parsed.Tree, jsonsyntax.NewLineIndex(parsed.Tree.Source), filePath)
}

func parseSnippetTree(tree *jsonsyntax.Tree, lineIndex *jsonsyntax.LineIndex, filePath string) (map[string]Snippet, error) {
	result := make(map[string]Snippet)
	root := jsonquery.RootValue(tree.Root)
	if root == nil || root.Kind() != jsonsyntax.JsonObject {
		return result, nil
	}

	extractValues("", root, result, filePath, lineIndex)
	return result, nil
}

func extractValues(
	prefix string,
	object *jsonsyntax.Node,
	result map[string]Snippet,
	filePath string,
	lineIndex *jsonsyntax.LineIndex,
) {
	for _, pair := range jsonquery.Pairs(object) {
		key := jsonquery.PairKey(pair)
		value := jsonquery.PairValue(pair)
		if key == nil || value == nil {
			continue
		}

		fullKey := jsonquery.StringValue(key)
		if prefix != "" {
			fullKey = prefix + "." + fullKey
		}

		if value.Kind() == jsonsyntax.JsonObject {
			extractValues(fullKey, value, result, filePath, lineIndex)
			continue
		}
		switch value.Kind() {
		case jsonsyntax.JsonString,
			jsonsyntax.JsonNumber,
			jsonsyntax.JsonBoolean,
			jsonsyntax.JsonNull:
			line, _ := lineIndex.Position(value.RangeTrimmedTrivia().Start)
			result[fullKey] = Snippet{
				Key:  fullKey,
				Text: jsonquery.ScalarText(value),
				File: filePath,
				Line: int(line) + 1,
			}
		}
	}
}
