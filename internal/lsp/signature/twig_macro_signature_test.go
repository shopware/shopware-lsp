package signature

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

func TestTwigMacroSignatureHelp(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/macros/forms.html.twig",
		[]byte(`{## Renders a form input. ##}
{% macro input(
    ## HTML field name.
    name,
    ## Initial field value.
    value = '',
    required = false,
) %}{% endmacro %}`),
	)))
	source := `{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email', 'value') }}
`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "'value'") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigMacroSignatureProvider(index).GetSignatureHelp(
		context.Background(),
		&lsp.SignatureHelpRequest{
			SignatureHelpParams: params,
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
	require.Len(t, result.Signatures, 1)
	assert.Equal(
		t,
		"input(name, value = '', required = false)",
		result.Signatures[0].Label,
	)
	assert.Equal(t, 1, result.ActiveParameter)
	assert.Equal(t, 1, result.Signatures[0].ActiveParameter)
	require.Len(t, result.Signatures[0].Parameters, 3)
	require.NotNil(t, result.Signatures[0].Documentation)
	assert.Equal(
		t,
		"Renders a form input.",
		result.Signatures[0].Documentation.Value,
	)
	require.NotNil(t, result.Signatures[0].Parameters[0].Documentation)
	assert.Equal(
		t,
		"HTML field name.",
		result.Signatures[0].Parameters[0].Documentation.Value,
	)
	require.NotNil(t, result.Signatures[0].Parameters[1].Documentation)
	assert.Equal(
		t,
		"Initial field value.",
		result.Signatures[0].Parameters[1].Documentation.Value,
	)
}
