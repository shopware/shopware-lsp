package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigMacroCompletionForNamespaceAndDirectImports(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/macros/forms.html.twig",
		[]byte(`{## Renders a form input. ##}
{% macro input(name, value = '') %}{% endmacro %}`),
	)))
	provider := NewTwigMacroCompletionProvider(index)

	tests := []struct {
		source string
		needle string
		label  string
	}{
		{
			source: `{% import 'macros/forms.html.twig' as forms %}
{{ forms.input() }}
`,
			needle: "forms.input",
			label:  "input",
		},
		{
			source: `{% from 'macros/forms.html.twig' import input as field %}
{{ field() }}
`,
			needle: "field()",
			label:  "field",
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file:///project/templates/page.html.twig",
			test.source,
			1,
		)
		offset := uint32(strings.LastIndex(test.source, test.needle) + len(test.needle) - 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		items := provider.GetCompletions(
			context.Background(),
			twigMacroCompletionRequest(document, node, offset),
		)
		requireCompletion(t, items, test.label)
		var found protocol.CompletionItem
		for _, item := range items {
			if item.Label == test.label {
				found = item
				break
			}
		}
		assert.Contains(t, found.Detail, "input(name, value = '')")
		assert.Equal(t, "Renders a form input.", found.Documentation.Value)
	}
}

func twigMacroCompletionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
