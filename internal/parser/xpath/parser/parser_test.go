package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/parser/xpath/syntax"
)

func TestParseLosslessXPath(t *testing.T) {
	source := `count(.//article[@data-id=$id and contains(@class, "active")]) > 0`
	result := Parse(source)
	require.NotNil(t, result.Tree)
	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Equal(t, syntax.XPathDocument, result.Tree.Root.Kind())
}

func TestParseReportsMalformedXPathAndKeepsTree(t *testing.T) {
	for _, source := range []string{
		"",
		`.//article[@id="open]`,
		`.//article[@id=$]`,
		`.//article[@]`,
		`.//article[]`,
		`.//article[@id='x'`,
		`.//article)`,
		`.//article#bad`,
	} {
		t.Run(source, func(t *testing.T) {
			result := Parse(source)
			require.NotNil(t, result.Tree)
			assert.Equal(t, source, result.Tree.Root.Text())
			assert.NotEmpty(t, result.Errors)
			for _, parseError := range result.Errors {
				assert.LessOrEqual(t, parseError.Range.Start, parseError.Range.End)
				assert.LessOrEqual(t, parseError.Range.End, uint32(len(source)))
			}
		})
	}
}
