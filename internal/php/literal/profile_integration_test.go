//go:build integration

package literal

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/require"
)

// TestShopwareTrunkLiteralProfile reports common scalar literal values without
// affecting the production inference path.
func TestShopwareTrunkLiteralProfile(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("real-world checkout not found at %s", root)
	}

	stringCounts := make(map[string]int)
	numberCounts := make(map[string]int)
	var documents, stringsTotal, numbersTotal int
	var distinctStringsPerDocument, distinctNumbersPerDocument int
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() ||
			!strings.EqualFold(filepath.Ext(path), ".php") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree := phpparser.ParseBytes(content).Tree
		documentStrings := make(map[string]struct{})
		documentNumbers := make(map[string]struct{})
		documents++
		for element := range tree.Root.Descendants() {
			node, ok := element.(*phpsyntax.Node)
			if !ok {
				continue
			}
			switch node.Kind() {
			case phpsyntax.PhpString:
				value := phpquery.StringValue(node)
				incrementOwned(stringCounts, value)
				documentStrings[value] = struct{}{}
				stringsTotal++
			case phpsyntax.PhpNumber:
				value, ok := TypeOf(node)
				if !ok {
					continue
				}
				normalized := value.String()
				incrementOwned(numberCounts, normalized)
				documentNumbers[normalized] = struct{}{}
				numbersTotal++
			}
		}
		distinctStringsPerDocument += len(documentStrings)
		distinctNumbersPerDocument += len(documentNumbers)
		return nil
	})
	require.NoError(t, err)

	t.Logf(
		"literal profile: documents=%d "+
			"strings=%d unique=%d per_document_unique=%d repeat_upper_bound=%d top=%s "+
			"numbers=%d unique=%d per_document_unique=%d repeat_upper_bound=%d top=%s",
		documents,
		stringsTotal,
		len(stringCounts),
		distinctStringsPerDocument,
		stringsTotal-distinctStringsPerDocument,
		formatTopLiterals(stringCounts, 20),
		numbersTotal,
		len(numberCounts),
		distinctNumbersPerDocument,
		numbersTotal-distinctNumbersPerDocument,
		formatTopLiterals(numberCounts, 10),
	)
}

func incrementOwned(counts map[string]int, value string) {
	if count, ok := counts[value]; ok {
		counts[value] = count + 1
		return
	}
	counts[strings.Clone(value)] = 1
}

type literalCount struct {
	value string
	count int
}

func formatTopLiterals(counts map[string]int, limit int) string {
	values := make([]literalCount, 0, len(counts))
	for value, count := range counts {
		values = append(values, literalCount{value: value, count: count})
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].count == values[right].count {
			return values[left].value < values[right].value
		}
		return values[left].count > values[right].count
	})
	if len(values) > limit {
		values = values[:limit]
	}
	var result strings.Builder
	for index, value := range values {
		if index > 0 {
			result.WriteString(", ")
		}
		text := value.value
		if len(text) > 80 {
			text = text[:80] + "…"
		}
		result.WriteString(strconv.Quote(text))
		result.WriteByte('=')
		result.WriteString(strconv.Itoa(value.count))
	}
	return result.String()
}
