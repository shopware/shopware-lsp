package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigMacroHoverShowsSignatureAndUsageCount(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/macros/forms.html.twig",
		[]byte(`{## Renders a form input. ##}
{% macro input(name, value = '') %}{% endmacro %}`),
	)))
	source := `{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email') }}
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/page.html.twig",
		[]byte(source),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "forms.input") + len("forms.") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigMacroHoverProvider("/project", index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "input(name, value = '')")
	assert.Contains(t, result.Contents.Value, "Renders a form input.")
	assert.Contains(t, result.Contents.Value, "macros/forms.html.twig")
	assert.Contains(t, result.Contents.Value, "1 indexed use")
}
