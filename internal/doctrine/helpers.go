package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func normalizeClass(value string) string { return strings.Trim(strings.TrimSpace(value), "\\") }

func hasRange(rng cst.TextRange) bool { return rng.Start != 0 || rng.End != 0 }

func xmlValueRange(node *xmlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	equals := strings.IndexByte(text, '=')
	if equals < 0 {
		return rng
	}
	value := strings.TrimSpace(text[equals+1:])
	valueOffset := strings.Index(text, value)
	if valueOffset < 0 {
		return rng
	}
	start := rng.Start + uint32(valueOffset)
	end := start + uint32(len(value))
	if len(value) >= 1 && (value[0] == '"' || value[0] == '\'') {
		start++
		if len(value) >= 2 && value[len(value)-1] == value[0] {
			end--
		}
	}
	return cst.TextRange{Start: start, End: end}
}

func yamlScalarRange(node *yamlsyntax.Node) cst.TextRange {
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

func rangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	if rng.Start == 0 && rng.End == 0 {
		return false
	}
	return offset >= rng.Start && offset <= rng.End
}
