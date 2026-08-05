// Package literal derives PHP literal types directly from lossless syntax.
package literal

import (
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// TypeOf returns the intrinsic type of a scalar literal node. These types do
// not depend on flow state and therefore do not need a semantic side-table
// entry.
func TypeOf(node *phpsyntax.Node) (types.Type, bool) {
	if node == nil {
		return types.Type{}, false
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		return types.LiteralString(phpquery.StringValue(node)), true
	case phpsyntax.PhpNumber:
		return numberType(trimmedNodeText(node)), true
	case phpsyntax.PhpBoolean:
		if strings.EqualFold(strings.TrimSpace(node.Text()), "true") {
			return types.True(), true
		}
		return types.False(), true
	case phpsyntax.PhpNull:
		return types.Null(), true
	default:
		return types.Type{}, false
	}
}

func trimmedNodeText(node *phpsyntax.Node) string {
	full := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	start := int(trimmed.Start - full.Start)
	end := int(trimmed.End - full.Start)
	text := node.Text()
	if start < 0 || start > end || end > len(text) {
		return text
	}
	return text[start:end]
}

func numberType(text string) types.Type {
	text = strings.ReplaceAll(strings.TrimSpace(text), "_", "")
	unsigned := strings.TrimLeft(text, "+-")
	lower := strings.ToLower(unsigned)
	if strings.HasPrefix(lower, "0x") ||
		strings.HasPrefix(lower, "0b") ||
		strings.HasPrefix(lower, "0o") {
		return types.LiteralInt(text)
	}
	if strings.ContainsAny(lower, ".e") {
		if _, err := strconv.ParseFloat(text, 64); err == nil {
			return types.LiteralFloat(text)
		}
		return types.Float()
	}
	return types.LiteralInt(text)
}
