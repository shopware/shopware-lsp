package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDocumentStructureAndLosslessness(t *testing.T) {
	source := `<?xml version="1.0"?><container><services><service id="App\Service"><tag name="kernel.event_subscriber"/></service></services></container>`
	result := Parse(source)

	require.NotNil(t, result.Tree)
	require.NotNil(t, result.Tree.Root)
	assert.Empty(t, result.Errors)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Equal(t, syntax.XmlDocument, result.Tree.Root.Kind())

	var elements int
	for element := range result.Tree.Root.Descendants() {
		if node, ok := element.(*syntax.Node); ok && node.Kind() == syntax.XmlElement {
			elements++
		}
	}
	assert.Equal(t, 4, elements)
}

func TestParseIncompleteXMLProducesUsableTree(t *testing.T) {
	source := `<container><service id="App\Service`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.NotEmpty(t, result.Errors)
}

func TestParseMismatchedTagsRecovers(t *testing.T) {
	source := `<root><child>value</root><next/>`
	result := Parse(source)

	assert.Equal(t, source, result.Tree.Root.Text())
	assert.NotEmpty(t, result.Errors)
}

func TestParseProjectXMLCorpus(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "..", "extension", "testdata", "manifest.xml"),
		filepath.Join("..", "..", "..", "systemconfig", "testdata", "common.xml"),
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			content, err := os.ReadFile(path)
			require.NoError(t, err)

			result := Parse(string(content))
			assert.Equal(t, string(content), result.Tree.Root.Text())
			assert.Empty(t, result.Errors)
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, source := range []string{
		"",
		"<",
		`<service id="`,
		`<?xml version="1.0"?><root/>`,
		`<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]><root>&amp;</root>`,
		`<root><![CDATA[<broken>]]><!-- comment --></root>`,
	} {
		f.Add(source)
	}

	f.Fuzz(func(t *testing.T, source string) {
		result := Parse(source)
		require.NotNil(t, result.Tree)
		require.NotNil(t, result.Tree.Root)
		assert.Equal(t, source, result.Tree.Root.Text())
	})
}
